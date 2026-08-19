// routes_test.go Web 服务 15 端点契约测试（TDD 先行），
// 行为规格对齐 rust_archive/apps-cli/src/web.rs 的路由表与响应语义。
// Contract tests for the 15 web endpoints (TDD first), aligned with the
// routing table and response semantics of rust_archive/apps-cli/src/web.rs.
package webserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	ikuaitoolbox "github.com/FelixJI/iKuai-Toolbox"
	"github.com/FelixJI/iKuai-Toolbox/internal/app"
	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
)

// errBoom 桩函数使用的确定性错误。
// errBoom is the deterministic error used by stubs.
var errBoom = errors.New("boom")

// newTestServer 构造带临时配置文件的最小 Server/RuntimeService；
// 登录指向 127.0.0.1:1，避免诊断/清理路径被真实网络拖慢。
// newTestServer builds a minimal Server/RuntimeService backed by a temp
// config file; logins point at 127.0.0.1:1 so diagnostics/clean paths are
// never slowed down by real networks.
func newTestServer(t *testing.T) (*Server, *runtime.RuntimeService, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test-config.yml")
	raw := "ikuai-url: http://127.0.0.1:1\nusername: admin\npassword: secret\n"
	cfg, err := config.ValidateAndSaveRawYAML(raw, cfgPath)
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rt := runtime.NewRuntimeService(cfg, "", "0 */5 * * * *", "ipgroup", nil)
	return NewServer(rt, cfg, cfgPath), rt, cfgPath
}

// gatedLoginServer 登录请求阻塞到 release 关闭，用于钉死 run-once 的在跑窗口。
// gatedLoginServer blocks login requests until release closes, pinning the
// in-flight window of run-once deterministically.
func gatedLoginServer(t *testing.T) (*httptest.Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"code":-1,"message":"blocked"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, release
}

// newGatedTestServer 配置指向 gatedLoginServer 的测试服务，清理时放行并等待收尾。
// newGatedTestServer points the config at gatedLoginServer; cleanup releases
// the gate and waits for the run to finish.
func newGatedTestServer(t *testing.T) *Server {
	t.Helper()
	gateSrv, release := gatedLoginServer(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test-config.yml")
	raw := "ikuai-url: " + gateSrv.URL + "\nusername: admin\npassword: secret\n"
	cfg, err := config.ValidateAndSaveRawYAML(raw, cfgPath)
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rt := runtime.NewRuntimeService(cfg, "", "0 */5 * * * *", "ispdomain", nil)
	s := NewServer(rt, cfg, cfgPath)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !rt.Status().Running {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	return s
}

// doReq 便捷执行一个请求并返回 recorder。
// doReq runs one request against the handler and returns the recorder.
func doReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// stubVar 替换包级函数缝并在测试结束后恢复。
// stubVar swaps a package-level function seam and restores it afterwards.
func stubVar[T any](t *testing.T, slot *T, fake T) {
	t.Helper()
	orig := *slot
	*slot = fake
	t.Cleanup(func() { *slot = orig })
}

// TestConfigEndpointContract GET /api/config：200 + no-store + 展平键
// （exe_path/conf_path/raw_yaml/ikuai-url/proxy.mode 默认 smart）。
// TestConfigEndpointContract: GET /api/config answers 200 + no-store with the
// flattened keys (exe_path/conf_path/raw_yaml/ikuai-url/proxy.mode smart).
func TestConfigEndpointContract(t *testing.T) {
	s, _, cfgPath := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	for _, key := range []string{"exe_path", "conf_path", "raw_yaml", "ikuai-url", "webui", "proxy", "cron"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %v", key, m)
		}
	}
	if got := m["exe_path"].(string); got == "" {
		t.Fatal("exe_path empty")
	}
	if got := m["conf_path"].(string); got != cfgPath {
		t.Fatalf("conf_path=%q, want %q", got, cfgPath)
	}
	if got := m["raw_yaml"].(string); !strings.Contains(got, "ikuai-url: http://127.0.0.1:1") {
		t.Fatalf("raw_yaml=%q", got)
	}
	if got := m["ikuai-url"].(string); got != "http://127.0.0.1:1" {
		t.Fatalf("ikuai-url=%q", got)
	}
	proxyMap := m["proxy"].(map[string]any)
	if got := proxyMap["mode"].(string); got != "smart" {
		t.Fatalf("proxy.mode=%q, want smart (apply_defaults)", got)
	}
}

// TestDefaultConfigContract GET /api/config/default：200 text/plain 原文返回内嵌默认配置。
// TestDefaultConfigContract: GET /api/config/default returns the embedded
// default YAML verbatim as 200 text/plain.
func TestDefaultConfigContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/config/default", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	if rec.Body.String() != config.EmbeddedDefaultYAML() {
		t.Fatal("body differs from embedded default yaml")
	}
}

// TestDiagnosticsReportContract GET /api/diagnostics/report：200 JSON
// {generated_at,text}，text 为诊断报告正文。
// TestDiagnosticsReportContract: GET /api/diagnostics/report answers 200 JSON
// {generated_at,text} with the report body inside text.
func TestDiagnosticsReportContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/diagnostics/report", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got := m["generated_at"].(string); got == "" {
		t.Fatal("generated_at empty")
	}
	if got := m["text"].(string); !strings.Contains(got, "IKB Diagnostics Report") {
		t.Fatalf("text=%q", got)
	}
}

// TestSaveRawContract POST /api/save-raw 成功：固定 JSON 文案、原文落盘、
// cfgHolder 刷新、运行时默认 module/cron 同步。
// TestSaveRawContract: a successful POST /api/save-raw returns the fixed JSON
// body, writes the raw YAML verbatim, refreshes cfgHolder and syncs the
// runtime default module/cron.
func TestSaveRawContract(t *testing.T) {
	s, rt, cfgPath := newTestServer(t)
	raw := "ikuai-url: http://10.0.0.9\nusername: admin\npassword: p\nmode: ispdomain\ncron: 0 0 6 * * *\n"
	payload, err := json.Marshal(map[string]string{"yaml_text": raw})
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/save-raw", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	if want := `{"status":"success","message":"Raw YAML saved successfully"}`; rec.Body.String() != want {
		t.Fatalf("body=%q, want %q", rec.Body.String(), want)
	}
	onDisk, err := osReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if onDisk != raw {
		t.Fatalf("disk=%q, want raw verbatim", onDisk)
	}
	rec2 := doReq(t, s.Handler(), http.MethodGet, "/api/config", "")
	var m map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if got := m["ikuai-url"].(string); got != "http://10.0.0.9" {
		t.Fatalf("ikuai-url after save=%q, want http://10.0.0.9", got)
	}
	st := rt.Status()
	if st.Module != "ispdomain" {
		t.Fatalf("runtime module=%q, want ispdomain", st.Module)
	}
	if st.CronExpr != "0 0 6 * * *" {
		t.Fatalf("runtime cron=%q, want 0 0 6 * * *", st.CronExpr)
	}
}

// TestSaveRawRejectsBadYAML POST /api/save-raw 失败：400 + "Failed to save config:" 前缀。
// TestSaveRawRejectsBadYAML: an invalid YAML answers 400 with the
// "Failed to save config:" prefix.
func TestSaveRawRejectsBadYAML(t *testing.T) {
	s, _, _ := newTestServer(t)
	payload := `{"yaml_text":"ikuai-url: [oops"}`
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/save-raw", payload)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "Failed to save config:") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// TestSaveRawRejectsMalformedJSON 请求体非 JSON 或缺 yaml_text 字段 => 400。
// TestSaveRawRejectsMalformedJSON: a non-JSON body or a missing yaml_text
// field answers 400.
func TestSaveRawRejectsMalformedJSON(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, body := range []string{"not json", "{}", `{"other":1}`} {
		rec := doReq(t, s.Handler(), http.MethodPost, "/api/save-raw", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%q code=%d, want 400", body, rec.Code)
		}
	}
}

// TestSaveRawExplicitModeSemantics 仅当 YAML 显式含 mode 才更新运行时 module
// （web.rs L210-222 的注释语义：防止默认 ispdomain 覆盖 CLI 模块）。
// TestSaveRawExplicitModeSemantics: the runtime module only follows the saved
// config when the raw YAML explicitly contains `mode` (web.rs L210-222:
// the ispdomain default must not override the CLI-chosen module).
func TestSaveRawExplicitModeSemantics(t *testing.T) {
	s, rt, _ := newTestServer(t)
	if st := rt.Status(); st.Module != "ipgroup" {
		t.Fatalf("initial module=%q, want ipgroup", st.Module)
	}

	noMode := "ikuai-url: http://10.0.0.9\ncron: 0 0 7 * * *\n"
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/save-raw", `{"yaml_text":"ikuai-url: http://10.0.0.9\ncron: 0 0 7 * * *\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save without mode: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = noMode
	st := rt.Status()
	if st.Module != "ipgroup" {
		t.Fatalf("module after modeless save=%q, want ipgroup kept", st.Module)
	}
	if st.CronExpr != "0 0 7 * * *" {
		t.Fatalf("cron after modeless save=%q, want 0 0 7 * * *", st.CronExpr)
	}

	rec = doReq(t, s.Handler(), http.MethodPost, "/api/save-raw", `{"yaml_text":"ikuai-url: http://10.0.0.9\nmode: iip\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save with mode: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if st := rt.Status(); st.Module != "iip" {
		t.Fatalf("module after explicit mode=%q, want iip", st.Module)
	}
}

// TestRemoteFetchContract POST /api/remote/fetch：成功 200 text/plain；
// 失败 502 text；透传 url/proxy/githubProxy。
// TestRemoteFetchContract: POST /api/remote/fetch maps success to 200
// text/plain and failure to 502 text, passing url/proxy/githubProxy through.
func TestRemoteFetchContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	var gotURL, gotGh string
	var gotProxy *config.ProxyConfig
	stubVar(t, &fetchRemoteConfigFn, func(rawURL string, proxy *config.ProxyConfig, githubProxy string) (string, error) {
		gotURL, gotProxy, gotGh = rawURL, proxy, githubProxy
		return "remote body", nil
	})
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/remote/fetch",
		`{"url":"http://example.com/cfg.yml","proxy":{"mode":"custom","url":"http://127.0.0.1:7890"},"githubProxy":"https://gh.example/"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}
	if rec.Body.String() != "remote body" {
		t.Fatalf("body=%q", rec.Body.String())
	}
	if gotURL != "http://example.com/cfg.yml" {
		t.Fatalf("url=%q", gotURL)
	}
	if gotProxy == nil || gotProxy.Mode != config.ProxyModeCustom || gotProxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("proxy=%+v", gotProxy)
	}
	if gotGh != "https://gh.example/" {
		t.Fatalf("githubProxy=%q", gotGh)
	}

	stubVar(t, &fetchRemoteConfigFn, func(string, *config.ProxyConfig, string) (string, error) {
		return "", errBoom
	})
	rec = doReq(t, s.Handler(), http.MethodPost, "/api/remote/fetch",
		`{"url":"http://example.com/cfg.yml","proxy":{"mode":"smart"},"githubProxy":""}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("error code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "boom" {
		t.Fatalf("error body=%q", rec.Body.String())
	}
}

// TestIkuaiLoginContract POST /api/test/ikuai-login：恒 200 JSON {ok,message}；
// 空 URL 走真实实现得到 ok:false。
// TestIkuaiLoginContract: POST /api/test/ikuai-login always answers 200 JSON
// {ok,message}; an empty URL goes through the real implementation with ok:false.
func TestIkuaiLoginContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	// 真实实现的空 URL 路径先验证（无网络依赖），再挂桩验证成功映射。
	// The real implementation's empty-URL path (network-free) goes first,
	// then a stub validates the success mapping.
	var m map[string]any
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/test/ikuai-login",
		`{"baseUrl":"","username":"","password":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty code=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != false || m["message"] != "Empty iKuai URL" {
		t.Fatalf("empty body=%v", m)
	}

	stubVar(t, &testIkuaiLoginFn, func(app.TestIkuaiLoginRequest) app.TestResult {
		return app.TestResult{OK: true, Message: "OK"}
	})
	rec = doReq(t, s.Handler(), http.MethodPost, "/api/test/ikuai-login",
		`{"baseUrl":"http://127.0.0.1:1","username":"admin","password":"pw"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true || m["message"] != "OK" {
		t.Fatalf("body=%v", m)
	}
}

// TestGithubProxyContract POST /api/test/github-proxy：恒 200 JSON {ok,message}。
// TestGithubProxyContract: POST /api/test/github-proxy always answers 200 JSON
// {ok,message}.
func TestGithubProxyContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	// 真实实现的空前缀路径先验证（无网络依赖），再挂桩验证成功映射。
	// The real implementation's empty-prefix path (network-free) goes first,
	// then a stub validates the success mapping.
	var m map[string]any
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/test/github-proxy", `{"githubProxy":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty code=%d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != false || m["message"] != "Empty github proxy" {
		t.Fatalf("empty body=%v", m)
	}

	stubVar(t, &testGithubProxyFn, func(app.TestGithubProxyRequest) app.TestResult {
		return app.TestResult{OK: true, Message: "OK url='x'"}
	})
	rec = doReq(t, s.Handler(), http.MethodPost, "/api/test/github-proxy", `{"githubProxy":"https://gh.example/"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true {
		t.Fatalf("body=%v", m)
	}
}

// TestGithubReleasesContract GET 用当前配置代理、POST 用请求体代理；
// 成功 200 JSON 数组，失败 502 text。
// TestGithubReleasesContract: GET uses the live config proxy and POST the
// request proxy; success answers a 200 JSON array, failure 502 text.
func TestGithubReleasesContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	var gotProxy *config.ProxyConfig
	stubVar(t, &fetchGithubReleasesFn, func(proxy *config.ProxyConfig) ([]app.GithubRelease, error) {
		gotProxy = proxy
		return []app.GithubRelease{{TagName: "v1.0.0", HTMLURL: "https://github.com/x", Prerelease: false, Draft: false, Name: "r1", PublishedAt: "2026-01-01T00:00:00Z", CreatedAt: "2026-01-01T00:00:00Z"}}, nil
	})
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/github/releases", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tag_name":"v1.0.0"`) {
		t.Fatalf("GET body=%q", rec.Body.String())
	}
	if gotProxy == nil || gotProxy.Mode != config.ProxyModeSmart {
		t.Fatalf("GET proxy=%+v, want config proxy", gotProxy)
	}

	rec = doReq(t, s.Handler(), http.MethodPost, "/api/github/releases", `{"proxy":{"mode":"custom","url":"http://127.0.0.1:7890"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST code=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotProxy == nil || gotProxy.Mode != config.ProxyModeCustom || gotProxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("POST proxy=%+v", gotProxy)
	}

	stubVar(t, &fetchGithubReleasesFn, func(*config.ProxyConfig) ([]app.GithubRelease, error) {
		return nil, errBoom
	})
	rec = doReq(t, s.Handler(), http.MethodGet, "/api/github/releases", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("error code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "boom" {
		t.Fatalf("error body=%q", rec.Body.String())
	}
}

// TestRuntimeStatusContract GET /api/runtime/status：200 JSON 键名逐一断言。
// TestRuntimeStatusContract: GET /api/runtime/status answers 200 with every
// JSON key asserted verbatim.
func TestRuntimeStatusContract(t *testing.T) {
	s, rt, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/runtime/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"running", "cron_running", "cron_expr", "module", "last_run_at", "next_run_at"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %v", key, m)
		}
	}
	if m["module"] != "ipgroup" || m["running"] != false {
		t.Fatalf("body=%v", m)
	}
	_ = rt
}

// TestRunOnceContract POST /api/run-once：在跑窗口内 started true→false；
// 坏请求体 400。
// TestRunOnceContract: POST /api/runtime/run-once yields started true then
// false inside the gated in-flight window; a malformed body answers 400.
func TestRunOnceContract(t *testing.T) {
	s := newGatedTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/runtime/run-once", `{"module":"ispdomain"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"started":true}`; rec.Body.String() != want {
		t.Fatalf("first body=%q, want %q", rec.Body.String(), want)
	}
	rec = doReq(t, s.Handler(), http.MethodPost, "/api/runtime/run-once", `{"module":"ispdomain"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"started":false}`; rec.Body.String() != want {
		t.Fatalf("second body=%q, want %q (reentrancy guard)", rec.Body.String(), want)
	}

	rec = doReq(t, s.Handler(), http.MethodPost, "/api/runtime/run-once", "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body code=%d", rec.Code)
	}
}

// TestCronStartContract POST /api/runtime/cron/start：合法表达式 200 success，
// 空表达式 400 "Failed to start cron:"。
// TestCronStartContract: a valid expression answers 200 success while an
// empty one answers 400 with the "Failed to start cron:" prefix.
func TestCronStartContract(t *testing.T) {
	s, rt, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/runtime/cron/start",
		`{"expr":"0 0 3 1 1 *","module":"ispdomain"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"status":"success"}`; rec.Body.String() != want {
		t.Fatalf("body=%q, want %q", rec.Body.String(), want)
	}
	if st := rt.Status(); !st.CronRunning {
		t.Fatal("cron_running=false after start")
	}
	if err := rt.StopCron(); err != nil {
		t.Fatalf("stop cron: %v", err)
	}

	rec = doReq(t, s.Handler(), http.MethodPost, "/api/runtime/cron/start", `{"expr":"","module":"ispdomain"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty expr code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "Failed to start cron:") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// TestCronStopContract POST /api/runtime/cron/stop：200 {"status":"success"}。
// TestCronStopContract: POST /api/runtime/cron/stop answers 200 {"status":"success"}.
func TestCronStopContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/runtime/cron/stop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"status":"success"}`; rec.Body.String() != want {
		t.Fatalf("body=%q, want %q", rec.Body.String(), want)
	}
}

// TestRuntimeStopContract POST /api/runtime/stop：200 {"status":"success"}。
// TestRuntimeStopContract: POST /api/runtime/stop answers 200 {"status":"success"}.
func TestRuntimeStopContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/runtime/stop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"status":"success"}`; rec.Body.String() != want {
		t.Fatalf("body=%q, want %q", rec.Body.String(), want)
	}
}

// TestCleanContract POST /api/runtime/clean：成功 200 success；
// 空 clean_tag 走真实实现 400 "Clean mode requires clean_tag"。
// TestCleanContract: POST /api/runtime/clean maps success to 200 and a blank
// clean_tag (through the real implementation) to 400 with
// "Clean mode requires clean_tag".
func TestCleanContract(t *testing.T) {
	s, _, _ := newTestServer(t)
	// 真实实现的空 tag 校验先验证（无网络依赖），再挂桩验证成功映射。
	// The real implementation's blank-tag guard (network-free) goes first,
	// then a stub validates the success mapping.
	rec := doReq(t, s.Handler(), http.MethodPost, "/api/runtime/clean", `{"clean_tag":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "Clean mode requires clean_tag" {
		t.Fatalf("empty body=%q", rec.Body.String())
	}

	var gotTag string
	stubVar(t, &runCleanFn, func(_ *config.Config, _, cleanTag string) error {
		gotTag = cleanTag
		return nil
	})
	rec = doReq(t, s.Handler(), http.MethodPost, "/api/runtime/clean", `{"clean_tag":"mytag"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if want := `{"status":"success"}`; rec.Body.String() != want {
		t.Fatalf("body=%q, want %q", rec.Body.String(), want)
	}
	if gotTag != "mytag" {
		t.Fatalf("clean_tag=%q", gotTag)
	}
}

// TestLogsTailContract GET /api/runtime/logs?tail=N：注入一条日志后
// tail=1 恰好取 1 条；缺省/非法 tail 回退 200 条上限。
// TestLogsTailContract: after injecting one log record, tail=1 returns
// exactly one record; a missing or invalid tail falls back to the 200 cap.
func TestLogsTailContract(t *testing.T) {
	s, rt, _ := newTestServer(t)
	if err := rt.StopAll(); err != nil { // appendSys TASK:任务停止，同步入缓冲
		t.Fatal(err)
	}
	rec := doReq(t, s.Handler(), http.MethodGet, "/api/runtime/logs?tail=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var logs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("len=%d, want 1 (body=%s)", len(logs), rec.Body.String())
	}
	if logs[0]["tag"] != "TASK:任务停止" {
		t.Fatalf("tag=%v", logs[0]["tag"])
	}
	for _, key := range []string{"ts", "module", "tag", "level", "detail"} {
		if _, ok := logs[0][key]; !ok {
			t.Fatalf("missing key %q", key)
		}
	}

	rec = doReq(t, s.Handler(), http.MethodGet, "/api/runtime/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("default code=%d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("default len=%d, want 1", len(logs))
	}
	rec = doReq(t, s.Handler(), http.MethodGet, "/api/runtime/logs?tail=0", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("tail=0 len=%d, want fallback to default", len(logs))
	}
}

// TestSSEStream SSE 契约：text/event-stream + no-store，注入日志后出现
// data:<LogRecord JSON>；客户端断开后消费 goroutine 退出（无订阅泄漏）。
// TestSSEStream: the stream answers text/event-stream + no-store, surfaces
// data:<LogRecord JSON> after an injected log, and the consuming goroutine
// exits once the client disconnects (no subscription leak).
func TestSSEStream(t *testing.T) {
	s, rt, _ := newTestServer(t)
	h := s.Handler()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/runtime/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(5*time.Second, cancel)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}

	if err := rt.StopAll(); err != nil { // 同步广播一条 TASK:任务停止
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	found := false
	for !found {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v (found=%v)", err, found)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") && strings.Contains(line, `"tag":"TASK:任务停止"`) {
			if !strings.Contains(line, `"module":`) || !strings.Contains(line, `"level":`) {
				t.Fatalf("data line not a LogRecord JSON: %q", line)
			}
			found = true
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not exit after client disconnect")
	}
}

// TestSSEKeepAliveComment 空闲时输出 ":" 注释行保活（对齐 axum KeepAlive）。
// TestSSEKeepAliveComment: an idle stream emits ":" comment lines as
// keep-alive (mirroring axum's KeepAlive).
func TestSSEKeepAliveComment(t *testing.T) {
	s, _, _ := newTestServer(t)
	stubVar(t, &sseKeepAliveInterval, 20*time.Millisecond)

	h := s.Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/runtime/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(5*time.Second, cancel)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if strings.TrimRight(line, "\r\n") == ":" {
			return
		}
	}
	t.Fatal("no keep-alive comment within 2s")
}

// TestSpaFallback 静态 SPA 回退：未知路径回 index.html（200 text/html）。
// TestSpaFallback: unknown paths fall back to index.html (200 text/html).
func TestSpaFallback(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, path := range []string{"/", "/unknown/path", "/deep/nested/route"} {
		rec := doReq(t, s.Handler(), http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("path=%q code=%d", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("path=%q Content-Type=%q", path, got)
		}
		if !strings.Contains(rec.Body.String(), "<!DOCTYPE") {
			t.Fatalf("path=%q body is not index.html", path)
		}
	}
}

// TestStaticMimeContract 静态资源按扩展名给 MIME；mimeType 表逐项断言。
// TestStaticMimeContract: static assets serve extension-based MIME types;
// the mimeType table is asserted entry by entry.
func TestStaticMimeContract(t *testing.T) {
	s, _, _ := newTestServer(t)

	cases := []struct {
		url  string
		want string
	}{
		{"/index.html", "text/html; charset=utf-8"},
		{"/favicon.png", "image/png"},
	}
	var jsPath, cssPath string
	err := fs.WalkDir(ikuaitoolbox.FrontendFS, "frontends/app/dist", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		trimmed := strings.TrimPrefix(p, "frontends/app/dist")
		switch {
		case jsPath == "" && strings.HasSuffix(p, ".js"):
			jsPath = trimmed
		case cssPath == "" && strings.HasSuffix(p, ".css"):
			cssPath = trimmed
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dist: %v", err)
	}
	if jsPath == "" || cssPath == "" {
		t.Fatalf("dist missing js/css assets (js=%q css=%q); run bun run build", jsPath, cssPath)
	}
	cases = append(cases,
		struct{ url, want string }{jsPath, "text/javascript"},
		struct{ url, want string }{cssPath, "text/css"},
	)
	for _, c := range cases {
		rec := doReq(t, s.Handler(), http.MethodGet, c.url, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("url=%q code=%d", c.url, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != c.want {
			t.Fatalf("url=%q Content-Type=%q, want %q", c.url, got, c.want)
		}
	}

	mimeCases := map[string]string{
		"a.html": "text/html; charset=utf-8",
		"a.js":   "text/javascript",
		"a.css":  "text/css",
		"a.png":  "image/png",
		"a.svg":  "image/svg+xml",
		"a.ico":  "image/x-icon",
		"a.webp": "image/webp",
		"a.json": "application/json",
		"a.txt":  "application/octet-stream",
		"a":      "application/octet-stream",
	}
	for name, want := range mimeCases {
		if got := mimeType(name); got != want {
			t.Fatalf("mimeType(%q)=%q, want %q", name, got, want)
		}
	}
}

// TestStaticNoIndexReturns404 dist 缺 index.html 时未知路径 404 "Not found"。
// TestStaticNoIndexReturns404: with index.html absent from the dist tree,
// unknown paths answer 404 "Not found".
func TestStaticNoIndexReturns404(t *testing.T) {
	s, _, _ := newTestServer(t)
	origFS, origErr := distFS, distFSErr
	distFS, distFSErr = fstest.MapFS{}, nil
	t.Cleanup(func() { distFS, distFSErr = origFS, origErr })

	rec := doReq(t, s.Handler(), http.MethodGet, "/anything", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rec.Code)
	}
	if rec.Body.String() != "Not found" {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// osReadFile 读文件为字符串。
// osReadFile reads a file into a string.
func osReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
