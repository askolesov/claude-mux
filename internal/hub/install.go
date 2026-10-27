package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

const releases = "https://github.com/askolesov/claude-mux/releases/download/"

// Backends finds the backend build the hub installs in an environment, one
// per platform: claude-mux-backend-<os>-<arch>. A released hub downloads it
// from the GitHub release of its own version, so the two always speak the
// same protocol; a development hub takes it from a directory of builds
// (restart.sh makes them).
type Backends struct {
	Dir     string // development builds; "" to download from the release
	Version string // the hub's version, a release tag
	Cache   string // downloads, by version
}

// Build is the build for a platform ("linux-arm64") and its version.
func (b Backends) Build(platform string) (path, ver string, err error) {
	name := "claude-mux-backend-" + platform
	if strings.HasPrefix(platform, "windows") {
		name += ".exe"
	}
	if b.Dir != "" {
		path = filepath.Join(b.Dir, name)
		if _, err := os.Stat(path); err != nil {
			return "", "", fmt.Errorf("no backend build for %s in %s", platform, b.Dir)
		}
		ver, err = version(path)
		return path, ver, err
	}
	if b.Version == "" || b.Version == "dev" {
		return "", "", errors.New("this hub is a development build: give it the backend builds with -backend-dir")
	}
	path = filepath.Join(b.Cache, b.Version, name)
	if _, err := os.Stat(path); err == nil {
		return path, b.Version, nil
	}
	if err := download(b.Version, name, path); err != nil {
		return "", "", fmt.Errorf("downloading the %s backend: %w", platform, err)
	}
	prune(b.Cache, b.Version)
	return path, b.Version, nil
}

// download fetches a release file and checks it against the release's
// checksums.txt before putting it at dst.
func download(ver, name, dst string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	get := func(file string) (io.ReadCloser, error) {
		resp, err := client.Get(releases + ver + "/" + file)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%s: %s", resp.Request.URL, resp.Status)
		}
		return resp.Body, nil
	}
	body, err := get("checksums.txt")
	if err != nil {
		return err
	}
	sums, err := io.ReadAll(io.LimitReader(body, 1<<16))
	_ = body.Close()
	if err != nil {
		return err
	}
	want := checksum(string(sums), name)
	if want == "" {
		return fmt.Errorf("release %s has no %s", ver, name)
	}

	body, err = get(name)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), name+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: checksum %s, the release says %s", name, got, want)
	}
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}

// checksum finds a file's sha256 in sha256sum's output.
func checksum(sums, name string) string {
	for line := range strings.Lines(sums) {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0]
		}
	}
	return ""
}

// installLocal puts a build at <dir>/<version>/claude-mux-backend, as the
// SSH bootstrap does on a host, and removes the other versions.
func installLocal(src, dir, ver string) (string, error) {
	dst := filepath.Join(dir, ver, "claude-mux-backend")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	// A new file renamed into place, never one rewritten: macOS kills a
	// binary whose code signature went stale.
	out, err := os.CreateTemp(filepath.Dir(dst), "claude-mux-backend.*.tmp")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(out.Name()) }()
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(out.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(out.Name(), dst)
	}
	if err != nil {
		return "", err
	}
	prune(dir, ver)
	return dst, nil
}

// prune removes every entry of dir but keep: versions nothing uses anymore.
func prune(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// version names a development build by its content, so a changed build is
// installed again and an unchanged one never is.
func version(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "dev-" + hex.EncodeToString(h.Sum(nil))[:12], nil
}

// platform maps `uname -sm` to a build's platform.
func platform(uname string) (string, error) {
	f := strings.Fields(strings.ToLower(uname))
	if len(f) != 2 {
		return "", fmt.Errorf("unexpected uname %q", uname)
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[f[1]]
	if (f[0] != "linux" && f[0] != "darwin") || arch == "" {
		return "", fmt.Errorf("no backend build for %s", uname)
	}
	return f[0] + "-" + arch, nil
}

// Connectors are the commands that reach environments, the user's own:
// ssh with its keys and ~/.ssh/config, docker, wsl.exe.
type Connectors struct {
	SSH    []string // "ssh", or a replacement such as "ssh -F <config>"
	Docker []string
	WSL    []string
}

// Kinds are the kinds of environment this machine can add.
func (c Connectors) Kinds() []string {
	kinds := []string{"ssh"}
	if _, err := exec.LookPath(c.Docker[0]); err == nil {
		kinds = append(kinds, "docker")
	}
	if _, err := exec.LookPath(c.WSL[0]); err == nil && runtime.GOOS == "windows" {
		kinds = append(kinds, "wsl")
	}
	return kinds
}

// shell is the command that runs a sh command line in an environment.
// BatchMode makes a host that wants a password fail instead of waiting for
// one; the keepalives notice a dead connection.
func (c Connectors) shell(cfg EnvConfig) (func(line string) []string, error) {
	with := func(base []string, args ...string) []string { return append(append([]string{}, base...), args...) }
	switch cfg.Kind {
	case "ssh":
		return func(line string) []string {
			return with(c.SSH, "-T", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", cfg.Target, line)
		}, nil
	case "docker":
		return func(line string) []string { return with(c.Docker, "exec", "-i", cfg.Target, "sh", "-c", line) }, nil
	case "wsl":
		return func(line string) []string { return with(c.WSL, "-d", cfg.Target, "--exec", "sh", "-c", line) }, nil
	}
	return nil, fmt.Errorf("unknown kind of environment %q", cfg.Kind)
}

// Targets lists what an environment of a kind can be added for: the hosts
// in ~/.ssh/config, the running containers, the WSL distros.
func (c Connectors) Targets(kind string) []string {
	var out []byte
	switch kind {
	case "ssh":
		home, _ := os.UserHomeDir()
		out, _ = os.ReadFile(filepath.Join(home, ".ssh", "config"))
		var hosts []string
		for line := range strings.Lines(string(out)) {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.EqualFold(f[0], "host") {
				continue
			}
			for _, h := range f[1:] {
				if !strings.ContainsAny(h, "*?!") {
					hosts = append(hosts, h)
				}
			}
		}
		return hosts
	case "docker":
		out, _ = sysproc.Hide(exec.Command(c.Docker[0], append(c.Docker[1:], "ps", "--format", "{{.Names}}")...)).Output()
	case "wsl":
		out, _ = sysproc.Hide(exec.Command(c.WSL[0], append(c.WSL[1:], "-l", "-q")...)).Output()
		// wsl.exe prints UTF-16.
		out = bytes.ReplaceAll(bytes.TrimPrefix(out, []byte{0xff, 0xfe}), []byte{0}, nil)
	}
	var names []string
	for _, n := range strings.Fields(string(out)) {
		if !strings.HasPrefix(n, "docker-desktop") {
			names = append(names, n)
		}
	}
	return names
}

func run(argv []string, stdin io.Reader) ([]byte, error) {
	cmd := sysproc.Hide(exec.Command(argv[0], argv[1:]...))
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, errors.New(msg)
		}
	}
	return out, err
}

// Prepare installs the backend in an environment if the right build isn't
// there yet, under ~/.claude-mux/bin/<version>/, and returns the command that
// runs it.
func (c Connectors) Prepare(cfg EnvConfig, b Backends) ([]string, error) {
	shell, err := c.shell(cfg)
	if err != nil {
		return nil, err
	}
	uname, err := run(shell("uname -sm"), nil)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", cfg.Kind, cfg.Target, err)
	}
	p, err := platform(string(uname))
	if err != nil {
		return nil, err
	}
	file, ver, err := b.Build(p)
	if err != nil {
		return nil, err
	}
	dir := "$HOME/.claude-mux/bin/" + ver
	bin := dir + "/claude-mux-backend"
	if _, err := run(shell("test -x "+bin), nil); err != nil {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		// Install, then remove the versions nothing uses anymore.
		install := fmt.Sprintf("mkdir -p %s && cat > %s.tmp && chmod +x %s.tmp && mv %s.tmp %s && "+
			"find $HOME/.claude-mux/bin -mindepth 1 -maxdepth 1 ! -name %s -exec rm -rf {} +", dir, bin, bin, bin, bin, ver)
		if _, err := run(shell(install), f); err != nil {
			return nil, fmt.Errorf("installing the backend in %s: %w", cfg.Target, err)
		}
	}
	return shell("exec " + bin), nil
}
