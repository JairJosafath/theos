/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Command theos-env runs an agent's commands in a test environment, a place
// with tools the sandbox lacks (docs/plans/test-environments.md, T4):
//
//	theos-env run <name> [--] <command> [args...]
//	theos-env test <name> --type <type> --app <file> --tests <file> [--out <dir>]
//	theos-env serve [--listen :7070] [--work /work] [--tls]
//	theos-env install <path>
//
// In the sandbox, run sends the working tree's tracked and untracked, not
// ignored, files to the environment the run leased as name, at
// THEOS_ENV_<NAME>_URL with THEOS_ENV_<NAME>_TOKEN (and the certificate in
// THEOS_ENV_<NAME>_CA for one over public HTTPS), runs the command there in
// the same folder, streams its output, writes the files it changed or added
// back into the tree, and exits with its exit code. In the environment,
// serve takes those runs with the lease's token in THEOS_ENV_TOKEN, one at
// a time, each on a fresh /work; with --tls, on a remote machine, over HTTPS
// with the certificate the token makes, which the run pins (its _CA), the
// certificate itself, not a host name. test runs an app's test package on
// the devices of an environment that runs test packages, such as AWS Device
// Farm's (step 10): it asks the operator over the Git host, by a request
// commit on the lease's branch, THEOS_ENV_<NAME>_URL git:theos-env/<lease>,
// and writes the report and artifacts the operator answers with into --out
// (test.go). install copies this binary to path: the lease pod's init
// container puts it next to the environment's tools. A static binary, so
// any image runs it.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// exitFailed is theos-env's own exit code when it could not run the
// command: the command's own codes go through unchanged.
const exitFailed = 125

// exitUsage is the exit code of a command line theos-env doesn't take.
const exitUsage = 2

func main() {
	os.Exit(mainWith(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

const usage = `usage:
  theos-env run <name> [--] <command> [args...]
  theos-env test <name> --type instrumentation|xctest|xctest-ui --app <file> --tests <file> [--out <dir>] [--timeout 3h]
  theos-env serve [--listen :7070] [--work /work] [--tls]
  theos-env install <path>
`

func mainWith(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "run":
		return runMain(args[1:], stdout, stderr, getenv)
	case "test":
		return testMain(args[1:], stdout, stderr, getenv)
	case "serve":
		return serveMain(args[1:], stderr, getenv)
	case "install":
		return installMain(args[1:], stderr)
	}
	_, _ = fmt.Fprint(stderr, usage)
	return exitUsage
}

// installMain copies this binary to the path given, executable.
func installMain(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	if err := install(args[0]); err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
		return exitFailed
	}
	return 0
}

func install(to string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(self) //nolint:gosec // this binary itself
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil { //nolint:gosec // a folder of tools anyone runs
		return err
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) //nolint:gosec // an executable
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
