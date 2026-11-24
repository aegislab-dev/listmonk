// Package subimporter implements a bulk ZIP/CSV importer of subscribers.
// It implements a simple queue for buffering imports and committing records
// to DB along with ZIP and CSV handling utilities. It is meant to be used as
// a singleton as each Importer instance is stateful, where it keeps track of
// an import in progress. Only one import should happen on a single importer
// instance at a time.
package subimporter

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/utils"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const (
	// commitBatchSize is the number of inserts to commit in a single SQL transaction.
	commitBatchSize = 10000
)

// Various import statuses.
const (
	StatusNone      = "none"
	StatusImporting = "importing"
	StatusStopping  = "stopping"
	StatusFinished  = "finished"
	StatusFailed    = "failed"

	ModeSubscribe = "subscribe"
	ModeBlocklist = "blocklist"
)

// Importer represents the bulk CSV subscriber import system.
type Importer struct {
	opt  Options
	db   *sql.DB
	i18n *i18n.I18n

	domainBlocklist       map[string]struct{}
	hasBlocklistWildcards bool
	hasBlocklist          bool

	domainAllowlist       map[string]struct{}
	hasAllowlistWildcards bool
	hasAllowlist          bool

	stop   chan bool
	status Status
	// rejected are the rows of the last import that the address rules refused (LST-53),
	// at most MaxRejectedRows of them. The counts in status are complete.
	rejected []RejectedRow
	// importSeq numbers the imports of this process, so that a link to the rejected rows
	// names the import it was shown for.
	importSeq int
	// owner is the user who started the import whose rejected rows are kept.
	owner int
	// verify decides the rows of a file in batches (LST-56); nil means the address rules of
	// step 1 only, row by row. The app sets it once it exists.
	verify func(emails []string) []AxmVerdict
	sync.RWMutex
}

// AxmVerdict is the answer of the address gate for one row: Reject is the reason the row is
// not imported ("" = it is), Warn the reason it is imported with a warning ("role", "unknown"),
// Suggest the corrected address of a misspelled domain.
type AxmVerdict struct {
	Reject, Warn, Suggest string
}

// SetAxmVerify sets the gate that decides the rows of a subscribe-mode import, a batch at a
// time. It is set once, before any import runs.
func (im *Importer) SetAxmVerify(fn func(emails []string) []AxmVerdict) {
	im.Lock()
	im.verify = fn
	im.Unlock()
}

// Options represents import options.
type Options struct {
	UpsertStmt         *sql.Stmt
	BlocklistStmt      *sql.Stmt
	UpdateListDateStmt *sql.Stmt
	PostCB             func(subject string, data any) error

	DomainBlocklist []string
	DomainAllowlist []string
}

// Session represents a single import session.
type Session struct {
	// id is the number of the import this session runs (Status.AxmImportID): a session that
	// has been replaced by a newer import writes nothing to the newer import's rejected rows.
	id       int
	im       *Importer
	subQueue chan SubReq
	log      *log.Logger

	opt SessionOpt
}

// SessionOpt represents the options for an importer session.
type SessionOpt struct {
	Filename           string `json:"filename"`
	Mode               string `json:"mode"`
	SubStatus          string `json:"subscription_status"`
	Overwrite          bool   `json:"overwrite"`
	OverwriteUserInfo  bool   `json:"overwrite_userinfo"`
	OverwriteSubStatus bool   `json:"overwrite_subscription_status"`
	Delim              string `json:"delim"`
	ListIDs            []int  `json:"lists"`

	// OwnerID is the user who starts the import (set by the handler, never read from the
	// request): only that user, or one who reads every subscriber, gets the rejected rows.
	OwnerID int `json:"-"`
	// AxmExcluded are the rows the column mapping screen left out before the upload (a bad
	// address, a dead domain, a misspelled one). They are counted with the rows the server
	// refuses, so that the box that ends the import is complete and survives a reload.
	AxmExcluded []ExcludedRow `json:"axm_excluded"`
	// AxmSourceRows says, for each row of the converted file the column mapping uploads, the
	// row it came from in the file the person chose. The rows the server refuses are then
	// numbered by the file the person knows.
	AxmSourceRows []int `json:"axm_source_rows"`
	// AxmFileRows is the number of records of the file the person chose, when the column
	// mapping uploads a converted file: the total of the import, as the raw upload of the
	// same file would count it (rows of the wrong width and repeated rows included).
	AxmFileRows int `json:"axm_file_rows"`
}

// ExcludedRow is a row the column mapping left out. Row is its row in the file the person chose.
type ExcludedRow struct {
	Row     int    `json:"row"`
	Email   string `json:"email"`
	Reason  string `json:"reason"`
	Suggest string `json:"suggest"`
}

// Status represents statistics from an ongoing import session.
type Status struct {
	Name     string `json:"name"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
	Status   string `json:"status"`
	// AxmRejected counts the rows that were not imported because the address was refused,
	// by reason (the Reject* constants). AxmRejectedTotal is their sum. See FORK.md, LST-53.
	AxmRejected      map[string]int `json:"axm_rejected,omitempty"`
	AxmRejectedTotal int            `json:"axm_rejected_total"`
	// AxmWarned counts the rows that were imported with a warning, by reason ("role",
	// "unknown"). AxmWarnedTotal is their sum. They are in Imported. See FORK.md, LST-56.
	AxmWarned      map[string]int `json:"axm_warned,omitempty"`
	AxmWarnedTotal int            `json:"axm_warned_total"`
	// AxmExcluded is how many of those rows the column mapping left out before the upload.
	AxmExcluded int `json:"axm_excluded"`
	// AxmImportID is the number of this import; the rejected rows are asked for by it.
	AxmImportID int `json:"axm_import_id"`
	logBuf      *bytes.Buffer
}

// The reasons a row is refused. format, provider and typo are the codes of addrcheck.
const (
	RejectFormat   = addrcheck.ClassFormat   // not an e-mail address
	RejectDomain   = "domain"                // the domain is blocked or not allowed by the settings
	RejectProvider = addrcheck.ClassProvider // breaks the mailbox name rules of the provider
	RejectTypo     = addrcheck.ClassTypo     // the domain is a known misspelling

	// LST-56: what the database and DNS say.
	RejectSuppressed = addrcheck.ClassSuppressed // blocklisted before
	RejectBounced    = addrcheck.ClassBounced    // a hard bounce or a complaint is on record
	RejectDisposable = addrcheck.ClassDisposable // a throwaway mailbox provider
	RejectNullMX     = addrcheck.ClassNullMX     // the domain accepts no mail
	RejectDead       = addrcheck.ClassDead       // no such domain, or no mail server
)

// gateBatch is how many rows of a file the gate decides at a time.
const gateBatch = 500

// MaxRejectedRows is how many refused rows an import keeps for the download.
const MaxRejectedRows = 50000

// RejectedRow is one refused row of the file. Row is the row of the uploaded file as a
// spreadsheet numbers it (the header is row 1, blank lines count, a cell with line breaks is
// one row), so that it is the same number the column mapping screen shows.
type RejectedRow struct {
	Row     int
	Email   string
	Reason  string
	Suggest string
}

// SubReq is a wrapper over the Subscriber model.
type SubReq struct {
	models.Subscriber
	Lists          []int    `json:"lists"`
	ListUUIDs      []string `json:"list_uuids"`
	PreconfirmSubs bool     `json:"preconfirm_subscriptions"`
	// AxmWarn is the warning the address gate gave the row ("role", "unknown"; LST-56). It is
	// counted when the row is committed, so a rolled back row is not counted.
	AxmWarn string `json:"-"`
	// axmFlush is not a row: the gate is about to read the database (LST-56), and the rows queued
	// so far are committed first, so that the import's own open transaction never keeps the
	// gate waiting for a connection (a pool of one connection would wait for good).
	axmFlush bool
}

type importStatusTpl struct {
	Name     string
	Status   string
	Imported int
	Total    int
	// AxmRejected: rows refused by the address rules, by reason (LST-53).
	AxmRejected map[string]int
	// AxmWarned: rows imported with a warning, by reason (LST-56).
	AxmWarned map[string]int
}

var (
	// ErrIsImporting is thrown when an import request is made while an
	// import is already running.
	ErrIsImporting = errors.New("import is already running")

	csvHeaders = map[string]bool{
		"email":      true,
		"name":       true,
		"attributes": true}

	regexCleanStr = regexp.MustCompile("[[:^ascii:]]")
)

// New returns a new instance of Importer.
func New(opt Options, db *sql.DB, i *i18n.I18n) *Importer {
	im := Importer{
		opt:             opt,
		db:              db,
		i18n:            i,
		domainBlocklist: make(map[string]struct{}, len(opt.DomainBlocklist)),
		domainAllowlist: make(map[string]struct{}, len(opt.DomainAllowlist)),
		status:          Status{Status: StatusNone, logBuf: bytes.NewBuffer(nil)},
		stop:            make(chan bool, 1),
		// A number no earlier run of the app has used (milliseconds: a run numbers its imports
		// one by one, far slower than that), so that a link kept from before a restart cannot
		// name another import.
		importSeq: int(time.Now().UnixMilli()),
	}

	// Domain blocklist.
	mp, hasWildcards := makeDomainMap(opt.DomainBlocklist)
	im.domainBlocklist = mp
	im.hasBlocklistWildcards = hasWildcards
	im.hasBlocklist = len(mp) > 0

	// Domain allowlist.
	mp, hasWildcards = makeDomainMap(opt.DomainAllowlist)
	im.domainAllowlist = mp
	im.hasAllowlistWildcards = hasWildcards
	im.hasAllowlist = len(mp) > 0

	return &im
}

// NewSession returns an new instance of Session. It takes the name
// of the uploaded file, but doesn't do anything with it but retains it for stats.
func (im *Importer) NewSession(opt SessionOpt) (*Session, error) {
	if !im.isDone() {
		return nil, errors.New("an import is already running")
	}

	// For API backwards compatibility, if the old 'overwrite'
	// field is set, set both overwrite fields to true.
	if opt.Overwrite {
		opt.OverwriteUserInfo = true
		opt.OverwriteSubStatus = true
	}

	// A stop request that arrived after the last import had read its file is of that import.
	select {
	case <-im.stop:
	default:
	}
	im.Lock()
	im.status = Status{Status: StatusImporting,
		Name:        opt.Filename,
		AxmRejected: map[string]int{},
		logBuf:      bytes.NewBuffer(nil)}
	im.rejected = nil
	im.importSeq++
	im.status.AxmImportID = im.importSeq
	im.owner = opt.OwnerID
	seq := im.importSeq
	im.Unlock()
	for _, r := range opt.AxmExcluded {
		im.rejectExcluded(seq, r)
	}

	s := &Session{
		im:       im,
		log:      log.New(im.status.logBuf, "", log.Ldate|log.Ltime|log.Lmicroseconds|log.Lshortfile),
		subQueue: make(chan SubReq, commitBatchSize),
		opt:      opt,
		id:       seq,
	}

	s.log.Printf("processing '%s'", opt.Filename)
	return s, nil
}

// GetStats returns the global Stats of the importer.
func (im *Importer) GetStats() Status {
	im.RLock()
	defer im.RUnlock()

	var rejected map[string]int
	if im.status.AxmRejectedTotal > 0 {
		rejected = make(map[string]int, len(im.status.AxmRejected))
		for k, v := range im.status.AxmRejected {
			rejected[k] = v
		}
	}
	var warned map[string]int
	if im.status.AxmWarnedTotal > 0 {
		warned = make(map[string]int, len(im.status.AxmWarned))
		for k, v := range im.status.AxmWarned {
			warned[k] = v
		}
	}
	return Status{
		Name:             im.status.Name,
		Status:           im.status.Status,
		Total:            im.status.Total,
		Imported:         im.status.Imported,
		AxmRejected:      rejected,
		AxmRejectedTotal: im.status.AxmRejectedTotal,
		AxmWarned:        warned,
		AxmWarnedTotal:   im.status.AxmWarnedTotal,
		AxmExcluded:      im.status.AxmExcluded,
		AxmImportID:      im.importSeq,
	}
}

// Why GetRejected gives no rows.
var (
	ErrRejectedGone   = errors.New("the rejected rows belong to a newer import")
	ErrRejectedDenied = errors.New("the rejected rows belong to another user")
)

// GetRejected returns the rows that import number id refused. The owner and the number are
// checked together with the rows, under one lock: a newer import cannot slip in between.
// all lets a user who reads every subscriber take the rows of another user's import.
func (im *Importer) GetRejected(id, userID int, all bool) ([]RejectedRow, error) {
	im.RLock()
	defer im.RUnlock()
	if id != im.importSeq {
		return nil, ErrRejectedGone
	}
	if !all && userID != im.owner {
		return nil, ErrRejectedDenied
	}
	return append([]RejectedRow(nil), im.rejected...), nil
}

// rejectExcluded counts a row that the column mapping left out. Only the reasons the screen
// gives are taken, and the values are cut to what a download can carry.
func (im *Importer) rejectExcluded(session int, r ExcludedRow) {
	switch r.Reason {
	case RejectFormat, RejectProvider, RejectTypo, RejectDead, RejectNullMX,
		RejectSuppressed, RejectBounced, RejectDisposable:
	default:
		return
	}
	im.Lock()
	im.status.AxmExcluded++
	im.Unlock()
	im.reject(session, RejectedRow{Row: r.Row, Email: clip(r.Email), Reason: r.Reason, Suggest: clip(r.Suggest)})
}

// reject counts a refused row and keeps it for the download.
func (im *Importer) reject(session int, row RejectedRow) {
	im.Lock()
	defer im.Unlock()
	if session != im.importSeq {
		return // a newer import has started: this row is not its row
	}
	if im.status.AxmRejected == nil {
		im.status.AxmRejected = map[string]int{}
	}
	im.status.AxmRejected[row.Reason]++
	im.status.AxmRejectedTotal++
	if len(im.rejected) < MaxRejectedRows {
		im.rejected = append(im.rejected, row)
	}
}

// addWarned counts rows that were imported with a warning, by reason. It is called when the
// rows are committed.
func (im *Importer) addWarned(session int, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	im.Lock()
	defer im.Unlock()
	if session != im.importSeq {
		return
	}
	if im.status.AxmWarned == nil {
		im.status.AxmWarned = map[string]int{}
	}
	for reason, n := range counts {
		im.status.AxmWarned[reason] += n
		im.status.AxmWarnedTotal += n
	}
}

// GetLogs returns the log entries of the last import session.
func (im *Importer) GetLogs() []byte {
	im.RLock()
	defer im.RUnlock()

	if im.status.logBuf == nil {
		return []byte{}
	}

	return im.status.logBuf.Bytes()
}

// setStatus sets the Importer's status.
func (im *Importer) setStatus(status string) {
	im.Lock()
	im.status.Status = status
	im.Unlock()
}

// getStatus get's the Importer's status.
func (im *Importer) getStatus() string {
	im.RLock()
	status := im.status.Status
	im.RUnlock()
	return status
}

// isDone returns true if the importer is working (importing|stopping).
func (im *Importer) isDone() bool {
	s := true
	im.RLock()
	if im.getStatus() == StatusImporting || im.getStatus() == StatusStopping {
		s = false
	}
	im.RUnlock()

	return s
}

// incrementImportCount sets the Importer's "imported" counter.
func (im *Importer) incrementImportCount(n int) {
	im.Lock()
	im.status.Imported += n
	im.Unlock()
}

// sendNotif sends admin notifications for import completions.
func (im *Importer) sendNotif(status string) error {
	var (
		s   = im.GetStats()
		out = importStatusTpl{
			Name:     s.Name,
			Status:   status,
			Imported: s.Imported,
			Total:    s.Total,

			AxmRejected: s.AxmRejected,
			AxmWarned:   s.AxmWarned,
		}
		subject = fmt.Sprintf("%s: %s import", cases.Title(language.Und).String(status), s.Name)
	)
	return im.opt.PostCB(subject, out)
}

// Start is a blocking function that selects on a channel queue until all
// subscriber entries in the import session are imported. It should be
// invoked as a goroutine.
func (s *Session) Start() {
	var (
		tx    *sql.Tx
		stmt  *sql.Stmt
		err   error
		total = 0
		cur   = 0
	)

	listIDs := make([]int, len(s.opt.ListIDs))
	copy(listIDs, s.opt.ListIDs)

	// warned counts the rows of the open transaction that came in with a warning (LST-56).
	warned := map[string]int{}
	for sub := range s.subQueue {
		if sub.axmFlush {
			if cur > 0 {
				if err := tx.Commit(); err != nil {
					tx.Rollback()
					s.log.Printf("error committing to DB: %v", err)
				} else {
					s.im.incrementImportCount(cur)
					s.im.addWarned(s.id, warned)
				}
				cur = 0
			}
			continue
		}
		if cur == 0 {
			warned = map[string]int{}
			// New transaction batch.
			tx, err = s.im.db.Begin()
			if err != nil {
				s.log.Printf("error creating DB transaction: %v", err)
				continue
			}

			if s.opt.Mode == ModeSubscribe {
				stmt = tx.Stmt(s.im.opt.UpsertStmt)
			} else {
				stmt = tx.Stmt(s.im.opt.BlocklistStmt)
			}
		}

		uu, err := uuid.NewV4()
		if err != nil {
			s.log.Printf("error generating UUID: %v", err)
			tx.Rollback()
			break
		}

		if s.opt.Mode == ModeSubscribe {
			_, err = stmt.Exec(uu, sub.Email, sub.Name, sub.Attribs, pq.Array(listIDs), s.opt.SubStatus, s.opt.OverwriteUserInfo, s.opt.OverwriteSubStatus)
		} else if s.opt.Mode == ModeBlocklist {
			_, err = stmt.Exec(uu, sub.Email, sub.Name, sub.Attribs)
		}
		if err != nil {
			s.log.Printf("error executing insert: %v", err)
			tx.Rollback()
			break
		}
		cur++
		total++
		if sub.AxmWarn != "" {
			warned[sub.AxmWarn]++
		}

		// Batch size is met. Commit.
		if cur%commitBatchSize == 0 {
			if err := tx.Commit(); err != nil {
				tx.Rollback()
				s.log.Printf("error committing to DB: %v", err)
			} else {
				s.im.incrementImportCount(cur)
				s.im.addWarned(s.id, warned)
				s.log.Printf("imported %d", total)
			}

			cur = 0
		}
	}

	// Queue's closed and there's nothing left to commit.
	if cur == 0 {
		s.im.setStatus(StatusFinished)
		s.log.Printf("imported finished")
		if _, err := s.im.opt.UpdateListDateStmt.Exec(pq.Array(listIDs)); err != nil {
			s.log.Printf("error updating lists date: %v", err)
		}
		s.im.sendNotif(StatusFinished)
		return
	}

	// Queue's closed and there are records left to commit.
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		s.im.setStatus(StatusFailed)
		s.log.Printf("error committing to DB: %v", err)
		s.im.sendNotif(StatusFailed)
		return
	}

	s.im.incrementImportCount(cur)
	s.im.addWarned(s.id, warned)
	s.im.setStatus(StatusFinished)
	s.log.Printf("imported finished")
	if _, err := s.im.opt.UpdateListDateStmt.Exec(pq.Array(listIDs)); err != nil {
		s.log.Printf("error updating lists date: %v", err)
	}

	s.im.sendNotif(StatusFinished)
}

// Stop stops an active import session.
func (s *Session) Stop() {
	close(s.subQueue)
}

// ExtractZIP takes a ZIP file's path and extracts all .csv files in it to
// a temporary directory, and returns the name of the temp directory and the
// list of extracted .csv files.
func (s *Session) ExtractZIP(srcPath string, maxCSVs int) (string, []string, error) {
	if s.im.isDone() {
		return "", nil, ErrIsImporting
	}

	failed := true
	defer func() {
		if failed {
			s.im.setStatus(StatusFailed)
		}
	}()

	z, err := zip.OpenReader(srcPath)
	if err != nil {
		return "", nil, err
	}
	defer z.Close()

	// Create a temporary directory to extract the files.
	dir, err := os.MkdirTemp("", "listmonk")
	if err != nil {
		s.log.Printf("error creating temporary directory for extracting ZIP: %v", err)
		return "", nil, err
	}

	files := make([]string, 0, len(z.File))
	for _, f := range z.File {
		fName := f.FileInfo().Name()

		// Skip directories.
		if f.FileInfo().IsDir() {
			s.log.Printf("skipping directory '%s'", fName)
			continue
		}

		// Skip files without the .csv extension.
		if !strings.HasSuffix(strings.ToLower(fName), ".csv") {
			s.log.Printf("skipping non .csv file '%s'", fName)
			continue
		}

		// Sanitize the file name to prevent ZIP slip path traversal.
		fName = filepath.Base(fName)

		s.log.Printf("extracting '%s'", fName)
		src, err := f.Open()
		if err != nil {
			s.log.Printf("error opening '%s' from ZIP: '%v'", fName, err)
			return "", nil, err
		}
		defer src.Close()

		out, err := os.OpenFile(dir+"/"+fName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			s.log.Printf("error creating '%s/%s': '%v'", dir, fName, err)
			return "", nil, err
		}
		defer out.Close()

		if _, err := io.Copy(out, src); err != nil {
			s.log.Printf("error extracting to '%s/%s': '%v'", dir, fName, err)
			return "", nil, err
		}
		s.log.Printf("extracted '%s'", fName)

		files = append(files, fName)
		if len(files) > maxCSVs {
			s.log.Printf("won't extract any more files. Maximum is %d", maxCSVs)
			break
		}
	}

	if len(files) == 0 {
		s.log.Println("no CSV files found in the ZIP")
		return "", nil, errors.New("no CSV files found in the ZIP")
	}

	failed = false
	return dir, files, nil
}

// LoadCSV loads a CSV file and validates and imports the subscriber entries in it.
func (s *Session) LoadCSV(srcPath string, delim rune) error {
	if s.im.isDone() {
		return ErrIsImporting
	}

	// Default status is "failed" in case the function
	// returns at one of the many possible errors.
	failed := true
	defer func() {
		if failed {
			s.im.setStatus(StatusFailed)
		}
	}()

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}

	// Count the total number of lines in the file. This doesn't distinguish
	// between "blank" and non "blank" lines, and is only used to derive
	// the progress percentage for the frontend.
	numLines, err := countLines(f)
	if err != nil {
		s.log.Printf("error counting lines in '%s': '%v'", srcPath, err)
		return err
	}

	if numLines == 0 {
		return errors.New("empty file")
	}

	// Exclude the header from count. The rows the column mapping left out for their address
	// are not in the file but are rows of the import, as their refusal is counted: the total
	// is on the same footing as the raw upload of the same file (LST-53).
	s.im.Lock()
	s.im.status.Total = numLines - 1
	if s.id == s.im.importSeq {
		s.im.status.Total += s.im.status.AxmExcluded
	}
	s.im.Unlock()

	// Rewind, now that we've done a linecount on the same handler.
	_, _ = f.Seek(0, 0)
	rd := csv.NewReader(f)
	rd.Comma = delim

	// Read the header.
	csvHdr, err := rd.Read()
	if err != nil {
		s.log.Printf("error reading header from '%s': '%v'", srcPath, err)
		return err
	}
	rows := sheetRows{}
	rows.next(rd, csvHdr, nil)

	hdrKeys := s.mapCSVHeaders(csvHdr, csvHeaders)
	// email is a required header.
	if _, ok := hdrKeys["email"]; !ok {
		s.log.Printf("'email' column not found in '%s'", srcPath)
		return errors.New("'email' column not found")
	}

	var (
		lnHdr = len(hdrKeys)
		i     = 0
		rec   = 0 // the records read so far, the header not counted
		// batch holds the rows the address gate has not decided yet (LST-56). The gate looks
		// at the database and at DNS, so it takes a batch at a time; the rows leave it, and
		// reach the queue, in the order of the file.
		batch []gateRow
	)
	flush := func() {
		s.decide(batch)
		batch = batch[:0]
	}
	// A read error ends the import as failed, but the rows read before it were queued before
	// this change and are still.
	defer func() {
		if failed {
			flush()
		}
	}()
	for {
		i++

		// Check for the stop signal.
		select {
		case <-s.im.stop:
			failed = false
			flush() // the rows already read are decided and queued, as they were row by row
			close(s.subQueue)
			s.log.Println("stop request received")
			return nil
		default:
		}

		cols, err := rd.Read()
		// The line of the file the row starts on: the reader skips blank lines, so counting
		// rows would run behind a spreadsheet's numbers.
		rec++
		fileLine := rows.next(rd, cols, err)
		if err == io.EOF {
			// The line count above is only a first guess: a cell with line breaks is one
			// record over several lines. Once every record is read, the total is the records
			// of the file (rows of the wrong width included, blank lines not). For the column
			// mapping it is the records of the file the person chose, so that both uploads of
			// one file give the same total; never less than what this import has counted.
			s.im.Lock()
			if s.id == s.im.importSeq {
				total := rec - 1 + s.im.status.AxmExcluded
				if s.opt.AxmFileRows > total {
					total = s.opt.AxmFileRows
				}
				s.im.status.Total = total
			}
			s.im.Unlock()
			break
		} else if err != nil {
			if err, ok := err.(*csv.ParseError); ok && err.Err == csv.ErrFieldCount {
				s.log.Printf("skipping line %d. %v", i, err)
				continue
			} else {
				s.log.Printf("error reading CSV '%s'", err)
				return err
			}
		}

		lnCols := len(cols)
		if lnCols < lnHdr {
			s.log.Printf("skipping line %d. column count (%d) does not match minimum header count (%d)", i, lnCols, lnHdr)
			continue
		}

		// Iterate the key map and based on the indices mapped earlier,
		// form a map of key: csv_value, eg: email: user@user.com.
		row := make(map[string]string, lnCols)
		for key := range hdrKeys {
			row[key] = cols[hdrKeys[key]]
		}

		sub := SubReq{}
		sub.Email = row["email"]

		if v, ok := row["name"]; ok {
			sub.Name = v
		}

		rawEmail := sub.Email
		sub, err = s.im.ValidateFields(sub)
		if err != nil {
			s.log.Printf("skipping line %d: %v: %v", i, err, cols)
			reason := RejectDomain
			if _, ferr := utils.SanitizeEmail(rawEmail); ferr != nil || len(rawEmail) > 1000 {
				reason = RejectFormat
			}
			row := RejectedRow{Row: s.rowOf(rec, fileLine), Email: clip(rawEmail), Reason: reason}
			if s.opt.Mode == ModeSubscribe && s.im.verify != nil {
				// Kept in the batch, so that the refused rows of the download keep the order of the file.
				batch = append(batch, gateRow{pre: &row})
				if len(batch) >= gateBatch {
					flush()
				}
			} else {
				s.im.reject(s.id, row)
			}
			continue
		}

		// An address that cannot receive mail is not added to a list (LST-53). Blocklisting
		// takes any address: the point there is to keep it out.
		if s.opt.Mode == ModeSubscribe && s.im.verify == nil {
			if reason, suggest := addrcheck.Rules(sub.Email); reason != "" {
				// The log is shown to everyone who may import: the address is not written to it.
				s.log.Printf("skipping line %d: address refused (%s)", i, reason)
				s.im.reject(s.id, RejectedRow{Row: s.rowOf(rec, fileLine), Email: sub.Email, Reason: reason, Suggest: suggest})
				continue
			}
		}

		// JSON attributes.
		if len(row["attributes"]) > 0 {
			var (
				attribs models.JSON
				b       = []byte(row["attributes"])
			)
			if err := json.Unmarshal(b, &attribs); err != nil {
				s.log.Printf("skipping invalid attributes JSON on line %d: %v", i, err)
			} else {
				sub.Attribs = attribs
			}
		}

		// Send the subscriber to the queue, after the address gate has decided it (LST-56).
		if s.opt.Mode == ModeSubscribe && s.im.verify != nil {
			batch = append(batch, gateRow{sub: sub, row: s.rowOf(rec, fileLine), line: i})
			if len(batch) >= gateBatch {
				flush()
			}
			continue
		}
		s.subQueue <- sub
	}

	flush()
	close(s.subQueue)
	failed = false
	// A stop request that came while the last batch waited for DNS has nothing left to stop. Left
	// in the channel it would cancel the next import.
	select {
	case <-s.im.stop:
	default:
	}

	return nil
}

// gateRow is a row of the file waiting for the address gate.
type gateRow struct {
	sub  SubReq
	row  int // the row number it is reported under
	line int // the line of the file, for the log
	// pre is a row that was refused before the gate (malformed, or a domain the settings do
	// not allow): the gate does not look at it, it is only kept in order.
	pre *RejectedRow
}

// decide asks the address gate about a batch of rows and queues the rows that may come in,
// in order. A refused row is counted and kept for the download; a row that comes in with a
// warning is counted. The log gets the reason code only: it is shown to everyone who may import.
func (s *Session) decide(rows []gateRow) {
	if len(rows) == 0 {
		return
	}
	var emails []string
	at := make([]int, len(rows)) // the position in emails of each row the gate decides
	for i, r := range rows {
		if r.pre != nil {
			at[i] = -1
			continue
		}
		at[i] = len(emails)
		emails = append(emails, r.sub.Email)
	}
	var verdicts []AxmVerdict
	if len(emails) > 0 {
		// The writer commits what it has before the gate asks the database for a connection. It
		// begins its next transaction only with the next row, which comes after the gate is done.
		s.subQueue <- SubReq{axmFlush: true}
		verdicts = s.im.verify(emails)
	}
	for i, r := range rows {
		if r.pre != nil {
			s.im.reject(s.id, *r.pre)
			continue
		}
		var v AxmVerdict
		if at[i] < len(verdicts) {
			v = verdicts[at[i]]
		}
		if v.Reject != "" {
			s.log.Printf("skipping line %d: address refused (%s)", r.line, v.Reject)
			s.im.reject(s.id, RejectedRow{Row: r.row, Email: r.sub.Email, Reason: v.Reject, Suggest: v.Suggest})
			continue
		}
		r.sub.AxmWarn = v.Warn
		s.subQueue <- r.sub
	}
}

// sheetRows numbers the records a csv.Reader returns as a spreadsheet numbers rows: blank
// lines count, and a cell with line breaks in it is one row. A record's row is the line it
// starts on less the line breaks inside the cells read before it.
type sheetRows struct{ embedded int }

// next gives the row of the record Read just returned, or 0 when it returned none. A record
// of the wrong width comes back with ErrFieldCount and is skipped, but its line breaks are
// counted too, or every later row would be numbered too high.
func (r *sheetRows) next(rd *csv.Reader, cols []string, err error) int {
	if err != nil && !errors.Is(err, csv.ErrFieldCount) {
		return 0
	}
	line, _ := rd.FieldPos(0)
	row := line - r.embedded
	r.embedded += countLineBreaks(cols)
	return row
}

// countLineBreaks counts the line breaks inside the cells of a record.
func countLineBreaks(cols []string) int {
	n := 0
	for _, c := range cols {
		n += strings.Count(c, "\n")
	}
	return n
}

// rowOf is the row number a refused record is reported under: its row in the file the person
// chose when the column mapping sent that, else its row in the uploaded file.
func (s *Session) rowOf(record, fileLine int) int {
	if record >= 1 && record <= len(s.opt.AxmSourceRows) {
		return s.opt.AxmSourceRows[record-1]
	}
	return fileLine
}

// clipLen is the longest address kept for a download: an address over 1000 characters is
// refused as malformed anyway, and a corrected one is not longer than the address.
const clipLen = 1000

// clip keeps a refused value to a length a download can carry.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > clipLen {
		return s[:clipLen]
	}
	return s
}

// Stop sends a signal to stop the existing import.
func (im *Importer) Stop() {
	if im.getStatus() != StatusImporting {
		im.Lock()
		im.status = Status{Status: StatusNone}
		im.Unlock()

		return
	}

	select {
	case im.stop <- true:
		im.setStatus(StatusStopping)
	default:
	}
}

// SanitizeEmail validates and sanitizes an e-mail string and returns the
// canonical (lowercased, trimmed) address. Domain allowlist/blocklist rules
// are enforced on top of the bare-address validation in utils.SanitizeEmail.
func (im *Importer) SanitizeEmail(email string) (string, error) {
	addr, err := utils.SanitizeEmail(email)
	if err != nil {
		return "", errors.New(im.i18n.T("subscribers.invalidEmail"))
	}

	// Check if the e-mail's domain is blocklisted. The e-mail domain and blocklist config
	// are always lowercase.
	if im.hasAllowlist || im.hasBlocklist {
		d := strings.Split(addr, "@")
		if len(d) != 2 {
			return addr, nil
		}

		domain := d[1]

		// If there's an allowlist, check if the domain is in it. Checking blocklist after that is moot.
		if im.hasAllowlist {
			if !im.checkInList(domain, im.hasAllowlistWildcards, im.domainAllowlist) {
				return "", errors.New(im.i18n.T("subscribers.domainBlocklisted"))
			}
		} else if im.hasBlocklist {
			if im.checkInList(domain, im.hasBlocklistWildcards, im.domainBlocklist) {
				return "", errors.New(im.i18n.T("subscribers.domainBlocklisted"))
			}
		}
	}

	return addr, nil
}

// ValidateFields validates incoming subscriber field values and returns sanitized fields.
func (im *Importer) ValidateFields(s SubReq) (SubReq, error) {
	if len(s.Email) > 1000 {
		return s, errors.New(im.i18n.T("subscribers.invalidEmail"))
	}

	em, err := im.SanitizeEmail(s.Email)
	if err != nil {
		return s, err
	}
	s.Email = strings.ToLower(em)

	// If there's no name, use the name part of the e-mail.
	s.Name = strings.TrimSpace(s.Name)
	if len(s.Name) == 0 {
		name := strings.ToLower(strings.Split(s.Email, "@")[0])

		parts := strings.Fields(strings.ReplaceAll(name, ".", " "))
		for n, p := range parts {
			parts[n] = cases.Title(language.Und).String(p)
		}

		s.Name = strings.Join(parts, " ")
	}

	return s, nil
}

// Check the domain against the given map of domains (block/allowlist).
func (im *Importer) checkInList(domain string, hasWildcards bool, mp map[string]struct{}) bool {
	// Check the domain as-is.
	if _, ok := mp[domain]; ok {
		return true
	}

	// If there are wildcards in the list and the email domain has a subdomain, check that.
	if hasWildcards && strings.Count(domain, ".") > 1 {
		parts := strings.Split(domain, ".")

		// Replace the first part of the subdomain with * and check if that exists in the list.
		// Eg: test.mail.example.com => *.mail.example.com
		parts[0] = "*"
		domain = strings.Join(parts, ".")

		if _, ok := mp[domain]; ok {
			return true
		}
	}

	return false
}

// mapCSVHeaders takes a list of headers obtained from a CSV file, a map of known headers,
// and returns a new map with each of the headers in the known map mapped by the position (0-n)
// in the given CSV list.
func (s *Session) mapCSVHeaders(csvHdrs []string, knownHdrs map[string]bool) map[string]int {
	// Map 0-n column index to the header keys, name: 0, email: 1 etc.
	// This is to allow dynamic ordering of columns in th CSV.
	hdrKeys := make(map[string]int)
	for i, h := range csvHdrs {
		// Clean the string of non-ASCII characters (BOM etc.).
		h := regexCleanStr.ReplaceAllString(strings.TrimSpace(h), "")
		if _, ok := knownHdrs[h]; !ok {
			s.log.Printf("ignoring unknown header '%s'", h)
			continue
		}
		hdrKeys[h] = i
	}

	return hdrKeys
}

// countLines counts the number of line breaks in a file. This does not
// distinguish between "blank" and non "blank" lines.
// Credit: https://stackoverflow.com/a/24563853
func countLines(r io.Reader) (int, error) {
	var (
		buf      = make([]byte, 32*1024)
		count    = 0
		lineSep  = byte('\n')
		lastByte byte
	)

	for {
		c, err := r.Read(buf)
		if c > 0 {
			count += bytes.Count(buf[:c], []byte{lineSep})
			lastByte = buf[c-1]
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
	}

	if lastByte != 0 && lastByte != lineSep {
		count++
	}

	return count, nil
}

func makeDomainMap(domains []string) (map[string]struct{}, bool) {
	var (
		out          = make(map[string]struct{}, len(domains))
		hasWildCards = false
	)
	for _, d := range domains {
		out[d] = struct{}{}

		// Domains with *. as the subdomain prefix, strip that
		// and add the full domain to the blocklist as well.
		// eg: *.example.com => example.com
		if strings.Contains(d, "*.") {
			hasWildCards = true
			out[strings.TrimPrefix(d, "*.")] = struct{}{}
		}
	}

	return out, hasWildCards
}
