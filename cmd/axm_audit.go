package main

import (
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	null "gopkg.in/volatiletech/null.v6"
)

// The audit log (axm_audit_log) says who did what to what and when. A handler writes a row
// after the action has succeeded. A failed write is logged and never changes the answer of
// the action that already happened. A row never holds a subscriber's e-mail, name or
// attribute value: a subscriber is an ID or a count, and the text of a bulk condition goes
// through scrubAuditText.

// Audit actions. The part before the dot is the area the audit screen filters by.
const (
	auditListCreate     = "list.create"
	auditListUpdate     = "list.update"
	auditListTeamChange = "list.team_change"
	auditListDelete     = "list.delete"
	auditListRestore    = "list.restore"
	auditListPurge      = "list.purge"

	auditSubDelete           = "subscriber.delete"
	auditSubBlocklist        = "subscriber.blocklist"
	auditSubListsAdd         = "subscriber.lists_add"
	auditSubListsRemove      = "subscriber.lists_remove"
	auditSubListsUnsubscribe = "subscriber.lists_unsubscribe"
	auditSubGC               = "subscriber.gc"

	auditImportStart  = "import.start"
	auditImportStop   = "import.stop"
	auditImportFinish = "import.finish"

	auditCampaignStart      = "campaign.start"
	auditCampaignSchedule   = "campaign.schedule"
	auditCampaignUnschedule = "campaign.unschedule"
	auditCampaignPause      = "campaign.pause"
	auditCampaignCancel     = "campaign.cancel"
	auditCampaignDelete     = "campaign.delete"

	auditRoleCreate     = "role.create"
	auditRoleUpdate     = "role.update"
	auditRoleDelete     = "role.delete"
	auditTeamCreate     = "team.create"
	auditTeamUpdate     = "team.update"
	auditTeamDelete     = "team.delete"
	auditTeamMembers    = "team.members"
	auditUserCreate     = "user.create"
	auditUserUpdate     = "user.update"
	auditUserDelete     = "user.delete"
	auditUserTwofa      = "user.twofa"
	auditSettingsUpdate = "settings.update"
)

// Target types.
const (
	auditTargetList       = "list"
	auditTargetSubscriber = "subscriber"
	auditTargetCampaign   = "campaign"
	auditTargetImport     = "import"
	auditTargetRole       = "role"
	auditTargetTeam       = "team"
	auditTargetUser       = "user"
	auditTargetSettings   = "settings"
)

// auditNoCount is passed as the count of an action that has none.
const auditNoCount = -1

// auditMaxText is the longest text (a bulk condition, a name) a row keeps.
const (
	auditMaxText = 500
	auditMaxName = 200
)

// auditAreas maps an area of the audit screen to the actions' first part.
var auditAreas = map[string][]string{
	"list":       {"list"},
	"subscriber": {"subscriber"},
	"import":     {"import"},
	"campaign":   {"campaign"},
	"access":     {"role", "team", "user"},
	"settings":   {"settings"},
}

type auditTarget struct {
	Type string
	ID   int
	Name string
}

var (
	// Any run of characters around an "@" (a whole address, or a part of one such as "kim.lee@"
	// or "%@example.com") is hidden. An address typed URL-encoded (%40) is read as one first.
	reAuditEmail = regexp.MustCompile(`[^\s'",()=<>;]*@[^\s'",()=<>;]*`)
	reAuditPct40 = regexp.MustCompile(`(?i)%40`)
)

// scrubAuditText hides e-mail addresses in a text that will be stored in the audit log and
// cuts it to auditMaxText characters. Any "@" that is left over is replaced too, so a row
// never holds one.
func scrubAuditText(s string) string {
	s = reAuditPct40.ReplaceAllString(s, "@")
	s = reAuditEmail.ReplaceAllString(s, "[email]")
	s = strings.ReplaceAll(s, "@", "(at)")
	if utf8.RuneCountInString(s) > auditMaxText {
		s = string([]rune(s)[:auditMaxText]) + "…"
	}
	return s
}

// scrubAuditDetail applies scrubAuditText to every text in a detail value.
func scrubAuditDetail(v any) any {
	switch t := v.(type) {
	case string:
		return scrubAuditText(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = scrubAuditDetail(x)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, x := range t {
			out[i] = scrubAuditText(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = scrubAuditDetail(x)
		}
		return out
	}
	return v
}

func cutName(s string) string {
	if utf8.RuneCountInString(s) > auditMaxName {
		return string([]rune(s)[:auditMaxName])
	}
	return s
}

// auditEntry builds the row for an action by user (the zero user is the server itself).
func auditEntry(user *auth.User, action string, t auditTarget, count int) models.AuditEntry {
	e := models.AuditEntry{
		Action:     action,
		TargetType: t.Type,
		TargetName: cutName(t.Name),
	}
	if t.ID > 0 {
		e.TargetID = null.IntFrom(t.ID)
	}
	if count >= 0 {
		e.Count = null.IntFrom(count)
	}
	if user == nil {
		e.UserType = models.AuditUserTypeSystem
		e.Username = "system"
		e.UserName = "system"
		return e
	}
	e.UserID = null.IntFrom(user.ID)
	e.Username = user.Username
	e.UserName = cutName(user.Name)
	e.UserType = user.Type
	return e
}

// auditAs writes an audit row for an action by the given user (nil = the server).
func (a *App) auditAs(user *auth.User, action string, t auditTarget, count int, detail map[string]any) {
	e := auditEntry(user, action, t, count)
	var d any
	if detail != nil {
		d = scrubAuditDetail(detail)
	}
	if err := a.core.AddAudit(e, d); err != nil {
		a.log.Printf("error writing the audit log (%s): %v", action, err)
	}
}

// audit writes an audit row for an action that the signed-in user of the request just did.
func (a *App) audit(c echo.Context, action string, t auditTarget, count int, detail map[string]any) {
	u, ok := c.Get(auth.UserHTTPCtxKey).(auth.User)
	if !ok {
		a.auditAs(nil, action, t, count, detail)
		return
	}
	a.auditAs(&u, action, t, count, detail)
}

// auditSystem writes an audit row for something the server did on its own.
func (a *App) auditSystem(action string, t auditTarget, count int, detail map[string]any) {
	a.auditAs(nil, action, t, count, detail)
}

// parseAuditTime reads a time from a query parameter: RFC 3339, or a date (the start of that
// day in UTC). endOfDay moves a bare date to the end of its day, for an inclusive "to".
func parseAuditTime(s string, endOfDay bool) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return &t, nil
}

// GetAudit returns a page of the audit log: GET /api/audit with from, to (a "to" date is
// inclusive), user_id, area, action, query, page and per_page.
func (a *App) GetAudit(c echo.Context) error {
	var (
		pg = a.pg.NewFromURL(c.Request().URL.Query())
		f  = models.AuditFilter{Offset: pg.Offset, Limit: pg.Limit, Query: strings.TrimSpace(c.FormValue("query"))}
		qp = c.QueryParams()
	)
	var err error
	if f.From, err = parseAuditTime(c.FormValue("from"), false); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "from"))
	}
	if f.To, err = parseAuditTime(c.FormValue("to"), true); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "to"))
	}
	if v := c.FormValue("user_id"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil || id < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "user_id"))
		}
		f.UserID = &id
	}
	for _, area := range qp["area"] {
		prefixes, ok := auditAreas[area]
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "area"))
		}
		f.Areas = append(f.Areas, prefixes...)
	}
	f.Actions = qp["action"]

	res, total, err := a.core.QueryAudit(f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{models.PageResults{
		Query:   f.Query,
		Results: res,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	}})
}

// GetAuditUsers returns the people who appear in the audit log (for its filter).
func (a *App) GetAuditUsers(c echo.Context) error {
	out, err := a.core.AuditUsers()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// diffStrings returns what is in b and not in a (added), and what is in a and not in b (removed).
func diffStrings(a, b []string) (added, removed []string) {
	in := func(s []string, v string) bool {
		for _, x := range s {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, v := range b {
		if !in(a, v) {
			added = append(added, v)
		}
	}
	for _, v := range a {
		if !in(b, v) {
			removed = append(removed, v)
		}
	}
	return added, removed
}

// auditIDsMax is how many IDs a row lists; a longer list keeps the count only.
const auditIDsMax = 50

func idsDetail(ids []int) any {
	if len(ids) == 0 || len(ids) > auditIDsMax {
		return nil
	}
	return ids
}

// listRefs returns {id, name} for the lists, for the detail of an audit row.
func (a *App) listRefs(ids []int) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	names, err := a.core.ListNames(ids)
	if err != nil {
		a.log.Printf("error reading list names for the audit log: %v", err)
	}
	for _, id := range ids {
		out = append(out, map[string]any{"id": id, "name": names[id]})
	}
	return out
}

// auditSubscribersByID writes the row of a bulk action on subscribers named by ID. One
// subscriber is the target; several are a count (and their IDs while there are few).
func (a *App) auditSubscribersByID(c echo.Context, action string, ids []int, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	if len(ids) == 1 {
		a.audit(c, action, auditTarget{Type: auditTargetSubscriber, ID: ids[0]}, auditNoCount, detail)
		return
	}
	detail["by"] = "ids"
	if v := idsDetail(ids); v != nil {
		detail["ids"] = v
	}
	a.audit(c, action, auditTarget{Type: auditTargetSubscriber}, len(ids), detail)
}

// bulkQuery is what a bulk action by query is about, for the audit row.
type bulkQuery struct {
	count   int
	counted bool
	detail  map[string]any
}

// countBulkQuery counts the subscribers a bulk action by query is about to reach and keeps
// the conditions for its audit row. A count that fails is left out (the action reports its
// own error). The e-mail addresses in a condition are hidden when the row is written.
func (a *App) countBulkQuery(req subQueryReq, query string, sourceListIDs []int) bulkQuery {
	b := bulkQuery{detail: map[string]any{"by": "query", "counted": "before"}}
	if req.All {
		b.detail["all"] = true
	}
	if req.Search != "" {
		b.detail["search"] = req.Search
	}
	if query != "" {
		b.detail["query"] = query
	}
	if len(req.ListIDs) > 0 {
		b.detail["lists"] = a.listRefs(req.ListIDs)
	}
	if req.SubscriptionStatus != "" {
		b.detail["subscription_status"] = req.SubscriptionStatus
	}
	n, err := a.core.CountSubscribersMatching(req.Search, query, sourceListIDs, req.SubscriptionStatus)
	if err != nil {
		a.log.Printf("error counting subscribers for the audit log: %v", err)
		return b
	}
	b.count, b.counted = n, true
	return b
}

// auditBulkQuery writes the row of a bulk action by query.
func (a *App) auditBulkQuery(c echo.Context, action string, b bulkQuery, detail map[string]any) {
	d := map[string]any{}
	for k, v := range b.detail {
		d[k] = v
	}
	for k, v := range detail {
		d[k] = v
	}
	count := auditNoCount
	if b.counted {
		count = b.count
	}
	a.audit(c, action, auditTarget{Type: auditTargetSubscriber}, count, d)
}

// purgeTrashAndAudit deletes the lists that are past the trash period (and writes list.purge
// for each, as the server) and the audit rows that are past their retention.
func purgeTrashAndAudit(co *core.Core, lo *log.Logger) {
	gone, err := co.PurgeTrash()
	if err != nil {
		lo.Printf("error deleting old lists from the trash: %v", err)
	}
	for _, l := range gone {
		e := auditEntry(nil, auditListPurge, auditTarget{auditTargetList, l.ID, l.Name}, l.SubCount)
		if err := co.AddAudit(e, map[string]any{"trash_days": core.TrashDays}); err != nil {
			lo.Printf("error writing the audit log (%s): %v", auditListPurge, err)
		}
	}
	if len(gone) > 0 {
		lo.Printf("deleted %d lists that had been in the trash for %d days", len(gone), core.TrashDays)
	}
	n, err := co.PurgeOldAudit()
	if err != nil {
		lo.Printf("error deleting old audit log rows: %v", err)
	}
	if n > 0 {
		lo.Printf("deleted %d audit log rows older than %d months", n, core.AuditRetentionMonths)
	}
}
