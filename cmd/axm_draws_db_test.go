package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
)

// Lists made by a draw are ordinary lists: the unchanged campaign engine sends to exactly
// the people drawn, once each, and the next draw is made of the people left (LST-23).
func TestDrawnListCampaignsSendEveryoneOnce(t *testing.T) {
	e := newSendsEnv(t, 60)

	var camps, lists []int
	drawn := map[int][]int{}
	for i := 0; i < 3; i++ {
		l, _, err := e.core.DrawList(e.listID, models.DrawSettings{Unit: models.DrawUnitCount, Value: 20}, core.DrawOpts{})
		if err != nil {
			t.Fatal(err)
		}
		lists = append(lists, l.ID)
		drawn[l.ID] = e.ids(`SELECT subscriber_id FROM subscriber_lists WHERE list_id = $1 ORDER BY subscriber_id`, l.ID)
		if len(drawn[l.ID]) != 20 {
			t.Fatalf("draw %d made %d", i, len(drawn[l.ID]))
		}
		var camp int
		if i == 0 {
			camp = e.campID
		} else {
			camp, _ = e.addCampaign("running")
		}
		e.db.MustExec(`UPDATE campaign_lists SET list_id = $2, list_name = 'drawn' WHERE campaign_id = $1`, camp, l.ID)
		camps = append(camps, camp)
	}
	// The three draws cover the 60 eligible people without a repeat.
	var all []int
	for _, l := range lists {
		all = append(all, drawn[l]...)
	}
	wantSame(t, "union of the draws", sortedCopy(all), e.eligible)

	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	e.startManager(e.store, msgr, logs)
	waitFor(t, "three finished campaigns", func() bool {
		var n int
		e.get(&n, `SELECT COUNT(*) FROM campaigns WHERE status = 'finished'`)
		return n == 3 && strings.Count(logs.String(), "send records:") == 3
	})
	noRepeats(t, msgr.sentIDs())
	wantSame(t, "delivered", msgr.sentIDs(), e.eligible)
	for i, camp := range camps {
		wantSame(t, fmt.Sprintf("recorded for campaign %d", i), e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1 ORDER BY subscriber_id`, camp), drawn[lists[i]])
	}

	// The people who got these campaigns are not candidates of the same source any more.
	p, err := e.core.PreviewListDraw(e.listID, models.DrawSettings{Unit: models.DrawUnitCount, Value: 20})
	if err != nil || p.Candidates != 0 || p.AlreadyDrawn != 60 {
		t.Fatalf("%+v %v", p, err)
	}
}

// A version 2 dynamic list leaves out the people on its exclude lists when a campaign is
// sent to it, with the engine's own batch query.
func TestDynamicV2ListCampaignLeavesOutExcluded(t *testing.T) {
	e := newSendsEnv(t, 57)
	var exclude int
	e.get(&exclude, `INSERT INTO lists (uuid, name, type, optin) VALUES (gen_random_uuid(), 'Customers', 'private', 'single') RETURNING id`)
	var skipped []int
	for i, id := range e.eligible {
		if i%5 == 0 {
			status := "confirmed"
			if i%10 == 0 {
				status = "unsubscribed" // any status on the exclude list counts
			}
			e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, $3::subscription_status)`, id, exclude, status)
			skipped = append(skipped, id)
		}
	}
	filter := fmt.Sprintf(`{"version":2,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[],"exclude":{"lists":[%d]}}`, e.listID, exclude)
	var dynID int
	e.get(&dynID, `INSERT INTO lists (uuid, name, type, optin, axm_filter) VALUES (gen_random_uuid(), 'Synthetic dynamic', 'private', 'single', $1::JSONB) RETURNING id`, filter)
	e.db.MustExec(`UPDATE campaign_lists SET list_id = $2 WHERE campaign_id = $1`, e.campID, dynID)
	want := minus(e.eligible, skipped)

	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	e.startManager(e.store, msgr, logs)
	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})
	noRepeats(t, msgr.sentIDs())
	wantSame(t, "delivered", msgr.sentIDs(), want)
	wantSame(t, "recorded", e.recorded(), want)
	wantSame(t, "gap", e.ids(sendGapSQL, e.campID), nil)
	_ = io.Discard
}

func sortedCopy(in []int) []int {
	out := append([]int{}, in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
