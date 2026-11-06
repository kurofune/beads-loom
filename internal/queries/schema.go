package queries

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// StatusDef is one status a project accepts. Category is bd's grouping
// (active | wip | done | frozen); it is "" for a legacy bare custom name.
type StatusDef struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

// Schema is the status and type vocabulary a project accepts: bd's built-ins
// first, then the project's own `status.custom` / `types.custom`.
type Schema struct {
	Statuses []StatusDef `json:"statuses"`
	Types    []string    `json:"types"`
}

// BuiltinStatuses is `bd statuses --json` → built_in_statuses (bd 1.3.2), in
// bd's order.
var BuiltinStatuses = []StatusDef{
	{Name: StatusOpen, Category: "active"},
	{Name: StatusInProgress, Category: "wip"},
	{Name: StatusBlocked, Category: "wip"},
	{Name: StatusDeferred, Category: "frozen"},
	{Name: StatusClosed, Category: "done"},
	{Name: StatusPinned, Category: "frozen"},
	{Name: StatusHooked, Category: "wip"},
}

// CoreTypes is `bd types --json` → core_types (bd 1.3.2), in bd's order.
var CoreTypes = []string{
	"task", "bug", "feature", "chore", "epic", "decision", "spike", "story", "milestone",
}

// Config keys bd stores the project's custom vocabulary under.
const (
	ConfigStatusCustom = "status.custom"
	ConfigTypesCustom  = "types.custom"
)

// ParseCustomStatuses reads bd's `status.custom` value: `name:category,...`.
// A bare name (no colon) is bd's legacy form and stays valid with no category.
func ParseCustomStatuses(v string) []StatusDef {
	var out []StatusDef
	for _, part := range strings.Split(v, ",") {
		name, category, _ := strings.Cut(part, ":")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, StatusDef{Name: name, Category: strings.TrimSpace(category)})
	}
	return out
}

// ParseCustomTypes reads bd's `types.custom` value: a comma-separated list.
func ParseCustomTypes(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// BuildSchema lays the built-ins first and appends each custom entry once,
// skipping any name already present.
func BuildSchema(statusCustom, typesCustom string) Schema {
	s := Schema{
		Statuses: append([]StatusDef{}, BuiltinStatuses...),
		Types:    append([]string{}, CoreTypes...),
	}
	seenStatus := map[string]bool{}
	for _, st := range s.Statuses {
		seenStatus[st.Name] = true
	}
	for _, st := range ParseCustomStatuses(statusCustom) {
		if !seenStatus[st.Name] {
			seenStatus[st.Name] = true
			s.Statuses = append(s.Statuses, st)
		}
	}
	seenType := map[string]bool{}
	for _, ty := range s.Types {
		seenType[ty] = true
	}
	for _, ty := range ParseCustomTypes(typesCustom) {
		if !seenType[ty] {
			seenType[ty] = true
			s.Types = append(s.Types, ty)
		}
	}
	return s
}

// SchemaFor reads the project's custom statuses and types from its Dolt
// `config` table in one query. A project with neither row gets the built-ins.
func SchemaFor(ctx context.Context, db *sql.DB) (*Schema, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT `key`, `value` FROM config WHERE `key` IN (?, ?)",
		ConfigStatusCustom, ConfigTypesCustom)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	defer rows.Close()

	var statusCustom, typesCustom string
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("schema: scan config row: %w", err)
		}
		switch key {
		case ConfigStatusCustom:
			statusCustom = value.String
		case ConfigTypesCustom:
			typesCustom = value.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}

	s := BuildSchema(statusCustom, typesCustom)
	return &s, nil
}
