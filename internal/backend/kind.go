package backend

import "fmt"

// Kind is what a tab runs: an agent (claude, codex) or a plain terminal. A
// tab's kind is set when it is made and never changes.
type Kind string

const (
	KindClaude   Kind = "claude"
	KindCodex    Kind = "codex"
	KindTerminal Kind = "terminal"
)

func (k Kind) valid() error {
	if k != KindClaude && k != KindCodex && k != KindTerminal {
		return fmt.Errorf("unknown tab kind %q", k)
	}
	return nil
}

// agent reports whether the kind runs an agent, which has hooks, a
// conversation to continue and a startup message.
func (k Kind) agent() bool { return k == KindClaude || k == KindCodex }
