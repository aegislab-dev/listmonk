package core

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
)

func presetErr(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted", what)
	}
}

func TestColumnPresetsDBCrud(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)

	// Built-in presets come first and need no row.
	all, err := c.GetColumnPresets()
	if err != nil || len(all) != 2 || all[0].ID != "creator" || all[1].ID != "b2b" || !all[1].Builtin {
		t.Fatal(all, err)
	}

	p, err := c.CreateColumnPreset("  Sales ops ", []string{" company", "mrr", "title"})
	if err != nil || p.Name != "Sales ops" || p.Builtin || !reflect.DeepEqual(p.Columns, []string{"company", "mrr", "title"}) {
		t.Fatalf("%+v %v", p, err)
	}
	id, ok := userPresetID(p.ID)
	if !ok {
		t.Fatal(p.ID)
	}
	if !c.ValidColumnPreset(p.ID) || c.ValidColumnPreset("u"+strconv.Itoa(id+1)) || c.ValidColumnPreset("u0") {
		t.Fatal("ValidColumnPreset")
	}

	// Rejected: names, columns.
	long := strings.Repeat("가", 41)
	for name, f := range map[string]func() error{
		"empty name":      func() error { _, e := c.CreateColumnPreset("   ", []string{"a"}); return e },
		"long name":       func() error { _, e := c.CreateColumnPreset(long, []string{"a"}); return e },
		"control name":    func() error { _, e := c.CreateColumnPreset("a\nb", []string{"a"}); return e },
		"same name":       func() error { _, e := c.CreateColumnPreset("SALES OPS", []string{"a"}); return e },
		"builtin ko name": func() error { _, e := c.CreateColumnPreset("크리에이터", []string{"a"}); return e },
		"builtin en name": func() error { _, e := c.CreateColumnPreset("b2b sales", []string{"a"}); return e },
		"no columns":      func() error { _, e := c.CreateColumnPreset("x", nil); return e },
		"nine columns": func() error {
			_, e := c.CreateColumnPreset("x", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"})
			return e
		},
		"duplicate column": func() error { _, e := c.CreateColumnPreset("x", []string{"a", "b", "a"}); return e },
		"blank column":     func() error { _, e := c.CreateColumnPreset("x", []string{"a", "  "}); return e },
		"control column":   func() error { _, e := c.CreateColumnPreset("x", []string{"a\x00"}); return e },
		"long column":      func() error { _, e := c.CreateColumnPreset("x", []string{strings.Repeat("k", 65)}); return e },
	} {
		presetErr(t, name, f())
	}
	// Eight columns, a name of 40 characters and a column that is not in the standard are fine.
	if _, err := c.CreateColumnPreset(strings.Repeat("가", 40), []string{"1", "2", "3", "4", "5", "6", "7", "회사 전화"}); err != nil {
		t.Fatal(err)
	}

	// Edit: name, order, columns; a name taken by another preset is refused, its own is not.
	up, err := c.UpdateColumnPreset(id, "Sales ops", []string{"title", "company"})
	if err != nil || !reflect.DeepEqual(up.Columns, []string{"title", "company"}) {
		t.Fatalf("%+v %v", up, err)
	}
	other, err := c.CreateColumnPreset("Other", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateColumnPreset(id, "other", []string{"a"}); err == nil {
		t.Fatal("renamed onto another preset")
	}
	if _, err := c.UpdateColumnPreset(id+1000, "Z", []string{"a"}); err == nil {
		t.Fatal("updated a missing preset")
	}
	if _, err := c.UpdateColumnPreset(id, "Sales ops", nil); err == nil {
		t.Fatal("emptied a preset")
	}

	// The list is by name, case ignored.
	got, err := c.GetColumnPresets()
	if err != nil || len(got) != 5 {
		t.Fatal(len(got), err)
	}
	if got[2].Name != "Other" && got[2].Name != strings.Repeat("가", 40) {
		t.Fatalf("order: %v", got)
	}

	// The 50th preset is the last.
	for i := len(got) - 2; i < 50; i++ {
		if _, err := c.CreateColumnPreset("bulk "+strconv.Itoa(i), []string{"a"}); err != nil {
			t.Fatal(i, err)
		}
	}
	presetErr(t, "51st preset", func() error { _, e := c.CreateColumnPreset("one too many", []string{"a"}); return e }())
	oid, _ := userPresetID(other.ID)
	if n, err := c.DeleteColumnPreset(oid); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := c.CreateColumnPreset("one more", []string{"a"}); err != nil {
		t.Fatal("a deleted preset frees a place:", err)
	}
	if _, err := c.DeleteColumnPreset(oid); err == nil {
		t.Fatal("deleted twice")
	}
}

func TestColumnPresetsDBDeleteResetsLists(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)
	mine, err := c.CreateColumnPreset("Mine", []string{"company", "mrr"})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := c.CreateColumnPreset("Keep", []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, preset string) models.List {
		l, err := c.CreateList(models.List{Name: name, AxmPreset: preset})
		if err != nil {
			t.Fatal(name, err)
		}
		if preset != "" && l.AxmPreset != preset {
			t.Fatal(name, l.AxmPreset)
		}
		return l
	}
	var onMine []models.List
	for _, n := range []string{"G", "A", "F", "B", "E", "C", "D"} {
		onMine = append(onMine, mk("on "+n, mine.ID))
	}
	onKeep := mk("on keep", keep.ID)
	onB2B := mk("on b2b", "b2b")
	onDefault := mk("on default", "")

	// Usage: counts for every preset, and the first five names by name (only when asked).
	usage, err := c.GetColumnPresetUsage(true)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ColumnPreset{}
	for _, p := range usage {
		byID[p.ID] = p
	}
	if byID[mine.ID].ListCount != 7 || byID[keep.ID].ListCount != 1 || byID["b2b"].ListCount != 1 || byID["creator"].ListCount != 1 {
		t.Fatalf("%+v", usage)
	}
	var names []string
	for _, l := range byID[mine.ID].Lists {
		names = append(names, l.Name)
	}
	if !reflect.DeepEqual(names, []string{"on A", "on B", "on C", "on D", "on E"}) {
		t.Fatal(names)
	}
	noNames, _ := c.GetColumnPresetUsage(false)
	for _, p := range noNames {
		if len(p.Lists) != 0 {
			t.Fatal("names leaked", p)
		}
	}

	// Deleting resets the lists that used it, and only those.
	mid, _ := userPresetID(mine.ID)
	n, err := c.DeleteColumnPreset(mid)
	if err != nil || n != 7 {
		t.Fatal(n, err)
	}
	for _, l := range onMine {
		got, _ := c.GetList(l.ID, "")
		if got.AxmPreset != DefaultColumnPreset {
			t.Fatal(l.Name, got.AxmPreset)
		}
	}
	for l, want := range map[int]string{onKeep.ID: keep.ID, onB2B.ID: "b2b", onDefault.ID: "creator"} {
		got, _ := c.GetList(l, "")
		if got.AxmPreset != want {
			t.Fatal(l, got.AxmPreset, want)
		}
	}
	if c.ValidColumnPreset(mine.ID) {
		t.Fatal("deleted preset still valid")
	}
	// A list cannot be saved with it any more, but a list that kept the ID reads as default.
	if _, err := c.UpdateList(onKeep.ID, models.List{Name: "on keep", AxmPreset: mine.ID}); err == nil {
		t.Fatal("saved a list with a deleted preset")
	}
	if _, err := c.CreateList(models.List{Name: "late", AxmPreset: mine.ID}); err == nil {
		t.Fatal("made a list with a deleted preset")
	}
}

func TestColumnPresetsDBInherit(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)
	mine, err := c.CreateColumnPreset("Mine", []string{"company"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := c.CreateList(models.List{Name: "base", AxmPreset: mine.ID})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := c.CreateList(models.List{Name: "plain"})
	dyn := func(name string, ids ...int) models.List {
		t.Helper()
		j := types.JSONText(testFilter("lists", ids))
		l, err := c.CreateList(models.List{Name: name, AxmFilter: &j})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if got := dyn("inherits mine", base.ID, plain.ID).AxmPreset; got != mine.ID {
		t.Fatal(got)
	}
	if got := dyn("inherits default", plain.ID, base.ID).AxmPreset; got != DefaultColumnPreset {
		t.Fatal(got)
	}
	// A base list whose preset ID points nowhere gives the default.
	db.MustExec(`UPDATE lists SET axm_preset='u9999' WHERE id=$1`, base.ID)
	if got := dyn("dangling", base.ID).AxmPreset; got != DefaultColumnPreset {
		t.Fatal(got)
	}
}

func TestColumnPresetsDBMigration(t *testing.T) {
	db, _, _ := dynamicTestDB(t)
	// The schema before this release, then the upgrade twice.
	db.MustExec(`DROP TABLE axm_column_presets`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_12(db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	db.MustExec(`INSERT INTO axm_column_presets (name, columns) VALUES ('Kept', '{a}')`)
	if err := migrations.V6_2_1_axm_12(db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	db.Get(&n, `SELECT COUNT(*) FROM axm_column_presets`)
	if n != 1 {
		t.Fatal(n)
	}
	// The case-insensitive unique name, and the sequence a backup role would need.
	if _, err := db.Exec(`INSERT INTO axm_column_presets (name, columns) VALUES ('KEPT', '{a}')`); err == nil {
		t.Fatal("same name in other case accepted")
	}
	var seqs int
	db.Get(&seqs, `SELECT COUNT(*) FROM pg_depend d JOIN pg_class c ON c.oid = d.objid AND c.relkind = 'S'
		WHERE d.refobjid = 'axm_column_presets'::regclass`)
	if seqs != 1 {
		t.Fatal("sequences:", seqs)
	}
}
