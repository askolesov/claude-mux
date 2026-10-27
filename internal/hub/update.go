package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latestRelease = "https://api.github.com/repos/askolesov/claude-mux/releases/latest"

// UpdateView is what the page sees of updates.
type UpdateView struct {
	Current   string    `json:"current"`
	Dev       bool      `json:"dev"` // a development build, which doesn't update
	LastCheck time.Time `json:"lastCheck,omitzero"`
	Latest    string    `json:"latest,omitempty"` // the newest release, when newer than this one
	Notes     string    `json:"notes,omitempty"`  // its release page
	Available bool      `json:"available"`        // Latest, unless skipped
	Error     string    `json:"error,omitempty"`  // why the last check failed
	Step      string    `json:"step,omitempty"`   // while updating
	Updating  bool      `json:"updating,omitempty"`
}

// updater checks GitHub for a newer release, every few days if the
// settings say so, and installs it in place of the running hub.
type updater struct {
	st       *store
	dir      string // downloads
	current  string
	onChange func()

	mu       sync.Mutex
	latest   string
	notes    string
	err      string
	step     string
	updating bool
	relaunch []string      // once an update is installed: the command that starts it
	restart  chan struct{} // closed then
}

func newUpdater(st *store, stateDir, current string, onChange func()) *updater {
	return &updater{st: st, dir: filepath.Join(stateDir, "update"),
		current: current, onChange: onChange, restart: make(chan struct{})}
}

// Restart is closed once an update is installed; Relaunch is then the
// command that starts it.
func (u *updater) Relaunch() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.relaunch
}

func (u *updater) dev() bool { _, ok := semver(u.current); return !ok }

func (u *updater) View() UpdateView {
	kept := u.st.get()
	u.mu.Lock()
	defer u.mu.Unlock()
	return UpdateView{Current: u.current, Dev: u.dev(), LastCheck: kept.Update.LastCheck,
		Latest: u.latest, Notes: u.notes, Available: u.latest != "" && u.latest != kept.Update.Skip,
		Error: u.err, Step: u.step, Updating: u.updating}
}

// Skip stops offering the newest release; a newer one is offered again.
func (u *updater) Skip() {
	u.mu.Lock()
	latest := u.latest
	u.mu.Unlock()
	u.st.change(func(h *hubState) { h.Update.Skip = latest })
	u.onChange()
}

// loop checks when a check is due: on start, then every hour.
func (u *updater) loop() {
	for {
		kept := u.st.get()
		due := kept.Settings.CheckUpdates && time.Since(kept.Update.LastCheck) >= time.Duration(kept.Settings.EveryDays)*24*time.Hour
		if due && !u.dev() {
			_ = u.Check(context.Background())
		}
		time.Sleep(time.Hour)
	}
}

// Check asks GitHub for the newest release.
func (u *updater) Check(ctx context.Context) error {
	if u.dev() {
		return errors.New("this is a development build, which doesn't update")
	}
	tag, notes, err := fetchLatest(ctx)
	u.mu.Lock()
	u.err = ""
	if err != nil {
		u.err = err.Error()
	} else {
		u.latest, u.notes = "", ""
		if newer(tag, u.current) {
			u.latest, u.notes = tag, notes
		}
	}
	u.mu.Unlock()
	if err == nil {
		u.st.change(func(h *hubState) { h.Update.LastCheck = time.Now() })
	}
	u.onChange()
	return err
}

func fetchLatest(ctx context.Context) (tag, notes string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestRelease, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub: %s", res.Status)
	}
	var rel struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rel); err != nil {
		return "", "", err
	}
	return rel.Tag, rel.URL, nil
}

// semver reads "v1.2.3".
func semver(v string) ([3]int, bool) {
	var n [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// newer says whether release a is newer than b.
func newer(a, b string) bool {
	x, ok1 := semver(a)
	y, ok2 := semver(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func (u *updater) setStep(step string) {
	u.mu.Lock()
	u.step = step
	u.mu.Unlock()
	u.onChange()
}

// Apply downloads the newest release and puts it in place of the running
// hub, telling step how it goes. Restart then fires: the hub closes its
// backends, which hang up their tabs, and Relaunch starts the new one,
// which resumes them.
func (u *updater) Apply(step func(string)) error {
	u.mu.Lock()
	ver := u.latest
	busy := u.updating
	u.updating = ver != "" && !busy
	u.mu.Unlock()
	switch {
	case busy:
		return errors.New("already updating")
	case ver == "":
		return errors.New("no newer version to update to")
	}
	report := func(s string) { u.setStep(s); step(s) }
	relaunch, err := installRelease(ver, u.dir, report)
	u.mu.Lock()
	u.updating, u.step = false, ""
	if err == nil {
		u.relaunch = relaunch
	}
	u.mu.Unlock()
	u.onChange()
	if err != nil {
		return err
	}
	close(u.restart)
	return nil
}

// installRelease downloads release ver for this machine and puts it where the
// running hub is, returning the command that starts it.
func installRelease(ver, dir string, step func(string)) ([]string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, err
	}
	if strings.Contains(exe, "/AppTranslocation/") {
		return nil, errors.New("claude-mux is running from the read-only copy macOS makes of a downloaded app: " +
			"move claude-mux.app to Applications, open it from there and update again")
	}
	asset := "claude-mux-" + runtime.GOOS + "-" + runtime.GOARCH
	switch runtime.GOOS {
	case "darwin":
		asset = "claude-mux-mac.zip"
	case "windows":
		asset += ".exe"
	}
	file := filepath.Join(dir, ver, asset)
	err = download(ver, asset, file, func(done, total int64) {
		step("downloading " + ver + " " + progress(done, total))
	})
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", ver, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	step("installing " + ver)
	args := os.Args[1:]
	if runtime.GOOS != "darwin" {
		return append([]string{exe}, args...), replaceFile(file, exe)
	}

	// The Mac release is the app, zipped.
	unpacked, err := os.MkdirTemp(dir, "app")
	if err != nil {
		return nil, err
	}
	if out, err := exec.Command("ditto", "-x", "-k", file, unpacked).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("unzipping: %s", strings.TrimSpace(string(out)))
	}
	app := filepath.Join(unpacked, "claude-mux.app")
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 { // a bare binary: take the one in the app
		return append([]string{exe}, args...), replaceFile(filepath.Join(app, "Contents", "MacOS", "claude-mux"), exe)
	}
	bundle := exe[:i+len(".app")]
	if err := replaceDir(app, bundle); err != nil {
		return nil, err
	}
	return append([]string{"open", "-n", bundle, "--args"}, args...), nil
}

// replaceFile puts src at dst, the running program. Windows won't overwrite
// a running program but lets it be renamed: it moves aside to dst.old,
// which the next start removes.
func replaceFile(src, dst string) error {
	tmp, err := copyTemp(src, filepath.Dir(dst))
	if err != nil {
		return writable(dst, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if runtime.GOOS == "windows" {
		_ = os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			return writable(dst, err)
		}
	}
	return writable(dst, os.Rename(tmp, dst))
}

// replaceDir swaps the app bundle dst for src. The running hub keeps
// running from the old one, which is gone once it exits.
func replaceDir(src, dst string) error {
	staged := dst + ".new"
	_ = os.RemoveAll(staged)
	if out, err := exec.Command("ditto", src, staged).CombinedOutput(); err != nil {
		return writable(dst, fmt.Errorf("%s", strings.TrimSpace(string(out))))
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil {
		_ = os.RemoveAll(staged)
		return writable(dst, err)
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Rename(old, dst)
		return writable(dst, err)
	}
	return os.RemoveAll(old)
}

func writable(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("can't replace %s (%w): put claude-mux somewhere you can write, or update it by hand", path, err)
}

// copyTemp copies src to a new executable file in dir and returns its path.
func copyTemp(src, dir string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.CreateTemp(dir, ".claude-mux-update-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(out.Name(), 0o755)
	}
	if err != nil {
		_ = os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}
