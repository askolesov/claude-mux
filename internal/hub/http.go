package hub

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"

	"github.com/askolesov/claude-mux/internal/stream"
	"github.com/gin-gonic/gin"
)

// handler serves the hub's API, over its Hub; the page reaches it from this
// machine only (localOnly).
type handler struct{ hub *Hub }

// Handler serves the page at /, the hub's own API under /hub/, and each
// environment's backend under /env/<name>/, with the prefix cut.
func (h *Hub) Handler() http.Handler {
	hh := handler{hub: h}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), localOnly)

	web, _ := fs.Sub(webFS, "web") // a fixed path in the embedded files: can't fail
	vendor, _ := fs.Sub(web, "vendor")
	r.GET("/", func(c *gin.Context) { c.FileFromFS("/", http.FS(web)) })
	r.StaticFileFS("/app.js", "app.js", http.FS(web))
	r.StaticFileFS("/style.css", "style.css", http.FS(web))
	r.StaticFS("/vendor", http.FS(vendor))

	hub := r.Group("/hub")
	hub.GET("/ws", hh.state)
	hub.GET("/info", hh.info)
	hub.POST("/open-log", hh.openLog)
	hub.POST("/pick-folder", hh.pickFolder)
	hub.GET("/targets/:kind", hh.targets)
	hub.POST("/environments", hh.addEnv)
	hub.PUT("/environments/order", hh.reorderEnvs)
	hub.POST("/environments/:name/reconnect", hh.reconnectEnv)
	hub.PUT("/environments/:name", hh.renameEnv)
	hub.DELETE("/environments/:name", hh.removeEnv)
	hub.PUT("/settings", hh.setSettings)
	hub.POST("/update/check", hh.checkUpdate)
	hub.POST("/update/apply", hh.applyUpdate)

	r.Any("/env/:name/*path", hh.toEnv)
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

// state streams everything the page shows over a WebSocket, on every
// change: the environments with each backend's own state, what this
// machine can add, the update state and the settings.
func (hh handler) state(c *gin.Context) {
	h := hh.hub
	stream.WebSocket(c, &h.changes, func() any {
		envs := h.list()
		views := make([]EnvView, 0, len(envs))
		for _, e := range envs {
			views = append(views, e.View())
		}
		return gin.H{"version": h.version, "platform": runtime.GOOS + "-" + runtime.GOARCH,
			"kinds": h.connectors.Kinds(), "stateDir": h.stateDir,
			"envs": views, "update": h.updates.View(), "settings": h.st.get().Settings}
	})
}

// info says a hub runs here, and its version: a second start asks it
// before showing this hub's page instead of starting its own.
func (hh handler) info(c *gin.Context) { respond(c, gin.H{"version": hh.hub.version}, nil) }

func (hh handler) openLog(c *gin.Context) { respond(c, nil, openLog(hh.hub.stateDir)) }

// pickFolder shows this machine's folder picker, for the local New tab.
func (hh handler) pickFolder(c *gin.Context) {
	var req struct {
		Start string `json:"start"`
	}
	if !bind(c, &req) {
		return
	}
	path, err := pickFolder(c.Request.Context(), req.Start)
	respond(c, gin.H{"path": path}, err)
}

func (hh handler) targets(c *gin.Context) {
	respond(c, hh.hub.connectors.Targets(Kind(c.Param("kind"))), nil)
}

func (hh handler) addEnv(c *gin.Context) {
	var req struct {
		Name   string `json:"name"`
		Kind   Kind   `json:"kind" binding:"required"`
		Target string `json:"target" binding:"required"`
	}
	if bind(c, &req) {
		respond(c, nil, hh.hub.Add(req.Name, req.Kind, req.Target))
	}
}

func (hh handler) reorderEnvs(c *gin.Context) {
	var req struct {
		Names []string `json:"names" binding:"required"`
	}
	if bind(c, &req) {
		respond(c, nil, hh.hub.Reorder(req.Names))
	}
}

func (hh handler) reconnectEnv(c *gin.Context) { respond(c, nil, hh.hub.Reconnect(c.Param("name"))) }

func (hh handler) renameEnv(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if bind(c, &req) {
		respond(c, nil, hh.hub.Rename(c.Param("name"), req.Name))
	}
}

func (hh handler) removeEnv(c *gin.Context) { respond(c, nil, hh.hub.Remove(c.Param("name"))) }

func (hh handler) setSettings(c *gin.Context) {
	var s Settings
	if bind(c, &s) {
		respond(c, nil, hh.hub.SetSettings(s))
	}
}

func (hh handler) checkUpdate(c *gin.Context) {
	respond(c, nil, hh.hub.updates.Check(c.Request.Context()))
}

// applyUpdate installs the newest release, streaming its progress
// ({"step": …}), and then restarts the hub into it. The update goes on if
// the page goes away.
func (hh handler) applyUpdate(c *gin.Context) {
	out := stream.NewLines(c)
	out.End(hh.hub.updates.Apply(func(step string) { out.Send(gin.H{"step": step}) }))
}

// toEnv forwards a request to an environment's backend, with
// /env/<name> cut from its path.
func (hh handler) toEnv(c *gin.Context) {
	e := hh.hub.env(c.Param("name"))
	if e == nil {
		respond(c, nil, errNoEnv)
		return
	}
	c.Request.URL.Path = c.Param("path")
	c.Request.URL.RawPath = ""
	e.ServeHTTP(c.Writer, c.Request)
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
