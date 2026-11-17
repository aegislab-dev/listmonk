package core

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// ColumnPreset is a set of attribute columns the subscribers table shows for a list.
type ColumnPreset struct {
	ID string `json:"id"`
	// Name is the name of a preset people made. Built-in presets have none: their name is the
	// i18n key lists.preset.<id>.
	Name string `json:"name,omitempty"`
	// Columns are attribute keys in display order.
	Columns []string `json:"columns"`
	// Builtin presets are fixed in this file and cannot be edited or deleted.
	Builtin bool `json:"builtin"`
	// ListCount and Lists (up to PresetListSample names, only for who may manage all lists)
	// are filled by the presets API only.
	ListCount int          `json:"list_count"`
	Lists     []PresetList `json:"lists,omitempty"`
}

// PresetList is a list that uses a preset.
type PresetList struct {
	ID   int    `json:"id" db:"id"`
	Name string `json:"name" db:"name"`

	Preset string `json:"-" db:"preset"`
}

// DefaultColumnPreset is the preset of new lists and of the subscribers page without a list,
// and the one a list falls back to when its preset is deleted.
const DefaultColumnPreset = "creator"

// ColumnPresets are the built-in presets. They are fixed here on purpose: the screen that
// makes presets only adds to them. The columns are keys of the standard attribute dictionary
// (axm_standard_attribs.json; a test checks it). Renaming one there and here is all it takes
// when the standard changes, as lists store only the preset ID.
//
// Presets people make live in the table axm_column_presets and get the ID "u<id>". A list
// whose preset no longer exists is shown with the default preset.
var ColumnPresets = []ColumnPreset{
	{ID: "creator", Columns: []string{"country", "follower"}, Builtin: true},
	{ID: "b2b", Columns: []string{"company", "title", "industry", "country"}, Builtin: true},
}

// builtinPresetNames are the names a preset people make cannot take (lower case, Korean and
// English, the same as in the i18n files).
var builtinPresetNames = map[string][]string{
	"creator": {"크리에이터", "creator"},
	"b2b":     {"b2b 세일즈", "b2b sales"},
}

const (
	presetNameMax    = 40
	presetColumnsMax = 8
	presetsMax       = 50
	// PresetListSample is how many list names a preset shows in the delete confirmation.
	PresetListSample = 5
)

func builtinPreset(id string) (ColumnPreset, bool) {
	for _, p := range ColumnPresets {
		if p.ID == id {
			p.Columns = append([]string{}, p.Columns...)
			return p, true
		}
	}
	return ColumnPreset{}, false
}

// userPresetID returns the row ID of a preset ID like "u12".
func userPresetID(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "u")
	if !ok || rest == "" || rest[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0 && strconv.Itoa(n) == rest
}

func presetBad(c *Core, key string) error {
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T(key))
}

func (c *Core) presetFail(err error) error {
	c.log.Printf("error in column preset query: %v", pqErrMsg(err))
	return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("globals.messages.internalError"))
}

// ValidColumnPreset tells whether a preset ID exists: a built-in one or a row of the table.
func (c *Core) ValidColumnPreset(id string) bool {
	if _, ok := builtinPreset(id); ok {
		return true
	}
	n, ok := userPresetID(id)
	if !ok {
		return false
	}
	var exists bool
	if err := c.q.AxmColumnPresetExists.Get(&exists, n); err != nil {
		c.log.Printf("error checking column preset: %v", pqErrMsg(err))
		return false
	}
	return exists
}

type presetRow struct {
	ID      int            `db:"id"`
	Name    string         `db:"name"`
	Columns pq.StringArray `db:"columns"`
}

func userPresetOf(r presetRow) ColumnPreset {
	return ColumnPreset{ID: "u" + strconv.Itoa(r.ID), Name: r.Name, Columns: append([]string{}, r.Columns...)}
}

// GetColumnPresets returns the built-in presets, then those people made by name.
func (c *Core) GetColumnPresets() ([]ColumnPreset, error) {
	out := []ColumnPreset{}
	for _, p := range ColumnPresets {
		b, _ := builtinPreset(p.ID)
		out = append(out, b)
	}
	var rows []presetRow
	if err := c.q.AxmGetColumnPresets.Select(&rows); err != nil {
		return nil, c.presetFail(err)
	}
	for _, r := range rows {
		out = append(out, userPresetOf(r))
	}
	return out, nil
}

// GetColumnPresetUsage returns the presets with the number of lists that use each and, when
// withLists is true, the names of up to PresetListSample of those lists.
func (c *Core) GetColumnPresetUsage(withLists bool) ([]ColumnPreset, error) {
	out, err := c.GetColumnPresets()
	if err != nil {
		return nil, err
	}
	var usage []struct {
		Preset string `db:"preset"`
		Lists  int    `db:"lists"`
	}
	if err := c.q.AxmColumnPresetUsage.Select(&usage); err != nil {
		return nil, c.presetFail(err)
	}
	counts := map[string]int{}
	for _, u := range usage {
		counts[u.Preset] = u.Lists
	}
	ids := make([]string, len(out))
	for i := range out {
		out[i].ListCount = counts[out[i].ID]
		ids[i] = out[i].ID
	}
	if !withLists {
		return out, nil
	}
	var names []PresetList
	if err := c.q.AxmColumnPresetListNames.Select(&names, pq.Array(ids), PresetListSample); err != nil {
		return nil, c.presetFail(err)
	}
	for _, n := range names {
		for i := range out {
			if out[i].ID == n.Preset {
				out[i].Lists = append(out[i].Lists, n)
			}
		}
	}
	return out, nil
}

// GetColumnPreset returns one preset by ID (built-in or "u<id>").
func (c *Core) GetColumnPreset(id string) (ColumnPreset, error) {
	if p, ok := builtinPreset(id); ok {
		return p, nil
	}
	n, ok := userPresetID(id)
	if ok {
		var r presetRow
		if err := c.q.AxmGetColumnPreset.Get(&r, n); err == nil {
			return userPresetOf(r), nil
		} else if !strings.Contains(err.Error(), "no rows") {
			return ColumnPreset{}, c.presetFail(err)
		}
	}
	return ColumnPreset{}, echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "preset"))
}

// checkPreset validates the name and columns of a preset people make and returns them
// cleaned: the name trimmed, the keys trimmed in the order given.
func (c *Core) checkPreset(name string, columns []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > presetNameMax || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", nil, presetBad(c, "presets.invalidName")
	}
	lower := strings.ToLower(name)
	for _, names := range builtinPresetNames {
		for _, b := range names {
			if lower == b {
				return "", nil, presetBad(c, "presets.exists")
			}
		}
	}
	if len(columns) < 1 {
		return "", nil, presetBad(c, "presets.columnsRequired")
	}
	if len(columns) > presetColumnsMax {
		return "", nil, presetBad(c, "presets.tooManyColumns")
	}
	clean := make([]string, 0, len(columns))
	seen := map[string]bool{}
	for _, k := range columns {
		k = strings.TrimSpace(k)
		if !validAttribKey(k) {
			return "", nil, presetBad(c, "presets.invalidColumn")
		}
		if seen[k] {
			return "", nil, presetBad(c, "presets.duplicateColumn")
		}
		seen[k] = true
		clean = append(clean, k)
	}
	return name, clean, nil
}

// CreateColumnPreset makes a preset: a name (1-40 characters, not used by another preset in
// any letter case, nor by a built-in one) and 1-8 distinct attribute keys in display order.
func (c *Core) CreateColumnPreset(name string, columns []string) (ColumnPreset, error) {
	name, columns, err := c.checkPreset(name, columns)
	if err != nil {
		return ColumnPreset{}, err
	}
	var id int
	if err := c.q.AxmCreateColumnPreset.Get(&id, name, pq.StringArray(columns), presetsMax); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return ColumnPreset{}, presetBad(c, "presets.tooMany")
		}
		if isUnique(err) {
			return ColumnPreset{}, presetBad(c, "presets.exists")
		}
		return ColumnPreset{}, c.presetFail(err)
	}
	return c.GetColumnPreset("u" + strconv.Itoa(id))
}

// UpdateColumnPreset changes the name and columns of a preset people made. Every list that
// uses it shows the new columns at once, as lists keep only its ID.
func (c *Core) UpdateColumnPreset(id int, name string, columns []string) (ColumnPreset, error) {
	name, columns, err := c.checkPreset(name, columns)
	if err != nil {
		return ColumnPreset{}, err
	}
	pid := "u" + strconv.Itoa(id)
	if _, err := c.GetColumnPreset(pid); err != nil {
		return ColumnPreset{}, err
	}
	if _, err := c.q.AxmUpdateColumnPreset.Exec(id, name, pq.StringArray(columns)); err != nil {
		if isUnique(err) {
			return ColumnPreset{}, presetBad(c, "presets.exists")
		}
		return ColumnPreset{}, c.presetFail(err)
	}
	return c.GetColumnPreset(pid)
}

// DeleteColumnPreset deletes a preset people made. The lists that used it are put on the
// default preset in the same transaction; their number is returned. Subscriber data is not
// touched: only the columns shown change.
func (c *Core) DeleteColumnPreset(id int) (int, error) {
	pid := "u" + strconv.Itoa(id)
	if _, err := c.GetColumnPreset(pid); err != nil {
		return 0, err
	}
	tx, err := c.db.Beginx()
	if err != nil {
		return 0, c.presetFail(err)
	}
	defer tx.Rollback()
	res, err := tx.Stmtx(c.q.AxmResetPresetLists).Exec(pid, DefaultColumnPreset)
	if err != nil {
		return 0, c.presetFail(err)
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Stmtx(c.q.AxmDeleteColumnPreset).Exec(id); err != nil {
		return 0, c.presetFail(err)
	}
	if err := tx.Commit(); err != nil {
		return 0, c.presetFail(err)
	}
	return int(n), nil
}

// newListPreset returns the preset a new list gets. A given preset must exist. Without one,
// a dynamic list takes the preset of its first base list and any other list the default.
func (c *Core) newListPreset(l models.List) (string, error) {
	if l.AxmPreset != "" {
		if !c.ValidColumnPreset(l.AxmPreset) {
			return "", echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("lists.preset.invalid"))
		}
		return l.AxmPreset, nil
	}
	if l.AxmFilter == nil {
		return DefaultColumnPreset, nil
	}
	f, err := ValidateDynamicFilter(*l.AxmFilter)
	if err != nil || f.Base.Type != "lists" || len(f.Base.Lists) == 0 {
		return DefaultColumnPreset, nil
	}
	base, err := c.GetList(f.Base.Lists[0], "")
	if err != nil {
		return "", err
	}
	if !c.ValidColumnPreset(base.AxmPreset) {
		return DefaultColumnPreset, nil
	}
	return base.AxmPreset, nil
}
