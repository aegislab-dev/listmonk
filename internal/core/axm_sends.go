package core

import (
	"time"
)

// SendRetentionMonths is how long campaign send records (axm_campaign_sends) are kept.
const SendRetentionMonths = 12

// sendDeleteBatch is the number of send records deleted per statement.
const sendDeleteBatch = 20000

// MarkCampaignSendOpened marks a view on the send record of the campaign message that
// went to the subscriber. kind is the client class of the request (models.AgentKind*).
// A message without a send record is left alone.
func (c *Core) MarkCampaignSendOpened(campUUID, subUUID string, kind int) error {
	_, err := c.q.AxmMarkSendOpened.Exec(campUUID, subUUID, kind)
	return err
}

// MarkCampaignSendClicked marks a tracked link click on a send record.
func (c *Core) MarkCampaignSendClicked(campUUID, subUUID string, kind int) error {
	_, err := c.q.AxmMarkSendClicked.Exec(campUUID, subUUID, kind)
	return err
}

// DeleteCampaignSends deletes the send records sent before the given time, a batch at a
// time, and returns how many were deleted.
func (c *Core) DeleteCampaignSends(before time.Time) (int, error) {
	total := 0
	for {
		var n int
		if err := c.q.AxmDeleteCampaignSends.Get(&n, before, sendDeleteBatch); err != nil {
			return total, err
		}
		total += n
		if n < sendDeleteBatch {
			return total, nil
		}
	}
}

// PurgeOldCampaignSends deletes the send records that are past the retention period.
func (c *Core) PurgeOldCampaignSends() (int, error) {
	return c.DeleteCampaignSends(time.Now().AddDate(0, -SendRetentionMonths, 0))
}
