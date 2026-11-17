package core

import (
	"encoding/json"
	"net/http"
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
// with the same conditions the action uses. Without any condition it is the (cached) total.
func (c *Core) CountSubscribersMatching(searchStr, queryExp string, listIDs []int, subStatus string) (int, error) {
	return c.getSubscriberCount(searchStr, sanitizeSQLExp(queryExp), subStatus, listIDs)
}

// CampaignRef is a campaign's ID and name.
type CampaignRef struct {
	ID   int    `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

// ResolveCampaignsToDelete returns the campaigns a delete by IDs or by query reaches.
func (c *Core) ResolveCampaignsToDelete(ids []int, query string, hasAllPerm bool, permittedLists []int) ([]CampaignRef, error) {
	queryStr := ""
	if len(ids) == 0 {
		queryStr = makeSearchString(query)
	}
	out := []CampaignRef{}
	err := c.q.AxmResolveCampaignsToDelete.Select(&out, pq.Array(ids), queryStr, hasAllPerm, pq.Array(permittedLists))
	return out, err
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
