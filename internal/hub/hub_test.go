package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalOnly(t *testing.T) {
	h, err := New(t.TempDir(), Backends{}, Connectors{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Handler()
	for _, c := range []struct {
		name, method, host, origin, ctype string
		want                              int
	}{
		{"the page", "GET", "127.0.0.1:7777", "", "", http.StatusOK},
		{"localhost", "GET", "localhost:7777", "", "", http.StatusOK},
		{"a rebound name", "GET", "evil.example:7777", "", "", http.StatusForbidden},
		{"another origin", "GET", "127.0.0.1:7777", "http://evil.example", "", http.StatusForbidden},
		{"its own origin", "GET", "127.0.0.1:7777", "http://127.0.0.1:7777", "", http.StatusOK},
		{"a form post", "POST", "127.0.0.1:7777", "", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := strings.NewReader("")
			if c.ctype != "" {
				body = strings.NewReader("a=b")
			}
			req := httptest.NewRequest(c.method, "http://"+c.host+"/hub/info", body)
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			if c.ctype != "" {
				req.Header.Set("Content-Type", c.ctype)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("got %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestReorder(t *testing.T) {
	h, err := New(t.TempDir(), Backends{}, Connectors{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.envs = append(h.envs, newEnv(EnvConfig{Name: "devbox", Kind: KindSSH, Target: "devbox"}, nil, func() {}))
	if err := h.Reorder([]string{"local"}); err == nil {
		t.Error("a missing environment was accepted")
	}
	if err := h.Reorder([]string{"devbox", "local"}); err != nil {
		t.Fatal(err)
	}
	if envs := h.st.get().Environments; len(envs) != 2 || envs[0].Name != "devbox" {
		t.Errorf("saved %+v", envs)
	}
}

func TestPageFiles(t *testing.T) {
	h, err := New(t.TempDir(), Backends{}, Connectors{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Handler()
	for _, path := range []string{"/", "/app.js", "/style.css", "/vendor/xterm.js", "/vendor/xterm.css", "/vendor/inter.woff2"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7777"+path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: %d, %d bytes", path, rec.Code, rec.Body.Len())
		}
	}
}

func TestRename(t *testing.T) {
	h, err := New(t.TempDir(), Backends{}, Connectors{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.envs = append(h.envs, newEnv(EnvConfig{Name: "devbox", Kind: KindSSH, Target: "devbox"}, nil, func() {}))
	if err := h.Rename("local", "here"); err == nil {
		t.Error("renamed local")
	}
	if err := h.Rename("devbox", "local"); err == nil {
		t.Error("took a name in use")
	}
	if err := h.Rename("devbox", "build-box"); err != nil {
		t.Fatal(err)
	}
	if envs := h.st.get().Environments; envs[1].Name != "build-box" || envs[1].Target != "devbox" {
		t.Errorf("saved %+v", envs)
	}
}

func TestRespond(t *testing.T) {
	h, err := New(t.TempDir(), Backends{}, Connectors{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Handler()
	for _, c := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"a missing environment", "GET", "/env/nope/api/state", "", http.StatusNotFound},
		{"reconnecting a missing one", "POST", "/hub/environments/nope/reconnect", "", http.StatusNotFound},
		{"renaming local", "PUT", "/hub/environments/local", `{"name":"x"}`, http.StatusBadRequest},
		{"a body that isn't the request", "PUT", "/hub/settings", `{"everyDays":"often"}`, http.StatusBadRequest},
		{"settings out of range", "PUT", "/hub/settings", `{"everyDays":0}`, http.StatusBadRequest},
		{"settings", "PUT", "/hub/settings", `{"checkUpdates":true,"everyDays":3}`, http.StatusNoContent},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://127.0.0.1:7777"+c.path, strings.NewReader(c.body))
			if c.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("got %d %s, want %d", rec.Code, rec.Body, c.want)
			}
		})
	}
}
