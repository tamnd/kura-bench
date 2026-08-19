//go:build !ladybug

// This is the ladybug runner on a machine that was not built with it.
//
// The engine is a C++ shared library, so it needs cgo and it needs the library
// fetched first, and neither of those is true of anything else in this
// repository. Rather than make the whole suite depend on a C toolchain, the
// real runner sits behind a build tag and this stands in for it, so that go
// build ./... and go vet ./... still cover the package on every machine.
//
// The message matters as much as the exit code. A runner that is simply missing
// from bin/ looks the same as one that crashed, and the orchestrator reports
// both as an engine that did not produce a row.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr,
		"this binary was built without ladybug: run third_party/ladybug/fetch.sh and then make ladybug")
	os.Exit(1)
}
