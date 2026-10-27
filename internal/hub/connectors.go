package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

// Connectors are the commands that reach environments, the user's own:
// ssh with its keys and ~/.ssh/config, docker, wsl.exe.
type Connectors struct {
	SSH    []string // "ssh", or a replacement such as "ssh -F <config>"
	Docker []string
	WSL    []string
}

// Kinds are the kinds of environment this machine can add.
func (c Connectors) Kinds() []Kind {
	var kinds []Kind
	if found(c.SSH) {
		kinds = append(kinds, KindSSH)
	}
	if found(c.Docker) {
		kinds = append(kinds, KindDocker)
	}
	if found(c.WSL) && runtime.GOOS == "windows" {
		kinds = append(kinds, KindWSL)
	}
	return kinds
}

// found says whether a command is there to run.
func found(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	_, err := exec.LookPath(argv[0])
	return err == nil
}

// shell is the command that runs a sh command line in an environment.
// BatchMode makes a host that wants a password fail instead of waiting for
// one; the keepalives notice a dead connection.
func (c Connectors) shell(cfg EnvConfig) (func(line string) []string, error) {
	with := func(base []string, args ...string) []string { return append(append([]string{}, base...), args...) }
	switch cfg.Kind {
	case KindSSH:
		return func(line string) []string {
			return with(c.SSH, "-T", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", cfg.Target, line)
		}, nil
	case KindDocker:
		return func(line string) []string { return with(c.Docker, "exec", "-i", cfg.Target, "sh", "-c", line) }, nil
	case KindWSL:
		return func(line string) []string { return with(c.WSL, "-d", cfg.Target, "--exec", "sh", "-c", line) }, nil
	}
	return nil, fmt.Errorf("unknown kind of environment %q", cfg.Kind)
}

// Targets lists what an environment of a kind can be added for: the hosts
// in ~/.ssh/config, the running containers, the WSL distros.
func (c Connectors) Targets(kind Kind) []string {
	if !slices.Contains(c.Kinds(), kind) {
		return nil
	}
	var out []byte
	switch kind {
	case KindSSH:
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
	case KindDocker:
		out, _ = sysproc.Hide(exec.Command(c.Docker[0], append(c.Docker[1:], "ps", "--format", "{{.Names}}")...)).Output()
	case KindWSL:
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
// runs it, with args (which must need no quoting), and the environment's
// platform. step says what it is doing, for the page.
func (c Connectors) Prepare(cfg EnvConfig, b Backends, args []string, step func(string)) (argv []string, plat string, err error) {
	shell, err := c.shell(cfg)
	if err != nil {
		return nil, "", err
	}
	step("checking the platform")
	uname, err := run(shell("uname -sm"), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%s %s: %w", cfg.Kind, cfg.Target, err)
	}
	p, err := platform(string(uname))
	if err != nil {
		return nil, "", err
	}
	file, ver, err := b.Build(p, step)
	if err != nil {
		return nil, "", err
	}
	dir := "$HOME/.claude-mux/bin/" + ver
	bin := dir + "/claude-mux-backend"
	if _, err := run(shell("test -x "+bin), nil); err != nil {
		f, err := os.Open(file)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = f.Close() }()
		var size int64
		if st, err := f.Stat(); err == nil {
			size = st.Size()
		}
		copying := &counting{r: f, total: size, onProgress: func(done, total int64) {
			step(fmt.Sprintf("copying the backend %s", progress(done, total)))
		}}
		// Install, then remove the versions nothing uses anymore.
		install := fmt.Sprintf("mkdir -p %s && cat > %s.tmp && chmod +x %s.tmp && mv %s.tmp %s && "+
			"find $HOME/.claude-mux/bin -mindepth 1 -maxdepth 1 ! -name %s -exec rm -rf {} +", dir, bin, bin, bin, bin, ver)
		if _, err := run(shell(install), copying); err != nil {
			return nil, "", fmt.Errorf("installing the backend in %s: %w", cfg.Target, err)
		}
	}
	return shell(strings.Join(append([]string{"exec", bin}, args...), " ")), p, nil
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
