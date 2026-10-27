// Package hub serves the claude-mux page and routes it to the environments'
// backends, each a child process reached over its stdin and stdout.
package hub

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed web
var webFS embed.FS

// Run serves the page on addr and runs the local backend (backendArgv) as
// a child, until interrupted. On the way out it closes the backends, which
// hang up their sessions and resume them on the next start.
func Run(addr string, backendArgv []string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("refusing to listen on %s: the page runs commands, keep it on loopback", addr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	envs := map[string]*Env{"local": NewEnv("local", backendArgv)}
	for _, e := range envs {
		go e.Supervise()
	}

	srv := &http.Server{Addr: addr, Handler: NewHandler(envs), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	fmt.Fprintf(os.Stderr, "claude-mux on http://%s\n", addr)

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	for _, e := range envs {
		e.Close()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return err
}

// NewHandler serves the page at / and each environment's backend under
// /env/<name>/, with the prefix cut.
func NewHandler(envs map[string]*Env) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), localOnly)

	web, _ := fs.Sub(webFS, "web")
	r.GET("/", func(c *gin.Context) { c.FileFromFS("/", http.FS(web)) })
	r.Any("/env/:name/*path", func(c *gin.Context) {
		e, ok := envs[c.Param("name")]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such environment"})
			return
		}
		c.Request.URL.Path = c.Param("path")
		c.Request.URL.RawPath = ""
		e.ServeHTTP(c.Writer, c.Request)
	})
	return r
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
