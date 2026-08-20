// auth_test.go BasicAuth 动态配置行为测试，规格对齐 rust_archive/apps-cli/src/web.rs
// 的 basic_auth/unauthorized（L451-519）。
// Dynamic-config BasicAuth tests, aligned with the basic_auth/unauthorized
// behavior of rust_archive/apps-cli/src/web.rs (L451-519).
package webserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
)

// newAuthTestServer 构造开启 BasicAuth（user=admin，pass 含冒号以覆盖
// splitn(2,':') 语义）的测试服务。
// newAuthTestServer builds a test server with BasicAuth on (user=admin, the
// password contains a colon to cover the splitn(2,':') semantics).
func newAuthTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth-config.yml")
	raw := "ikuai-url: http://127.0.0.1:1\nwebui:\n  user: admin\n  pass: s3:cret\n"
	cfg, err := config.ValidateAndSaveRawYAML(raw, cfgPath)
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rt := runtime.NewRuntimeService(cfg, "", "0 */5 * * * *", "ipgroup", nil)
	return NewServer(rt, cfg, cfgPath), cfgPath
}

// basicAuthHeader 生成 Basic 凭据头。
// basicAuthHeader renders the Basic credentials header.
func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// TestBasicAuthDisabledWhenUserEmpty webui.user 为空时免认证直接放行。
// TestBasicAuthDisabledWhenUserEmpty: a blank webui.user bypasses auth.
func TestBasicAuthDisabledWhenUserEmpty(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBasicAuthUnauthorizedResponses 缺凭据/错凭据/坏 Base64/错方案 =>
// 401 + WWW-Authenticate + "Unauthorized"；正确凭据 200；静态资源同样受保护。
// TestBasicAuthUnauthorizedResponses: missing/wrong credentials, broken
// Base64 or a wrong scheme answer 401 + WWW-Authenticate + "Unauthorized";
// correct credentials pass, and static assets stay protected too.
func TestBasicAuthUnauthorizedResponses(t *testing.T) {
	s, _ := newAuthTestServer(t)
	h := s.Handler()

	unauthorized := []struct {
		name   string
		path   string
		header string
	}{
		{"no credentials", "/api/config", ""},
		{"static protected", "/", ""},
		{"wrong password", "/api/config", basicAuthHeader("admin", "nope")},
		{"wrong user", "/api/config", basicAuthHeader("root", "s3:cret")},
		{"broken base64", "/api/config", "Basic !!!not-base64!!!"},
		{"wrong scheme", "/api/config", "Bearer " + base64.StdEncoding.EncodeToString([]byte("admin:s3:cret"))},
		{"user without colon part", "/api/config", "Basic " + base64.StdEncoding.EncodeToString([]byte("adminonly"))},
	}
	for _, c := range unauthorized {
		req, _ := http.NewRequest(http.MethodGet, c.path, nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: code=%d body=%s", c.name, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="Restricted"` {
			t.Fatalf("%s: WWW-Authenticate=%q", c.name, got)
		}
		if rec.Body.String() != "Unauthorized" {
			t.Fatalf("%s: body=%q", c.name, rec.Body.String())
		}
	}

	req, _ := http.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("admin", "s3:cret"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct creds code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBasicAuthDynamicAfterSaveRaw 配置更新立即生效：save-raw 去掉 webui.user
// 后免凭据放行；换成新账密后旧凭据 401、新凭据 200。
// TestBasicAuthDynamicAfterSaveRaw: config updates apply immediately — after
// save-raw drops webui.user, credential-less requests pass; after switching
// to a new account the old credentials get 401 while the new ones pass.
func TestBasicAuthDynamicAfterSaveRaw(t *testing.T) {
	s, _ := newAuthTestServer(t)
	h := s.Handler()

	rec := doReq(t, h, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("before save code=%d", rec.Code)
	}

	// save-raw 自身也受 BasicAuth 保护，须带旧凭据调用。
	// save-raw itself sits behind BasicAuth, so the old credentials go along.
	req, _ := http.NewRequest(http.MethodPost, "/api/save-raw", strings.NewReader(`{"yaml_text":"ikuai-url: http://127.0.0.1:1\n"}`))
	req.Header.Set("Authorization", basicAuthHeader("admin", "s3:cret"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("save code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("after auth removed code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/save-raw",
		`{"yaml_text":"ikuai-url: http://127.0.0.1:1\nwebui:\n  user: bob\n  pass: eve\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save2 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old creds must fail after user switch: code=%d", rec.Code)
	}
	req, _ = http.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("bob", "eve"))
	rec2 = httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("new creds code=%d body=%s", rec2.Code, rec2.Body.String())
	}

	var m map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	webui := m["webui"].(map[string]any)
	if webui["user"] != "bob" {
		t.Fatalf("webui.user=%v, want bob", webui["user"])
	}
	if !strings.Contains(rec2.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("Cache-Control missing no-store")
	}
}
