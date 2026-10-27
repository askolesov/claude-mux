package backend

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// The default CheckOrigin rejects cross-origin upgrades, so another site
// open in the browser can't reach a terminal.
var upgrader = websocket.Upgrader{}

type Handler struct {
	m          *Manager
	ext        *Externals
	defaultCwd string
}

// NewHandler serves the backend's API. Only the hub reaches it, over the
// backend's stdio, so it trusts every request; the hub checks them.
func NewHandler(m *Manager, ext *Externals, defaultCwd string) http.Handler {
	h := &Handler{m: m, ext: ext, defaultCwd: defaultCwd}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	api.GET("/state", h.state)
	api.GET("/events", h.events)
	api.POST("/sessions", h.create)
	api.PUT("/sessions/order", h.reorder)
	api.PATCH("/sessions/:id", h.rename)
	api.PUT("/sessions/:id/group", h.setGroup)
	api.DELETE("/sessions/:id", h.close)
	api.POST("/sessions/:id/resume", h.resume)
	api.POST("/sessions/:id/handoff", h.handoff)
	api.POST("/sessions/:id/seen", h.seen)
	api.GET("/sessions/:id/term", h.term)
	api.POST("/external/:sid/takeover", h.takeover)
	api.POST("/external/:sid/send", h.send)
	api.DELETE("/external/:sid", h.closeExternal)
	api.POST("/external/:sid/rename", h.renameExternal)
	api.GET("/transcript/:sid", h.transcript)
	api.PUT("/favourites/:sid", h.favourite)
	api.DELETE("/favourites/:sid", h.unfavourite)
	api.POST("/favourites/:sid/start", h.startFavourite)
	api.POST("/favourites/:sid/terminal", h.favouriteInTerminal)
	api.POST("/groups/rename", h.renameGroup)
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

type State struct {
	Sessions   []SessionView `json:"sessions"`
	External   []External    `json:"external"`
	Favourites []Favourite   `json:"favourites"`
	DefaultCwd string        `json:"defaultCwd"`
}

func (h *Handler) snapshot() State {
	return State{Sessions: h.m.List(), External: h.ext.List(), Favourites: h.m.Favourites(), DefaultCwd: h.defaultCwd}
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
		Name  string `json:"name"`
		Group string `json:"group"`
		Cwd   string `json:"cwd"`
		Args  string `json:"args"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	cwd := expandHome(strings.TrimSpace(req.Cwd))
	if cwd == "" {
		cwd = h.defaultCwd
	}
	s, err := h.m.Create(strings.TrimSpace(req.Name), strings.TrimSpace(req.Group), cwd, strings.Fields(req.Args))
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

func (h *Handler) close(c *gin.Context)   { respond(c, nil, h.m.Close(c.Param("id"))) }
func (h *Handler) handoff(c *gin.Context) { respond(c, nil, h.m.Handoff(c.Param("id"))) }
func (h *Handler) resume(c *gin.Context)  { respond(c, nil, h.m.Resume(c.Param("id"))) }
func (h *Handler) seen(c *gin.Context)    { respond(c, nil, h.m.MarkSeen(c.Param("id"))) }

func (h *Handler) takeover(c *gin.Context) {
	sid := c.Param("sid")
	var req struct {
		Name string `json:"name"`
		Cwd  string `json:"cwd" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	// One live process per session: two would fork its transcript.
	if err := h.ext.Stop(sid); err != nil {
		respond(c, nil, err)
		return
	}
	s, err := h.m.Takeover(sid, req.Name, req.Cwd, nil)
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

func (h *Handler) favourite(c *gin.Context) {
	var f Favourite
	if err := c.ShouldBindJSON(&f); err != nil {
		respond(c, nil, err)
		return
	}
	f.ClaudeSessionID = c.Param("sid")
	respond(c, nil, h.m.SetFavourite(f))
}

func (h *Handler) unfavourite(c *gin.Context) {
	h.m.Unfavourite(c.Param("sid"))
	respond(c, nil, nil)
}

// runningAnywhere refuses to start a conversation that already runs: two
// processes on one session fork its transcript.
func (h *Handler) runningAnywhere(sid string) error {
	for _, v := range h.m.List() {
		if v.ClaudeSessionID == sid && v.PID != 0 && v.Status != StatusExited {
			return errors.New("it already runs in a tab here")
		}
	}
	for _, x := range h.ext.List() {
		if x.SessionID == sid {
			return errors.New("it already runs in another terminal")
		}
	}
	return nil
}

func (h *Handler) startFavourite(c *gin.Context) {
	if err := h.runningAnywhere(c.Param("sid")); err != nil {
		respond(c, nil, err)
		return
	}
	s, err := h.m.StartFavourite(c.Param("sid"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, s.View(), nil)
}

func (h *Handler) favouriteInTerminal(c *gin.Context) {
	if err := h.runningAnywhere(c.Param("sid")); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.m.FavouriteInTerminal(c.Param("sid")))
}

func (h *Handler) renameExternal(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond(c, nil, err)
		return
	}
	respond(c, nil, h.ext.Rename(c.Param("sid"), req.Name))
}

// closeExternal ends a session running in another terminal; its window and
// shell stay open.
func (h *Handler) closeExternal(c *gin.Context) { respond(c, nil, h.ext.Stop(c.Param("sid"))) }

func (h *Handler) transcript(c *gin.Context) {
	entries, err := Transcript(c.Param("sid"), 40)
	respond(c, entries, err)
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
