package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// External is a claude session running outside the manager: another
// terminal, a background agent. It comes from `claude agents --json`.
type External struct {
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Title     string `json:"title"`
}

// Externals polls `claude agents --json` and keeps the sessions that aren't
// the manager's own.
type Externals struct {
	m *Manager

	mu      sync.Mutex
	list    []External
	pending map[string]string // session id → a rename waiting for it to be idle
}

func (e *Externals) List() []External {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.list
}

func (e *Externals) Poll(ctx context.Context, every time.Duration) {
	for {
		e.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (e *Externals) refresh(ctx context.Context) {
	out, err := exec.CommandContext(ctx, "claude", "agents", "--json").Output()
	if err != nil {
		return
	}
	var all []External
	if json.Unmarshal(out, &all) != nil {
		return
	}
	own := map[int]bool{}
	for _, v := range e.m.List() {
		own[v.PID] = true
	}
	list := []External{}
	for _, x := range all {
		if own[x.PID] {
			e.m.FollowClaudeName(x.PID, x.Name)
			continue
		}
		x.Title = transcriptTitle(x.SessionID)
		e.m.renameFavourite(x.SessionID, x.Name)
		if x.Status == "idle" {
			e.renameIfPending(x)
		}
		list = append(list, x)
	}
	e.mu.Lock()
	changed := !jsonEqual(e.list, list)
	e.list = list
	e.mu.Unlock()
	if changed {
		e.m.notify()
	}
}

// Rename renames a session running in another terminal through claude: the
// rename keys typed into its window, now if it is idle, else once the poll
// sees it idle.
func (e *Externals) Rename(sid, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("empty name")
	}
	for _, x := range e.List() {
		if x.SessionID != sid {
			continue
		}
		e.mu.Lock()
		if e.pending == nil {
			e.pending = map[string]string{}
		}
		e.pending[sid] = name
		e.mu.Unlock()
		if x.Status == "idle" {
			e.renameIfPending(x)
		}
		return nil
	}
	return errNotFound
}

func (e *Externals) renameIfPending(x External) {
	e.mu.Lock()
	name, ok := e.pending[x.SessionID]
	delete(e.pending, x.SessionID)
	e.mu.Unlock()
	if !ok {
		return
	}
	clear, command := renameKeys(name)
	go func() {
		if err := TypeInto(x.PID, clear+command); err != nil {
			fmt.Fprintln(os.Stderr, "rename:", err)
		}
	}()
}

// Stop hangs up the claude process running session sid elsewhere, as closing
// its terminal window would, and waits for it to exit. Only that process is
// signalled: its window and shell stay open. A session not running is fine.
func (e *Externals) Stop(sid string) error {
	pid := 0
	for _, x := range e.List() {
		if x.SessionID == sid {
			pid = x.PID
		}
	}
	if pid == 0 {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop process %d: %w", pid, err)
	}
	for deadline := time.Now().Add(6 * time.Second); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d didn't exit; quit it in its terminal", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.mu.Lock()
	e.list = slices.DeleteFunc(slices.Clone(e.list), func(x External) bool { return x.SessionID == sid })
	e.mu.Unlock()
	e.m.notify()
	return nil
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// TranscriptEntry is one message of a transcript, flattened for reading.
type TranscriptEntry struct {
	Role string `json:"role"` // user, assistant, tool
	Text string `json:"text"`
}

func transcriptPath(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 || strings.ContainsAny(sessionID, `/\`) {
		return "", errNotFound
	}
	return matches[0], nil
}

// readTail returns up to the last n bytes of a file, from a line start.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(st.Size()-n, 0)
	data, err := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return data, nil
}

// transcriptTitle is the session's latest AI-generated title, if any.
func transcriptTitle(sessionID string) string {
	path, err := transcriptPath(sessionID)
	if err != nil {
		return ""
	}
	data, err := readTail(path, 256*1024)
	if err != nil {
		return ""
	}
	title := ""
	for line := range bytes.Lines(data) {
		if !bytes.Contains(line, []byte(`"ai-title"`)) {
			continue
		}
		var r struct {
			Type    string `json:"type"`
			AITitle string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &r) == nil && r.Type == "ai-title" {
			title = r.AITitle
		}
	}
	return title
}

// Transcript returns the last limit messages of a claude session's transcript.
func Transcript(sessionID string, limit int) ([]TranscriptEntry, error) {
	path, err := transcriptPath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := readTail(path, 4<<20)
	if err != nil {
		return nil, err
	}
	var entries []TranscriptEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var r struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || (r.Type != "user" && r.Type != "assistant") {
			continue
		}
		entries = append(entries, flatten(r.Type, r.Message.Content)...)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	return entries[max(len(entries)-limit, 0):], nil
}

// flatten turns one message's content into entries: text as the role's,
// tool calls as one-line tool entries. Tool results and thinking are skipped.
func flatten(role string, content json.RawMessage) []TranscriptEntry {
	var text string
	if json.Unmarshal(content, &text) == nil {
		if text = strings.TrimSpace(text); text == "" || strings.HasPrefix(text, "<") {
			return nil // empty, or harness-injected markup
		}
		return []TranscriptEntry{{Role: role, Text: text}}
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []TranscriptEntry
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" && !strings.HasPrefix(t, "<") {
				out = append(out, TranscriptEntry{Role: role, Text: t})
			}
		case "tool_use":
			out = append(out, TranscriptEntry{Role: "tool", Text: b.Name + " " + toolSummary(b.Input)})
		}
	}
	return out
}

// toolSummary picks the one input field that says what a tool call did.
func toolSummary(input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, k := range []string{"description", "command", "file_path", "pattern", "prompt", "url", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			if len(v) > 160 {
				v = v[:160] + "…"
			}
			return v
		}
	}
	return ""
}
