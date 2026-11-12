package core

import (
	"net/http"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// ColumnPreset is a fixed set of attribute columns the subscribers table shows for a list.
type ColumnPreset struct {
	ID string `json:"id"`
	// Columns are attribute keys in display order.
	Columns []string `json:"columns"`
}

// DefaultColumnPreset is the preset of new lists and of the subscribers page without a list.
const DefaultColumnPreset = "creator"

// ColumnPresets are the presets a list can have. They are fixed here on purpose: there is no
// screen to edit them. The keys follow the standard attribute names; renaming one here is all
// it takes when the standard changes, as lists store only the preset ID.
//
// There is one preset for now. Lists already store their preset (lists.axm_preset), so adding
// one here, with its name in the i18n files (lists.preset.<id>), is enough to offer it: the
// list form shows its preset choice and the subscribers page its preset line only when there
// is more than one.
var ColumnPresets = []ColumnPreset{
	{ID: "creator", Columns: []string{"country", "follower"}},
}

// ValidColumnPreset tells whether a preset ID exists.
func ValidColumnPreset(id string) bool {
	for _, p := range ColumnPresets {
		if p.ID == id {
			return true
		}
	}
	return false
}

// newListPreset returns the preset a new list gets. A given preset must exist. Without one,
// a dynamic list takes the preset of its first base list and any other list the default.
func (c *Core) newListPreset(l models.List) (string, error) {
	if l.AxmPreset != "" {
		if !ValidColumnPreset(l.AxmPreset) {
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
	if !ValidColumnPreset(base.AxmPreset) {
		return DefaultColumnPreset, nil
	}
	return base.AxmPreset, nil
}
