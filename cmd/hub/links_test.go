package main

import "testing"

func TestOpenLinkRefuses(t *testing.T) {
	for _, link := range []string{"file:///etc/passwd", "/Applications/Calculator.app", "javascript:alert(1)", "https://", "ssh://host"} {
		if openLink(link) == nil {
			t.Errorf("opened %q", link)
		}
	}
}
