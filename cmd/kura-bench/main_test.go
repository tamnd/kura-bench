package main

import "testing"

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
