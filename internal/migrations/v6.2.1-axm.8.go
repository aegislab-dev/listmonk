package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_8 adds team roles: nullable roles.axm_team_tag and roles.axm_team_user_role_id, a unique index on the tag, three
// functions and two triggers that keep a team role's lists equal to the lists carrying its
// tag. No table is added, so a backup role with per-object grants needs nothing new. The
// roles and lists queries, the permission checks and the send engine are not touched.
func V6_2_1_axm_8(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM team roles. Keep this block identical to V6_2_1_axm_8.
-- Team roles: a list role whose axm_team_tag is set holds exactly the lists that carry
-- that tag, as ordinary child rows (parent_id, list_id) with list:get and list:manage.
-- Nothing else about roles changes, so the permission checks read them as before.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS axm_team_tag VARCHAR(100) NULL;
-- The user role that members of the team get when an admin adds them (a convenience default).
ALTER TABLE roles ADD COLUMN IF NOT EXISTS axm_team_user_role_id INTEGER NULL REFERENCES roles(id) ON DELETE SET NULL ON UPDATE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_axm_team_tag ON roles(axm_team_tag) WHERE axm_team_tag IS NOT NULL;

-- Makes the child rows of the team roles match the lists' tags. p_role_id / p_list_id
-- limit the work to one role or one list (NULL = all). Safe to run again.
CREATE OR REPLACE FUNCTION axm_sync_team_lists(p_role_id INT, p_list_id INT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM roles c USING roles r
    WHERE c.parent_id = r.id AND r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
        AND (p_role_id IS NULL OR r.id = p_role_id)
        AND (p_list_id IS NULL OR c.list_id = p_list_id)
        AND NOT EXISTS (SELECT 1 FROM lists l WHERE l.id = c.list_id AND r.axm_team_tag = ANY(l.tags));

    INSERT INTO roles (parent_id, list_id, type, permissions)
    SELECT r.id, l.id, 'list', ARRAY['list:get', 'list:manage']
    FROM roles r JOIN lists l ON r.axm_team_tag = ANY(l.tags)
    WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
        AND (p_role_id IS NULL OR r.id = p_role_id)
        AND (p_list_id IS NULL OR l.id = p_list_id)
    ON CONFLICT (parent_id, list_id) DO UPDATE SET permissions = EXCLUDED.permissions;
END;
$$;

CREATE OR REPLACE FUNCTION axm_team_lists_trigger()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM roles WHERE axm_team_tag IS NOT NULL) THEN
        PERFORM axm_sync_team_lists(NULL, NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_team_lists_sync ON lists;
CREATE TRIGGER axm_team_lists_sync AFTER INSERT OR UPDATE OF tags ON lists
    FOR EACH ROW EXECUTE FUNCTION axm_team_lists_trigger();

CREATE OR REPLACE FUNCTION axm_team_role_trigger()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM axm_sync_team_lists(NEW.id, NULL);
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_team_role_sync ON roles;
CREATE TRIGGER axm_team_role_sync AFTER INSERT OR UPDATE OF axm_team_tag ON roles
    FOR EACH ROW WHEN (NEW.parent_id IS NULL AND NEW.type = 'list' AND NEW.axm_team_tag IS NOT NULL)
    EXECUTE FUNCTION axm_team_role_trigger();
-- End of AXM team roles.`)
	return err
}
