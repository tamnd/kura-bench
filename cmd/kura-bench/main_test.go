package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An engine that logs while it indexes must not cost itself a row in the table,
// so the result is read from the last line rather than from all of stdout.
func TestTheResultIsTheLastLineOfStdout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"just the result", `{"engine":"x"}`, `{"engine":"x"}`},
		{"a trailing newline", "{\"engine\":\"x\"}\n", `{"engine":"x"}`},
		{"chatter first", "detect longest field\nmore chatter\n{\"engine\":\"x\"}\n", `{"engine":"x"}`},
		{"windows line endings", "chatter\r\n{\"engine\":\"x\"}\r\n", `{"engine":"x"}`},
		{"nothing at all", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(lastLine([]byte(c.in))); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// A run over a real corpus can spend hours in one engine's index phase, and the
// point of naming the engines is to say which of them you want the numbers for
// first. Alphabetical order would put an engine you are only running out of
// curiosity ahead of the four you are waiting on.
func TestEnginesRunInTheOrderTheyWereNamed(t *testing.T) {
	dir := runners(t, "alpha", "beta", "gamma")

	got := names(t, config{binDir: dir, engines: []string{"gamma", "alpha"}})
	if want := []string{"gamma", "alpha"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Without the flag there is no order to honour, so the directory's is used and
// it has to be stable, because a report is easier to read against an earlier one
// when the rows come out the same way twice.
func TestWithoutTheFlagEveryRunnerRunsInDirectoryOrder(t *testing.T) {
	dir := runners(t, "gamma", "alpha", "beta")

	got := names(t, config{binDir: dir})
	if want := []string{"alpha", "beta", "gamma"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Naming an engine that is not there is a typo or a runner that was never built.
// Skipping it quietly means finding out hours later that the row you started the
// run for is the one missing from the table.
func TestNamingAnEngineThatIsNotThereFailsBeforeAnythingRuns(t *testing.T) {
	dir := runners(t, "alpha")

	_, err := discover(config{binDir: dir, engines: []string{"alpha", "tantivy"}})
	if err == nil {
		t.Fatal("a missing runner was accepted")
	}
	if !strings.Contains(err.Error(), "tantivy") {
		t.Errorf("the error does not say which engine is missing: %v", err)
	}
}

func names(t *testing.T, cfg config) []string {
	t.Helper()
	found, err := discover(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(found))
	for _, r := range found {
		out = append(out, r.name)
	}
	return out
}

// runners makes a directory that looks like one make build wrote, plus a file
// that is not a runner so the naming rule is exercised too.
func runners(t *testing.T, engines ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, e := range engines {
		write(t, filepath.Join(dir, e+"-runner"))
	}
	write(t, filepath.Join(dir, "kura-corpus"))
	return dir
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
