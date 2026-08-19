// app_test.go app 服务层测试：请求别名解码 / 更新检查 / 远程拉取 / URL 归一化 /
// 清理顺序 / 配置元信息 / 诊断报告，全部走标准 testing + httptest。
// 行为规格对齐 crates/core/src/app/ 各文件。
// app service-layer tests: request alias decoding / update checks / remote
// fetching / URL normalization / clean ordering / config meta / the
// diagnostics report, all on the standard testing + httptest stack,
// aligned with the files under crates/core/src/app/.
package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// TestRequestAliases 前端两种命名都必须解到同一字段（diagnostics.rs L18-30 的 serde alias）：
// Tauri 走 camelCase（baseUrl / githubProxy），HTTP 走 snake_case（base_url / github_proxy）。
// TestRequestAliases verifies both frontend spellings decode into the same
// field (the serde aliases of diagnostics.rs L18-30): the Tauri side sends
// camelCase (baseUrl / githubProxy) while HTTP sends snake_case (base_url / github_proxy).
func TestRequestAliases(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"login camelCase", `{"baseUrl":"http://192.168.1.1","username":"admin","password":"p"}`, "http://192.168.1.1"},
		{"login snake_case", `{"base_url":"http://192.168.1.2","username":"admin","password":"p"}`, "http://192.168.1.2"},
	}
	for _, tc := range cases {
		var req TestIkuaiLoginRequest
		if err := json.Unmarshal([]byte(tc.json), &req); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if req.BaseURL != tc.want {
			t.Errorf("%s: BaseURL = %q, want %q", tc.name, req.BaseURL, tc.want)
		}
		if req.Username != "admin" || req.Password != "p" {
			t.Errorf("%s: credentials = %q/%q, want admin/p", tc.name, req.Username, req.Password)
		}
	}

	proxyCases := []struct {
		name string
		json string
		want string
	}{
		{"ghproxy camelCase", `{"githubProxy":"https://gh.example.com"}`, "https://gh.example.com"},
		{"ghproxy snake_case", `{"github_proxy":"https://gh2.example.com"}`, "https://gh2.example.com"},
	}
	for _, tc := range proxyCases {
		var req TestGithubProxyRequest
		if err := json.Unmarshal([]byte(tc.json), &req); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if req.GithubProxy != tc.want {
			t.Errorf("%s: GithubProxy = %q, want %q", tc.name, req.GithubProxy, tc.want)
		}
	}
}

// TestRequestRequiredFields 必填字段缺失须报错，对齐 serde derive 对无默认值 String 的拒绝。
// TestRequestRequiredFields asserts a missing required field errors out,
// mirroring serde's rejection of absent no-default String fields.
func TestRequestRequiredFields(t *testing.T) {
	if err := json.Unmarshal([]byte(`{"username":"admin","password":"p"}`), &TestIkuaiLoginRequest{}); err == nil {
		t.Error("missing base_url should fail decoding")
	}
	if err := json.Unmarshal([]byte(`{}`), &TestGithubProxyRequest{}); err == nil {
		t.Error("missing github proxy should fail decoding")
	}
}

// TestResultJSONShape ok/message 键名是前端契约。
// TestResultJSONShape pins the ok/message keys as the frontend contract.
func TestResultJSONShape(t *testing.T) {
	b, err := json.Marshal(TestResult{OK: true, Message: "OK"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	if _, ok := m["ok"]; !ok {
		t.Errorf("json %s missing key ok", b)
	}
	if _, ok := m["message"]; !ok {
		t.Errorf("json %s missing key message", b)
	}
	if _, ok := m["OK"]; ok {
		t.Errorf("json %s leaked Go field name OK", b)
	}
}

// newFakeIkuai 构造记录调用序列的假爱快服务：登录返回 code 0，
// /Action/call 按 func_name 记账并返回空 data（无可删行）。
// newFakeIkuai builds a fake iKuai that records the call sequence: login
// answers code 0 while /Action/call records func_name and returns empty data
// (nothing to delete).
func newFakeIkuai(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Action/login":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		case "/Action/call":
			var req struct {
				FuncName string `json:"func_name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode call body: %v", err)
			}
			mu.Lock()
			calls = append(calls, req.FuncName)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"message":"ok","results":{"total":0,"data":[]}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// directProxyConfig 返回直连语义的代理配置（system 模式对回环地址永不走代理）。
// directProxyConfig returns a proxy config with direct semantics (system mode
// never proxies loopback addresses).
func directProxyConfig() *config.ProxyConfig {
	return &config.ProxyConfig{Mode: config.ProxyModeSystem}
}

// TestNormalizeBaseURL 空串直通、已含协议直通、其余补 http://（url.rs L1-10）。
// TestNormalizeBaseURL: empty passthrough, scheme passthrough, else prepend
// http:// (url.rs L1-10).
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"192.168.1.1", "http://192.168.1.1"},
		{"  192.168.1.1:80  ", "http://192.168.1.1:80"},
		{"https://192.168.1.1", "https://192.168.1.1"},
		{"http://192.168.1.1", "http://192.168.1.1"},
	}
	for _, tc := range cases {
		if got := NormalizeBaseURL(tc.in); got != tc.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFetchGithubReleasesOK 解码与请求头契约（github.rs L19-60）：UA ikb-core、
// Accept vnd.github+json，必填字段齐备的数组解出条目，Option 字段缺省为空串。
// TestFetchGithubReleasesOK pins the decode and header contract (github.rs
// L19-60): UA ikb-core, Accept vnd.github+json, a well-formed array decodes,
// and absent optional fields land on the empty string.
func TestFetchGithubReleasesOK(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"tag_name":"v1.2.3","name":null,"prerelease":false,"draft":false,"html_url":"https://github.com/x/v1.2.3","published_at":null,"created_at":"2026-08-01T00:00:00Z"},
{"tag_name":"v2.0.0","name":"二号线","prerelease":true,"draft":false,"html_url":"https://github.com/x/v2.0.0","published_at":"2026-08-02T00:00:00Z"}]`)
	}))
	defer srv.Close()

	releases, err := fetchGithubReleasesFrom(srv.URL, directProxyConfig())
	if err != nil {
		t.Fatalf("fetchGithubReleasesFrom: %v", err)
	}
	if gotUA != "ikb-core" {
		t.Errorf("User-Agent = %q, want ikb-core", gotUA)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", gotAccept)
	}
	if len(releases) != 2 {
		t.Fatalf("len(releases) = %d, want 2", len(releases))
	}
	first := releases[0]
	if first.TagName != "v1.2.3" || first.Name != "" || first.Prerelease || first.Draft ||
		first.HTMLURL != "https://github.com/x/v1.2.3" || first.PublishedAt != "" ||
		first.CreatedAt != "2026-08-01T00:00:00Z" {
		t.Errorf("releases[0] = %+v", first)
	}
	second := releases[1]
	if second.Name != "二号线" || !second.Prerelease || second.PublishedAt != "2026-08-02T00:00:00Z" {
		t.Errorf("releases[1] = %+v", second)
	}
}

// TestFetchGithubReleasesErrors 非 2xx 与坏载荷的英文错误格式（github.rs L42-59）。
// TestFetchGithubReleasesErrors pins the English error formats for non-2xx and
// malformed payloads (github.rs L42-59).
func TestFetchGithubReleasesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "upstream\nbusy")
	}))
	defer srv.Close()

	_, err := fetchGithubReleasesFrom(srv.URL, directProxyConfig())
	if err == nil {
		t.Fatal("want error for 503")
	}
	want := "HTTP 503 Service Unavailable url='" + srv.URL + "' body='upstream busy'"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"tag_name":"v1"}]`)
	}))
	defer srv2.Close()
	if _, err := fetchGithubReleasesFrom(srv2.URL, directProxyConfig()); err == nil {
		t.Error("missing required release fields should fail decoding")
	}

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not-json`)
	}))
	defer srv3.Close()
	if _, err := fetchGithubReleasesFrom(srv3.URL, directProxyConfig()); err == nil ||
		!strings.HasPrefix(err.Error(), "Failed to decode response") {
		t.Errorf("decode error = %v, want Failed to decode response prefix", err)
	}
}

// TestFetchRemoteConfig 直拉、ghproxy 改写与错误格式（fetch.rs L5-36）。
// TestFetchRemoteConfig covers direct fetches, ghproxy rewrites and the error
// formats (fetch.rs L5-36).
func TestFetchRemoteConfig(t *testing.T) {
	if _, err := FetchRemoteConfig("   ", directProxyConfig(), ""); err == nil ||
		err.Error() != "Remote URL is empty" {
		t.Errorf("empty url err = %v, want Remote URL is empty", err)
	}

	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		io.WriteString(w, "raw-config-body")
	}))
	defer srv.Close()

	text, err := FetchRemoteConfig(srv.URL+"/cfg.yml", directProxyConfig(), "")
	if err != nil {
		t.Fatalf("FetchRemoteConfig: %v", err)
	}
	if text != "raw-config-body" {
		t.Errorf("text = %q", text)
	}
	if gotUA != "ikb-core" {
		t.Errorf("User-Agent = %q, want ikb-core", gotUA)
	}
	if gotPath != "/cfg.yml" {
		t.Errorf("path = %q, want /cfg.yml", gotPath)
	}

	// smart + ghproxy 命中 github 源：URL 改写为前缀拼接并强制直连（PlanRuleFetch）。
	// smart + ghproxy on a github source: the URL is rewritten as a prefix join
	// and forced direct (PlanRuleFetch).
	gh := &config.ProxyConfig{Mode: config.ProxyModeSmart}
	text, err = FetchRemoteConfig("https://github.com/FelixJI/iKuai-Toolbox/raw/main/config.yml",
		gh, srv.URL+"/ghp")
	if err != nil {
		t.Fatalf("FetchRemoteConfig via ghproxy: %v", err)
	}
	if text != "raw-config-body" {
		t.Errorf("ghproxy text = %q", text)
	}
	wantPath := "/ghp/https://github.com/FelixJI/iKuai-Toolbox/raw/main/config.yml"
	if gotPath != wantPath {
		t.Errorf("ghproxy path = %q, want %q", gotPath, wantPath)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv2.Close()
	if _, err := FetchRemoteConfig(srv2.URL+"/missing", directProxyConfig(), ""); err == nil ||
		err.Error() != "HTTP 404 Not Found" {
		t.Errorf("http err = %v, want HTTP 404 Not Found", err)
	}
}
