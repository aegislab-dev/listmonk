package core

import (
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// GetListCampaigns returns, per list, the newest few campaigns that sent to it or are
// sending or scheduled to (never drafts), with the list's total in each row (LST-63).
func (c *Core) GetListCampaigns(listIDs []int, includeCount bool) ([]models.ListCampaignUse, error) {
	out := []models.ListCampaignUse{}
	if len(listIDs) == 0 {
		return out, nil
	}
	if err := c.q.AxmGetListCampaigns.Select(&out, pq.Array(listIDs), models.ListUsePerList, includeCount); err != nil {
		c.log.Printf("error fetching list campaigns: %v", pqErrMsg(err))
		return nil, err
	}
	return out, nil
}
