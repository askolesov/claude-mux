package main

import "testing"

func TestStartedByOS(t *testing.T) {
	for _, c := range []struct {
		name string
		term string
		ppid int
		want bool
	}{
		{"Finder or Explorer", "", 1, true},
		{"open in a terminal", "xterm-256color", 1, true},
		{"a terminal", "xterm-256color", 4242, false},
	} {
		if got := startedByOS(c.term, c.ppid); got != c.want {
			t.Errorf("%s: startedByOS = %v, want %v", c.name, got, c.want)
		}
	}
}
