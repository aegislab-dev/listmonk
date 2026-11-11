package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// DynamicFilter is a versioned data format, never an SQL expression.
type DynamicFilter struct {
	Version    int                `json:"version"`
	Base       DynamicBase        `json:"base"`
	Match      string             `json:"match"`
	Conditions []DynamicCondition `json:"conditions"`
}
type DynamicBase struct {
	Type  string `json:"type"`
	Lists []int  `json:"lists,omitempty"`
}
type DynamicCondition struct {
	Key   string  `json:"key"`
	Op    string  `json:"op"`
	Value *string `json:"value,omitempty"`
}

// ValidateDynamicFilter rejects unknown fields and normalizes the persisted format.
func ValidateDynamicFilter(raw []byte) (DynamicFilter, error) {
	var f DynamicFilter
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return f, fmt.Errorf("invalid trailing JSON")
	}
	if f.Version != 1 {
		return f, fmt.Errorf("unsupported filter version")
	}
	if f.Match != "all" && f.Match != "any" {
		return f, fmt.Errorf("invalid match")
	}
	switch f.Base.Type {
	case "all":
		if len(f.Base.Lists) != 0 {
			return f, fmt.Errorf("all base cannot specify lists")
		}
	case "lists":
		if len(f.Base.Lists) < 1 || len(f.Base.Lists) > 20 {
			return f, fmt.Errorf("select 1–20 base lists")
		}
		seen := map[int]bool{}
		ids := []int{}
		for _, id := range f.Base.Lists {
			if id < 1 || int64(id) > 2147483647 {
				return f, fmt.Errorf("invalid base list ID")
			}
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		f.Base.Lists = ids
	default:
		return f, fmt.Errorf("invalid base type")
	}
	if len(f.Conditions) < 1 || len(f.Conditions) > 20 {
		return f, fmt.Errorf("provide 1–20 conditions")
	}
	for i := range f.Conditions {
		c := &f.Conditions[i]
		c.Key = strings.TrimSpace(c.Key)
		if utf8.RuneCountInString(c.Key) < 1 || utf8.RuneCountInString(c.Key) > 64 || strings.IndexFunc(c.Key, unicode.IsControl) >= 0 {
			return f, fmt.Errorf("invalid attribute key")
		}
		switch c.Op {
		case "empty":
			if c.Value != nil {
				return f, fmt.Errorf("empty condition must not specify a value")
			}
		case "eq", "contains", "has":
			if c.Value == nil {
				return f, fmt.Errorf("condition requires a value")
			}
			v := strings.TrimSpace(*c.Value)
			if utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 200 || strings.ContainsRune(v, 0) {
				return f, fmt.Errorf("invalid condition value")
			}
			c.Value = &v
		default:
			return f, fmt.Errorf("invalid condition operator")
		}
	}
	return f, nil
}

// ValidateDynamicBase checks live list existence and rejects nested dynamic lists.
func (c *Core) ValidateDynamicBase(f DynamicFilter) error {
	if f.Base.Type == "all" {
		return nil
	}
	lists, err := c.GetLists("", "", false, f.Base.Lists)
	if err != nil {
		return err
	}
	if len(lists) != len(f.Base.Lists) {
		return echo.NewHTTPError(http.StatusBadRequest, "Unknown base list")
	}
	for _, l := range lists {
		if l.AxmFilter != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Dynamic lists cannot be base lists")
		}
	}
	return nil
}

func (c *Core) DynamicListIDs(ids []int) ([]int, error) {
	out := []int{}
	if len(ids) == 0 {
		return out, nil
	}
	err := c.q.AxmGetDynamicListIDs.Select(&out, pq.Array(ids))
	return out, err
}

func (c *Core) PreviewDynamicFilter(f DynamicFilter, listID int) (int, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return 0, err
	}
	var id any
	if listID > 0 {
		id = listID
	}
	var n int
	err = c.q.AxmPreviewDynamicFilter.Get(&n, string(raw), id)
	return n, err
}

func dynamicListQuery(ids, dynamicIDs []int, query string) ([]int, string) {
	if len(dynamicIDs) == 0 {
		return ids, query
	}
	query = sanitizeSQLExp(query)
	dyn := map[int]bool{}
	parts := []string{}
	for _, id := range dynamicIDs {
		dyn[id] = true
		parts = append(parts, "subscribers.id IN (SELECT subscriber_id FROM axm_dynamic_list_members("+strconv.Itoa(id)+"))")
	}
	regular := []string{}
	for _, id := range ids {
		if !dyn[id] {
			regular = append(regular, strconv.Itoa(id))
		}
	}
	if len(regular) > 0 {
		parts = append(parts, "EXISTS (SELECT 1 FROM subscriber_lists axm_sl WHERE axm_sl.subscriber_id = subscribers.id AND axm_sl.list_id IN ("+strings.Join(regular, ",")+"))")
	}
	if query == "" {
		query = "TRUE"
	}
	return []int{}, "(" + query + ") AND (" + strings.Join(parts, " OR ") + ")"
}

// Only validated DB IDs enter the generated expression. Attribute keys/values stay in JSONB.
func (c *Core) resolveDynamicListQuery(ids []int, subStatus, query string) ([]int, string, error) {
	if subStatus != "" || len(ids) == 0 {
		return ids, query, nil
	}
	dynamicIDs, err := c.DynamicListIDs(ids)
	if err != nil {
		return nil, "", err
	}
	ids, query = dynamicListQuery(ids, dynamicIDs, query)
	return ids, query, nil
}

func (c *Core) dynamicListCounts(lists []models.List) error {
	ids := []int{}
	for _, l := range lists {
		if l.AxmFilter != nil {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var counts []struct {
		ID    int `db:"id"`
		Count int `db:"count"`
	}
	if err := c.q.AxmDynamicListCounts.Select(&counts, pq.Array(ids)); err != nil {
		return err
	}
	byID := map[int]int{}
	for _, r := range counts {
		byID[r.ID] = r.Count
	}
	for i := range lists {
		if lists[i].AxmFilter != nil {
			lists[i].SubscriberCount = byID[lists[i].ID]
		}
	}
	return nil
}

func normalizeDynamicList(l *models.List) error {
	if l.AxmFilter == nil {
		return nil
	}
	f, err := ValidateDynamicFilter(*l.AxmFilter)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	raw, _ := json.Marshal(f)
	j := types.JSONText(raw)
	l.AxmFilter = &j
	l.Type = models.ListTypePrivate
	l.Optin = models.ListOptinSingle
	return nil
}
