package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_14 adds the domains that may receive a campaign test message without being
// subscribers (LST-41), which start as a copy of the sender domains, and the table of the
// users' own confirmed addresses.
func V6_2_1_axm_14(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`INSERT INTO settings (key, value)
SELECT 'axm.test_domains', COALESCE((SELECT value FROM settings WHERE key = 'axm.sender_domains'), '[]'::JSONB)
ON CONFLICT (key) DO NOTHING;`)
	if err != nil {
		return err
	}
	// The address of a user that may receive a test as their own, once its mailbox confirmed it.
	_, err = db.Exec(`-- AXM test addresses (LST-41). Keep this block identical to V6_2_1_axm_14.
CREATE TABLE IF NOT EXISTS axm_test_addresses (
    user_id           INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE,
    email             TEXT NOT NULL CHECK (email = LOWER(email)),
    token_hash        TEXT NULL,
    token_expires_at  TIMESTAMP WITH TIME ZONE NULL,
    verified_at       TIMESTAMP WITH TIME ZONE NULL,
    last_sent_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_test_addr_token ON axm_test_addresses(token_hash) WHERE token_hash IS NOT NULL;
-- End of AXM test addresses.`)
	return err
}
