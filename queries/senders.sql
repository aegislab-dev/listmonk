-- name: list-sender-addresses
SELECT s.*, COALESCE(u.name, '') AS requester_name FROM sender_addresses s
LEFT JOIN users u ON u.id=s.requested_by ORDER BY s.email;

-- name: get-sender-address
SELECT * FROM sender_addresses WHERE email=$1;

-- name: upsert-sender-address
WITH sender AS (
INSERT INTO sender_addresses(email, token_hash, token_expires_at, requested_by)
VALUES ($1, $2, NOW() + INTERVAL '24 hours', $3)
ON CONFLICT(email) DO UPDATE SET
 token_hash=EXCLUDED.token_hash, token_expires_at=EXCLUDED.token_expires_at,
 requested_by=EXCLUDED.requested_by, last_sent_at=NOW(), updated_at=NOW()
WHERE sender_addresses.status='pending' AND sender_addresses.last_sent_at <= NOW() - INTERVAL '60 seconds'
RETURNING *
)
SELECT s.*, COALESCE(u.name, '') AS requester_name FROM sender s
LEFT JOIN users u ON u.id=s.requested_by;

-- name: get-sender-token
SELECT * FROM sender_addresses WHERE token_hash=$1 AND status='pending' AND token_expires_at > NOW();

-- name: verify-sender-token
UPDATE sender_addresses SET status='verified', verified_at=NOW(), updated_at=NOW(),
 token_hash=NULL, token_expires_at=NULL
WHERE token_hash=$1 AND status='pending' AND token_expires_at > NOW() RETURNING email;

-- name: sender-address-in-use
SELECT EXISTS(SELECT 1 FROM campaigns WHERE status IN ('scheduled','running','paused')
AND LOWER(TRIM(CASE WHEN reply_to LIKE '%<%>' THEN SUBSTRING(reply_to FROM '<([^<>]+)>$') ELSE reply_to END))=$1);

-- name: delete-sender-address
DELETE FROM sender_addresses WHERE id=$1;

-- name: update-campaign-from
UPDATE campaigns SET from_email=$2, updated_at=NOW() WHERE id=$1 AND status='draft';
