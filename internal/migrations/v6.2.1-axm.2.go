package migrations

import (
	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
	"log"
)

// V6_2_1_axm_2 adds optional account senders and verified reply addresses.
func V6_2_1_axm_2(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS reply_to TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS sender_addresses (
    id SERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE CHECK (email = LOWER(email)),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified')),
    token_hash TEXT NULL,
    token_expires_at TIMESTAMPTZ NULL,
    requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    verified_at TIMESTAMPTZ NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sender_token ON sender_addresses(token_hash) WHERE token_hash IS NOT NULL;

INSERT INTO settings (key, value) VALUES
 ('axm.sender_domains', '[]'), ('axm.default_from', '""'), ('axm.default_reply_to', '""')
 ON CONFLICT (key) DO NOTHING;`)
	return err
}
