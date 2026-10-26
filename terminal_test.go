package main

import (
	"strings"
	"testing"
)

func TestKeystrokeChunks(t *testing.T) {
	long := strings.Repeat("a", 100)
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"short", "hi", []string{"hi\r"}},
		{"long line", long, []string{strings.Repeat("a", 48) + "\x1b", "\x7f" + strings.Repeat("a", 48) + "\x1b", "\x7faaaa\r"}},
		{"two lines", "one\ntwo", []string{"one\x1b", "two\r"}},
		{"blank line", "one\n\ntwo", []string{"one\x1b", "\x1b", "two\r"}},
		{"crlf", "one\r\ntwo", []string{"one\x1b", "two\r"}},
	}
	for _, c := range cases {
		got := keystrokeChunks(c.text)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSplitBytesKeepsCharacters(t *testing.T) {
	s := strings.Repeat("я", 40) // 2 bytes each
	for _, p := range splitBytes(s, 48) {
		if len(p) > 48 || !strings.HasPrefix(s, p) && !strings.Contains(s, p) {
			t.Fatalf("bad piece %q", p)
		}
		for _, r := range p {
			if r != 'я' {
				t.Fatalf("piece cut inside a character: %q", p)
			}
		}
	}
}
