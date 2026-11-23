package core

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// AuditRetentionMonths is how long audit rows (axm_audit_log) are kept. It is the same period
// as the campaign send records (SendRetentionMonths). If a rule that applies to this system
// asks for longer, change this number.
const AuditRetentionMonths = 12

// auditDeleteBatch is the number of audit rows deleted per statement.
const auditDeleteBatch = 20000

// AddAudit adds one row to the audit log. detail is stored as JSON and must not carry a
// subscriber's e-mail, name or attribute value (the caller scrubs it).
func (c *Core) AddAudit(e models.AuditEntry, detail any) error {
	b := []byte("{}")
	if detail != nil {
		var err error
		if b, err = json.Marshal(detail); err != nil {
			return err
		}
	}
	_, err := c.q.AxmInsertAudit.Exec(e.UserID, e.Username, e.UserName, e.UserType, e.Action,
		e.TargetType, e.TargetID, e.TargetName, e.Count, string(b))
	return err
}

// QueryAudit returns one page of the audit log and the number of rows that match.
func (c *Core) QueryAudit(f models.AuditFilter) ([]models.AuditEntry, int, error) {
	if f.Areas == nil {
		f.Areas = []string{}
	}
	if f.Actions == nil {
		f.Actions = []string{}
	}
	out := []models.AuditEntry{}
	if err := c.db.Select(&out, c.q.AxmQueryAudit, f.From, f.To, f.UserID, pq.Array(f.Areas), pq.Array(f.Actions), f.Query, f.Offset, f.Limit); err != nil {
		c.log.Printf("error fetching audit log: %v", err)
		return nil, 0, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{audit.title}", "error", pqErrMsg(err)))
	}
	total := 0
	if len(out) > 0 {
		total = out[0].Total
	}
	return out, total, nil
}

// AuditUsers returns the people who appear in the audit log.
func (c *Core) AuditUsers() ([]models.AuditUser, error) {
	out := []models.AuditUser{}
	if err := c.q.AxmAuditUsers.Select(&out); err != nil {
		c.log.Printf("error fetching audit users: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{audit.title}", "error", pqErrMsg(err)))
	}
	return out, nil
}

// DeleteAudit deletes the audit rows made before the given time, a batch at a time, and
// returns how many were deleted.
func (c *Core) DeleteAudit(before time.Time) (int, error) {
	total := 0
	for {
		var n int
		if err := c.q.AxmDeleteAudit.Get(&n, before, auditDeleteBatch); err != nil {
			return total, err
		}
		total += n
		if n < auditDeleteBatch {
			return total, nil
		}
	}
}

// PurgeOldAudit deletes the audit rows that are past the retention period.
func (c *Core) PurgeOldAudit() (int, error) {
	return c.DeleteAudit(time.Now().AddDate(0, -AuditRetentionMonths, 0))
}

// CountSubscribersMatching counts the subscribers a bulk action by query is about to reach,
// with the same conditions the action uses (search text, SQL condition, lists, subscription
// status). Only a bulk action with no condition at all uses the (cached) total: that cached
// count ignores the search text, so any condition makes the count exact.
func (c *Core) CountSubscribersMatching(searchStr, queryExp string, listIDs []int, subStatus string) (int, error) {
	// Resolve dynamic lists the way the actions do, and keep the list IDs non-nil: a NULL array
	// makes the count's list condition false.
	listIDs, queryExp, err := c.resolveDynamicListQuery(listIDs, subStatus, sanitizeSQLExp(queryExp))
	if err != nil {
		return 0, err
	}
	if listIDs == nil {
		listIDs = []int{}
	}
	if queryExp == "" && (searchStr != "" || subStatus != "" || len(listIDs) > 0) {
		queryExp = "TRUE"
	}
	return c.getSubscriberCount(searchStr, queryExp, subStatus, listIDs)
}

// DeletedCampaign is a campaign a delete reached, with the values it had when it was deleted
// (or refused): the status, how many messages it had sent and how many it was to send.
type DeletedCampaign struct {
	ID      int    `db:"id" json:"id"`
	Name    string `db:"name" json:"name"`
	Status  string `db:"status" json:"status"`
	Sent    int    `db:"sent" json:"sent"`
	ToSend  int    `db:"to_send" json:"to_send"`
	Block   string `db:"block" json:"-"`
	Deleted bool   `db:"deleted" json:"-"`
}

// Why a campaign is not deleted (DeletedCampaign.Block).
const (
	campaignBlockSending = "sending" // running or paused
	campaignBlockSent    = "sent"    // it has send records, or sent mail not long ago
)

// DeleteCampaignsGuarded deletes the campaigns a delete by IDs or by query reaches, except
// that a campaign that is sending (running, paused) or has sent mail is refused, and then
// nothing is deleted (409). Deleting it would drop its send records (the "recent sends"
// exclusion and the reaction counts read them) and the mail already queued would go out
// without a record. It returns the campaigns that were reached with the values they had.
// bulk is true for a delete of several campaigns at once (IDs or search): the refusal then
// counts and names them even when only one was reached.
//
// Two statements in one transaction: the first locks the campaigns (and waits for a
// transaction that is recording their send records), the second reads what is committed by
// then and deletes. One statement would decide on the send records of the moment it started.
func (c *Core) DeleteCampaignsGuarded(ids []int, query string, bulk bool, hasAllPerm bool, permittedLists []int) ([]DeletedCampaign, error) {
	queryStr := ""
	if len(ids) == 0 {
		queryStr = makeSearchString(query)
	}
	fail := func(err error) ([]DeletedCampaign, error) {
		c.log.Printf("error deleting campaigns: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.campaigns}", "error", pqErrMsg(err)))
	}

	tx, err := c.db.Beginx()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()

	locked := []int{}
	if err := tx.Stmtx(c.q.AxmLockCampaignsToDelete).Select(&locked, pq.Array(ids), queryStr, hasAllPerm, pq.Array(permittedLists)); err != nil {
		return fail(err)
	}
	out := []DeletedCampaign{}
	if len(locked) > 0 {
		if err := tx.Stmtx(c.q.AxmDeleteCampaignsGuarded).Select(&out, pq.Array(locked), SendRetentionMonths); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}

	blocked := []DeletedCampaign{}
	for _, d := range out {
		if d.Block != "" {
			blocked = append(blocked, d)
		}
	}
	if len(blocked) == 0 {
		return out, nil
	}

	// A single delete says why. A delete of several names the first three, even when it
	// reached one.
	if !bulk {
		d := blocked[0]
		key := "campaigns.axmDeleteSent"
		if d.Block == campaignBlockSending {
			key = "campaigns.axmDeleteSending"
		}
		return nil, echo.NewHTTPError(http.StatusConflict,
			c.i18n.Ts(key, "name", d.Name, "status", c.i18n.T("campaigns.status."+d.Status)))
	}
	names := ""
	for i, d := range blocked {
		if i == 3 {
			names += " …"
			break
		}
		if i > 0 {
			names += ", "
		}
		names += "「" + d.Name + "」"
	}
	return nil, echo.NewHTTPError(http.StatusConflict,
		c.i18n.Ts("campaigns.axmDeleteBlockedMany", "num", strconv.Itoa(len(blocked)), "names", names))
}

// ListNames returns the names of the lists with the given IDs (a list that is gone is left out).
func (c *Core) ListNames(ids []int) (map[int]string, error) {
	rows := []struct {
		ID   int    `db:"id"`
		Name string `db:"name"`
	}{}
	if err := c.db.Select(&rows, `SELECT id, name FROM lists WHERE id = ANY($1)`, pq.Array(ids)); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// CountBouncedSubscribers counts the subscribers that have a bounce record.
func (c *Core) CountBouncedSubscribers() (int, error) {
	n := 0
	err := c.db.Get(&n, `SELECT COUNT(DISTINCT subscriber_id) FROM bounces`)
	return n, err
}
