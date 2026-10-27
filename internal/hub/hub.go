// Package hub serves the claude-mux page and routes it to the environments'
// backends, each a child process reached over its stdin and stdout.
package hub

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed web
var webFS embed.FS

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

// Hub runs the environments: local, always first, and the ones added in the
// page, kept in environments.json.
type Hub struct {
	stateDir   string
	file       string
	version    string
	backends   Backends
	connectors Connectors
	defaultCwd string

	mu   sync.Mutex
	envs []*Env
	subs map[chan struct{}]struct{}
}

func New(stateDir string, backends Backends, connectors Connectors, defaultCwd string) (*Hub, error) {
	h := &Hub{stateDir: stateDir, file: filepath.Join(stateDir, "environments.json"), version: backends.Version,
		backends: backends, connectors: connectors, defaultCwd: defaultCwd, subs: map[chan struct{}]struct{}{}}
	var saved []EnvConfig
	data, err := os.ReadFile(h.file)
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", h.file, err)
	}
	h.envs = append(h.envs, h.newEnv(EnvConfig{Name: "local", Kind: "local"}))
	for _, cfg := range saved {
		h.envs = append(h.envs, h.newEnv(cfg))
	}
	return h, nil
}

func (h *Hub) newEnv(cfg EnvConfig) *Env {
	prepare := func() ([]string, error) {
		if cfg.Kind != "local" {
			return h.connectors.Prepare(cfg, h.backends)
		}
		// Installed like any other environment's; the local backend shares
		// the hub's state directory.
		src, ver, err := h.backends.Build(runtime.GOOS + "-" + runtime.GOARCH)
		if err != nil {
			return nil, err
		}
		bin, err := installLocal(src, filepath.Join(h.stateDir, "bin"), ver)
		return []string{bin, "-state", h.stateDir, "-cwd", h.defaultCwd}, err
	}
	return NewEnv(cfg, prepare, h.notify)
}

// Start serves the page on addr and starts the environments' backends. The
// returned stop closes the backends, which hang up their tabs and resume them
// on the next start, then the server.
func (h *Hub) Start(addr string) (stop func(), err error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("refusing to listen on %s: the page runs commands, keep it on loopback", addr)
	}
	// The port first: a second hub must not start the same tabs again.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	for _, e := range h.list() {
		go e.Supervise()
	}
	srv := &http.Server{Handler: h.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "claude-mux:", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "claude-mux %s on http://%s\n", h.version, addr)

	return func() {
		var wg sync.WaitGroup
		for _, e := range h.list() {
			wg.Add(1)
			go func() { defer wg.Done(); e.Close() }()
		}
		wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}, nil
}

func (h *Hub) list() []*Env {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*Env(nil), h.envs...)
}

func (h *Hub) env(name string) *Env {
	for _, e := range h.list() {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Add adds an environment of a kind (ssh, docker, wsl) for a target (a
// host, a container, a distro) and connects it.
func (h *Hub) Add(name, kind, target string) error {
	target = strings.TrimSpace(target)
	if name = strings.TrimSpace(name); name == "" {
		name = target
	}
	if !slices.Contains(h.connectors.Kinds(), kind) {
		return fmt.Errorf("this machine can't add a %s environment", kind)
	}
	if target == "" || strings.HasPrefix(target, "-") || strings.ContainsAny(target, " \t\n") {
		return errors.New("give the host as ssh takes it (host, user@host, an alias from ~/.ssh/config), the container or the distro")
	}
	if !validName.MatchString(name) {
		return errors.New("a name is up to 40 letters, digits, dots, dashes and underscores")
	}
	if h.env(name) != nil {
		return fmt.Errorf("there is already an environment named %s", name)
	}
	e := h.newEnv(EnvConfig{Name: name, Kind: kind, Target: target})
	h.mu.Lock()
	h.envs = append(h.envs, e)
	h.mu.Unlock()
	go e.Supervise()
	h.save()
	h.notify()
	return nil
}

// Remove disconnects an environment and forgets it. Its backend hangs up
// its tabs and keeps them: adding the environment again brings them back.
func (h *Hub) Remove(name string) error {
	e := h.env(name)
	if e == nil {
		return errors.New("no such environment")
	}
	if e.Kind == "local" {
		return errors.New("the local environment stays")
	}
	h.mu.Lock()
	h.envs = slices.DeleteFunc(h.envs, func(x *Env) bool { return x == e })
	h.mu.Unlock()
	go e.Close()
	h.save()
	h.notify()
	return nil
}

func (h *Hub) save() {
	saved := []EnvConfig{}
	for _, e := range h.list() {
		if e.Kind != "local" {
			saved = append(saved, e.EnvConfig)
		}
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err == nil {
		err = os.WriteFile(h.file, data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "save environments:", err)
	}
}

func (h *Hub) notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) views() []EnvView {
	envs := h.list()
	views := make([]EnvView, 0, len(envs))
	for _, e := range envs {
		views = append(views, e.View())
	}
	return views
}

// Handler serves the page at /, the hub's own API under /hub/, and each
// environment's backend under /env/<name>/, with the prefix cut.
func (h *Hub) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), localOnly)

	web, _ := fs.Sub(webFS, "web")
	r.GET("/", func(c *gin.Context) { c.FileFromFS("/", http.FS(web)) })

	r.GET("/hub/events", h.events)
	r.GET("/hub/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": h.version, "kinds": h.connectors.Kinds()})
	})
	r.GET("/hub/targets/:kind", func(c *gin.Context) { c.JSON(http.StatusOK, h.connectors.Targets(c.Param("kind"))) })
	r.POST("/hub/environments", func(c *gin.Context) {
		var req struct {
			Name   string `json:"name"`
			Kind   string `json:"kind" binding:"required"`
			Target string `json:"target" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respond(c, h.Add(req.Name, req.Kind, req.Target))
	})
	r.DELETE("/hub/environments/:name", func(c *gin.Context) { respond(c, h.Remove(c.Param("name"))) })

	r.Any("/env/:name/*path", func(c *gin.Context) {
		e := h.env(c.Param("name"))
		if e == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such environment"})
			return
		}
		c.Request.URL.Path = c.Param("path")
		c.Request.URL.RawPath = ""
		e.ServeHTTP(c.Writer, c.Request)
	})
	return r
}

func respond(c *gin.Context, err error) {
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// events streams the environments on every change, as server-sent events.
func (h *Hub) events(c *gin.Context) {
	sub := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, sub)
		h.mu.Unlock()
	}()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		data, _ := json.Marshal(h.views())
		if _, err := c.Writer.Write(append(append([]byte("data: "), data...), '\n', '\n')); err != nil {
			return
		}
		c.Writer.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-sub:
		case <-ping.C:
		}
	}
}

// localOnly turns away requests that didn't come from this machine's own
// page: a foreign Host (DNS rebinding), a foreign Origin, or a cross-site
// write sent as a form instead of JSON. Anything that reaches a backend can
// start processes.
func localOnly(c *gin.Context) {
	host, _, err := net.SplitHostPort(c.Request.Host)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if o := c.GetHeader("Origin"); o != "" && o != "http://"+c.Request.Host {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.ContentLength > 0 &&
		!strings.HasPrefix(c.ContentType(), "application/json") {
		c.AbortWithStatus(http.StatusUnsupportedMediaType)
		return
	}
	c.Next()
}
