package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Backends holds the backend builds the hub installs, one per platform:
// claude-mux-backend-<os>-<arch>. For now they come from a local directory
// (restart.sh builds them); releases come later.
type Backends struct {
	Dir string
}

// File is the build for a platform ("linux-arm64").
func (b Backends) File(platform string) (string, error) {
	path := filepath.Join(b.Dir, "claude-mux-backend-"+platform)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no backend build for %s in %s", platform, b.Dir)
	}
	return path, nil
}

// Local is the build for the hub's own machine.
func (b Backends) Local() (string, error) { return b.File(runtime.GOOS + "-" + runtime.GOARCH) }

// version names a build by its content, so a changed build is installed
// again and an unchanged one never is.
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

// SSH reaches hosts with the user's ssh: keys and ~/.ssh/config as they are.
// BatchMode makes a host that wants a password fail instead of waiting for
// one; the keepalives notice a dead connection.
type SSH struct {
	Command []string // "ssh", or a replacement such as "ssh -F <config>"
}

func (s SSH) argv(host string, remote ...string) []string {
	argv := append([]string{}, s.Command...)
	argv = append(argv, "-T", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", host)
	return append(argv, remote...)
}

func (s SSH) run(host string, stdin io.Reader, remote string) ([]byte, error) {
	argv := s.argv(host, remote)
	cmd := exec.Command(argv[0], argv[1:]...)
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

// Prepare installs the backend on host if the right build isn't there yet,
// under ~/.claude-mux/bin/<version>/, and returns the command that runs it.
func (s SSH) Prepare(host string, b Backends) ([]string, error) {
	uname, err := s.run(host, nil, "uname -sm")
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", host, err)
	}
	p, err := platform(string(uname))
	if err != nil {
		return nil, err
	}
	file, err := b.File(p)
	if err != nil {
		return nil, err
	}
	ver, err := version(file)
	if err != nil {
		return nil, err
	}
	dir := "$HOME/.claude-mux/bin/" + ver
	bin := dir + "/claude-mux-backend"
	if _, err := s.run(host, nil, "test -x "+bin); err != nil {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		install := fmt.Sprintf("mkdir -p %s && cat > %s.tmp && chmod +x %s.tmp && mv %s.tmp %s", dir, bin, bin, bin, bin)
		if _, err := s.run(host, f, install); err != nil {
			return nil, fmt.Errorf("installing the backend on %s: %w", host, err)
		}
	}
	return s.argv(host, "exec "+bin), nil
}
