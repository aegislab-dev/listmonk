package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/media"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// store implements DataSource over the primary
// database.
type store struct {
	queries *models.Queries
	core    *core.Core
	media   media.Store

	// AXM (LST-20): the connection that holds the campaign sending lock.
	db       *sqlx.DB
	lockMut  sync.Mutex
	lockConn *sql.Conn
}

// campaignLockSQL takes the lock that lets one app instance send campaigns (AXM,
// LST-20). It is a session lock: it stays with the connection it was taken on and
// Postgres releases it when that connection ends, however the app went away. The key
// is per schema, so installs that share a database do not block each other.
const (
	campaignLockSQL     = `SELECT pg_try_advisory_lock(4282445, hashtext(current_schema()::TEXT))`
	campaignLockTimeout = 5 * time.Second

	// pgUndefinedTable is the Postgres error code for a relation that does not exist.
	pgUndefinedTable = "42P01"
)

type runningCamp struct {
	IsDynamic        bool   `db:"is_dynamic"`
	CampaignID       int    `db:"campaign_id"`
	CampaignType     string `db:"campaign_type"`
	LastSubscriberID int    `db:"last_subscriber_id"`
	MaxSubscriberID  int    `db:"max_subscriber_id"`
	ListID           int    `db:"list_id"`
}

func newManagerStore(q *models.Queries, c *core.Core, m media.Store, db *sqlx.DB) *store {
	return &store{
		queries: q,
		core:    c,
		media:   m,
		db:      db,
	}
}

// NextCampaigns retrieves active campaigns ready to be processed excluding
// campaigns that are also being processed. Progress saves own sent counts;
// discovery must not update them, as Select can fail after the SQL commits.
func (s *store) NextCampaigns(currentIDs []int64) ([]*models.Campaign, error) {
	// AXM (LST-47): a follow-up campaign is checked against its original before it can be
	// picked up. The first check covers the ones that are due now. A schedule can fall due while
	// that check runs (a lock wait, a long list), so every campaign the query then finds is looked
	// at again, and a follow-up that was not checked yet is checked before the result is given
	// back. If a check fails nothing is picked up in this round: it is tried again in the next
	// one, and a follow-up never starts unchecked.
	ctx := context.Background()
	if _, _, err := s.core.RecheckFollowups(ctx, currentIDs); err != nil {
		return nil, err
	}
	for round := 0; ; round++ {
		var out []*models.Campaign
		if err := s.queries.NextCampaigns.Select(&out, pq.Int64Array(currentIDs)); err != nil {
			return out, err
		}
		// A follow-up is given back only when its last check was made for the campaign as it is
		// now. A check made before a later time was saved or the lists were changed does not count,
		// whenever in this call it was made. Once next-campaigns has started a campaign, the editor
		// cannot change it any more (core.UpdateCampaign), so what is checked here is what is sent.
		ids := make([]int, 0, len(out))
		for _, camp := range out {
			ids = append(ids, camp.ID)
		}
		stale, err := s.core.FollowupsToCheck(ctx, ids)
		if err != nil {
			return nil, err
		}
		if len(stale) == 0 {
			return out, nil
		}
		if round >= 3 {
			// Still changing: give back what is checked, the rest comes in the next round.
			skip := map[int]bool{}
			for _, id := range stale {
				skip[id] = true
			}
			kept := out[:0]
			for _, camp := range out {
				if !skip[camp.ID] {
					kept = append(kept, camp)
				}
			}
			return kept, nil
		}
		if _, _, err := s.core.RecheckFollowupsByID(ctx, stale); err != nil {
			return nil, err
		}
	}
}

// NextSubscribers retrieves a subset of subscribers of a given campaign.
// Since batches are processed sequentially, the retrieval is ordered by ID,
// and every batch takes the last ID of the last batch and fetches the next
// batch above that.
//
// AXM (LST-20): the caller passes the last ID it has read (lastSubID). The campaign's
// last_subscriber_id in the DB is not the read position any more, and reading a batch
// does not change it.
func (s *store) NextSubscribers(campID, lastSubID, limit int) ([]models.Subscriber, error) {
	var camps []runningCamp
	if err := s.queries.GetRunningCampaign.Select(&camps, campID); err != nil {
		return nil, err
	}

	// AXM (LST-25): no row means the campaign is not running. That is not the same
	// as having no subscribers left, which would finish it.
	if len(camps) == 0 {
		return nil, manager.ErrCampaignNotRunning
	}

	query := s.queries.NextCampaignSubscribers
	var listIDs []int
	for _, c := range camps {
		if c.ListID == 0 {
			continue
		}
		listIDs = append(listIDs, c.ListID)
		if c.IsDynamic {
			query = s.queries.NextCampaignSubscribersDynamic
		}
	}

	if len(listIDs) == 0 {
		return nil, nil
	}

	// AXM (LST-20): read after the caller's position, not after the campaign's
	// checkpoint in the DB.
	camps[0].LastSubscriberID = lastSubID

	var out []models.Subscriber
	read := func() error {
		out = out[:0]
		return query.Select(&out, camps[0].CampaignID, camps[0].CampaignType, camps[0].LastSubscriberID, camps[0].MaxSubscriberID, pq.Array(listIDs), limit)
	}
	if err := read(); err != nil {
		return out, err
	}

	// AXM (LST-47): the people of a follow-up's batch are judged once more as it is read, after every
	// reaction or send record of the original being written now is committed (see
	// core.GuardFollowupBatch). Whoever it holds back is left out by reading the batch again (the
	// query skips held people); that ends, as every round holds someone more.
	for {
		ids := make([]int, len(out))
		for i, sub := range out {
			ids[i] = sub.ID
		}
		held, err := s.core.GuardFollowupBatch(context.Background(), campID, ids)
		if err != nil {
			return nil, err
		}
		if held == 0 {
			break
		}
		if err := read(); err != nil {
			return out, err
		}
	}

	// AXM (LST-47): a follow-up is sent only while its last check is for the campaign as it is now.
	// After a pause and resume (or another change) that the sender has not checked yet, for example
	// while the pipe was being set up, the batch is dropped and the pipe stops without finishing:
	// the next scan checks the campaign again and starts it anew. Asked after the batch is read,
	// so a change made while it was read is caught too.
	stale, err := s.core.FollowupStale(campID)
	if err != nil {
		return nil, err
	}
	if stale {
		return nil, manager.ErrCampaignNotRunning
	}
	return out, nil
}

// GetCampaign fetches a campaign from the database.
func (s *store) GetCampaign(campID int) (*models.Campaign, error) {
	var out = &models.Campaign{}
	err := s.queries.GetCampaign.Get(out, campID, nil, nil, "default")
	return out, err
}

// UpdateCampaignStatus updates a campaign's status.
func (s *store) UpdateCampaignStatus(campID int, status string) error {
	_, err := s.queries.UpdateCampaignStatus.Exec(campID, status)
	return err
}

// UpdateCampaignCounts updates a campaign's status.
func (s *store) UpdateCampaignCounts(campID int, toSend int, sent int, lastSubID int) error {
	_, err := s.queries.UpdateCampaignCounts.Exec(campID, toSend, sent, lastSubID)
	return err
}

// GetAttachment fetches a media attachment blob.
func (s *store) GetAttachment(mediaID int) (models.Attachment, error) {
	m, err := s.core.GetMedia(mediaID, "", "", s.media)
	if err != nil {
		return models.Attachment{}, err
	}

	b, err := s.media.GetBlob(m.URL)
	if err != nil {
		return models.Attachment{}, err
	}

	return models.Attachment{
		Name:    m.Filename,
		Content: b,
		Header:  manager.MakeAttachmentHeader(m.Filename, "base64", m.ContentType),
	}, nil
}

// GetInlineAttachmentByFilename fetches a media item by filename and returns
// it as an inline attachment along with the Content-ID value. The lookup is
// uniform across filesystem and S3 providers because both use the same media
// store interface; the first match for a given filename is returned.
func (s *store) GetInlineAttachmentByFilename(filename string) (models.Attachment, string, error) {
	m, err := s.core.GetMedia(0, "", filename, s.media)
	if err != nil {
		return models.Attachment{}, "", err
	}

	b, err := s.media.GetBlob(m.URL)
	if err != nil {
		return models.Attachment{}, "", err
	}

	cid := manager.MakeContentID(m.Filename)
	return models.Attachment{
		Name:     m.Filename,
		Content:  b,
		Header:   manager.MakeInlineAttachmentHeader(m.Filename, "", m.ContentType, cid),
		IsInline: true,
	}, cid, nil
}

// CreateLink registers a URL with a UUID for tracking clicks and returns the UUID.
func (s *store) CreateLink(url string) (string, error) {
	// Create a new UUID for the URL. If the URL already exists in the DB
	// the UUID in the database is returned.
	uu, err := uuid.NewV4()
	if err != nil {
		return "", err
	}

	var out string
	if err := s.queries.CreateLink.Get(&out, uu, url); err != nil {
		return "", err
	}

	return out, nil
}

// RecordCampaignSends saves the send records of delivered campaign messages in one
// statement. The age of every message is taken here, at write time, so that a queued or
// retried record still gets the time it was sent at.
func (s *store) RecordCampaignSends(sends []models.CampaignSend) error {
	var (
		campIDs = make(pq.Int64Array, len(sends))
		subIDs  = make(pq.Int64Array, len(sends))
		ages    = make(pq.Int64Array, len(sends))
	)
	for i, r := range sends {
		campIDs[i] = int64(r.CampaignID)
		subIDs[i] = int64(r.SubscriberID)
		ages[i] = max(time.Since(r.SentAt).Milliseconds(), 0)
	}

	_, err := s.queries.AxmRecordCampaignSends.Exec(campIDs, subIDs, ages)
	return err
}

// CountCampaignSends returns the number of send records of a campaign.
func (s *store) CountCampaignSends(campID int) (int, error) {
	var n int
	err := s.queries.AxmCountCampaignSends.Get(&n, campID)
	return n, err
}

// GetCampaignSentIDs returns the subscribers above the ID lastSubID that have a send
// record for the campaign. A send records table that does not exist (it was dropped
// while the app runs) holds no record of anyone, so that case is an empty result and
// not an error: the campaign goes out, as it does when records cannot be written.
func (s *store) GetCampaignSentIDs(campID, lastSubID int) ([]int, error) {
	var out []int
	err := s.queries.AxmGetCampaignSentIDs.Select(&out, campID, lastSubID)

	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == pgUndefinedTable {
		return nil, nil
	}
	return out, err
}

// HoldCampaignLock reports whether this app instance holds the campaign sending lock.
// It takes the lock if it is free. A lock taken earlier is checked by using its
// connection: if the connection is gone, so is the lock, and another instance may
// have it by now.
func (s *store) HoldCampaignLock() (bool, error) {
	s.lockMut.Lock()
	defer s.lockMut.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), campaignLockTimeout)
	defer cancel()

	if s.lockConn != nil {
		var one int
		err := s.lockConn.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
		if err == nil {
			return true, nil
		}

		// The check can also fail on a connection that is alive (a timeout).
		// End the session so that the lock is certainly free.
		s.dropLockConn()
		return false, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}

	var ok bool
	if err := conn.QueryRowContext(ctx, campaignLockSQL).Scan(&ok); err != nil || !ok {
		conn.Close()
		return false, err
	}

	s.lockConn = conn
	return true, nil
}

// ReleaseCampaignLock gives the campaign sending lock up by ending its session.
func (s *store) ReleaseCampaignLock() {
	s.lockMut.Lock()
	defer s.lockMut.Unlock()

	if s.lockConn != nil {
		s.dropLockConn()
	}
}

// dropLockConn ends the session that holds the lock. Closing a pooled connection would
// only hand it back to the pool with the lock still on it, so the pool is told to
// discard it.
func (s *store) dropLockConn() {
	s.lockConn.Raw(func(any) error { return driver.ErrBadConn })
	s.lockConn.Close()
	s.lockConn = nil
}

// BlocklistSubscriber blocklists a subscriber permanently.
func (s *store) BlocklistSubscriber(id int64) error {
	_, err := s.queries.BlocklistSubscribers.Exec(pq.Int64Array{id})
	return err
}

// DeleteSubscriber deletes a subscriber from the DB.
func (s *store) DeleteSubscriber(id int64) error {
	// The query takes the IDs and the UUIDs as two arguments (AXM, LST-25: it was given one).
	_, err := s.queries.DeleteSubscribers.Exec(pq.Int64Array{id}, pq.StringArray{})
	return err
}
