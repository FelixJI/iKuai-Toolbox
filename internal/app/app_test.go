// app_test.go app 服务层测试：请求别名解码 / 更新检查 / 远程拉取 / URL 归一化 /
// 清理顺序 / 配置元信息 / 诊断报告，全部走标准 testing + httptest。
// 行为规格对齐 rust_archive/crates/core/src/app/ 各文件。
// app service-layer tests: request alias decoding / update checks / remote
// fetching / URL normalization / clean ordering / config meta / the
// diagnostics report, all on the standard testing + httptest stack,
// aligned with the files under rust_archive/crates/core/src/app/.
package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
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

// TestCleanOrder 清理顺序固定为 custom_isp→stream_domain→ip_group→ipv6_group→stream_ipport
// （clean.rs L39-68）；ip_group 与 ipv6_group 都走爱快 route_object 功能名。
// TestCleanOrder pins the fixed clean order custom_isp→stream_domain→ip_group→
// ipv6_group→stream_ipport (clean.rs L39-68); ip_group and ipv6_group both hit
// the iKuai route_object func.
func TestCleanOrder(t *testing.T) {
	srv, callsPtr := newFakeIkuai(t)
	cfg := &config.Config{}

	err := RunClean(cfg, srv.URL+",admin,pass", "mytag")
	if err != nil {
		t.Fatalf("RunClean: %v", err)
	}

	want := []string{
		"custom_isp",
		"stream_domain",
		"route_object",
		"route_object",
		"stream_ipport",
	}
	if len(*callsPtr) != len(want) {
		t.Fatalf("calls = %v, want %v", *callsPtr, want)
	}
	for i, fn := range want {
		if (*callsPtr)[i] != fn {
			t.Fatalf("calls = %v, want %v", *callsPtr, want)
		}
	}
}

// TestCleanRequiresTag 空/纯空白 clean_tag 必须报 "Clean mode requires clean_tag"
// （clean.rs L8/L20-23），且不得发起任何登录或 API 调用。
// TestCleanRequiresTag asserts a blank clean_tag yields "Clean mode requires
// clean_tag" (clean.rs L8/L20-23) without any login or API call.
func TestCleanRequiresTag(t *testing.T) {
	srv, callsPtr := newFakeIkuai(t)
	for _, tag := range []string{"", "   "} {
		err := RunClean(&config.Config{}, srv.URL+",admin,pass", tag)
		if err == nil {
			t.Fatalf("RunClean(tag=%q) should fail", tag)
		}
		if err.Error() != "Clean mode requires clean_tag" {
			t.Errorf("err = %q, want Clean mode requires clean_tag", err)
		}
	}
	if len(*callsPtr) != 0 {
		t.Errorf("no API calls expected, got %v", *callsPtr)
	}
}

// TestCleanLoginError 登录失败映射为 clean step login failed（clean.rs L29-37）。
// TestCleanLoginError maps a login failure to "clean step login failed" (clean.rs L29-37).
func TestCleanLoginError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":10000,"message":"bad credentials"}`)
	}))
	defer srv.Close()

	err := RunClean(&config.Config{}, srv.URL+",admin,pass", "mytag")
	if err == nil {
		t.Fatal("RunClean should fail on login error")
	}
	want := "clean step login failed: api error: bad credentials"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// TestCleanBadCliLogin CLI 登录三元组格式错误映射为 login params error（clean.rs L11）。
// TestCleanBadCliLogin maps a malformed CLI login triple to the login-params
// error (clean.rs L11).
func TestCleanBadCliLogin(t *testing.T) {
	_, callsPtr := newFakeIkuai(t)
	err := RunClean(&config.Config{}, "only-two-parts", "mytag")
	if err == nil {
		t.Fatal("RunClean should fail on malformed cli login")
	}
	want := "login params error: command line parameter format error"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if len(*callsPtr) != 0 {
		t.Errorf("no API calls expected, got %v", *callsPtr)
	}
}

// ghFake ghproxy 探测假服务：所有路径返回固定文本，记录 Range/UA 头。
// ghFake is the ghproxy probe fake: every path answers fixed text while the
// Range/UA headers are recorded.
type ghFake struct {
	*httptest.Server
	rangesSeen *[]string
	uasSeen    *[]string
}

// newGhFake 构造 ghFake。
// newGhFake builds a ghFake.
func newGhFake(t *testing.T, body string) *ghFake {
	t.Helper()
	var mu sync.Mutex
	ranges := []string{}
	uas := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		uas = append(uas, r.Header.Get("User-Agent"))
		mu.Unlock()
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &ghFake{Server: srv, rangesSeen: &ranges, uasSeen: &uas}
}

// TestMaskSecret 密码只保留长度信息（diagnostics.rs L176-184）：空 => (empty)，
// 非空 => (set,len=rune 数)。
// TestMaskSecret keeps length info only (diagnostics.rs L176-184): blank =>
// (empty), otherwise (set,len=rune count).
func TestMaskSecret(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "(empty)"},
		{"   ", "(empty)"},
		{"abc", "(set,len=3)"},
		{" 中文密码 ", "(set,len=4)"},
	}
	for _, tc := range cases {
		if got := maskSecret(tc.in); got != tc.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeCronForReport 报告侧 cron 归一化（diagnostics.rs L186-207）：
// 候选序列 raw → "0 raw" → "0 raw *" → "raw *"，首个可解析者胜出。
// TestNormalizeCronForReport covers the report-side cron normalization
// (diagnostics.rs L186-207): the candidate order raw → "0 raw" → "0 raw *" →
// "raw *"; the first parseable candidate wins.
func TestNormalizeCronForReport(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"*/5 * * * *", "0 */5 * * * *"},
		{" 0 */5 * * * * ", "0 */5 * * * *"},
		{"@daily", "@daily"},
		{"@every 1h30m", "@every 1h30m"},
	}
	for _, tc := range cases {
		got, err := normalizeCronExprForReport(tc.in)
		if err != nil {
			t.Errorf("normalizeCronExprForReport(%q) err: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeCronExprForReport(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := normalizeCronExprForReport("not-a-cron"); err == nil ||
		err.Error() != "Invalid cron expression" {
		t.Errorf("invalid cron err = %v, want Invalid cron expression", err)
	}
	if _, err := normalizeCronExprForReport("   "); err == nil ||
		err.Error() != "cron expression is empty" {
		t.Errorf("empty cron err = %v, want cron expression is empty", err)
	}

	sched, err := reportCronParser.Parse("0 */5 * * * *")
	if err != nil {
		t.Fatalf("parse normalized: %v", err)
	}
	if next := sched.Next(time.Now()); !next.After(time.Now()) {
		t.Errorf("next fire %v should be in the future", next)
	}
}

// TestIkuaiLoginProbe 登录连通性测试（diagnostics.rs L32-68）：空 URL/空用户名的前置
// 校验、自动补 http://、成功 OK 与失败携带 api 错误文案。
// TestIkuaiLoginProbe covers the login probe (diagnostics.rs L32-68): the
// empty-URL/empty-username guards, the http:// auto-prefix, the OK success and
// the api-error failure text.
func TestIkuaiLoginProbe(t *testing.T) {
	if r := TestIkuaiLogin(TestIkuaiLoginRequest{}); r.OK || r.Message != "Empty iKuai URL" {
		t.Errorf("empty url = %+v", r)
	}
	if r := TestIkuaiLogin(TestIkuaiLoginRequest{BaseURL: "http://192.168.1.1", Username: "   "}); r.OK || r.Message != "Empty username" {
		t.Errorf("empty username = %+v", r)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	// 无协议头自动补 http://（normalize_base_url）。
	// A missing scheme gets http:// prepended (normalize_base_url).
	r := TestIkuaiLogin(TestIkuaiLoginRequest{BaseURL: srv.URL[len("http://"):], Username: " admin ", Password: "p"})
	if !r.OK || r.Message != "OK" {
		t.Errorf("login probe = %+v, want ok", r)
	}

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":10000,"message":"bad credentials"}`)
	}))
	defer srvFail.Close()
	r = TestIkuaiLogin(TestIkuaiLoginRequest{BaseURL: srvFail.URL, Username: "admin", Password: "p"})
	if r.OK || r.Message != "api error: bad credentials" {
		t.Errorf("login probe fail = %+v, want api error", r)
	}
}

// TestGithubProxyProbe ghproxy 直连探测（diagnostics.rs L70-168）：拼前缀、UA 浏览器、
// HTML 误回检测、非 2xx 摘录与空前缀拒绝。
// TestGithubProxyProbe covers the direct ghproxy probe (diagnostics.rs
// L70-168): prefix joining, the browser UA, the HTML misdetection, the non-2xx
// excerpt and the empty-prefix rejection.
func TestGithubProxyProbe(t *testing.T) {
	if r := TestGithubProxy(TestGithubProxyRequest{GithubProxy: "  "}); r.OK || r.Message != "Empty github proxy" {
		t.Errorf("empty proxy = %+v", r)
	}

	srv := newGhFake(t, "# ignore\n*.log\n")
	// 无尾斜杠前缀按 "{prefix}/{URL}" 拼接 / a prefix without a trailing slash
	// joins as "{prefix}/{URL}".
	r := TestGithubProxy(TestGithubProxyRequest{GithubProxy: srv.URL})
	if !r.OK || r.Message != "OK url='"+srv.URL+"/"+githubProxyProbeURL+"'" {
		t.Errorf("probe = %+v", r)
	}
	// 尾斜杠前缀直接拼接 / a trailing-slash prefix joins verbatim.
	r = TestGithubProxy(TestGithubProxyRequest{GithubProxy: srv.URL + "/"})
	if !r.OK || r.Message != "OK url='"+srv.URL+"/"+githubProxyProbeURL+"'" {
		t.Errorf("trailing slash probe = %+v", r)
	}
	if len(*srv.uasSeen) == 0 || !strings.HasPrefix((*srv.uasSeen)[0], "Mozilla/5.0") {
		t.Errorf("UA seen = %v, want Mozilla/5.0 prefix", *srv.uasSeen)
	}

	htmlSrv := newGhFake(t, "<html><body>blocked</body></html>\n")
	r = TestGithubProxy(TestGithubProxyRequest{GithubProxy: htmlSrv.URL})
	if r.OK || !strings.HasPrefix(r.Message, "Unexpected HTML url='") ||
		!strings.Contains(r.Message, "body='<html>") {
		t.Errorf("html probe = %+v", r)
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "denied\nby proxy")
	}))
	defer errSrv.Close()
	r = TestGithubProxy(TestGithubProxyRequest{GithubProxy: errSrv.URL})
	if r.OK || !strings.HasPrefix(r.Message, "HTTP 403 Forbidden url='") ||
		!strings.Contains(r.Message, "body='denied by proxy'") {
		t.Errorf("http error probe = %+v", r)
	}
}

// TestPasswordMasked 报告文本中出现 (set,len= 而绝不明文（任务书步骤 4 指定用例）。
// TestPasswordMasked asserts the report carries (set,len= and never a plaintext
// secret (the mandatory Step-4 case from the task brief).
func TestPasswordMasked(t *testing.T) {
	loginSrv, _ := newFakeIkuai(t)
	cfg := &config.Config{
		IkuaiURL: loginSrv.URL, Username: "admin", Password: "中文密码",
		Proxy:       config.ProxyConfig{Mode: config.ProxyModeSmart, Pass: "proxy-pw"},
		GithubProxy: "", WebUI: config.WebUiConfig{Enable: true, Port: "19001", User: "wui", Pass: "webui-pw"},
	}
	report := BuildDiagnosticsReport(cfg, "some/config.yml", nil, loginSrv.URL+",admin,中文密码")
	if !strings.Contains(report.Text, "password: (set,len=4)") {
		t.Errorf("report missing masked config password:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "proxy.pass: (set,len=8)") {
		t.Errorf("report missing masked proxy pass:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "webui.pass: (set,len=8)") {
		t.Errorf("report missing masked webui pass:\n%s", report.Text)
	}
	for _, plain := range []string{"中文密码", "proxy-pw", "webui-pw"} {
		if strings.Contains(report.Text, plain) {
			t.Errorf("report leaked plaintext secret %q:\n%s", plain, report.Text)
		}
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		t.Errorf("GeneratedAt %q not RFC3339: %v", report.GeneratedAt, err)
	}
}

// TestBuildDiagnosticsReport 全量报告（diagnostics.rs L322-491）：各段顺序、runtime 段、
// 登录/ghproxy 检查、规则 URL Range 抽样探测与探测顺序。
// TestBuildDiagnosticsReport covers the full report (diagnostics.rs L322-491):
// section order, the runtime block, login/ghproxy checks, the rule-URL Range
// sampling probes and their order.
func TestBuildDiagnosticsReport(t *testing.T) {
	loginSrv, _ := newFakeIkuai(t)
	body := "#ignore\n*.log\n"
	ghSrv := newGhFake(t, body)
	probeURL := "https://raw.githubusercontent.com/a/b/main/list.txt"
	ipGroupURL := "https://github.com/a/b/raw/main/v4.txt"

	cfg := &config.Config{
		IkuaiURL: loginSrv.URL, Username: "admin", Password: "secret-pw",
		Cron: "*/5 * * * *", RunMode: "cronAft", Module: "ispdomain",
		Proxy:       config.ProxyConfig{Mode: config.ProxyModeSmart},
		GithubProxy: ghSrv.URL,
		WebUI:       config.WebUiConfig{Enable: true, Port: "19001", User: "wui", Pass: "webui-pw"},
		CustomIsp:   []config.CustomIspItem{{Tag: "t1", URL: probeURL}},
		IpGroup:     []config.IpGroupItem{{Tag: "g1", URL: ipGroupURL}},
	}
	rt := &runtime.RuntimeStatus{
		Running: true, CronRunning: true, Module: "ispdomain", CronExpr: "0 */5 * * * *",
		LastRunAt: "2026-08-19T10:00:00+08:00", NextRunAt: "2026-08-19T10:05:00+08:00",
	}
	report := BuildDiagnosticsReport(cfg, "some/config.yml", rt, "")

	for _, want := range []string{
		"IKB Diagnostics Report\n",
		"ikb-core: " + CoreVersion + "\n",
		"[config]\n",
		"path: some/config.yml\n",
		"ikuai_url: " + loginSrv.URL + "\n",
		"username: admin\n",
		"cron: */5 * * * *\n",
		"run-mode: cronAft\n",
		"mode: ispdomain\n",
		"proxy.mode: Smart\n",
		"github-proxy: " + ghSrv.URL + "\n",
		"webui.enable: true\n",
		"webui.port: 19001\n",
		"[rules]\n",
		"custom-isp: 1\n",
		"stream-domain: 0\n",
		"ip-group: 1\n",
		"ipv6-group: 0\n",
		"stream-ipport: 0\n",
		"[cron]\n",
		"normalized: 0 */5 * * * *\n",
		"[runtime]\n",
		"running: true\n",
		"cron_running: true\n",
		"cron_expr: 0 */5 * * * *\n",
		"last_run_at: 2026-08-19T10:00:00+08:00\n",
		"[checks]\n",
		"login.source: Config\n",
		"ikuai.login: OK\n",
		"github-proxy.test: OK\n",
		"\n[url-probe]\n",
		"custom-isp:t1: OK status=200 OK via=direct ghproxy=1 bytes=" + strconv.Itoa(len(body)) + " url='" + probeURL + "'\n",
		"ip-group:g1: OK status=200 OK via=direct ghproxy=1 bytes=" + strconv.Itoa(len(body)) + " url='" + ipGroupURL + "'\n",
	} {
		if !strings.Contains(report.Text, want) {
			t.Errorf("report missing %q:\n%s", want, report.Text)
		}
	}

	// next: 行必须是可解析的 RFC3339 未来时刻。
	// The next: line must parse as a future RFC3339 timestamp.
	nextLine := ""
	for _, line := range strings.Split(report.Text, "\n") {
		if strings.HasPrefix(line, "next: ") {
			nextLine = strings.TrimPrefix(line, "next: ")
		}
	}
	if nextLine == "" {
		t.Fatalf("report missing next: line:\n%s", report.Text)
	}
	nextAt, err := time.Parse(time.RFC3339, nextLine)
	if err != nil {
		t.Fatalf("next %q not RFC3339: %v", nextLine, err)
	}
	if !nextAt.After(time.Now().Add(-time.Minute)) {
		t.Errorf("next %v unexpectedly in the past", nextAt)
	}

	// 探测顺序：custom-isp 在 ip-group 之前；ghproxy 探测请求带 Range 与 ikb-core UA。
	// Probe order: custom-isp precedes ip-group; the ghproxy probe carries the
	// Range header and the ikb-core UA.
	if strings.Index(report.Text, "custom-isp:t1:") > strings.Index(report.Text, "ip-group:g1:") {
		t.Errorf("probe order wrong:\n%s", report.Text)
	}
	sawRange, sawUA := false, false
	for _, rg := range *ghSrv.rangesSeen {
		if rg == "bytes=0-2047" {
			sawRange = true
		}
	}
	for _, ua := range *ghSrv.uasSeen {
		if ua == "ikb-core" {
			sawUA = true
		}
	}
	if !sawRange {
		t.Errorf("no probe carried Range bytes=0-2047, saw %v", *ghSrv.rangesSeen)
	}
	if !sawUA {
		t.Errorf("no probe carried UA ikb-core, saw %v", *ghSrv.uasSeen)
	}

	// 密码绝不出现明文 / plaintext secrets never appear.
	if strings.Contains(report.Text, "secret-pw") || strings.Contains(report.Text, "webui-pw") {
		t.Errorf("report leaked a plaintext secret:\n%s", report.Text)
	}
}

// TestBuildDiagnosticsReportDegraded 无网络降级路径：非法 cron、登录参数解析失败、
// 无规则条目时无 [url-probe] 段。
// TestBuildDiagnosticsReportDegraded covers the no-network degraded path: an
// invalid cron, a login-params failure and no [url-probe] section when there
// are no rule entries.
func TestBuildDiagnosticsReportDegraded(t *testing.T) {
	cfg := &config.Config{Cron: "not-a-cron"}
	report := BuildDiagnosticsReport(cfg, "config.yml", nil, "bad-cli-login")
	for _, want := range []string{
		"[cron]\nerror: Invalid cron expression\n",
		"[checks]\nlogin.resolve: FAIL (command line parameter format error)\n",
	} {
		if !strings.Contains(report.Text, want) {
			t.Errorf("report missing %q:\n%s", want, report.Text)
		}
	}
	if strings.Contains(report.Text, "[url-probe]") {
		t.Errorf("no rule entries should yield no url-probe section:\n%s", report.Text)
	}
	if strings.Contains(report.Text, "[runtime]") {
		t.Errorf("nil runtime status should omit the runtime section:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "password: (empty)") {
		t.Errorf("blank password should mask as (empty):\n%s", report.Text)
	}
}

// TestBuildConfigMeta 配置元信息（config_meta.rs L13-34）：config 键展开（flatten）、
// conf_path 绝对化、raw_yaml 原文回读。
// TestBuildConfigMeta covers config meta (config_meta.rs L13-34): flattened
// config keys, the absolute conf_path and the raw_yaml round-trip.
func TestBuildConfigMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	yamlText := "ikuai-url: http://192.168.1.1\nusername: admin\npassword: secret\ncron: \"*/5 * * * *\"\n"
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	meta, err := BuildConfigMeta(cfg, path)
	if err != nil {
		t.Fatalf("BuildConfigMeta: %v", err)
	}
	if meta.ConfPath != path {
		t.Errorf("ConfPath = %q, want %q", meta.ConfPath, path)
	}
	if meta.RawYAML != yamlText {
		t.Errorf("RawYAML = %q, want %q", meta.RawYAML, yamlText)
	}

	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	if m["ikuai-url"] != "http://192.168.1.1" {
		t.Errorf("meta[%q] = %v", "ikuai-url", m["ikuai-url"])
	}
	if m["conf_path"] != path {
		t.Errorf("meta conf_path = %v", m["conf_path"])
	}
	if m["raw_yaml"] != yamlText {
		t.Errorf("meta raw_yaml = %v", m["raw_yaml"])
	}
	if _, ok := m["config"]; ok {
		t.Errorf("config must be flattened, found nested key %s", b)
	}

	// 相对路径以进程 cwd 绝对化（to_abs_path 的 cwd.join 语义）。
	// A relative path resolves against the process cwd (the cwd.join of to_abs_path).
	t.Chdir(dir)
	metaRel, err := BuildConfigMeta(cfg, "config.yml")
	if err != nil {
		t.Fatalf("BuildConfigMeta relative: %v", err)
	}
	if want := filepath.Join(dir, "config.yml"); metaRel.ConfPath != want {
		t.Errorf("ConfPath = %q, want %q", metaRel.ConfPath, want)
	}
}
