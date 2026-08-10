package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                            | Ligne |
// |--------------|-------------------------------------------------------------------|-------|
// | printVersion | Writes the one line that identifies this binary                     | 48    |
// | runVersion   | The `flowlio version` command                                       | 58    |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THIS FILE EXISTS AT ALL.
//
// `.goreleaser.yaml` has passed `-X main.version={{.Version}} -X main.commit={{.Commit}}` since the
// first release. The Go linker writes those values into a symbol it can find, and SAYS NOTHING when
// it cannot: no symbol existed here, so v0.1.0 through v0.3.0 shipped binaries that carried no
// version at all, and there was no command to ask one for it either. A user reporting a bug could
// not name what they were running, and neither could `flowlio doctor`.
//
// The two identifiers below are the symbols those flags aim at. They are the one place in this
// repository where a mutable package-level `var` is right: `-X` only writes to a `var` of type
// string, so a `const` would compile and stay `dev` in every release forever — the exact failure
// this file corrects. Nothing writes to them at run time.

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// version is the release this binary was cut from. Overwritten at link time by goreleaser; the
// value below is what a `go build` or a `go run` from a checkout carries, and saying `dev` is the
// honest answer there — a checkout is not a release.
var version = "dev"

// commit is the commit that release points at, empty outside a goreleaser build. A version alone
// does not locate a bug reported against a build from main.
var commit = ""

// printVersion writes the single line that identifies this binary: what it is, which release, which
// commit, and the toolchain it was built with.
//
// ONE LINE, and greppable, because the first thing done with it is pasting it into a bug report.
// The commit is dropped rather than printed empty: `flowlio dev ()` reads as a bug in the command
// itself.
func printVersion(out io.Writer) {
	if commit == "" {
		_, _ = fmt.Fprintf(out, "flowlio %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}
	_, _ = fmt.Fprintf(out, "flowlio %s (%s, %s %s/%s)\n", version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// runVersion is the `flowlio version` command. It takes no argument and reaches no network: it has
// to answer on a host whose instance is down, which is one of the moments somebody asks.
func runVersion(_ []string) error {
	printVersion(os.Stdout)
	return nil
}
