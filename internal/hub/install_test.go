package hub

import "testing"

func TestPlatform(t *testing.T) {
	for uname, want := range map[string]string{
		"Linux x86_64\n": "linux-amd64",
		"Linux aarch64":  "linux-arm64",
		"Darwin arm64":   "darwin-arm64",
		"Darwin x86_64":  "darwin-amd64",
	} {
		if got, err := platform(uname); err != nil || got != want {
			t.Errorf("platform(%q) = %q, %v; want %q", uname, got, err, want)
		}
	}
	for _, uname := range []string{"", "Linux", "FreeBSD amd64", "Linux riscv64", "MINGW64_NT-10.0 x86_64"} {
		if got, err := platform(uname); err == nil {
			t.Errorf("platform(%q) = %q, want an error", uname, got)
		}
	}
}
