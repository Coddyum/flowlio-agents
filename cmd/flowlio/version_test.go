package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The version line is what a user pastes into a bug report, so both shapes are asserted on the
// actual text: with a commit when there is one, without the empty parentheses when there is not.
func TestPrintVersionSaysWhatItIs(t *testing.T) {
	original, originalCommit := version, commit
	t.Cleanup(func() { version, commit = original, originalCommit })

	version, commit = "v9.9.9", "abcdef1"
	var out bytes.Buffer
	printVersion(&out)
	if got := out.String(); !strings.HasPrefix(got, "flowlio v9.9.9 (abcdef1, go") {
		t.Errorf("stamped build printed %q, expected it to name the release then the commit", got)
	}

	version, commit = "dev", ""
	out.Reset()
	printVersion(&out)
	got := out.String()
	if !strings.HasPrefix(got, "flowlio dev (go") {
		t.Errorf("unstamped build printed %q, expected `flowlio dev (go…`", got)
	}
	if strings.Contains(got, ", ") {
		t.Errorf("unstamped build printed %q, expected no room left for an empty commit", got)
	}
}

// THE REGRESSION THIS FILE EXISTS FOR.
//
// `.goreleaser.yaml` passes `-X main.version=…`. The Go linker writes that value into a symbol it
// can find and says NOTHING when it cannot — so v0.1.0 through v0.3.0 shipped binaries carrying no
// version, and nothing failed. A unit test on printVersion would have stayed green throughout: the
// defect was never in the code, it was in the two files disagreeing about a name.
//
// So the assertion is the agreement itself: every `-X main.<name>` in the release configuration
// names a package-level string var actually declared in the binary that flag applies to. Rename
// `version` here, or drop this file, and this test goes red.
func TestEveryLdflagTargetIsDeclared(t *testing.T) {
	root := filepath.Join("..", "..")

	raw, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}

	targets := regexp.MustCompile(`-X main\.([A-Za-z_][A-Za-z0-9_]*)=`).FindAllStringSubmatch(string(raw), -1)
	if len(targets) == 0 {
		t.Fatal("no `-X main.<name>=` found in .goreleaser.yaml — the stamping was removed, or this test lost its subject")
	}

	// Both binaries are built from the same ldflags block, so a name has to exist in both.
	for _, pkg := range []string{"cmd/flowlio", "cmd/api"} {
		declared := packageStringVars(t, filepath.Join(root, pkg))
		for _, target := range targets {
			if !declared[target[1]] {
				t.Errorf("%s: .goreleaser.yaml stamps `main.%s`, which is declared nowhere in that package — "+
					"the linker will silently write nothing", pkg, target[1])
			}
		}
	}
}

// packageStringVars collects the package-level `var` names of a directory. It reads the source
// rather than the built binary on purpose: this test has to fail at `go test`, long before a tag
// pushes anything to anyone.
//
// File by file rather than through parser.ParseDir, which is deprecated: the grouping into packages
// it offers is exactly what is not needed here — `-X main.<name>` aims at whatever the built binary
// declares, and every non-test .go file of a cmd directory is part of it.
func packageStringVars(t *testing.T, dir string) map[string]bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	names := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, ident := range value.Names {
					names[ident.Name] = true
				}
			}
		}
	}
	return names
}
