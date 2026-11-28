package main

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"gopkg.in/volatiletech/null.v6"
)

// AXM (LST-71): things a campaign shares with others (its template, the From list of the
// settings) are changed after it was scheduled, and the scheduled campaign goes out with the
// change and no check (LST-66, LST-67 limits). The pre-send window only warns of what it cannot
// block, and a template or a setting is saved by someone who is not looking at that campaign, so
// the save itself is refused when it would take away what the scheduled campaign needs to be sent
// at all: the link to leave the list (a template) or its sender's place on the From list. The
// message names the campaigns and says what to do (unschedule them, or keep the link / the address).
// A campaign that is already in that state is not a reason to refuse: only what the change takes
// away is. Other warnings of the window (tracking, copies) are not judged here.

// scheduledNamesMax is how many campaigns a refusal names before it counts the rest.
const scheduledNamesMax = 3

// scheduledList is "“name” (#id), …" for the refusal text, cut after scheduledNamesMax.
func scheduledList(refs []core.ScheduledCampaignRef) string {
	parts := make([]string, 0, scheduledNamesMax+1)
	for i, r := range refs {
		if i == scheduledNamesMax {
			parts = append(parts, fmt.Sprintf("+%d", len(refs)-scheduledNamesMax))
			break
		}
		parts = append(parts, fmt.Sprintf("“%s” (#%d)", cutName(scrubAuditText(r.Name)), r.ID))
	}
	return strings.Join(parts, ", ")
}

// campaignUnsubscribeIn tells, for the scheduled campaign as the sender reads it, whether its
// mail has the unsubscribe link in every state it is sent in, for the example subscriber and
// one with no attributes. known is false when nothing could be rendered.
func (a *App) campaignUnsubscribeIn(camp models.Campaign) (ok, known bool) {
	camp.UpdatedAt = null.TimeFrom(time.Now())
	subs := []models.Subscriber{dummySubscriber, bareSubscriber}
	var renders []string
	for _, st := range manager.SendStatuses(camp.SendAt) {
		for i := range subs {
			renders = append(renders, st+"#"+strconv.Itoa(i))
		}
	}
	return unsubscribeInAll(renders, func(key string) (bool, error) {
		st, n, _ := strings.Cut(key, "#")
		i, _ := strconv.Atoi(n)
		return a.manager.HasUnsubscribeLink(&camp, subs[i], st)
	})
}

// checkScheduledTemplate refuses (400) saving template id with body when the scheduled regular
// campaigns that go out with it would lose their unsubscribe link: they had it with the template
// as saved and would not have it with the new body.
func (a *App) checkScheduledTemplate(id int, body string) error {
	refs, err := a.core.GetScheduledByTemplate(id)
	if err != nil {
		return err
	}
	var lost []core.ScheduledCampaignRef
	for _, ref := range refs {
		camp, err := a.core.GetCampaignForSendCheck(ref.ID, 0)
		if err != nil {
			return err
		}
		if before, known := a.campaignUnsubscribeIn(camp); !known || !before {
			continue
		}
		camp.TemplateBody = body
		if after, known := a.campaignUnsubscribeIn(camp); known && !after {
			lost = append(lost, ref)
		}
	}
	if len(lost) == 0 {
		return nil
	}
	return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("templates.scheduledUnsubscribe",
		"n", strconv.Itoa(len(lost)), "campaigns", scheduledList(lost)))
}

// checkScheduledSenders refuses (400) a change of the From list of the settings that takes the
// From of a scheduled campaign off the list: the campaign was allowed to go out from that address
// by the list as it was, and would go out from an address the list no longer allows. The list
// only applies without account senders (sender domains), so only a change from a list to a list
// is judged: starting a list, or dropping it, leaves campaigns scheduled before as they were.
func (a *App) checkScheduledSenders(oldDomains, oldList, newDomains, newList []string) error {
	if len(oldDomains) > 0 || len(newDomains) > 0 || len(oldList) == 0 || len(newList) == 0 || slices.Equal(oldList, newList) {
		return nil
	}
	refs, err := a.core.GetScheduledSenders()
	if err != nil {
		return err
	}
	var bad []core.ScheduledCampaignRef
	for _, ref := range refs {
		if _, ok := matchFromAddress(ref.FromEmail, oldList); !ok {
			continue
		}
		if _, ok := matchFromAddress(ref.FromEmail, newList); !ok {
			bad = append(bad, ref)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("settings.scheduledFrom",
		"n", strconv.Itoa(len(bad)), "campaigns", scheduledList(bad)))
}
