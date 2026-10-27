package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const releases = "https://github.com/askolesov/claude-mux/releases/download/"

// Backends finds the backend build the hub installs in an environment, one
// per platform: claude-mux-backend-<os>-<arch>. A released hub downloads it
// from the GitHub release of its own version, so the two always speak the
// same protocol; a development hub takes it from a directory of builds
// (scripts/restart.sh makes them).
type Backends struct {
	Dir     string // development builds; "" to download from the release
	Version string // the hub's version, a release tag
	Cache   string // downloads, by version
}

// Build is the build for a platform ("linux-arm64") and its version. step
// says what it is doing, for the page.
func (b Backends) Build(platform string, step func(string)) (path, ver string, err error) {
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
	if err := download(b.Version, name, path, func(done, total int64) {
		step(fmt.Sprintf("downloading the backend %s", progress(done, total)))
	}); err != nil {
		return "", "", fmt.Errorf("downloading the %s backend: %w", platform, err)
	}
	prune(b.Cache, b.Version)
	return path, b.Version, nil
}

// download fetches a release file and checks it against the release's
// checksums.txt before putting it at dst, telling onProgress as it goes.
func download(ver, name, dst string, onProgress func(done, total int64)) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	var total int64
	get := func(file string) (io.ReadCloser, error) {
		resp, err := client.Get(releases + ver + "/" + file)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%s: %s", resp.Request.URL, resp.Status)
		}
		total = resp.ContentLength
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
	_, err = io.Copy(io.MultiWriter(f, h), &counting{r: body, total: total, onProgress: onProgress})
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

// counting reports how much of a transfer has been read, every megabyte.
type counting struct {
	r          io.Reader
	done, last int64
	total      int64 // -1 if unknown
	onProgress func(done, total int64)
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.done-c.last >= 1<<20 || (err == io.EOF && c.done != c.last) {
		c.last = c.done
		c.onProgress(c.done, c.total)
	}
	return n, err
}

// progress is "12 / 22 MB", or "12 MB" when the size is unknown.
func progress(done, total int64) string {
	if total <= 0 {
		return fmt.Sprintf("%d MB", done>>20)
	}
	return fmt.Sprintf("%d / %d MB", done>>20, total>>20)
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
