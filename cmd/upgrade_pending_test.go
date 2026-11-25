package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func names(list []migFunc) []string {
	out := []string{}
	for _, m := range list {
		out = append(out, m.version)
	}
	return out
}

// Fork migration numbers are handed out per task and tasks do not merge in number order
// (LST-17's .12 is released before LST-27's .10 and .11). A database must still get the
// numbers that were missing when it moved past them.
func TestPendingMigrationsOutOfOrder(t *testing.T) {
	list := []migFunc{{version: "v6.0.0"}, {version: "v6.2.0"}, {version: "v6.2.1-axm.9"}, {version: "v6.2.1-axm.10"}, {version: "v6.2.1-axm.11"}, {version: "v6.2.1-axm.12"}, {version: "v6.2.1-axm.13"}}
	for name, c := range map[string]struct {
		recorded []string
		want     []string
	}{
		"nothing recorded runs everything":     {nil, []string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9", "v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.12", "v6.2.1-axm.13"}},
		"the usual upgrade":                    {[]string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9"}, []string{"v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.12", "v6.2.1-axm.13"}},
		"up to date":                           {[]string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9", "v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.12", "v6.2.1-axm.13"}, []string{}},
		"a number was skipped, then recorded":  {[]string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9", "v6.2.1-axm.12"}, []string{"v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.13"}},
		"the skipped ones ran later, in order": {[]string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9", "v6.2.1-axm.12", "v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.13"}, []string{}},
		"only .11 was recorded late":           {[]string{"v6.0.0", "v6.2.0", "v6.2.1-axm.9", "v6.2.1-axm.12", "v6.2.1-axm.10"}, []string{"v6.2.1-axm.11", "v6.2.1-axm.13"}},
		// A new install records only its last version: older numbers are history, not pending.
		"fresh install at .12":  {[]string{"v6.2.1-axm.12"}, []string{"v6.2.1-axm.13"}},
		"fresh install at .13":  {[]string{"v6.2.1-axm.13"}, []string{}},
		"fresh official v6.2.0": {[]string{"v6.2.0"}, []string{"v6.2.1-axm.9", "v6.2.1-axm.10", "v6.2.1-axm.11", "v6.2.1-axm.12", "v6.2.1-axm.13"}},
	} {
		if got := names(pendingMigrations(list, c.recorded)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

// The recorded versions are read from the settings table the way upgrade records them.
func TestGetPendingMigrationsFromDB(t *testing.T) {
	e := newSendsEnv(t, 0)
	run := func(recorded []string) []string {
		b, _ := json.Marshal(recorded)
		e.db.MustExec(`DELETE FROM settings WHERE key = 'migrations'`)
		e.db.MustExec(`INSERT INTO settings (key, value) VALUES ('migrations', $1::JSONB)`, string(b))
		last, toRun, err := getPendingMigrations(e.db)
		if err != nil {
			t.Fatal(err)
		}
		_ = last
		return names(toRun)
	}
	all := names(migList)
	if got := run([]string{all[len(all)-1]}); len(got) != 0 {
		t.Fatalf("a database at the last version has pending migrations: %v", got)
	}
	// Recorded as an install plus an upgrade that ran the newest number before an older one.
	got := run([]string{"v6.2.0", "v6.2.1-axm.12", "v6.2.1-axm.5"})
	for _, v := range got {
		if v == "v6.2.1-axm.12" || v == "v6.2.1-axm.5" {
			t.Fatalf("a recorded migration is pending again: %v", got)
		}
	}
	if len(got) == 0 {
		t.Fatal("the migrations between were not found pending")
	}
	last, _, _ := getPendingMigrations(e.db)
	if last != "v6.2.1-axm.12" {
		t.Fatalf("highest recorded is %s", last)
	}
	// No record at all is a database that was never installed: everything is pending.
	e.db.MustExec(`DELETE FROM settings WHERE key = 'migrations'`)
	if _, toRun, err := getPendingMigrations(e.db); err != nil || len(toRun) != len(migList) {
		t.Fatalf("%d of %d, %v", len(toRun), len(migList), err)
	}
}

func TestCampaignCountUpgradeSmoke(t *testing.T) {
	e := newSendsEnv(t, 0)
	e.db.MustExec(`INSERT INTO settings (key, value) VALUES ('migrations', '["v6.2.1-axm.17"]')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	e.db.MustExec(`DROP FUNCTION axm_filter_members_unlimited(JSONB, INT, INT, INT)`)
	e.db.MustExec(`DROP TABLE axm_campaign_expired_sends`)
	for i := 0; i < 2; i++ {
		upgrade(e.db, nil, false, true)
		var recorded string
		e.get(&recorded, `SELECT value->>-1 FROM settings WHERE key = 'migrations'`)
		if recorded != "v6.2.1-axm.19" {
			t.Fatalf("upgrade %d: got %s", i+1, recorded)
		}
		var count int
		e.get(&count, `SELECT COUNT(*) FROM axm_campaign_expired_sends`)
		if count != 0 {
			t.Fatalf("upgrade %d: got %d expired totals", i+1, count)
		}
		last, pending, err := getPendingMigrations(e.db)
		if err != nil || last != recorded || len(pending) != 0 {
			t.Fatalf("upgrade %d: %s, %+v, %v", i+1, last, names(pending), err)
		}
	}
}
