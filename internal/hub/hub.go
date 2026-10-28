// Package hub serves the claude-mux page and routes it to the environments'
// backends, each a child process reached over its stdin and stdout.
package hub

import (
	"context"
	"embed"
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

	"github.com/askolesov/claude-mux/internal/stream"
	"github.com/gin-gonic/gin"
)

//go:embed web
var webFS embed.FS

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

var errNoEnv = errors.New("no such environment")

// Hub runs the environments: local, which is always there, and the ones
// added in the page, in the order the page shows them, kept in hub.json.
type Hub struct {
	stateDir   string
	st         *store
	version    string
	backends   Backends
	connectors Connectors
	defaultCwd string
	updates    *updater

	changes stream.Notifier // wakes the page's event stream

	// NewWindow opens another window onto the hub, as a process of its own
	// (POST /hub/windows); nil where windows aren't processes.
	NewWindow func() error

	mu   sync.Mutex
	envs []*Env
}

// New loads hub.json from stateDir; Start then runs it.
func New(stateDir string, backends Backends, connectors Connectors, defaultCwd string) (*Hub, error) {
	st, err := openStore(stateDir)
	if err != nil {
		return nil, err
	}
	h := &Hub{stateDir: stateDir, st: st, version: backends.Version,
		backends: backends, connectors: connectors, defaultCwd: defaultCwd}
	h.updates = newUpdater(st, stateDir, backends.Version, h.changes.Notify)
	for _, cfg := range st.get().Environments {
		h.envs = append(h.envs, h.newEnv(cfg))
	}
	return h, nil
}

func (h *Hub) newEnv(cfg EnvConfig) *Env {
	prepare := func(step func(string)) ([]string, string, error) {
		args := []string{"-resume", h.st.get().ResumeDefault.Flag()}
		if cfg.Kind != KindLocal {
			return h.connectors.Prepare(cfg, h.backends, args, step)
		}
		// Installed like any other environment's; the local backend shares
		// the hub's state directory.
		plat := runtime.GOOS + "-" + runtime.GOARCH
		src, ver, err := h.backends.Build(plat, step)
		if err != nil {
			return nil, "", err
		}
		bin, err := installLocal(src, filepath.Join(h.stateDir, "bin"), ver)
		return append([]string{bin, "-state", h.stateDir, "-cwd", h.defaultCwd}, args...), plat, err
	}
	return newEnv(cfg, prepare, h.changes.Notify)
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
	go h.updates.loop()
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

// Restart is closed once an update is installed: the hub should stop, and
// Relaunch start the new one.
func (h *Hub) Restart() <-chan struct{} { return h.updates.restart }

// Relaunch is the command that starts the installed update, or nil.
func (h *Hub) Relaunch() []string { return h.updates.Relaunch() }

// SetSettings saves the settings, the page's own among them.
func (h *Hub) SetSettings(s Settings) error {
	if err := s.validate(); err != nil {
		return err
	}
	h.st.change(func(st *hubState) { st.Settings = s })
	h.changes.Notify()
	return nil
}

func (h *Hub) list() []*Env {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*Env(nil), h.envs...)
}

func (h *Hub) env(name string) *Env {
	for _, e := range h.list() {
		if e.Config().Name == name {
			return e
		}
	}
	return nil
}

// Add adds an environment of a kind (ssh, docker, wsl) for a target (a
// host, a container, a distro) and connects it.
func (h *Hub) Add(name string, kind Kind, target string) error {
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
	if err := h.checkName(name); err != nil {
		return err
	}
	e := h.newEnv(EnvConfig{Name: name, Kind: kind, Target: target})
	h.mu.Lock()
	h.envs = append(h.envs, e)
	h.mu.Unlock()
	go e.Supervise()
	h.save()
	h.changes.Notify()
	return nil
}

func (h *Hub) checkName(name string) error {
	if !validName.MatchString(name) {
		return errors.New("a name is up to 40 letters, digits, dots, dashes and underscores")
	}
	if h.env(name) != nil {
		return fmt.Errorf("there is already an environment named %s", name)
	}
	return nil
}

// Rename renames an environment; local keeps its name. Its backend goes on
// running: only the hub and the page know environments by name.
func (h *Hub) Rename(name, to string) error {
	e := h.env(name)
	switch {
	case e == nil:
		return errNoEnv
	case e.Config().Kind == KindLocal:
		return errors.New("the local environment keeps its name")
	case to == name:
		return nil
	}
	if err := h.checkName(to); err != nil {
		return err
	}
	e.rename(to)
	h.save()
	h.changes.Notify()
	return nil
}

// Remove disconnects an environment and forgets it. Its backend hangs up
// its tabs and keeps them: adding the environment again brings them back.
func (h *Hub) Remove(name string) error {
	e := h.env(name)
	if e == nil {
		return errNoEnv
	}
	if e.Config().Kind == KindLocal {
		return errors.New("the local environment stays")
	}
	h.mu.Lock()
	h.envs = slices.DeleteFunc(h.envs, func(x *Env) bool { return x == e })
	h.mu.Unlock()
	go e.Close()
	h.save()
	h.changes.Notify()
	return nil
}

// Reconnect starts an environment's backend again.
func (h *Hub) Reconnect(name string) error {
	e := h.env(name)
	if e == nil {
		return errNoEnv
	}
	e.Reconnect()
	return nil
}

func (h *Hub) save() {
	var envs []EnvConfig
	for _, e := range h.list() {
		envs = append(envs, e.Config())
	}
	h.st.change(func(st *hubState) { st.Environments = envs })
}

// Reorder puts the environments in the order of names, which lists each of
// them once.
func (h *Hub) Reorder(names []string) error {
	h.mu.Lock()
	byName := map[string]*Env{}
	for _, e := range h.envs {
		byName[e.Config().Name] = e
	}
	var envs []*Env
	for _, n := range names {
		if e := byName[n]; e != nil {
			envs = append(envs, e)
			delete(byName, n)
		}
	}
	if len(byName) > 0 || len(envs) != len(h.envs) {
		h.mu.Unlock()
		return errors.New("give every environment once")
	}
	h.envs = envs
	h.mu.Unlock()
	h.save()
	h.changes.Notify()
	return nil
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
	vendor, _ := fs.Sub(web, "vendor")
	r.GET("/", func(c *gin.Context) { c.FileFromFS("/", http.FS(web)) })
	r.StaticFileFS("/app.js", "app.js", http.FS(web))
	r.StaticFileFS("/style.css", "style.css", http.FS(web))
	r.StaticFS("/vendor", http.FS(vendor))

	r.GET("/hub/events", h.events)
	r.GET("/hub/info", func(c *gin.Context) {
		respond(c, gin.H{"version": h.version, "kinds": h.connectors.Kinds(), "stateDir": h.stateDir}, nil)
	})
	r.POST("/hub/open-log", func(c *gin.Context) { respond(c, nil, h.OpenLog()) })
	r.POST("/hub/windows", func(c *gin.Context) {
		if h.NewWindow == nil {
			respond(c, nil, errors.New("this hub opens no windows of its own"))
			return
		}
		respond(c, nil, h.NewWindow())
	})
	r.POST("/hub/pick-folder", func(c *gin.Context) {
		var req struct {
			Start string `json:"start"`
		}
		if !bind(c, &req) {
			return
		}
		path, err := pickFolder(c.Request.Context(), req.Start)
		respond(c, gin.H{"path": path}, err)
	})
	r.GET("/hub/targets/:kind", func(c *gin.Context) { respond(c, h.connectors.Targets(Kind(c.Param("kind"))), nil) })
	r.POST("/hub/environments", func(c *gin.Context) {
		var req struct {
			Name   string `json:"name"`
			Kind   Kind   `json:"kind" binding:"required"`
			Target string `json:"target" binding:"required"`
		}
		if bind(c, &req) {
			respond(c, nil, h.Add(req.Name, req.Kind, req.Target))
		}
	})
	r.PUT("/hub/environments/order", func(c *gin.Context) {
		var req struct {
			Names []string `json:"names" binding:"required"`
		}
		if bind(c, &req) {
			respond(c, nil, h.Reorder(req.Names))
		}
	})
	r.POST("/hub/environments/:name/reconnect", func(c *gin.Context) { respond(c, nil, h.Reconnect(c.Param("name"))) })
	r.PUT("/hub/environments/:name", func(c *gin.Context) {
		var req struct {
			Name string `json:"name" binding:"required"`
		}
		if bind(c, &req) {
			respond(c, nil, h.Rename(c.Param("name"), strings.TrimSpace(req.Name)))
		}
	})
	r.DELETE("/hub/environments/:name", func(c *gin.Context) { respond(c, nil, h.Remove(c.Param("name"))) })
	r.PUT("/hub/settings", func(c *gin.Context) {
		var s Settings
		if bind(c, &s) {
			respond(c, nil, h.SetSettings(s))
		}
	})
	r.POST("/hub/update/check", func(c *gin.Context) { respond(c, nil, h.updates.Check(c.Request.Context())) })
	r.POST("/hub/update/apply", h.applyUpdate)

	r.Any("/env/:name/*path", func(c *gin.Context) {
		e := h.env(c.Param("name"))
		if e == nil {
			respond(c, nil, errNoEnv)
			return
		}
		c.Request.URL.Path = c.Param("path")
		c.Request.URL.RawPath = ""
		e.ServeHTTP(c.Writer, c.Request)
	})
	return r
}

// bind reads the request's JSON body into v, or answers 400 and says so.
func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		respond(c, nil, err)
		return false
	}
	return true
}

// respond writes v, or maps err to its status, as the backend's API does.
func respond(c *gin.Context, v any, err error) {
	switch {
	case errors.Is(err, errNoEnv):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case v == nil:
		c.Status(http.StatusNoContent)
	default:
		c.JSON(http.StatusOK, v)
	}
}

// applyUpdate installs the newest release, streaming its progress
// ({"step": …}), and then restarts the hub into it. The update goes on if
// the page goes away.
func (h *Hub) applyUpdate(c *gin.Context) {
	out := stream.NewLines(c)
	out.End(h.updates.Apply(func(step string) { out.Send(gin.H{"step": step}) }))
}

// events streams the environments, the update state and the settings on
// every change.
func (h *Hub) events(c *gin.Context) {
	stream.Events(c, &h.changes, func() any {
		return gin.H{"version": h.version, "platform": runtime.GOOS + "-" + runtime.GOARCH,
			"envs": h.views(), "update": h.updates.View(), "settings": h.st.get().Settings}
	})
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
