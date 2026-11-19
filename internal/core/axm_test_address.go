package core

import (
	"database/sql"
	"strings"
)

// A user's own address for campaign test messages (LST-41). A profile address proves
// nothing by itself, so the user's mailbox confirms it once through a mailed link. One row
// per user holds the address that was asked for; a different profile address later is not
// confirmed until it is asked for and confirmed again.

// TestAddressVerified tells whether the mailbox of email confirmed it for the user.
func (c *Core) TestAddressVerified(userID int, email string) (bool, error) {
	var ok bool
	err := c.db.Get(&ok, `SELECT verified_at IS NOT NULL FROM axm_test_addresses WHERE user_id = $1 AND email = $2`,
		userID, strings.ToLower(email))
	if err == sql.ErrNoRows {
		return false, nil
	}
	return ok, err
}

// RequestTestAddress stores a new verification token for the user's address. It returns
// false, and changes nothing, when a mail for this user went out less than a minute ago.
func (c *Core) RequestTestAddress(userID int, email, tokenHash string) (bool, error) {
	var id int
	err := c.db.Get(&id, `INSERT INTO axm_test_addresses AS t (user_id, email, token_hash, token_expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '24 hours')
		ON CONFLICT (user_id) DO UPDATE SET email = EXCLUDED.email, token_hash = EXCLUDED.token_hash,
			token_expires_at = EXCLUDED.token_expires_at, verified_at = NULL, last_sent_at = NOW()
		WHERE t.last_sent_at <= NOW() - INTERVAL '60 seconds'
		RETURNING user_id`, userID, strings.ToLower(email), tokenHash)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// GetTestAddressToken returns the address a valid, unused token belongs to.
func (c *Core) GetTestAddressToken(tokenHash string) (string, error) {
	var email string
	err := c.db.Get(&email, `SELECT email FROM axm_test_addresses
		WHERE token_hash = $1 AND verified_at IS NULL AND token_expires_at > NOW()`, tokenHash)
	return email, err
}

// VerifyTestAddressToken confirms the address of a valid token and uses the token up.
func (c *Core) VerifyTestAddressToken(tokenHash string) error {
	var id int
	return c.db.Get(&id, `UPDATE axm_test_addresses SET verified_at = NOW(), token_hash = NULL, token_expires_at = NULL
		WHERE token_hash = $1 AND verified_at IS NULL AND token_expires_at > NOW() RETURNING user_id`, tokenHash)
}
