package main

// The server needs its own answer to "what is running here". An operator debugging an instance has
// a container and a log, not a checkout, and the reply has to come from the binary itself.
//
// Dispatched BEFORE the configuration is read, like `mint-admin-token`: asking a process what
// version it is must not require a database to be reachable. That is exactly the situation in which
// the question gets asked.
//
// See cmd/flowlio/version.go for why these are `var` and not `const`.

import (
	"fmt"
	"io"
	"runtime"
)

// versionCommand is the subcommand name: `flowlio-api version`.
const versionCommand = "version"

// version is the release this binary was cut from, `dev` outside a goreleaser build.
var version = "dev"

// commit is the commit that release points at, empty outside a goreleaser build.
var commit = ""

// printVersion writes the single line that identifies this binary.
//
// It names `flowlio-api` and not `flowlio`: the two binaries ship in the same archive and are
// released together, so a version line that did not say which one is talking would be useless in
// the only place it is read — somebody else's paste of it.
func printVersion(out io.Writer) {
	if commit == "" {
		_, _ = fmt.Fprintf(out, "flowlio-api %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}
	_, _ = fmt.Fprintf(out, "flowlio-api %s (%s, %s %s/%s)\n", version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
