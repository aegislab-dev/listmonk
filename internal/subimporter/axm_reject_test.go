package subimporter

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/i18n"
)

// LST-53: what the importer keeps of the rows it refuses.
func TestRejectedRowsBelongToTheirImport(t *testing.T) {
	im := &Importer{status: Status{AxmRejected: map[string]int{}}, importSeq: 7}
	im.reject(7, RejectedRow{Row: 2, Email: "a@gmail.con", Reason: RejectTypo, Suggest: "a@gmail.com"})
	// A session of an import that a newer one replaced writes nothing.
	im.importSeq = 8
	im.owner = 5
	im.status = Status{AxmRejected: map[string]int{}}
	im.rejected = nil
	im.reject(7, RejectedRow{Row: 3, Email: "old@gmail.con", Reason: RejectTypo})
	if im.status.AxmRejectedTotal != 0 || len(im.rejected) != 0 {
		t.Fatalf("a replaced session counted a row: %+v %v", im.status, im.rejected)
	}
	im.reject(8, RejectedRow{Row: 3, Email: "new@gmail.con", Reason: RejectTypo})
	if rows, err := im.GetRejected(8, 5, false); err != nil || len(rows) != 1 || rows[0].Email != "new@gmail.con" {
		t.Fatalf("rows %v %v", rows, err)
	}
	if _, err := im.GetRejected(8, 6, false); err != ErrRejectedDenied {
		t.Fatalf("another user: %v", err)
	}
	if _, err := im.GetRejected(8, 6, true); err != nil {
		t.Fatalf("a user who reads all: %v", err)
	}
	if _, err := im.GetRejected(7, 5, false); err != ErrRejectedGone {
		t.Fatalf("an old number: %v", err)
	}
}

func TestLongAddressesAreKeptWhole(t *testing.T) {
	long := strings.Repeat("a", 214) + "@gmail.con"
	im := &Importer{status: Status{AxmRejected: map[string]int{}}, importSeq: 1}
	im.rejectExcluded(1, ExcludedRow{Row: 4, Email: long, Reason: RejectTypo, Suggest: strings.TrimSuffix(long, "n")})
	if got := im.rejected[0]; got.Email != long || got.Suggest != strings.TrimSuffix(long, "n") {
		t.Fatalf("cut: %d %d", len(got.Email), len(got.Suggest))
	}
}

func TestRowNumbersAreSpreadsheetRows(t *testing.T) {
	// 1 header, 2 address, 3 a name with a line break and an extra column (wrong width),
	// 4 blank line, 5 a name with a line break, 6 address.
	in := "email,name\na@x.example,A\nb@x.example,\"two\nlines\",extra\n\nc@x.example,\"also\ntwo\"\nd@gmail.con,D\n"
	rd := csv.NewReader(strings.NewReader(in))
	rows := sheetRows{}
	hdr, _ := rd.Read()
	rows.next(rd, hdr, nil)
	var got []int
	for {
		cols, err := rd.Read()
		if err == io.EOF {
			break
		}
		got = append(got, rows.next(rd, cols, err))
	}
	if want := []int{2, 3, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
}

func TestRowNumbersFollowTheChosenFile(t *testing.T) {
	s := &Session{opt: SessionOpt{AxmSourceRows: []int{2, 5, 9}}}
	if s.rowOf(2, 3) != 5 || s.rowOf(3, 4) != 9 {
		t.Fatal("the mapped rows are numbered by their source")
	}
	if s.rowOf(4, 12) != 12 || (&Session{}).rowOf(1, 6) != 6 {
		t.Fatal("without a source the line of the uploaded file is used")
	}
}

// The total of an import is the records of the file, not its lines, and a mapped upload counts
// the rows the column mapping left out, as the raw upload of the same file counts them.
func TestImportTotalIsTheFileRecords(t *testing.T) {
	load := func(body string, opt SessionOpt) Status {
		t.Helper()
		path := filepath.Join(t.TempDir(), "in.csv")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		im := &Importer{status: Status{Status: StatusNone}, importSeq: 1}
		opt.Mode = ModeSubscribe
		s, err := im.NewSession(opt)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.LoadCSV(path, ','); err != nil {
			t.Fatal(err)
		}
		return im.GetStats()
	}
	excluded := []ExcludedRow{{Row: 3, Email: "not-an-address", Reason: RejectFormat}}

	// Raw: three records over six lines, and a blank line.
	if st := load("email,name\ngood@example.org,\"a\nb\"\n\nbad@gmail.con,\"c\nd\"\nabc@gmail.com,\"e\nf\"\n", SessionOpt{}); st.Total != 3 || st.AxmRejectedTotal != 2 {
		t.Fatalf("raw: total %d, refused %d", st.Total, st.AxmRejectedTotal)
	}
	// Mapped: two records uploaded and one left out.
	if st := load("email,name\ngood@example.org,\"a\nb\"\nbad@gmail.con,B\n", SessionOpt{AxmExcluded: excluded}); st.Total != 3 || st.AxmRejectedTotal != 2 || st.AxmExcluded != 1 {
		t.Fatalf("mapped: total %d, refused %d, left out %d", st.Total, st.AxmRejectedTotal, st.AxmExcluded)
	}
	// Mapped, with the records of the chosen file (a row of the wrong width was not uploaded).
	if st := load("email,name\ngood@example.org,A\nbad@gmail.con,B\n", SessionOpt{AxmExcluded: excluded, AxmFileRows: 4}); st.Total != 4 {
		t.Fatalf("mapped with file rows: total %d", st.Total)
	}
}

// LST-56: the rows of a file go through the address gate a batch at a time, and what comes
// out keeps the order and the row numbers of the file.
func TestGateBatchesKeepOrderAndRows(t *testing.T) {
	for _, n := range []int{1, 499, 500, 501, 1203} {
		var sizes []int
		verify := func(emails []string) []AxmVerdict {
			sizes = append(sizes, len(emails))
			out := make([]AxmVerdict, len(emails))
			for i, e := range emails {
				switch {
				case strings.HasPrefix(e, "dead"):
					out[i].Reject = RejectDead
				case strings.HasPrefix(e, "typo"):
					out[i].Reject, out[i].Suggest = RejectTypo, "fixed@example.org"
				case strings.HasPrefix(e, "role"):
					out[i].Warn = "role"
				case strings.HasPrefix(e, "slow"):
					out[i].Warn = "unknown"
				}
			}
			return out
		}
		var in strings.Builder
		in.WriteString("email,name\n")
		var want []string
		wantDead, wantTyo, wantRole, wantSlow := 0, 0, 0, 0
		var deadRows []int
		for i := 0; i < n; i++ {
			prefix := "ok"
			switch i % 7 {
			case 3:
				prefix, wantDead = "dead", wantDead+1
				deadRows = append(deadRows, i+2)
			case 5:
				prefix, wantTyo = "typo", wantTyo+1
			case 6:
				prefix, wantRole = "role", wantRole+1
			case 1:
				prefix, wantSlow = "slow", wantSlow+1
			}
			email := prefix + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + "-" + itoa(i) + "@example.org"
			in.WriteString(email + ",N\n")
			if prefix != "dead" && prefix != "typo" {
				want = append(want, email)
			}
		}
		path := filepath.Join(t.TempDir(), "in.csv")
		if err := os.WriteFile(path, []byte(in.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		im := &Importer{status: Status{Status: StatusNone}, importSeq: 1}
		im.SetAxmVerify(verify)
		s, err := im.NewSession(SessionOpt{Mode: ModeSubscribe})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		warned := map[string]int{}
		done := make(chan struct{})
		go func() {
			for sub := range s.subQueue {
				if sub.axmFlush {
					// Not a row: the commit asked for before each gate batch.
					continue
				}
				got = append(got, sub.Email)
				if sub.AxmWarn != "" {
					warned[sub.AxmWarn]++
				}
			}
			close(done)
		}()
		if err := s.LoadCSV(path, ','); err != nil {
			t.Fatal(err)
		}
		<-done
		st := im.GetStats()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("n=%d: the queued rows are not the accepted rows in order (%d vs %d)", n, len(got), len(want))
		}
		if st.Total != n || st.AxmRejected[RejectDead] != wantDead || st.AxmRejected[RejectTypo] != wantTyo ||
			st.AxmRejectedTotal != wantDead+wantTyo {
			t.Fatalf("n=%d: stats %+v", n, st)
		}
		// The rows carry their warning to the commit, which counts them (a row that is rolled
		// back is not counted); nothing is counted before that.
		if warned["role"] != wantRole || warned["unknown"] != wantSlow || st.AxmWarnedTotal != 0 {
			t.Fatalf("n=%d: warned %+v, counted %d", n, warned, st.AxmWarnedTotal)
		}
		for _, sz := range sizes[:max(0, len(sizes)-1)] {
			if sz != gateBatch {
				t.Fatalf("n=%d: batch sizes %v", n, sizes)
			}
		}
		rows, _ := im.GetRejected(st.AxmImportID, 0, true)
		// The first refused row is the first dead row of the file, by its row number.
		if len(deadRows) > 0 {
			found := false
			for _, r := range rows {
				if r.Reason == RejectDead && r.Row == deadRows[0] {
					found = true
				}
			}
			if !found {
				t.Fatalf("n=%d: the dead row %d is not reported under its file row: %v", n, deadRows[0], rows[:min(3, len(rows))])
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Without a gate the rules of step 1 apply row by row, as before.
func TestNoGateKeepsTheRulesOfStepOne(t *testing.T) {
	im := &Importer{status: Status{Status: StatusNone}, importSeq: 1}
	path := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(path, []byte("email,name\na@gmail.con,A\nkim.lee@example.org,B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := im.NewSession(SessionOpt{Mode: ModeSubscribe})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LoadCSV(path, ','); err != nil {
		t.Fatal(err)
	}
	if st := im.GetStats(); st.AxmRejected[RejectTypo] != 1 || st.AxmRejectedTotal != 1 || st.AxmWarnedTotal != 0 {
		t.Fatalf("%+v", st)
	}
}

// The refused rows of the download keep the order of the file when malformed rows and rows the
// gate refuses are mixed (the malformed ones are kept in the batch, in order).
func TestGateKeepsRefusedRowsInFileOrder(t *testing.T) {
	lang, err := i18n.New([]byte(`{"_.code":"en","_.name":"English","subscribers.invalidEmail":"invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	im := &Importer{status: Status{Status: StatusNone}, importSeq: 1, i18n: lang}
	im.SetAxmVerify(func(emails []string) []AxmVerdict {
		out := make([]AxmVerdict, len(emails))
		for i, e := range emails {
			if strings.HasPrefix(e, "dead") {
				out[i].Reject = RejectDead
			}
		}
		return out
	})
	path := filepath.Join(t.TempDir(), "in.csv")
	body := "email,name\ndead1@example.org,A\nnot-an-address,B\ndead2@example.org,C\nbroken,D\nok@example.org,E\ndead3@example.org,F\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := im.NewSession(SessionOpt{Mode: ModeSubscribe})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range s.subQueue {
		}
	}()
	if err := s.LoadCSV(path, ','); err != nil {
		t.Fatal(err)
	}
	rows, _ := im.GetRejected(im.GetStats().AxmImportID, 0, true)
	var got []int
	for _, r := range rows {
		got = append(got, r.Row)
	}
	if want := []int{2, 3, 4, 5, 7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
}

// A stop request that comes after the last batch was read does not cancel the next import.
func TestStopAfterTheFileWasReadDoesNotLeak(t *testing.T) {
	im := &Importer{status: Status{Status: StatusNone}, importSeq: 1, stop: make(chan bool, 1)}
	im.SetAxmVerify(func(emails []string) []AxmVerdict {
		im.stop <- true // Stop() while the last batch waits for DNS
		return make([]AxmVerdict, len(emails))
	})
	path := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(path, []byte("email,name\na@example.org,A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := im.NewSession(SessionOpt{Mode: ModeSubscribe})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range s.subQueue {
		}
	}()
	if err := s.LoadCSV(path, ','); err != nil {
		t.Fatal(err)
	}
	select {
	case <-im.stop:
		t.Fatal("the stop request is still waiting for the next import")
	default:
	}
}
