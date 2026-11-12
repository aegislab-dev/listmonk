package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
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
	// Value2 is the exclusive upper bound of "between".
	Value2 *string `json:"value2,omitempty"`
}

// The same two patterns as the axm_to_number() DB function: a plain decimal with an optional
// two-digit exponent, or a decimal with thousands commas in groups of three.
var (
	dynamicPlainNumber   = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]{1,2})?$`)
	dynamicGroupedNumber = regexp.MustCompile(`^[+-]?[0-9]{1,3}(,[0-9]{3})+(\.[0-9]*)?$`)
)

// parseDynamicNumber reads a condition value by the rule the DB applies to attribute values
// and returns it as a plain decimal such as "100000" or "-0.5".
func parseDynamicNumber(s string) (string, *big.Rat, bool) {
	s = strings.Trim(s, " \t\n\r")
	if len(s) > 64 {
		return "", nil, false
	}
	if dynamicGroupedNumber.MatchString(s) {
		s = strings.ReplaceAll(s, ",", "")
	} else if !dynamicPlainNumber.MatchString(s) {
		return "", nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", nil, false
	}
	if r.IsInt() {
		return r.Num().String(), r, true
	}
	// 64 digits and a two-digit exponent leave at most 163 decimals, so this is exact.
	return strings.TrimRight(r.FloatString(170), "0"), r, true
}

func dynamicNumericOp(op string) bool {
	switch op {
	case "gte", "gt", "lte", "lt", "between":
		return true
	}
	return false
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
		if c.Value2 != nil && c.Op != "between" {
			return f, fmt.Errorf("only between takes a second value")
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
		case "gte", "gt", "lte", "lt", "between":
			if c.Value == nil {
				return f, fmt.Errorf("condition requires a value")
			}
			v, low, ok := parseDynamicNumber(strings.TrimSpace(*c.Value))
			if !ok {
				return f, fmt.Errorf("condition value must be a number")
			}
			c.Value = &v
			if c.Op != "between" {
				break
			}
			if c.Value2 == nil {
				return f, fmt.Errorf("between requires two values")
			}
			v2, high, ok := parseDynamicNumber(strings.TrimSpace(*c.Value2))
			if !ok {
				return f, fmt.Errorf("condition value must be a number")
			}
			if low.Cmp(high) >= 0 {
				return f, fmt.Errorf("between requires the first value to be smaller")
			}
			c.Value2 = &v2
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

// notNumberFilter returns a filter over the same base that matches subscribers whose value
// for a numeric condition is present but is not a number, or nil without numeric conditions.
// Its operator exists only in the DB function and is never accepted in a saved filter.
func (f DynamicFilter) notNumberFilter() *DynamicFilter {
	out := DynamicFilter{Version: 1, Base: f.Base, Match: "any"}
	seen := map[string]bool{}
	for _, c := range f.Conditions {
		if dynamicNumericOp(c.Op) && !seen[c.Key] {
			seen[c.Key] = true
			out.Conditions = append(out.Conditions, DynamicCondition{Key: c.Key, Op: "not_number"})
		}
	}
	if len(out.Conditions) == 0 {
		return nil
	}
	return &out
}

// DynamicPreview shows how a filter selects subscribers before it is saved.
type DynamicPreview struct {
	Count int `json:"count"`
	// NotNumber counts subscribers of the base that number conditions skip because their
	// value is present but is not a number.
	NotNumber int             `json:"not_number"`
	Excluded  DynamicExcluded `json:"excluded"`
	// Sample is nil when the caller may not read subscribers.
	Sample []DynamicSample `json:"sample"`
}

// DynamicExcluded counts subscribers the conditions match but the list leaves out.
type DynamicExcluded struct {
	Blocklisted  int `db:"blocklisted" json:"blocklisted"`
	Unsubscribed int `db:"unsubscribed" json:"unsubscribed"`
	OptedOut     int `db:"opted_out" json:"opted_out"`
}

// DynamicSample is a member with only the attributes the conditions use.
type DynamicSample struct {
	ID      int                        `json:"id"`
	Email   string                     `json:"email"`
	Name    string                     `json:"name"`
	Attribs map[string]json.RawMessage `json:"attribs"`
}

// PreviewDynamicFilter counts the members of a filter, explains the excluded ones and, when
// sampleSize is positive, returns the first members by ID. Every number goes through
// axm_filter_match(), so the preview follows the rule that list counts and campaigns use.
func (c *Core) PreviewDynamicFilter(f DynamicFilter, listID, sampleSize int) (DynamicPreview, error) {
	var (
		out DynamicPreview
		id  any
	)
	if listID > 0 {
		id = listID
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return out, err
	}
	jobs := []func() error{
		func() error { return c.q.AxmPreviewDynamicFilter.Get(&out.Count, string(raw), id) },
		func() error { return c.q.AxmPreviewDynamicExcluded.Get(&out.Excluded, string(raw), id) },
	}
	if nf := f.notNumberFilter(); nf != nil {
		jobs = append(jobs, func() error {
			raw, err := json.Marshal(nf)
			if err != nil {
				return err
			}
			return c.q.AxmPreviewDynamicFilter.Get(&out.NotNumber, string(raw), id)
		})
	}
	if sampleSize > 0 {
		jobs = append(jobs, func() error {
			var rows []struct {
				ID      int            `db:"id"`
				Email   string         `db:"email"`
				Name    string         `db:"name"`
				Attribs types.JSONText `db:"attribs"`
			}
			if err := c.q.AxmPreviewDynamicSample.Select(&rows, string(raw), id, sampleSize); err != nil {
				return err
			}
			out.Sample = make([]DynamicSample, 0, len(rows))
			for _, r := range rows {
				all := map[string]json.RawMessage{}
				// Attributes that are not a JSON object simply have no values to show.
				_ = json.Unmarshal(r.Attribs, &all)
				shown := map[string]json.RawMessage{}
				for _, cond := range f.Conditions {
					if v, ok := all[cond.Key]; ok {
						shown[cond.Key] = v
					}
				}
				out.Sample = append(out.Sample, DynamicSample{ID: r.ID, Email: r.Email, Name: r.Name, Attribs: shown})
			}
			return nil
		})
	}
	// The queries are independent reads; each job writes its own field.
	errs := make(chan error, len(jobs))
	for _, job := range jobs {
		go func() { errs <- job() }()
	}
	for range jobs {
		if e := <-errs; e != nil && err == nil {
			err = e
		}
	}
	return out, err
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
