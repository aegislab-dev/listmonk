package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
	"golang.org/x/mod/semver"
)

// migFunc represents a migration function for a particular version.
// fn (generally) executes database migrations and additionally
// takes the filesystem and config objects in case there are additional bits
// of logic to be performed before executing upgrades. fn is idempotent.
type migFunc struct {
	version string
	fn      func(*sqlx.DB, stuffbin.FileSystem, *koanf.Koanf, *log.Logger) error
}

// migList is the list of available migList ordered by the semver.
// Each migration is a Go file in internal/migrations named after the semver.
// The functions are named as: v0.7.0 => migrations.V0_7_0() and are idempotent.
var migList = []migFunc{
	{"v0.4.0", migrations.V0_4_0},
	{"v0.7.0", migrations.V0_7_0},
	{"v0.8.0", migrations.V0_8_0},
	{"v0.9.0", migrations.V0_9_0},
	{"v1.0.0", migrations.V1_0_0},
	{"v2.0.0", migrations.V2_0_0},
	{"v2.1.0", migrations.V2_1_0},
	{"v2.2.0", migrations.V2_2_0},
	{"v2.3.0", migrations.V2_3_0},
	{"v2.4.0", migrations.V2_4_0},
	{"v2.5.0", migrations.V2_5_0},
	{"v3.0.0", migrations.V3_0_0},
	{"v4.0.0", migrations.V4_0_0},
	{"v4.1.0", migrations.V4_1_0},
	{"v5.0.0", migrations.V5_0_0},
	{"v5.1.0", migrations.V5_1_0},
	{"v6.0.0", migrations.V6_0_0},
	{"v6.1.0", migrations.V6_1_0},
	{"v6.2.0", migrations.V6_2_0},
	{"v6.2.1-axm.1", migrations.V6_2_1_axm_1},
	{"v6.2.1-axm.2", migrations.V6_2_1_axm_2},
	{"v6.2.1-axm.3", migrations.V6_2_1_axm_3},
	{"v6.2.1-axm.4", migrations.V6_2_1_axm_4},
	{"v6.2.1-axm.5", migrations.V6_2_1_axm_5},
	{"v6.2.1-axm.6", migrations.V6_2_1_axm_6},
	{"v6.2.1-axm.7", migrations.V6_2_1_axm_7},
	{"v6.2.1-axm.8", migrations.V6_2_1_axm_8},
	{"v6.2.1-axm.9", migrations.V6_2_1_axm_9},
	{"v6.2.1-axm.10", migrations.V6_2_1_axm_10},
	{"v6.2.1-axm.11", migrations.V6_2_1_axm_11},
	{"v6.2.1-axm.12", migrations.V6_2_1_axm_12},
	{"v6.2.1-axm.13", migrations.V6_2_1_axm_13},
	{"v6.2.1-axm.14", migrations.V6_2_1_axm_14},
	{"v6.2.1-axm.15", migrations.V6_2_1_axm_15},
	{"v6.2.1-axm.16", migrations.V6_2_1_axm_16},
	{"v6.2.1-axm.17", migrations.V6_2_1_axm_17},
	{"v6.2.1-axm.18", migrations.V6_2_1_axm_18},
	{"v6.2.1-axm.19", migrations.V6_2_1_axm_19},
}

// upgrade upgrades the database to the current version by running SQL migration files
// for all version from the last known version to the current one.
// If record is false, migration versions are not recorded in the DB (used for nightly builds).
func upgrade(db *sqlx.DB, fs stuffbin.FileSystem, prompt bool, record bool) {
	if prompt {
		var ok string
		fmt.Printf("** IMPORTANT: Take a backup of the database before upgrading.\n")
		fmt.Print("continue (y/n)?  ")
		if _, err := fmt.Scanf("%s", &ok); err != nil {
			lo.Fatalf("error reading value from terminal: %v", err)
		}
		if strings.ToLower(ok) != "y" {
			fmt.Println("upgrade cancelled")
			return
		}
	}

	_, toRun, err := getPendingMigrations(db)
	if err != nil {
		lo.Fatalf("error checking migrations: %v", err)
	}

	// No migrations to run.
	if len(toRun) == 0 {
		lo.Printf("no upgrades to run. Database is up to date.")
		return
	}

	// Execute migrations in succession.
	for _, m := range toRun {
		lo.Printf("running migration %s", m.version)
		if err := m.fn(db, fs, ko, lo); err != nil {
			lo.Fatalf("error running migration %s: %v", m.version, err)
		}

		// Record the migration version in the settings table. There was no
		// settings table until v0.7.0, so ignore the no-table errors.
		// For nightly builds, skip recording so migrations re-run on each boot.
		if record {
			if err := recordMigrationVersion(m.version, db); err != nil {
				if isTableNotExistErr(err) {
					continue
				}
				lo.Fatalf("error recording migration version %s: %v", m.version, err)
			}
		}
	}

	lo.Printf("upgrade complete")
}

// checkUpgrade checks if the current database schema matches the expected
// binary version.
func checkUpgrade(db *sqlx.DB) {
	lastVer, toRun, err := getPendingMigrations(db)
	if err != nil {
		lo.Fatalf("error checking migrations: %v", err)
	}

	// No migrations to run.
	if len(toRun) == 0 {
		return
	}

	var vers []string
	for _, m := range toRun {
		vers = append(vers, m.version)
	}

	lo.Fatalf(`there are %d pending database upgrade(s): %v. The last upgrade was %s. Backup the database and run listmonk --upgrade`,
		len(toRun), vers, lastVer)
}

// getPendingMigrations gets the pending migrations by comparing the migrations recorded
// in the DB against all migrations listed in `migrations`. It returns the highest recorded
// semver and the migrations to run, in list order.
func getPendingMigrations(db *sqlx.DB) (string, []migFunc, error) {
	recorded, err := getRecordedMigrations(db)
	if err != nil {
		return "", nil, err
	}
	last := "v0.0.0"
	for _, v := range recorded {
		if semver.Compare(v, last) > 0 {
			last = v
		}
	}
	return last, pendingMigrations(migList, recorded), nil
}

// pendingMigrations picks the migrations to run. Upstream's rule is "everything above the
// last recorded version". The fork keeps that and adds one more: a migration that was never
// recorded and is above the oldest recorded version runs too, even if a higher number was
// recorded first. Fork migration numbers are handed out per task (LST-17's .12 was released
// before LST-27's .10 and .11, say), so a database at .12 must still get .10 and .11 when
// they arrive. A migration below the oldest recorded version belongs to history from before
// this database was installed and is skipped, as a new install records only its last version.
func pendingMigrations(list []migFunc, recorded []string) []migFunc {
	done := map[string]bool{}
	oldest, newest := "", "v0.0.0"
	for _, v := range recorded {
		done[v] = true
		if oldest == "" || semver.Compare(v, oldest) < 0 {
			oldest = v
		}
		if semver.Compare(v, newest) > 0 {
			newest = v
		}
	}
	if oldest == "" {
		oldest = "v0.0.0"
	}
	var toRun []migFunc
	for _, m := range list {
		if semver.Compare(m.version, newest) > 0 || (!done[m.version] && semver.Compare(m.version, oldest) > 0) {
			toRun = append(toRun, m)
		}
	}
	return toRun
}

// getRecordedMigrations returns every migration version recorded in the DB, oldest first as
// recorded. There is none before v0.7.0 or on a database that was never installed.
func getRecordedMigrations(db *sqlx.DB) ([]string, error) {
	var raw []byte
	if err := db.Get(&raw, `SELECT COALESCE((SELECT value FROM settings WHERE key='migrations'), '[]'::JSONB)`); err != nil {
		if isTableNotExistErr(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("error reading the recorded migrations: %v", err)
	}
	return out, nil
}

// isTableNotExistErr checks if the given error represents a Postgres/pq
// "table does not exist" error.
func isTableNotExistErr(err error) bool {
	if p, ok := err.(*pq.Error); ok {
		// `settings` table does not exist. It was introduced in v0.7.0.
		if p.Code == "42P01" {
			return true
		}
	}
	return false
}
