package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"reflect"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/queries"
)

// bdConfigValue reads one config key the way a beads user would, through bd.
func bdConfigValue(t *testing.T, workdir, key string) string {
	t.Helper()
	cmd := exec.Command("bd", "config", "get", key, "--json")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bd config get %s: %v", key, err)
	}
	var got struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode bd config get %s: %v (%s)", key, err, out)
	}
	return got.Value
}

func getSchema(t *testing.T, s *Server) queries.Schema {
	t.Helper()
	rec := do(t, s, http.MethodGet, "/api/projects/loom/schema", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /schema = %d: %s", rec.Code, rec.Body.String())
	}
	var schema queries.Schema
	if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

// configuredCustomTypes is `bd config get types.custom`, as configured.
func configuredCustomTypes(t *testing.T, workdir string) []string {
	t.Helper()
	return queries.ParseCustomTypes(bdConfigValue(t, workdir, queries.ConfigTypesCustom))
}

// TestSchemaCustomTypesMatchBdConfigAgainstLiveDB: the endpoint's custom types
// are what `bd config get types.custom` reports for this repo.
func TestSchemaCustomTypesMatchBdConfigAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	s := newLiveHTTPServer(t)
	workdir := loomtest.RepoRoot(t)

	schema := getSchema(t, s)
	if len(schema.Types) < len(queries.CoreTypes) ||
		!reflect.DeepEqual(schema.Types[:len(queries.CoreTypes)], queries.CoreTypes) {
		t.Fatalf("types = %v, want the core types first", schema.Types)
	}
	custom := schema.Types[len(queries.CoreTypes):]
	want := configuredCustomTypes(t, workdir)
	if len(want) == 0 {
		t.Fatal("this repo's database defines no types.custom; the live fixture needs one")
	}
	if !reflect.DeepEqual(custom, want) {
		t.Errorf("custom types = %v, want bd config get types.custom = %v", custom, want)
	}

	if !reflect.DeepEqual(schema.Statuses[:len(queries.BuiltinStatuses)], queries.BuiltinStatuses) {
		t.Errorf("statuses = %v, want the built-ins first", schema.Statuses)
	}
	wantStatuses := queries.ParseCustomStatuses(bdConfigValue(t, workdir, queries.ConfigStatusCustom))
	if got := schema.Statuses[len(queries.BuiltinStatuses):]; len(got) != len(wantStatuses) {
		t.Errorf("custom statuses = %v, want bd config get status.custom = %v", got, wantStatuses)
	}
}

// TestCreateWithCustomTypeAgainstLiveDB: a POST carrying the project's first
// types.custom entry goes through bd, and the issue reads back with it. On this
// repo that entry is `message`, which bd stores as a wisp, so this also proves
// GET /issues/{id} reads the wisps table.
func TestCreateWithCustomTypeAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	c := bdexec.New(bdexec.NewExecRunner())
	s := newLiveHTTPServerWithBD(t, c)
	workdir := loomtest.RepoRoot(t)

	custom := configuredCustomTypes(t, workdir)
	if len(custom) == 0 {
		t.Fatal("this repo's database defines no types.custom; nothing to create with")
	}
	typ := custom[0]

	body, err := json.Marshal(map[string]any{
		"title": loomtest.ScratchTitlePrefix + " httpapi custom-type create live test",
		"type":  typ,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues", string(body))
	var created queries.IssueDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	t.Cleanup(func() {
		if created.ID == "" {
			return
		}
		if err := c.Delete(context.Background(), workdir, []string{created.ID}); err != nil {
			t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture: %v", created.ID, err)
		}
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("POST /issues type=%s = %d: %s", typ, rec.Code, rec.Body.String())
	}
	if created.ID == "" {
		t.Fatalf("POST /issues returned no id: %s", rec.Body.String())
	}

	get := do(t, s, http.MethodGet, "/api/projects/loom/issues/"+created.ID, "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET /issues/%s = %d: %s", created.ID, get.Code, get.Body.String())
	}
	var read queries.IssueDetail
	if err := json.Unmarshal(get.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Type != typ {
		t.Errorf("type read back = %q, want %q", read.Type, typ)
	}
}

// TestWispRelationshipsReadBackAgainstLiveDB: a `message` (stored as a wisp)
// created under a normal parent and blocked by a normal issue, and a normal
// issue that depends on it, all read back on GET /issues/{id} of the message.
func TestWispRelationshipsReadBackAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	c := bdexec.New(bdexec.NewExecRunner())
	s := newLiveHTTPServerWithBD(t, c)
	workdir := loomtest.RepoRoot(t)

	var scratch []string
	t.Cleanup(func() {
		if len(scratch) == 0 {
			return
		}
		if err := c.Delete(context.Background(), workdir, scratch); err != nil {
			t.Errorf("CLEANUP FAILED — scratch beads %v are still in the fixture: %v", scratch, err)
		}
	})
	create := func(fields map[string]any) string {
		t.Helper()
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		rec := do(t, s, http.MethodPost, "/api/projects/loom/issues", string(body))
		var created queries.IssueDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &created)
		if created.ID != "" {
			scratch = append([]string{created.ID}, scratch...)
		}
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("POST /issues %v = %d: %s", fields, rec.Code, rec.Body.String())
		}
		if created.ID == "" {
			t.Fatalf("POST /issues returned no id: %s", rec.Body.String())
		}
		return created.ID
	}
	title := func(role string) string {
		return loomtest.ScratchTitlePrefix + " httpapi wisp relationships live test " + role
	}

	parent := create(map[string]any{"title": title("parent"), "type": "task"})
	blocker := create(map[string]any{"title": title("blocker"), "type": "task"})
	msg := create(map[string]any{
		"title":  title("message"),
		"type":   "message",
		"parent": parent,
		"deps":   []map[string]string{{"id": blocker, "type": "blocks"}},
	})
	dependent := create(map[string]any{
		"title": title("dependent"),
		"type":  "task",
		"deps":  []map[string]string{{"id": msg, "type": "blocks"}},
	})

	get := do(t, s, http.MethodGet, "/api/projects/loom/issues/"+msg, "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET /issues/%s = %d: %s", msg, get.Code, get.Body.String())
	}
	var read queries.IssueDetail
	if err := json.Unmarshal(get.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Type != "message" {
		t.Fatalf("type read back = %q, want message", read.Type)
	}
	if read.Parent == nil || read.Parent.ID != parent {
		t.Errorf("parent = %+v, want %s", read.Parent, parent)
	}
	if read.Deps.Parent == nil || read.Deps.Parent.ID != parent {
		t.Errorf("deps.parent = %+v, want %s", read.Deps.Parent, parent)
	}
	refIDs := func(refs []queries.IssueRef) []string {
		ids := []string{}
		for _, r := range refs {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := refIDs(read.Deps.BlockedBy); !reflect.DeepEqual(got, []string{blocker}) {
		t.Errorf("deps.blockedBy = %v, want [%s]", got, blocker)
	}
	if got := refIDs(read.Deps.Blocks); !reflect.DeepEqual(got, []string{dependent}) {
		t.Errorf("deps.blocks = %v, want [%s]", got, dependent)
	}
	if want := (queries.DepCounts{BlockedBy: 1, Blocks: 1}); read.DepCounts != want {
		t.Errorf("depCounts = %+v, want %+v", read.DepCounts, want)
	}
}
