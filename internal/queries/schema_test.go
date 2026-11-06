package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

func TestParseCustomStatusesReadsNameCategoryPairs(t *testing.T) {
	got := queries.ParseCustomStatuses("in_review:active,qa_testing:wip")
	want := []queries.StatusDef{
		{Name: "in_review", Category: "active"},
		{Name: "qa_testing", Category: "wip"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCustomStatuses = %#v, want %#v", got, want)
	}
}

func TestParseCustomStatusesKeepsABareLegacyNameAndDropsBlanks(t *testing.T) {
	got := queries.ParseCustomStatuses(" legacy , ,review: done ,")
	want := []queries.StatusDef{
		{Name: "legacy", Category: ""},
		{Name: "review", Category: "done"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCustomStatuses = %#v, want %#v", got, want)
	}
}

func TestParseCustomTypesSplitsOnCommas(t *testing.T) {
	got := queries.ParseCustomTypes("rfc,memo")
	want := []string{"rfc", "memo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCustomTypes = %#v, want %#v", got, want)
	}
	if got := queries.ParseCustomTypes(" rfc , ,memo,"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCustomTypes with blanks = %#v, want %#v", got, want)
	}
}

func TestBuildSchemaPutsBuiltinsFirstThenTheProjectsOwn(t *testing.T) {
	s := queries.BuildSchema("in_review:active,qa_testing:wip", "rfc,memo")

	wantStatuses := append(append([]queries.StatusDef{}, queries.BuiltinStatuses...),
		queries.StatusDef{Name: "in_review", Category: "active"},
		queries.StatusDef{Name: "qa_testing", Category: "wip"},
	)
	if !reflect.DeepEqual(s.Statuses, wantStatuses) {
		t.Errorf("statuses = %#v, want %#v", s.Statuses, wantStatuses)
	}
	wantTypes := append(append([]string{}, queries.CoreTypes...), "rfc", "memo")
	if !reflect.DeepEqual(s.Types, wantTypes) {
		t.Errorf("types = %#v, want %#v", s.Types, wantTypes)
	}
}

func TestBuildSchemaDoesNotRepeatABuiltinTheProjectAlsoLists(t *testing.T) {
	s := queries.BuildSchema("open:active,in_review:active", "task,rfc,rfc")
	if n := len(s.Statuses); n != len(queries.BuiltinStatuses)+1 {
		t.Errorf("statuses = %#v, want the built-ins plus in_review once", s.Statuses)
	}
	if n := len(s.Types); n != len(queries.CoreTypes)+1 {
		t.Errorf("types = %#v, want the core types plus rfc once", s.Types)
	}
}

func TestBuildSchemaOffersEveryConfiguredTypeInOrder(t *testing.T) {
	s := queries.BuildSchema("", "message,run,gate,check")
	want := append(append([]string{}, queries.CoreTypes...), "message", "run", "gate", "check")
	if !reflect.DeepEqual(s.Types, want) {
		t.Errorf("types = %v, want %v (types.custom as configured)", s.Types, want)
	}
}

func TestBuiltinsMatchBd(t *testing.T) {
	names := make([]string, 0, len(queries.BuiltinStatuses))
	for _, s := range queries.BuiltinStatuses {
		names = append(names, s.Name)
	}
	// The order and names of KNOWN_STATUSES in web/src/api/types.ts.
	want := []string{"open", "in_progress", "blocked", "deferred", "closed", "pinned", "hooked"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("built-in statuses = %v, want %v", names, want)
	}
	wantTypes := []string{"task", "bug", "feature", "chore", "epic", "decision", "spike", "story", "milestone"}
	if !reflect.DeepEqual(queries.CoreTypes, wantTypes) {
		t.Errorf("core types = %v, want %v", queries.CoreTypes, wantTypes)
	}
}

// configConn answers the schema query from an in-memory config table.
type configConn struct {
	rows  [][2]string
	query string
}

func (*configConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (*configConn) Close() error { return nil }
func (*configConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *configConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.query = query
	out := &existingIDsRows{columns: []string{"key", "value"}}
	for _, r := range c.rows {
		out.values = append(out.values, []driver.Value{r[0], r[1]})
	}
	return out, nil
}

type configDriver struct{ conn *configConn }

func (d configDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

var configDriverSeq atomic.Int64

func openConfigFixture(t *testing.T, rows ...[2]string) (*sql.DB, *configConn) {
	t.Helper()
	conn := &configConn{rows: rows}
	name := fmt.Sprintf("loom-config-%d", configDriverSeq.Add(1))
	sql.Register(name, configDriver{conn})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, conn
}

func TestSchemaForAnEmptyConfigTableIsExactlyTheBuiltins(t *testing.T) {
	db, conn := openConfigFixture(t)

	got, err := queries.SchemaFor(context.Background(), db)
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	if !reflect.DeepEqual(got.Statuses, queries.BuiltinStatuses) {
		t.Errorf("statuses = %#v, want exactly the built-ins", got.Statuses)
	}
	if !reflect.DeepEqual(got.Types, queries.CoreTypes) {
		t.Errorf("types = %#v, want exactly the core types", got.Types)
	}
	if !strings.Contains(conn.query, "FROM config") {
		t.Errorf("query = %q, want it to read the config table", conn.query)
	}
}

func TestSchemaForReadsTheProjectsCustomRows(t *testing.T) {
	db, _ := openConfigFixture(t,
		[2]string{"types.custom", "rfc,memo"},
		[2]string{"status.custom", "in_review:active"},
	)

	got, err := queries.SchemaFor(context.Background(), db)
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	if last := got.Types[len(got.Types)-2:]; !reflect.DeepEqual(last, []string{"rfc", "memo"}) {
		t.Errorf("custom types = %v, want [rfc memo]", last)
	}
	if last := got.Statuses[len(got.Statuses)-1]; last != (queries.StatusDef{Name: "in_review", Category: "active"}) {
		t.Errorf("last status = %#v, want in_review:active", last)
	}
}
