package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/askolesov/claude-mux/internal/stream"
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
	api.PUT("/sessions/:id", h.configure)
	api.PUT("/sessions/:id/name", h.rename)
	api.DELETE("/sessions/:id", h.close)
	api.POST("/sessions/:id/start", h.start)
	api.POST("/sessions/:id/restart", h.restart)
	api.POST("/sessions/:id/seen", h.seen)
	api.GET("/sessions/:id/term", h.term)
	api.POST("/groups", h.createGroup)
	api.PUT("/groups/:id", h.renameGroup)
	api.DELETE("/groups/:id", h.deleteGroup)
	api.PUT("/nodes/:id/place", h.move)
	api.GET("/dirs", h.dirs)
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
	Groups     []Group       `json:"groups"`
	DefaultCwd string        `json:"defaultCwd"`
}

func (h *Handler) snapshot() StateView {
	return StateView{Claude: h.m.Claude(), Sessions: h.m.List(), Groups: h.m.Groups(), DefaultCwd: h.defaultCwd}
}

func (h *Handler) state(c *gin.Context) { c.JSON(http.StatusOK, h.snapshot()) }

// events streams the whole state on every change.
func (h *Handler) events(c *gin.Context) {
	stream.Events(c, &h.m.changes, func() any { return h.snapshot() })
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
		Name          string     `json:"name"`
		Parent        string     `json:"parent"`
		After         string     `json:"after"` // a child of parent to go after; "" for its end
		Cwd           string     `json:"cwd"`
		Args          string     `json:"args"`
		Resume        ResumeMode `json:"resume"`
		ResumeMessage string     `json:"resumeMessage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	cwd := expandHome(strings.TrimSpace(req.Cwd))
	if cwd == "" {
		cwd = h.defaultCwd
	}
	s, err := h.m.Create(strings.TrimSpace(req.Name), req.Parent, req.After, cwd, req.Resume, req.ResumeMessage, strings.Fields(req.Args))
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, s.View(), nil)
}

// configure takes the tab's settings as Tab settings saves them.
func (h *Handler) configure(c *gin.Context) {
	var req struct {
		Cwd           string     `json:"cwd" binding:"required"`
		Args          string     `json:"args"`
		Resume        ResumeMode `json:"resume"`
		ResumeMessage string     `json:"resumeMessage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.Configure(c.Param("id"), expandHome(strings.TrimSpace(req.Cwd)), req.Resume, req.ResumeMessage, strings.Fields(req.Args)))
}

func (h *Handler) rename(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.Rename(c.Param("id"), req.Name))
}

// dirs completes a directory path as it is typed in New tab: the
// directories that the last part of prefix starts, here.
func (h *Handler) dirs(c *gin.Context) {
	c.JSON(http.StatusOK, completeDir(c.Query("prefix")))
}

func (h *Handler) createGroup(c *gin.Context) {
	var req struct {
		Name   string `json:"name" binding:"required"`
		Parent string `json:"parent"`
		After  string `json:"after"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	g, err := h.m.CreateGroup(req.Name, req.Parent, req.After)
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, g, nil)
}

func (h *Handler) renameGroup(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.RenameGroup(c.Param("id"), req.Name))
}

func (h *Handler) deleteGroup(c *gin.Context) { respond(c, nil, h.m.DeleteGroup(c.Param("id"))) }

// move places a tab or a group: into parent, before or after one of its
// children, or at its end.
func (h *Handler) move(c *gin.Context) {
	var req struct {
		Parent string `json:"parent"`
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.Move(c.Param("id"), req.Parent, req.Before, req.After))
}

func (h *Handler) checkClaude(c *gin.Context) { respond(c, h.m.CheckClaude(), nil) }

// installClaude runs the official installer, which takes a while, and
// streams its output as it comes. The install goes on if the page goes away.
func (h *Handler) installClaude(c *gin.Context) {
	out := stream.NewLines(c)
	out.End(h.m.InstallClaude(context.WithoutCancel(c.Request.Context()), out))
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
