-- name: axm-get-teams
-- The team roles with the number of their lists and members. $1 are the team-wide
-- permissions (LST-68): wide is the ones the default user role has, wide_members the number
-- of members whose user role has one of them (or is the super admin role).
SELECT r.id, r.name, r.axm_team_tag AS tag, r.axm_team_user_role_id AS user_role_id,
    (SELECT COUNT(*) FROM roles c WHERE c.parent_id = r.id) AS lists,
    (SELECT COUNT(*) FROM users u WHERE u.list_role_id = r.id) AS members,
    COALESCE((SELECT ARRAY(SELECT p FROM UNNEST(d.permissions) AS p WHERE p = ANY($1::TEXT[]) ORDER BY p)
        FROM roles d WHERE d.id = r.axm_team_user_role_id), '{}') AS wide,
    (SELECT COUNT(*) FROM users u JOIN roles ur ON ur.id = u.user_role_id
        WHERE u.list_role_id = r.id AND (ur.id = 1 OR ur.permissions && $1::TEXT[])) AS wide_members
FROM roles r
WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
ORDER BY r.name;

-- name: axm-get-team-members
SELECT u.id, u.username, u.name, u.type, u.status, u.user_role_id, u.list_role_id AS team_id
FROM users u JOIN roles r ON r.id = u.list_role_id
WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
ORDER BY u.list_role_id, u.id;

-- name: axm-get-team-role
-- One list role: its team tag is NULL when it is an ordinary list role.
SELECT id, name, axm_team_tag AS tag, axm_team_user_role_id AS user_role_id
FROM roles WHERE id = $1 AND parent_id IS NULL AND type = 'list';

-- name: axm-count-team-roles
SELECT COUNT(*) FROM roles WHERE parent_id IS NULL AND type = 'list' AND axm_team_tag IS NOT NULL;

-- name: axm-create-team-role
INSERT INTO roles (name, type, permissions, axm_team_tag, axm_team_user_role_id, created_at, updated_at)
VALUES ($1, 'list', '{}', $2, $3, NOW(), NOW()) RETURNING id;

-- name: axm-update-team-role
UPDATE roles SET name = $2, axm_team_tag = $3, axm_team_user_role_id = $4, updated_at = NOW()
WHERE id = $1 AND parent_id IS NULL AND type = 'list' AND axm_team_tag IS NOT NULL;

-- name: axm-retag-lists
-- Replaces the tag $1 by $2 on every list that has it (no duplicates, order kept).
UPDATE lists SET tags = ARRAY(
        SELECT t FROM (SELECT t, MIN(n) AS n FROM UNNEST(ARRAY_REPLACE(tags, $1, $2)) WITH ORDINALITY AS u(t, n) GROUP BY t) x ORDER BY n),
    updated_at = NOW()
WHERE $1 = ANY(tags);

-- name: axm-delete-team-role
DELETE FROM roles WHERE id = $1 AND parent_id IS NULL AND type = 'list' AND axm_team_tag IS NOT NULL;

-- name: axm-count-list-role-users
SELECT COUNT(*) FROM users WHERE list_role_id = $1;

-- name: axm-count-team-lists
SELECT COUNT(*) FROM lists WHERE $1 = ANY(tags);

-- name: axm-user-role-facts
-- For each user role id: whether it is the super admin role, and which of the team-wide
-- permissions $2 it carries (LST-68: lists:*_all, subscribers:get_all, campaigns:*_all).
SELECT id, (id = 1) AS super_admin, (permissions && $2::TEXT[]) AS wide,
    ARRAY(SELECT p FROM UNNEST(permissions) AS p WHERE p = ANY($2::TEXT[]) ORDER BY p) AS wide_perms
FROM roles WHERE id = ANY($1::INT[]) AND type = 'user' AND parent_id IS NULL;

-- name: axm-team-candidate-users
-- The users with the given IDs, their user role and list role, to check who may join a
-- team. $2 are the team-wide permissions.
SELECT u.id, (u.user_role_id = 1) AS super_admin, u.list_role_id,
    COALESCE(ur.permissions && $2::TEXT[], FALSE) AS wide
FROM users u LEFT JOIN roles ur ON ur.id = u.user_role_id
WHERE u.id = ANY($1::INT[]);

-- name: axm-user-role-team-use
-- The teams that use the user role $1: as their default user role, or by a member (LST-68).
SELECT r.name AS team, COALESCE(r.axm_team_user_role_id = $1, FALSE) AS is_default,
    (SELECT COUNT(*) FROM users u WHERE u.list_role_id = r.id AND u.user_role_id = $1) AS members
FROM roles r
WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
    AND (r.axm_team_user_role_id = $1 OR EXISTS (SELECT 1 FROM users u WHERE u.list_role_id = r.id AND u.user_role_id = $1))
ORDER BY r.name;

-- name: axm-release-team-members
UPDATE users SET list_role_id = NULL, updated_at = NOW()
WHERE list_role_id = $1 AND NOT (id = ANY($2::INT[]));

-- name: axm-assign-team-members
-- $3 is the user role members get (0 = keep theirs).
UPDATE users SET list_role_id = $1, user_role_id = (CASE WHEN $3::INT > 0 THEN $3::INT ELSE user_role_id END), updated_at = NOW()
WHERE id = ANY($2::INT[]);

-- name: axm-apply-team-user-role
UPDATE users SET user_role_id = $2, updated_at = NOW() WHERE list_role_id = $1 AND user_role_id != 1;

-- name: axm-visible-teams
-- The team tags on the lists $2 (all lists when $1), with the number of those lists and
-- the team role of each tag.
WITH t AS (
    SELECT tag, COUNT(*) AS lists
    FROM lists l, UNNEST(l.tags) AS tag
    WHERE STARTS_WITH(tag, $3) AND ($1 OR l.id = ANY($2::INT[]))
    GROUP BY tag
)
SELECT COALESCE(t.tag, r.axm_team_tag) AS tag, COALESCE(t.lists, 0) AS lists, r.id AS role_id, r.name AS role_name
FROM t FULL JOIN (SELECT id, name, axm_team_tag FROM roles WHERE parent_id IS NULL AND type = 'list' AND axm_team_tag IS NOT NULL) r
    ON r.axm_team_tag = t.tag
WHERE t.tag IS NOT NULL OR $1
ORDER BY 1;

-- name: axm-teamless-list-ids
SELECT id FROM lists WHERE NOT EXISTS (SELECT 1 FROM UNNEST(tags) AS t WHERE STARTS_WITH(t, $1));

-- name: axm-subscriber-other-teams
-- For subscribers $1: the teams of the lists they are active on that the viewer ($2 = the
-- lists the viewer may read) cannot read, with the number of such lists per team. A list
-- without a team counts for the team "". A list with two team tags counts for both.
SELECT sl.subscriber_id, COALESCE(t.tag, '') AS team, COUNT(*) AS lists
FROM subscriber_lists sl
JOIN lists l ON l.id = sl.list_id
LEFT JOIN LATERAL (
    SELECT tag FROM UNNEST(l.tags) AS tag WHERE STARTS_WITH(tag, $3)
    UNION ALL SELECT NULL WHERE NOT EXISTS (SELECT 1 FROM UNNEST(l.tags) AS tag WHERE STARTS_WITH(tag, $3))
) t ON TRUE
WHERE sl.subscriber_id = ANY($1::INT[]) AND sl.status != 'unsubscribed' AND NOT (sl.list_id = ANY($2::INT[]))
    AND l.axm_filter IS NULL
GROUP BY sl.subscriber_id, COALESCE(t.tag, '')
ORDER BY sl.subscriber_id, 2;

-- name: axm-subscriber-locked
-- Team boundary (LST-42). For subscribers $1: whether they have a subscription row (any
-- status, so unsubscribed rows and the exclusion rows of dynamic lists count too) on a list
-- outside $2, the lists the user may manage. Such a subscriber is "locked" for that user:
-- the row itself (name, e-mail, attributes, status, blocklist, delete, bounces) is not theirs
-- alone to change.
SELECT s.id AS subscriber_id,
    EXISTS (
        SELECT 1 FROM subscriber_lists sl
        WHERE sl.subscriber_id = s.id AND NOT (sl.list_id = ANY($2::INT[]))
    ) AS locked
FROM subscribers s WHERE s.id = ANY($1::INT[]);

-- name: axm-query-subscribers-multi
-- raw: true
-- query-subscribers for more than one list ID (LST-42). The upstream query joins
-- subscriber_lists, so a subscriber on two of the lists comes back twice. Here the same join
-- and conditions only pick the IDs, and the rows are read once per subscriber. The join stays
-- inside so that an SQL condition that names subscriber_lists keeps working.
SELECT subscribers.* FROM subscribers
    WHERE subscribers.id IN (
        SELECT subscribers.id FROM subscribers
        LEFT JOIN subscriber_lists
        ON (
            (CASE WHEN CARDINALITY($1::INT[]) > 0 THEN true ELSE false END)
            AND subscriber_lists.subscriber_id = subscribers.id
            AND ($2 = '' OR subscriber_lists.status = $2::subscription_status)
        )
        WHERE (CARDINALITY($1) = 0 OR subscriber_lists.list_id = ANY($1::INT[]))
        AND (CASE WHEN $3 != '' THEN name ~* $3 OR email ~* $3 ELSE TRUE END)
        AND %query%
    )
    ORDER BY %order% OFFSET $4 LIMIT (CASE WHEN $5 < 1 THEN NULL ELSE $5 END);

-- name: axm-query-subscribers-multi-count
-- raw: true
-- The count of axm-query-subscribers-multi: people, not subscription rows.
SELECT COUNT(DISTINCT subscribers.id) AS total FROM subscribers
    LEFT JOIN subscriber_lists
    ON (
        (CASE WHEN CARDINALITY($1::INT[]) > 0 THEN true ELSE false END)
        AND subscriber_lists.subscriber_id = subscribers.id
        AND ($2 = '' OR subscriber_lists.status = $2::subscription_status)
    )
    WHERE (CARDINALITY($1) = 0 OR subscriber_lists.list_id = ANY($1::INT[]))
    AND (CASE WHEN $3 != '' THEN name ~* $3 OR email ~* $3 ELSE TRUE END)
    AND %query%;

-- name: axm-lock-subscribers
-- Team boundary (LST-42). Takes the row locks of subscribers $1 for the rest of the
-- transaction. A new subscription row needs a key-share lock on its subscriber, so no
-- subscription can be added to these people between the "locked?" check and the change.
SELECT id FROM subscribers WHERE id = ANY($1::INT[]) ORDER BY id FOR UPDATE;

-- name: axm-update-subscriber-lists
-- The subscriptions part of update-subscriber-with-lists, without writing the subscriber row
-- (LST-42): for a subscriber whose profile the user may not change. $2 are the lists to be
-- on, $3 the status of new subscriptions, $4 whether to remove the others, $5 the lists
-- whose subscriptions may be removed (never empty: an ID no list has when there is none).
WITH s AS (
    SELECT id, status FROM subscribers WHERE id = $1
),
listIDs AS (
    SELECT id FROM lists WHERE id = ANY($2::INT[])
),
d AS (
    DELETE FROM subscriber_lists WHERE $4 = TRUE AND subscriber_id = $1
        AND list_id != ALL(SELECT id FROM listIDs)
        AND list_id = ANY($5::INT[])
)
INSERT INTO subscriber_lists (subscriber_id, list_id, status)
    SELECT s.id, l.id,
        (CASE WHEN s.status = 'blocklisted' THEN 'unsubscribed'::subscription_status ELSE $3::subscription_status END)
    FROM s, listIDs l
    ON CONFLICT (subscriber_id, list_id) DO UPDATE
    SET status = (
        CASE
            WHEN (SELECT status FROM s) = 'blocklisted' THEN 'unsubscribed'::subscription_status
            WHEN subscriber_lists.status = 'confirmed' THEN 'confirmed'
            WHEN subscriber_lists.status = 'unsubscribed' THEN 'unsubscribed'::subscription_status
            ELSE $3::subscription_status
        END
    );

-- name: axm-query-bounces
-- Bounce rows for a user who may not read all lists (LST-68): the people with a subscription
-- row (any status) on a list in $5. Same columns and filters as query-bounces ($1 bounce,
-- $2 campaign, $3 subscriber, $4 source). The sort is one of the five fields in $6 and $7
-- ('asc' or 'desc'), written as CASE branches, so that no text goes into the statement.
SELECT COUNT(*) OVER () AS total,
    bounces.id,
    bounces.type,
    bounces.source,
    bounces.meta,
    bounces.created_at,
    bounces.subscriber_id,
    subscribers.uuid AS subscriber_uuid,
    subscribers.email AS email,
    subscribers.status as subscriber_status,
    (
        CASE WHEN bounces.campaign_id IS NOT NULL
        THEN JSON_BUILD_OBJECT('id', bounces.campaign_id, 'name', campaigns.name)
        ELSE NULL END
    ) AS campaign
FROM bounces
LEFT JOIN subscribers ON (subscribers.id = bounces.subscriber_id)
LEFT JOIN campaigns ON (campaigns.id = bounces.campaign_id)
WHERE ($1 = 0 OR bounces.id = $1)
    AND ($2 = 0 OR bounces.campaign_id = $2)
    AND ($3 = 0 OR bounces.subscriber_id = $3)
    AND ($4 = '' OR bounces.source = $4)
    AND EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.subscriber_id = bounces.subscriber_id AND sl.list_id = ANY($5::INT[]))
ORDER BY
    (CASE WHEN $6 = 'email' AND $7 = 'asc' THEN subscribers.email END) ASC,
    (CASE WHEN $6 = 'email' AND $7 = 'desc' THEN subscribers.email END) DESC,
    (CASE WHEN $6 = 'campaign_name' AND $7 = 'asc' THEN campaigns.name END) ASC,
    (CASE WHEN $6 = 'campaign_name' AND $7 = 'desc' THEN campaigns.name END) DESC,
    (CASE WHEN $6 = 'source' AND $7 = 'asc' THEN bounces.source END) ASC,
    (CASE WHEN $6 = 'source' AND $7 = 'desc' THEN bounces.source END) DESC,
    (CASE WHEN $6 = 'created_at' AND $7 = 'asc' THEN bounces.created_at END) ASC,
    (CASE WHEN $6 = 'created_at' AND $7 = 'desc' THEN bounces.created_at END) DESC,
    (CASE WHEN $6 = 'type' AND $7 = 'asc' THEN bounces.type END) ASC,
    (CASE WHEN $6 = 'type' AND $7 = 'desc' THEN bounces.type END) DESC,
    bounces.id DESC
OFFSET $8 LIMIT (CASE WHEN $9 < 1 THEN NULL ELSE $9 END);

-- name: axm-bounce-scope
-- For bounces $1: their subscriber, and whether that subscriber has a subscription row (any
-- status) on a list in $2, the lists the user may read (LST-68). IDs that are no bounce are left out.
SELECT b.id, b.subscriber_id,
    EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.subscriber_id = b.subscriber_id AND sl.list_id = ANY($2::INT[])) AS readable
FROM bounces b WHERE b.id = ANY($1::INT[]) ORDER BY b.id;

-- name: axm-bounces-in-scope
-- Every bounce whose subscriber has a subscription row (any status) on a list in $1 (LST-68).
SELECT b.id, b.subscriber_id FROM bounces b
WHERE EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.subscriber_id = b.subscriber_id AND sl.list_id = ANY($1::INT[]))
ORDER BY b.id;

-- name: axm-delete-bounces-scoped
-- Deletes the bounces of subscribers $1 that are still in the user's reach ($2, the lists
-- the user may read) (LST-68).
DELETE FROM bounces
WHERE subscriber_id = ANY($1::INT[])
    AND EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.subscriber_id = bounces.subscriber_id AND sl.list_id = ANY($2::INT[]));

-- name: axm-bounced-subscribers-in-lists
-- The subscribers that have a bounce record and a subscription row (any status) on a list in
-- $1 (LST-68), for "blocklist all bounced subscribers" by a user who manages those lists.
SELECT DISTINCT b.subscriber_id FROM bounces b
WHERE EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.subscriber_id = b.subscriber_id AND sl.list_id = ANY($1::INT[]))
ORDER BY b.subscriber_id;

-- name: axm-readable-campaigns
-- Of campaigns $1, the ones that send to a list in $2, the lists the user may read (LST-68).
SELECT c.id FROM campaigns c
WHERE c.id = ANY($1::INT[])
    AND EXISTS (SELECT 1 FROM campaign_lists cl WHERE cl.campaign_id = c.id AND cl.list_id = ANY($2::INT[]));
