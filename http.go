package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed web
var webFS embed.FS

// The default CheckOrigin rejects cross-origin upgrades, so another site
// open in the browser can't reach a terminal.
var upgrader = websocket.Upgrader{}

type Handler struct {
	m          *Manager
	ext        *Externals
	defaultCwd string
}

func NewHandler(m *Manager, ext *Externals, defaultCwd string) http.Handler {
	h := &Handler{m: m, ext: ext, defaultCwd: defaultCwd}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), localOnly)

	web, _ := fs.Sub(webFS, "web")
	r.GET("/", func(c *gin.Context) { c.FileFromFS("/", http.FS(web)) })

	api := r.Group("/api")
	api.GET("/state", h.state)
	api.GET("/events", h.events)
	api.POST("/sessions", h.create)
	api.PATCH("/sessions/:id", h.rename)
	api.DELETE("/sessions/:id", h.remove)
	api.POST("/sessions/:id/kill", h.kill)
	api.POST("/sessions/:id/resume", h.resume)
	api.POST("/sessions/:id/handoff", h.handoff)
	api.POST("/sessions/:id/seen", h.seen)
	api.GET("/sessions/:id/term", h.term)
	api.POST("/external/:sid/takeover", h.takeover)
	api.POST("/external/:sid/send", h.send)
	api.GET("/transcript/:sid", h.transcript)

	r.POST("/hook/:id", h.hook)
	return r
}

// localOnly turns away requests that didn't come from this machine's own
// page: a foreign Host (DNS rebinding), a foreign Origin, or a cross-site
// write sent as a form instead of JSON. Anything that reaches the API can
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

type State struct {
	Sessions   []SessionView `json:"sessions"`
	External   []External    `json:"external"`
	DefaultCwd string        `json:"defaultCwd"`
}

func (h *Handler) snapshot() State {
	return State{Sessions: h.m.List(), External: h.ext.List(), DefaultCwd: h.defaultCwd}
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
		Name string `json:"name"`
		Cwd  string `json:"cwd"`
		Args string `json:"args"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	cwd := expandHome(strings.TrimSpace(req.Cwd))
	if cwd == "" {
		cwd = h.defaultCwd
	}
	s, err := h.m.Create(strings.TrimSpace(req.Name), cwd, strings.Fields(req.Args))
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, s.View(), nil)
}

func (h *Handler) rename(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.Rename(c.Param("id"), strings.TrimSpace(req.Name)))
}

func (h *Handler) remove(c *gin.Context)  { respond(c, nil, h.m.Remove(c.Param("id"))) }
func (h *Handler) kill(c *gin.Context)    { respond(c, nil, h.m.Kill(c.Param("id"))) }
func (h *Handler) handoff(c *gin.Context) { respond(c, nil, h.m.Handoff(c.Param("id"))) }
func (h *Handler) resume(c *gin.Context)  { respond(c, nil, h.m.Resume(c.Param("id"))) }
func (h *Handler) seen(c *gin.Context)    { respond(c, nil, h.m.MarkSeen(c.Param("id"))) }

func (h *Handler) takeover(c *gin.Context) {
	sid := c.Param("sid")
	for _, x := range h.ext.List() {
		if x.SessionID == sid {
			respond(c, nil, errors.New("it is still running elsewhere: quit it there first (/exit)"))
			return
		}
	}
	var req struct {
		Name string `json:"name"`
		Cwd  string `json:"cwd" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	s, err := h.m.Takeover(sid, req.Name, req.Cwd)
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, s.View(), nil)
}

// send types a message into the terminal window of a session claude-mux
// doesn't own.
func (h *Handler) send(c *gin.Context) {
	var req struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	for _, x := range h.ext.List() {
		if x.SessionID == c.Param("sid") {
			respond(c, nil, TypeInto(x.PID, req.Text))
			return
		}
	}
	respond(c, nil, errNotFound)
}

func (h *Handler) transcript(c *gin.Context) {
	entries, err := Transcript(c.Param("sid"), 40)
	respond(c, entries, err)
}

func (h *Handler) hook(c *gin.Context) {
	var p HookPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.ApplyHook(c.Param("id"), p))
}

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
