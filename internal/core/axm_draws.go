package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// drawNameMax is the longest list name a draw makes (the list form allows 200 characters).
const drawNameMax = 200

// DrawOpts is what a draw needs besides its settings.
type DrawOpts struct {
	// Name of the new list. Empty makes the default name.
	Name string

	// Describe makes the description of the new list from the source name, the time and the
	// numbers. Nil leaves the description empty.
	Describe func(source string, at time.Time, r models.DrawCounts) string

	// UserID is the user who draws, 0 for none.
	UserID int

	// TeamTags, when SetTeams is true, replace the team tags the new list inherits from the
	// source (none = no team). The handler decides them from who draws.
	SetTeams bool
	TeamTags []string
}

type drawSource struct {
	ID      int            `db:"id"`
	Name    string         `db:"name"`
	Status  string         `db:"status"`
	Preset  string         `db:"axm_preset"`
	Tags    pq.StringArray `db:"tags"`
	Dynamic bool           `db:"dynamic"`
	Drawn   bool           `db:"drawn"`
}

func drawBadRequest(msg string) error { return echo.NewHTTPError(http.StatusBadRequest, msg) }

// checkDrawSource rejects a source that may not be drawn from: one that does not exist, is
// archived, or is itself a drawn list.
func checkDrawSource(src drawSource) error {
	if src.Status == models.ListStatusArchived {
		return drawBadRequest("An archived list cannot be drawn from")
	}
	if src.Drawn {
		return drawBadRequest("A drawn list cannot be drawn from again")
	}
	return nil
}

func (c *Core) drawCounts(q interface {
	Get(dest any, args ...any) error
}, sourceID int, s models.DrawSettings) (models.DrawCounts, error) {
	var out models.DrawCounts
	if err := q.Get(&out, sourceID, s.ExcludeScope, s.ExcludeDays); err != nil {
		return out, err
	}
	out.Requested = s.Want(out.Target)
	out.WillDraw = min(out.Requested, out.Candidates)
	return out, nil
}

// PreviewListDraw counts a draw from a list without making anything.
func (c *Core) PreviewListDraw(sourceID int, s models.DrawSettings) (models.DrawCounts, error) {
	if err := s.Validate(); err != nil {
		return models.DrawCounts{}, drawBadRequest(err.Error())
	}
	var src drawSource
	if err := c.q.AxmDrawSource.Get(&src, sourceID); err != nil {
		if err == sql.ErrNoRows {
			return models.DrawCounts{}, drawBadRequest("Unknown list")
		}
		return models.DrawCounts{}, err
	}
	if err := checkDrawSource(src); err != nil {
		return models.DrawCounts{}, err
	}
	out, err := c.drawCounts(c.q.AxmDrawCounts, sourceID, s)
	if err != nil {
		c.log.Printf("error counting a list draw: %v", pqErrMsg(err))
	}
	return out, err
}

// drawDefaultName is "{source} · {date} · {n}명" with the source name cut to fit.
func drawDefaultName(source string, at time.Time, n int) string {
	suffix := fmt.Sprintf(" · %s · %s명", at.Format("2006-01-02"), groupThousands(n))
	if room := drawNameMax - utf8.RuneCountInString(suffix); utf8.RuneCountInString(source) > room {
		source = string([]rune(source)[:room])
	}
	return source + suffix
}

func groupThousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// uniqueDrawName returns name, or name with " (2)", " (3)"... when a list has it already.
func uniqueDrawName(taken map[string]bool, name string) string {
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		base := name
		if room := drawNameMax - utf8.RuneCountInString(suffix); utf8.RuneCountInString(base) > room {
			base = string([]rune(base)[:room])
		}
		if n := base + suffix; !taken[n] {
			return n
		}
	}
}

// DrawList picks the people of a list that are not yet drawn or sent to and puts them on a
// new ordinary list. Everything happens in one transaction under a lock on the source, so
// two draws from the same source never overlap.
func (c *Core) DrawList(sourceID int, s models.DrawSettings, o DrawOpts) (models.List, models.ListDraw, error) {
	var none models.ListDraw
	if err := s.Validate(); err != nil {
		return models.List{}, none, drawBadRequest(err.Error())
	}
	tx, err := c.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return models.List{}, none, err
	}
	defer tx.Rollback()

	if _, err := tx.Stmtx(c.q.AxmDrawLock).Exec(sourceID); err != nil {
		return models.List{}, none, err
	}
	var src drawSource
	if err := tx.Stmtx(c.q.AxmDrawSource).Get(&src, sourceID); err != nil {
		if err == sql.ErrNoRows {
			return models.List{}, none, drawBadRequest("Unknown list")
		}
		return models.List{}, none, err
	}
	if err := checkDrawSource(src); err != nil {
		return models.List{}, none, err
	}
	counts, err := c.drawCounts(tx.Stmtx(c.q.AxmDrawCounts), sourceID, s)
	if err != nil {
		c.log.Printf("error counting a list draw: %v", pqErrMsg(err))
		return models.List{}, none, err
	}
	if counts.WillDraw < 1 {
		return models.List{}, none, drawBadRequest("There is nobody left to draw")
	}

	now := time.Now()
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = drawDefaultName(src.Name, now, counts.WillDraw)
	}
	var names []string
	if err := tx.Stmtx(c.q.AxmDrawNames).Select(&names, name, name+" (%"); err != nil {
		return models.List{}, none, err
	}
	taken := map[string]bool{}
	for _, n := range names {
		taken[n] = true
	}
	name = uniqueDrawName(taken, name)

	tags := normalizeTags(append([]string{}, src.Tags...))
	if o.SetTeams {
		tags = append(models.WithoutTeamTags(tags), o.TeamTags...)
	}
	has := false
	for _, t := range tags {
		has = has || t == models.DrawListTag
	}
	if !has {
		tags = append(tags, models.DrawListTag)
	}
	desc := ""
	if o.Describe != nil {
		desc = o.Describe(src.Name, now, counts)
	}
	uu, err := uuid.NewV4()
	if err != nil {
		return models.List{}, none, err
	}
	var newID int
	if err := tx.Stmtx(c.q.AxmDrawCreateList).Get(&newID, uu.String(), name, pq.StringArray(tags), desc, src.Preset); err != nil {
		c.log.Printf("error creating a drawn list: %v", pqErrMsg(err))
		return models.List{}, none, err
	}
	res, err := tx.Stmtx(c.q.AxmDrawAddMembers).Exec(sourceID, newID, s.ExcludeScope, s.ExcludeDays, counts.WillDraw)
	if err != nil {
		c.log.Printf("error filling a drawn list: %v", pqErrMsg(err))
		return models.List{}, none, err
	}
	drawn, _ := res.RowsAffected()
	counts.WillDraw = int(drawn)

	settings, _ := json.Marshal(s)
	result, _ := json.Marshal(map[string]int{
		"target": counts.Target, "already_drawn": counts.AlreadyDrawn,
		"already_received": counts.AlreadyReceived, "candidates": counts.Candidates, "drawn": counts.WillDraw,
	})
	var by any
	if o.UserID > 0 {
		by = o.UserID
	}
	var rec struct {
		ID      int       `db:"id"`
		DrawnAt time.Time `db:"drawn_at"`
	}
	if err := tx.Stmtx(c.q.AxmDrawRecord).Get(&rec, newID, sourceID, src.Name, name, by, string(settings), string(result)); err != nil {
		c.log.Printf("error recording a list draw: %v", pqErrMsg(err))
		return models.List{}, none, err
	}
	if err := tx.Commit(); err != nil {
		return models.List{}, none, err
	}

	_ = c.refreshCache(matListSubStats, false)
	list, err := c.GetList(newID, "")
	if err != nil {
		return models.List{}, none, err
	}
	// GetList adds the status counts to the total again for a single list; the list is new
	// and holds exactly the people drawn.
	list.SubscriberCount = counts.WillDraw
	draw := models.ListDraw{
		ID: rec.ID, ListID: &newID, SourceListID: &sourceID, SourceName: src.Name, ListName: name,
		DrawnAt: rec.DrawnAt, Settings: types.JSONText(settings), Result: types.JSONText(result),
	}
	if o.UserID > 0 {
		draw.DrawnBy = &o.UserID
	}
	return list, draw, nil
}

// GetListDraws returns draw records, newest first: those of one source, or of the given
// drawn lists, or all of them when both are empty. limit bounds the result.
func (c *Core) GetListDraws(sourceID int, listIDs []int, limit int) ([]models.ListDraw, error) {
	if listIDs == nil {
		listIDs = []int{}
	}
	out := []models.ListDraw{}
	if err := c.q.AxmGetListDraws.Select(&out, sourceID, pq.Array(listIDs), limit); err != nil {
		c.log.Printf("error fetching list draws: %v", pqErrMsg(err))
		return nil, err
	}
	return out, nil
}
