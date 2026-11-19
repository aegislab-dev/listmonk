package core

import (
	"database/sql"
	"net/http"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

func (c *Core) ListSenderAddresses() ([]models.SenderAddress, error) {
	out := []models.SenderAddress{}
	err := c.q.ListSenderAddresses.Select(&out)
	return out, err
}

func (c *Core) SenderAddressVerified(email string) (bool, error) {
	var out models.SenderAddress
	err := c.q.GetSenderAddress.Get(&out, email)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return out.Status == "verified", err
}

// RequestSenderAddress atomically rotates a pending token, enforcing the resend interval.
func (c *Core) RequestSenderAddress(email, hash string, userID int) (models.SenderAddress, error) {
	var out models.SenderAddress
	err := c.q.UpsertSenderAddress.Get(&out, email, hash, userID)
	if err == sql.ErrNoRows {
		return out, echo.NewHTTPError(http.StatusTooManyRequests, c.i18n.T("senders.resendWait"))
	}
	return out, err
}

func (c *Core) GetSenderToken(hash string) (models.SenderAddress, error) {
	var out models.SenderAddress
	err := c.q.GetSenderToken.Get(&out, hash)
	return out, err
}

func (c *Core) VerifySenderToken(hash string) error {
	var email string
	return c.q.VerifySenderToken.Get(&email, hash)
}

func (c *Core) DeleteSenderAddress(id, userID int, admin bool) error {
	// Serialize deletion with token rotation/verification, and protect the ownership check.
	tx, err := c.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var out models.SenderAddress
	if err := tx.Get(&out, "SELECT * FROM sender_addresses WHERE id=$1 FOR UPDATE", id); err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, c.i18n.T("senders.notFound"))
		}
		return err
	}
	if !admin && (!out.RequestedBy.Valid || out.RequestedBy.Int != userID) {
		return echo.NewHTTPError(http.StatusForbidden, c.i18n.T("senders.deleteDenied"))
	}
	var inUse bool
	if err := tx.Stmtx(c.q.SenderAddressInUse).Get(&inUse, out.Email); err != nil {
		return err
	}
	if inUse {
		return echo.NewHTTPError(http.StatusConflict, c.i18n.T("senders.inUse"))
	}
	if _, err := tx.Stmtx(c.q.DeleteSenderAddress).Exec(id); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Core) UpdateCampaignFrom(id int, from string) error {
	_, err := c.q.UpdateCampaignFrom.Exec(id, from)
	return err
}
