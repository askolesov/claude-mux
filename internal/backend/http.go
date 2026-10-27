package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// The default CheckOrigin rejects cross-origin upgrades, so another site
// open in the browser can't reach a terminal.
var upgrader = websocket.Upgrader{}

type Handler struct {
	m          *Manager
	defaultCwd string
}

// NewHandler serves the backend's API. Only the hub reaches it, over the
// backend's stdio, so it trusts every request; the hub checks them.
func NewHandler(m *Manager, defaultCwd string) http.Handler {
	h := &Handler{m: m, defaultCwd: defaultCwd}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	api.GET("/state", h.state)
	api.GET("/events", h.events)
	api.POST("/sessions", h.create)
	api.PUT("/sessions/order", h.reorder)
	api.PUT("/sessions/:id", h.configure)
	api.PUT("/sessions/:id/group", h.setGroup)
	api.PUT("/resume-default", h.setResumeDefault)
	api.DELETE("/sessions/:id", h.close)
	api.POST("/sessions/:id/start", h.start)
	api.POST("/sessions/:id/restart", h.restart)
	api.POST("/sessions/:id/seen", h.seen)
	api.GET("/sessions/:id/term", h.term)
	api.POST("/groups/rename", h.renameGroup)
	api.POST("/claude/check", h.checkClaude)
	api.POST("/claude/install", h.installClaude)
	return r
}

// NewHookHandler serves the hook port: claude's hooks post here, through
// `claude-mux-backend hook`. The token, new on every start, keeps other users
// of a shared host from posting statuses.
func NewHookHandler(m *Manager, token string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/hook/:token/:id", func(c *gin.Context) {
		if c.Param("token") != token {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		var p HookPayload
		if err := c.ShouldBindJSON(&p); err != nil {
			respond(c, nil, err)
			return
		}
		respond(c, nil, m.ApplyHook(c.Param("id"), p))
	})
	return r
}

// respond writes v, or maps err to its status.
func respond(c *gin.Context, v any, err error) {
	switch {
	case errors.Is(err, errNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case v == nil:
		c.Status(http.StatusNoContent)
	default:
		c.JSON(http.StatusOK, v)
	}
}

type StateView struct {
	Claude     Claude        `json:"claude"`
	Sessions   []SessionView `json:"sessions"`
	DefaultCwd string        `json:"defaultCwd"`
}

func (h *Handler) snapshot() StateView {
	h.m.mu.Lock()
	claude := h.m.claude
	h.m.mu.Unlock()
	return StateView{Claude: claude, Sessions: h.m.List(), DefaultCwd: h.defaultCwd}
}

func (h *Handler) state(c *gin.Context) { c.JSON(http.StatusOK, h.snapshot()) }

// events streams the whole state on every change, as server-sent events.
func (h *Handler) events(c *gin.Context) {
	sub := h.m.Subscribe()
	defer h.m.Unsubscribe(sub)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		data, _ := json.Marshal(h.snapshot())
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

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func (h *Handler) create(c *gin.Context) {
	var req struct {
		Name          string `json:"name"`
		Group         string `json:"group"`
		Cwd           string `json:"cwd"`
		Args          string `json:"args"`
		Resume        string `json:"resume"`
		ResumeMessage string `json:"resumeMessage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	cwd := expandHome(strings.TrimSpace(req.Cwd))
	if cwd == "" {
		cwd = h.defaultCwd
	}
	s, err := h.m.Create(strings.TrimSpace(req.Name), strings.TrimSpace(req.Group), cwd, req.Resume, req.ResumeMessage, strings.Fields(req.Args))
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, s.View(), nil)
}

func (h *Handler) reorder(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	h.m.Reorder(req.IDs)
	respond(c, nil, nil)
}

// configure takes the tab's settings as Tab settings saves them.
func (h *Handler) configure(c *gin.Context) {
	var req struct {
		Group         string `json:"group"`
		Cwd           string `json:"cwd" binding:"required"`
		Args          string `json:"args"`
		Resume        string `json:"resume"`
		ResumeMessage string `json:"resumeMessage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.Configure(c.Param("id"), req.Group, expandHome(strings.TrimSpace(req.Cwd)), req.Resume, req.ResumeMessage, strings.Fields(req.Args)))
}

// setResumeDefault takes the message tabs get when they start again, which
// the hub sends when it changes.
func (h *Handler) setResumeDefault(c *gin.Context) {
	var d ResumeDefault
	if err := c.ShouldBindJSON(&d); err != nil {
		respond(c, nil, err)
		return
	}
	h.m.SetResumeDefault(d)
	respond(c, nil, nil)
}

func (h *Handler) setGroup(c *gin.Context) {
	var req struct {
		Group string `json:"group"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.SetGroup(c.Param("id"), req.Group))
}

func (h *Handler) renameGroup(c *gin.Context) {
	var req struct {
		From string `json:"from" binding:"required"`
		To   string `json:"to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.RenameGroup(req.From, req.To))
}

func (h *Handler) checkClaude(c *gin.Context) { respond(c, h.m.CheckClaude(), nil) }

// installClaude runs the official installer; it takes a while, and the page
// shows its output.
// installClaude streams the installer's output as it comes, a JSON line per
// chunk ({"out": …}), and ends with {"done": true} or {"error": …}. The
// install goes on if the page goes away.
func (h *Handler) installClaude(c *gin.Context) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)
	out := &lines{enc: json.NewEncoder(c.Writer), flush: c.Writer.Flush}
	if err := h.m.InstallClaude(context.WithoutCancel(c.Request.Context()), out); err != nil {
		out.send(gin.H{"error": err.Error()})
		return
	}
	out.send(gin.H{"done": true})
}

// lines writes what it is given as JSON lines, flushing each.
type lines struct {
	mu    sync.Mutex
	enc   *json.Encoder
	flush func()
}

func (l *lines) send(v any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(v)
	l.flush()
}

func (l *lines) Write(p []byte) (int, error) {
	l.send(gin.H{"out": string(p)})
	return len(p), nil
}

func (h *Handler) close(c *gin.Context)   { respond(c, nil, h.m.Close(c.Param("id"))) }
func (h *Handler) start(c *gin.Context)   { respond(c, nil, h.m.Start(c.Param("id"))) }
func (h *Handler) restart(c *gin.Context) { respond(c, nil, h.m.Restart(c.Param("id"))) }
func (h *Handler) seen(c *gin.Context)    { respond(c, nil, h.m.MarkSeen(c.Param("id"))) }

// term bridges a browser terminal to a session's pty. The browser sends
// JSON text frames ({"t":"in","d":…} or {"t":"resize","c":…,"r":…}); the
// server sends the pty's raw output as binary frames.
func (h *Handler) term(c *gin.Context) {
	s, err := h.m.Get(c.Param("id"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()

	backlog, out := s.attach()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if ws.WriteMessage(websocket.BinaryMessage, backlog) != nil {
			return
		}
		for p := range out {
			if ws.WriteMessage(websocket.BinaryMessage, p) != nil {
				return
			}
		}
	}()

	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var msg struct {
			T string `json:"t"`
			D string `json:"d"`
			C uint16 `json:"c"`
			R uint16 `json:"r"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.T {
		case "in":
			s.write([]byte(msg.D))
		case "resize":
			s.resize(msg.C, msg.R)
		}
	}
	s.detach(out)
	close(out)
	<-done
}
