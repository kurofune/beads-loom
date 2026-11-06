package httpapi_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

/*
Loom performs no generation.

Weekly initiative updates arrive from one place only: values an agent wrote into
bd's key-value space, read back through `bd kv`. The server drafts nothing,
summarises nothing, and calls no model. That is a load-bearing property — it is
why a published update is a human decision about an agent's words rather than
about Loom's — so it is checked mechanically here instead of asserted in prose.

Two facts carry it, and both are checked over every non-test Go file in the
module:

 1. Every process the server can start resolves to a known binary. `bd` and
    `git` are the data sources; `summoner` is the operator's own agent runner,
    launched only from an explicit request and never from a read path.
 2. No source file imports an LLM SDK, and no string literal names an LLM CLI.

A new `exec.Command("claude", …)`, a non-literal binary this analysis cannot
resolve, or an `anthropic`/`openai` import all turn this test red.
*/

// allowedBinaries are the executables Loom may start, each named by a string
// literal in the source that resolves it.
var allowedBinaries = map[string]string{
	"bd":       "the beads CLI — Loom's only write path and its kv reader",
	"git":      "read-only history for reconciliation sweeps",
	"summoner": "the operator's djinn run launcher, started only from an explicit summon request",
}

// allowedIndirectSources are the expressions an exec binary argument may come
// from when it is not a literal in the same function. Each is an operator-set
// override whose default is a literal checked by the rule above.
var allowedIndirectSources = map[string]string{
	"Path":          "ExecRunner.Path — a caller override for the bd/git executable; the empty default falls back to the literal in the same function",
	"ResolveBinary": "Manager.ResolveBinary — the configured summoner path, defaulting to the SummonerBinary literal",
	"exec.LookPath": "PATH lookup of a literal binary name",
	"filepath.Join": "a path assembled from checked components",
	"os.Executable": "Loom re-invoking itself",
}

// llmImportMarkers are substrings that would appear in the import path of any
// model SDK.
var llmImportMarkers = []string{
	"anthropic", "openai", "langchain", "genai", "cohere", "mistral",
	"ollama", "huggingface", "llama", "bedrock-runtime", "vertexai",
}

// llmBinaryMarkers are executables that would mean the server can invoke a
// model directly.
var llmBinaryMarkers = []string{
	"claude", "codex", "chatgpt", "gemini", "ollama", "llm", "aider", "goose",
}

func TestServerHasNoGenerationPath(t *testing.T) {
	root := moduleRoot(t)
	files := goSourceFiles(t, root)
	if len(files) < 20 {
		t.Fatalf("walked %d Go files from %s; the walk is not reaching the module", len(files), root)
	}

	fset := token.NewFileSet()
	var execSites int
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)

		checkImports(t, rel, file)
		checkStringLiterals(t, rel, fset, file)
		execSites += checkExecCalls(t, rel, fset, file)
	}

	// The walk is only evidence if it actually found the exec sites it claims
	// to police; a refactor that hides them behind a wrapper must not read as
	// "no exec calls, therefore clean".
	if execSites < 4 {
		t.Fatalf("found %d exec call sites, want the known ones in bdexec, gitread, runs and main", execSites)
	}
}

// checkImports fails on any import path naming a model SDK.
func checkImports(t *testing.T, rel string, file *ast.File) {
	t.Helper()
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		lower := strings.ToLower(path)
		for _, marker := range llmImportMarkers {
			if strings.Contains(lower, marker) {
				t.Errorf("%s imports %q: Loom performs no generation, so no model SDK may be linked in",
					rel, path)
			}
		}
	}
}

// checkStringLiterals fails on a literal naming an LLM CLI, which is how a new
// exec of a model runner would most likely arrive.
func checkStringLiterals(t *testing.T, rel string, fset *token.FileSet, file *ast.File) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		// Whole-token match: "llm" must not fire on "allmatches", and a URL in
		// a doc string must not fire on its host.
		for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !('a' <= r && r <= 'z')
		}) {
			for _, marker := range llmBinaryMarkers {
				if field == marker {
					t.Errorf("%s: string literal %q names the LLM CLI %q — Loom performs no generation",
						fset.Position(lit.Pos()), value, marker)
				}
			}
		}
		return true
	})
}

// checkExecCalls resolves the binary argument of every exec call and returns
// how many sites it inspected.
func checkExecCalls(t *testing.T, rel string, fset *token.FileSet, file *ast.File) int {
	t.Helper()
	sites := 0
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			verb, ok := execVerb(call)
			if !ok {
				return true
			}
			sites++
			pos := fset.Position(call.Pos())

			binaryArg := 0
			if verb == "CommandContext" {
				binaryArg = 1
			}
			if len(call.Args) <= binaryArg {
				t.Errorf("%s: exec.%s has no binary argument", pos, verb)
				return true
			}

			literals, indirect := resolveBinary(fn.Body, call.Args[binaryArg])
			if len(literals) == 0 && len(indirect) == 0 {
				t.Errorf("%s: cannot resolve the binary argument of exec.%s to any source; "+
					"every process Loom starts must be traceable to a named binary", pos, verb)
				return true
			}
			for _, lit := range literals {
				if _, ok := allowedBinaries[lit]; !ok {
					t.Errorf("%s: exec.%s runs %q, which is not one of %s",
						pos, verb, lit, sortedKeys(allowedBinaries))
				}
			}
			for _, src := range indirect {
				if _, ok := allowedIndirectSources[src]; !ok {
					t.Errorf("%s: exec.%s takes its binary from %s, which is not a known source (%s)",
						pos, verb, src, sortedKeys(allowedIndirectSources))
				}
			}
			return true
		})
		return false
	})
	if sites > 0 {
		t.Logf("%s: %d exec call site(s) inspected", rel, sites)
	}
	return sites
}

// execVerb reports whether a call is exec.Command / exec.CommandContext, or one
// of the lower-level process starters that would bypass them.
func execVerb(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch pkg.Name + "." + sel.Sel.Name {
	case "exec.Command", "exec.CommandContext":
		return sel.Sel.Name, true
	case "os.StartProcess", "syscall.Exec", "syscall.ForkExec":
		return sel.Sel.Name, true
	}
	return "", false
}

// resolveBinary traces the binary expression back to the string literals and
// named indirect sources it can come from within one function body.
func resolveBinary(body *ast.BlockStmt, expr ast.Expr) (literals, indirect []string) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if v, err := strconv.Unquote(e.Value); err == nil {
				return []string{v}, nil
			}
		}
	case *ast.SelectorExpr:
		return nil, []string{e.Sel.Name}
	case *ast.CallExpr:
		return callSource(e)
	case *ast.Ident:
		return resolveIdent(body, e.Name)
	}
	return nil, nil
}

// resolveIdent collects every value assigned to a local name inside one body.
func resolveIdent(body *ast.BlockStmt, name string) (literals, indirect []string) {
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != name || i >= len(assign.Rhs) {
				continue
			}
			lits, ind := resolveBinary(body, assign.Rhs[i])
			literals = append(literals, lits...)
			indirect = append(indirect, ind...)
		}
		return true
	})
	return literals, indirect
}

// callSource names the function a binary came from, unwrapping the one call
// whose literal argument IS the binary: exec.LookPath("bd").
func callSource(call *ast.CallExpr) (literals, indirect []string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		if ident, ok := call.Fun.(*ast.Ident); ok {
			return nil, []string{ident.Name}
		}
		return nil, nil
	}
	name := sel.Sel.Name
	if pkg, ok := sel.X.(*ast.Ident); ok {
		qualified := pkg.Name + "." + name
		if qualified == "exec.LookPath" && len(call.Args) == 1 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					return []string{v}, []string{qualified}
				}
			}
		}
		if _, known := allowedIndirectSources[qualified]; known {
			return nil, []string{qualified}
		}
	}
	return nil, []string{name}
}

// --- walking --------------------------------------------------------------

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

func goSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "web", ".git", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v", keys)
}
