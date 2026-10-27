package hub

import "testing"

func TestChecksum(t *testing.T) {
	sums := "aaa  claude-mux-backend-linux-amd64\nbbb *claude-mux-backend-linux-arm64\nccc  claude-mux-darwin-arm64\n"
	for name, want := range map[string]string{
		"claude-mux-backend-linux-amd64": "aaa",
		"claude-mux-backend-linux-arm64": "bbb",
		"claude-mux-backend-linux":       "",
	} {
		if got := checksum(sums, name); got != want {
			t.Errorf("checksum(%q) = %q, want %q", name, got, want)
		}
	}
}
