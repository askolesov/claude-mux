package backend

import (
	"runtime"
	"testing"
)

func TestInstallCommandPerKind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the unix installers")
	}
	for kind, want := range map[Kind]string{
		KindClaude:   "curl -fsSL https://claude.ai/install.sh | bash",
		KindCodex:    "curl -fsSL https://chatgpt.com/codex/install.sh | sh",
		KindTerminal: "",
	} {
		argv, line := installCommand(kind)
		if line != want || (want == "") != (argv == nil) {
			t.Errorf("%s: %q %q, want %q", kind, argv, line, want)
		}
	}
}
