package updates

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// docSection is the heading both documents carry for the bd kv contract.
const docSection = "Weekly initiative updates"

// The contract is documented so anyone can write an update without the retro
// skill; these tests fail when the wire shape moves and the docs do not.
func TestDocsNameTheStoredUpdateContract(t *testing.T) {
	for _, doc := range []string{"README.md", "docs/ARCHITECTURE.md"} {
		section := readDocSection(t, doc)
		want := append([]string{KeyPrefix, "<epic-id>.<YYYY-Www>", "bodySha", "bd kv set"}, storedUpdateTags(t)...)
		for _, needle := range want {
			if !strings.Contains(section, needle) {
				t.Errorf("%s %q section does not mention %q", doc, docSection, needle)
			}
		}
	}
}

func TestReadmeExampleDecodesAsOneDraft(t *testing.T) {
	key, value := readmeExample(t)
	epic, week, ok := splitKey(key)
	if !ok {
		t.Fatalf("README example key %q is not %s<epic-id>.<YYYY-Www>", key, KeyPrefix)
	}

	got := FromKV(map[string]string{key: value}, epic)

	if len(got) != 1 {
		t.Fatalf("FromKV(README example) = %d updates, want 1: %#v", len(got), got)
	}
	u := got[0]
	if u.ParseError != "" {
		t.Fatalf("README example value does not decode: %s\nvalue: %s", u.ParseError, value)
	}
	if u.Status != "draft" || u.Week != week || u.Body == "" || u.GeneratedAt == "" || u.Schema != 1 {
		t.Fatalf("README example decoded to %#v, want a schema 1 draft for %s with a body and generated_at", u, week)
	}
	if uncited := ValidateCitations(epic, u.Body); len(uncited) > 0 {
		t.Fatalf("README example body could never publish; uncited claims: %q", uncited)
	}
}

func storedUpdateTags(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(storedUpdate{})
	tags := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			t.Fatalf("storedUpdate.%s has no JSON tag", typ.Field(i).Name)
		}
		tags = append(tags, "`"+tag+"`")
	}
	return tags
}

// readDocSection returns the text under the docSection heading, up to the next
// heading of the same or a higher level.
func readDocSection(t *testing.T, doc string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", doc))
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}
	lines := strings.Split(string(raw), "\n")
	start, level := -1, 0
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if inFence {
			continue
		}
		hashes := len(line) - len(strings.TrimLeft(line, "#"))
		if hashes == 0 || !strings.HasPrefix(line[hashes:], " ") {
			continue
		}
		if start < 0 {
			if strings.TrimSpace(line[hashes:]) == docSection {
				start, level = i, hashes
			}
			continue
		}
		if hashes <= level {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("%s has no %q heading", doc, docSection)
	}
	return strings.Join(lines[start:], "\n")
}

var kvSetLine = regexp.MustCompile(`(?m)^bd kv set (\S+) '([^']*)'$`)

// readmeExample pulls the one `bd kv set <key> '<json>'` command out of the
// README section's fenced blocks, exactly as a reader would paste it.
func readmeExample(t *testing.T) (key, value string) {
	t.Helper()
	section := readDocSection(t, "README.md")
	var fenced strings.Builder
	inFence := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			fenced.WriteString(line + "\n")
		}
	}
	matches := kvSetLine.FindAllStringSubmatch(fenced.String(), -1)
	if len(matches) != 1 {
		t.Fatalf("README %q section has %d fenced `bd kv set <key> '<json>'` lines, want 1", docSection, len(matches))
	}
	return matches[0][1], matches[0][2]
}
