package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The integration seam holds only while core packages cannot reach across
// it. Two import paths sit behind the seam: the run machinery and the djinn
// adapter. Only the composition root (package main at the module root) and
// the adapter itself may import either. The gate reads the production package
// graph — what the binary links — so a _test.go file is outside it.
const (
	modulePath = "github.com/kurofune/beads-loom"
	adapterPkg = modulePath + "/internal/integration/djinn"
)

var forbiddenImports = []string{
	modulePath + "/internal/runs",
	adapterPkg,
}

type listedPackage struct {
	ImportPath string
	Name       string
	Imports    []string
	Module     *struct{ Path string }
}

// mayCrossSeam reports whether pkg is allowed to import a path behind the seam.
func mayCrossSeam(pkg listedPackage) bool {
	if pkg.ImportPath == modulePath && pkg.Name == "main" {
		return true
	}
	return pkg.ImportPath == adapterPkg || strings.HasPrefix(pkg.ImportPath, adapterPkg+"/")
}

// boundaryViolations lists every forbidden import one of the module's own
// packages makes, one line per edge, naming the importer and the import.
func boundaryViolations(pkgs []listedPackage) []string {
	var out []string
	for _, pkg := range pkgs {
		if pkg.Module == nil || pkg.Module.Path != modulePath || mayCrossSeam(pkg) {
			continue
		}
		for _, imp := range pkg.Imports {
			for _, forbidden := range forbiddenImports {
				if imp == forbidden {
					out = append(out, fmt.Sprintf("%s imports %s", pkg.ImportPath, forbidden))
				}
			}
		}
	}
	return out
}

// observeModuleTree opens every directory `./...` can match, from the module
// root down, in this process. The package graph comes from a `go list`
// subprocess, and `go test` keys its result cache only on files the test
// process itself opens; an opened directory keys on each entry's name, size
// and mtime. Without this, an edit that adds a forbidden import or a new
// package reuses the last passing result and the gate never runs.
func observeModuleTree(t *testing.T, root string) {
	t.Helper()
	var walk func(dir string)
	walk = func(dir string) {
		f, err := os.Open(dir)
		if err != nil {
			t.Fatalf("open %s: %v", dir, err)
		}
		entries, err := f.Readdir(-1)
		f.Close()
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" {
				continue
			}
			sub := filepath.Join(dir, name)
			if _, err := os.Stat(filepath.Join(sub, "go.mod")); err == nil {
				continue
			}
			walk(sub)
		}
	}
	walk(root)
}

// modulePackages runs `go list -deps -json ./...` from the module root.
func modulePackages(t *testing.T) []listedPackage {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	root := strings.TrimSpace(string(out))
	observeModuleTree(t, root)
	cmd := exec.Command("go", "list", "-deps", "-json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps -json ./...: %v\n%s", err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var pkg listedPackage
		if err := dec.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

func TestBoundaryCorePackagesDoNotImportDjinn(t *testing.T) {
	pkgs := modulePackages(t)
	var sawRoot, sawAdapter bool
	for _, pkg := range pkgs {
		sawRoot = sawRoot || (pkg.ImportPath == modulePath && pkg.Name == "main")
		sawAdapter = sawAdapter || pkg.ImportPath == adapterPkg
	}
	if !sawRoot || !sawAdapter {
		t.Fatalf("package graph is missing the composition root (%v) or the djinn adapter (%v); the gate would guard nothing", sawRoot, sawAdapter)
	}
	for _, v := range boundaryViolations(pkgs) {
		t.Errorf("%s: only package main at the module root and %s/... may import it", v, adapterPkg)
	}
}

func TestBoundaryRuleNamesTheImporterAndTheForbiddenImport(t *testing.T) {
	mod := &struct{ Path string }{Path: modulePath}
	pkgs := []listedPackage{
		{ImportPath: modulePath, Name: "main", Imports: forbiddenImports, Module: mod},
		{ImportPath: adapterPkg, Name: "djinn", Imports: []string{modulePath + "/internal/runs"}, Module: mod},
		{ImportPath: adapterPkg + "/sub", Name: "sub", Imports: forbiddenImports, Module: mod},
		{ImportPath: modulePath + "/internal/httpapi", Name: "httpapi", Imports: []string{"net/http", modulePath + "/internal/runs"}, Module: mod},
		{ImportPath: modulePath + "/cmd/tool", Name: "main", Imports: []string{adapterPkg}, Module: mod},
		{ImportPath: "net/http", Name: "http", Imports: []string{"io"}},
	}
	got := boundaryViolations(pkgs)
	want := []string{
		modulePath + "/internal/httpapi imports " + modulePath + "/internal/runs",
		modulePath + "/cmd/tool imports " + adapterPkg,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n got %q\nwant %q", got, want)
	}
}
