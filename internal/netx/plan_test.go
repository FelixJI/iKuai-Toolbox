// plan_test.go 代理规划层测试，行为对齐 crates/core/src/net.rs（175 行）。
// 每个期望值均标注 net.rs 行号推导依据；测试先于实现编写（TDD）。
// plan_test.go proxy planning tests, behavior mirrors crates/core/src/net.rs (175 lines).
// Every expected value cites the net.rs lines it derives from; tests were written before the implementation (TDD).
package netx

import (
	"net/http"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// matrixNet 用矩阵行构造 NetConfig（mode 取规范名，与 ProxyMode 常量一致）。
// matrixNet builds a NetConfig from a matrix row (modes are the canonical ProxyMode values).
func matrixNet(mode, proxyURL, ghproxy string) NetConfig {
	return NetConfigFromParts(&config.ProxyConfig{Mode: config.ProxyMode(mode), URL: proxyURL}, ghproxy)
}

// TestPlanRuleFetchMatrix 决策矩阵，前四行为任务书 Step 1 指定用例，其余从 net.rs L70-107 推导。
// TestPlanRuleFetchMatrix decision matrix; the first four rows are the cases mandated by
// the task brief, the rest derive from net.rs L70-107.
func TestPlanRuleFetchMatrix(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		proxyURL  string
		ghproxy   string
		url       string
		wantURL   string
		wantProxy ProxyChoice
		wantGh    bool
	}{
		// 任务书 Step 1 矩阵（net.rs L75-76、L84-99）。
		{"smart+ghproxy+github源", "smart", "", "https://gh.x/", "https://raw.githubusercontent.com/a/b", "https://gh.x/https://raw.githubusercontent.com/a/b", ProxyDirect, true},
		{"smart+ghproxy+非github源", "smart", "", "https://gh.x/", "https://example.com/l.txt", "https://example.com/l.txt", ProxySystem, false},
		{"smart无ghproxy", "smart", "", "", "https://example.com/l.txt", "https://example.com/l.txt", ProxySystem, false},
		{"custom", "custom", "", "", "https://example.com/l.txt", "https://example.com/l.txt", ProxyCustom, false},
		// system 模式恒走系统代理且不改写 URL（net.rs L86）。
		{"system不改写", "system", "", "https://gh.x/", "https://raw.githubusercontent.com/a/b", "https://raw.githubusercontent.com/a/b", ProxySystem, false},
		// github.com 前缀同样命中 ghproxy 改写（net.rs L46）。
		{"smart+ghproxy+github.com源", "smart", "", "https://gh.x/", "https://github.com/owner/repo", "https://gh.x/https://github.com/owner/repo", ProxyDirect, true},
		// 前缀无尾斜杠时 join 补斜杠（net.rs L60-66）。
		{"smart+ghproxy无尾斜杠", "smart", "", "https://gh.x", "https://github.com/a/b", "https://gh.x/https://github.com/a/b", ProxyDirect, true},
		// ghproxy 无协议时补 https://（net.rs L49-58）。
		{"smart+ghproxy无协议", "smart", "", "gh.x/", "https://raw.githubusercontent.com/a/b", "https://gh.x/https://raw.githubusercontent.com/a/b", ProxyDirect, true},
		// ghproxy 两端空白先 trim（net.rs L73、L61）。
		{"smart+ghproxy空白包裹", "smart", "", "  https://gh.x  ", "https://github.com/a/b", "https://gh.x/https://github.com/a/b", ProxyDirect, true},
		// can_use_ghproxy 不看 proxy_url，且 Direct 分支先于 proxy_url 分支（net.rs L75-76、L88-91）。
		{"smart+ghproxy优先于自定义代理", "smart", "http://127.0.0.1:7890", "https://gh.x/", "https://github.com/a/b", "https://gh.x/https://github.com/a/b", ProxyDirect, true},
		// smart 无改写且配置了自定义代理时走 Custom（net.rs L92-98）。
		{"smart+自定义代理+非github源", "smart", "http://127.0.0.1:7890", "", "https://example.com/l.txt", "https://example.com/l.txt", ProxyCustom, false},
		// http 协议的 github 不命中 ghproxy（net.rs L45-47 只认 https 前缀）。
		{"smart+ghproxy+http协议github源", "smart", "", "https://gh.x/", "http://github.com/a/b", "http://github.com/a/b", ProxySystem, false},
		// 原始 URL 两端空白先 trim（net.rs L71、L65）。
		{"url空白被trim", "smart", "", "", "  https://example.com/l.txt  ", "https://example.com/l.txt", ProxySystem, false},
	}
	for _, tc := range cases {
		got := PlanRuleFetch(matrixNet(tc.mode, tc.proxyURL, tc.ghproxy), tc.url)
		if got.URL != tc.wantURL || got.Proxy != tc.wantProxy || got.UsedGithubProxy != tc.wantGh {
			t.Errorf("%s: PlanRuleFetch(mode=%q, proxyURL=%q, ghproxy=%q, url=%q) = %+v, want {URL:%q Proxy:%v UsedGithubProxy:%v}",
				tc.name, tc.mode, tc.proxyURL, tc.ghproxy, tc.url, got, tc.wantURL, tc.wantProxy, tc.wantGh)
		}
	}
}

// TestPlanGithubAPINeverRewrites 对齐 net.rs L111-132：GitHub API 规划从不改写 URL、从不使用 ghproxy。
// TestPlanGithubAPINeverRewrites mirrors net.rs L111-132: GitHub API plans never rewrite the URL and never use ghproxy.
func TestPlanGithubAPINeverRewrites(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		proxyURL  string
		ghproxy   string
		url       string
		wantProxy ProxyChoice
	}{
		// 即使 smart+ghproxy，api.github.com 也不被改写（proxy_url 为空回退 System，L118-123）。
		{"smart+ghproxy+api不改写", "smart", "", "https://gh.x/", "https://api.github.com/repos/o/r/releases/latest", ProxySystem},
		// github.com 前缀在规则下载中会改写，在 API 规划中同样不改写（L126-128 直接 url.to_string）。
		{"smart+ghproxy+github.com也不改写", "smart", "", "https://gh.x/", "https://github.com/owner/repo", ProxySystem},
		// smart 且配置自定义代理走 Custom（L119-122）。
		{"smart+自定义代理", "smart", "http://127.0.0.1:7890", "https://gh.x/", "https://api.github.com/repos/o/r/releases", ProxyCustom},
		{"custom", "custom", "", "", "https://api.github.com/repos/o/r/releases", ProxyCustom},
		{"system", "system", "", "https://gh.x/", "https://api.github.com/repos/o/r/releases", ProxySystem},
	}
	for _, tc := range cases {
		got := PlanGithubAPI(matrixNet(tc.mode, tc.proxyURL, tc.ghproxy), tc.url)
		if got.URL != tc.url || got.Proxy != tc.wantProxy || got.UsedGithubProxy {
			t.Errorf("%s: PlanGithubAPI(mode=%q, proxyURL=%q, ghproxy=%q, url=%q) = %+v, want {URL:%q Proxy:%v UsedGithubProxy:false}",
				tc.name, tc.mode, tc.proxyURL, tc.ghproxy, tc.url, got, tc.url, tc.wantProxy)
		}
	}
}

// TestIsGithubURLForGhproxy 对齐 net.rs L44-47：trim 后仅认 https 的两个前缀。
// TestIsGithubURLForGhproxy mirrors net.rs L44-47: after trimming, only the two https prefixes match.
func TestIsGithubURLForGhproxy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://raw.githubusercontent.com/a/b", true},
		{"https://github.com/a", true},
		{"  https://github.com/a  ", true},
		{"", false},
		// 前缀含尾斜杠，裸域不算（net.rs L46 的 "https://github.com/"）。
		{"https://github.com", false},
		{"http://github.com/a", false},
		{"http://raw.githubusercontent.com/a", false},
		{"https://api.github.com/x", false},
		{"https://github.com.evil.com/a", false},
		{"https://gitlab.com/a", false},
	}
	for _, tc := range cases {
		if got := IsGithubURLForGhproxy(tc.in); got != tc.want {
			t.Errorf("IsGithubURLForGhproxy(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeURLPrefix 对齐 net.rs L49-58：trim、空串直通、含 :// 直通、否则补 https://。
// TestNormalizeURLPrefix mirrors net.rs L49-58: trim, empty passthrough, contains "://" passthrough, else prepend https://.
func TestNormalizeURLPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"gh.x", "https://gh.x"},
		{"gh.x/", "https://gh.x/"},
		{"http://gh.x", "http://gh.x"},
		{"https://gh.x", "https://gh.x"},
		{"  gh.x  ", "https://gh.x"},
		{"   ", ""},
		// Rust contains("://") 只看是否包含，"://weird" 原样返回（net.rs L54-56）。
		{"://weird", "://weird"},
	}
	for _, tc := range cases {
		if got := NormalizeURLPrefix(tc.in); got != tc.want {
			t.Errorf("NormalizeURLPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNetConfigFromConfigAndParts 对齐 net.rs L28-42：字段原样拷贝（不 trim）。
// TestNetConfigFromConfigAndParts mirrors net.rs L28-42: fields are copied verbatim (no trimming).
func TestNetConfigFromConfigAndParts(t *testing.T) {
	cfg := &config.Config{
		GithubProxy: "https://gh.x/",
		Proxy: config.ProxyConfig{
			Mode: config.ProxyModeCustom,
			URL:  " http://127.0.0.1:7890 ",
			User: " u ",
			Pass: " p ",
		},
	}
	want := NetConfig{
		Mode:        config.ProxyModeCustom,
		ProxyURL:    " http://127.0.0.1:7890 ",
		ProxyUser:   " u ",
		ProxyPass:   " p ",
		GithubProxy: "https://gh.x/",
	}
	if got := NetConfigFromConfig(cfg); got != want {
		t.Errorf("NetConfigFromConfig = %+v, want %+v", got, want)
	}
	if got := NetConfigFromParts(&cfg.Proxy, cfg.GithubProxy); got != want {
		t.Errorf("NetConfigFromParts = %+v, want %+v", got, want)
	}
}

// proxyOf 取出 Transport 并调用其 Proxy 函数，返回代理 URL 字符串（nil 代理返回空串）。
// proxyOf extracts the Transport and invokes its Proxy func, returning the proxy URL
// as a string (an empty string for a nil proxy).
func proxyOf(t *testing.T, hc *http.Client, rawurl string) string {
	t.Helper()
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", hc.Transport)
	}
	if tr.Proxy == nil {
		// Transport.Proxy 为 nil 即直连（net/http 语义），不调用 nil 函数字段。
		// A nil Transport.Proxy means direct connection (net/http semantics); never call the nil func field.
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(%s): %v", rawurl, err)
	}
	if u == nil {
		return ""
	}
	return u.String()
}

// TestApplyProxyChoiceDirect 对齐 net.rs L141-143：Direct 显式禁用代理，忽略环境变量。
// TestApplyProxyChoiceDirect mirrors net.rs L141-143: Direct explicitly disables the proxy, ignoring env vars.
func TestApplyProxyChoiceDirect(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	t.Setenv("NO_PROXY", "")
	hc := &http.Client{}
	ApplyProxyChoice(hc, NetConfig{}, ProxyDirect)
	if hc.Transport == nil {
		t.Fatal("Direct: Transport must be replaced, got nil")
	}
	if got := proxyOf(t, hc, "https://example.com/dir"); got != "" {
		t.Errorf("Direct proxy = %q, want empty (no proxy even with HTTPS_PROXY set)", got)
	}
}

// TestApplyProxyChoiceSystem 对齐 net.rs L145-147：System 保持默认行为，读取环境代理变量。
// TestApplyProxyChoiceSystem mirrors net.rs L145-147: System keeps the default behavior of honoring env proxies.
func TestApplyProxyChoiceSystem(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	t.Setenv("NO_PROXY", "")
	hc := &http.Client{}
	ApplyProxyChoice(hc, NetConfig{}, ProxySystem)
	if hc.Transport == nil {
		t.Fatal("System: Transport must be replaced, got nil")
	}
	if got := proxyOf(t, hc, "https://example.com/dir"); got != "http://env-proxy:8080" {
		t.Errorf("System proxy = %q, want http://env-proxy:8080 from HTTPS_PROXY", got)
	}
}

// TestApplyProxyChoiceCustom 对齐 net.rs L149-172：显式代理 URL、默认 7890、basic auth、禁用系统代理。
// TestApplyProxyChoiceCustom mirrors net.rs L149-172: explicit proxy URL, 7890 default, basic auth, system proxies disabled.
func TestApplyProxyChoiceCustom(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	t.Setenv("NO_PROXY", "")

	hc := &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "http://127.0.0.1:7890"}, ProxyCustom)
	if got := proxyOf(t, hc, "https://example.com/dir"); got != "http://127.0.0.1:7890" {
		t.Errorf("Custom explicit proxy = %q, want http://127.0.0.1:7890 (env must be ignored)", got)
	}

	// proxy_url 为空白时回退默认 7890（net.rs L150-156 先 trim 再判空）。
	hc = &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "   "}, ProxyCustom)
	if got := proxyOf(t, hc, "https://example.com/dir"); got != "http://127.0.0.1:7890" {
		t.Errorf("Custom default proxy = %q, want http://127.0.0.1:7890", got)
	}

	// user 非空时携带 basic auth，user/pass 先 trim（net.rs L160-165）。
	hc = &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "http://p.example:3128", ProxyUser: " u1 ", ProxyPass: " p1 "}, ProxyCustom)
	u := proxyUser(t, hc, "https://example.com/dir")
	if u != "u1:p1" {
		t.Errorf("Custom basic auth = %q, want u1:p1", u)
	}

	// 仅 user 非空时密码为空串仍然携带（Rust basic_auth(user, "") 语义）。
	hc = &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "http://p.example:3128", ProxyUser: "u2"}, ProxyCustom)
	if u := proxyUser(t, hc, "https://example.com/dir"); u != "u2:" {
		t.Errorf("Custom user-only auth = %q, want u2:", u)
	}

	// user 为空时不携带认证信息。
	hc = &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "http://p.example:3128", ProxyPass: "p3"}, ProxyCustom)
	if u := proxyUser(t, hc, "https://example.com/dir"); u != "" {
		t.Errorf("Custom no-user auth = %q, want empty", u)
	}

	// 代理 URL 不可解析时回退直连（Go 签名无错误返回，取舍见 plan.go）。
	hc = &http.Client{}
	ApplyProxyChoice(hc, NetConfig{ProxyURL: "http://[::1"}, ProxyCustom)
	if got := proxyOf(t, hc, "https://example.com/dir"); got != "" {
		t.Errorf("Custom invalid proxy = %q, want empty (direct fallback)", got)
	}
}

// proxyUser 校验代理 URL 的 userinfo（user:password 形式；无认证返回空串）。
// proxyUser checks the proxy URL userinfo in user:password form (empty when absent).
func proxyUser(t *testing.T, hc *http.Client, rawurl string) string {
	t.Helper()
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", hc.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("expected a Proxy func carrying userinfo, got nil")
	}
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(%s): %v", rawurl, err)
	}
	if u == nil || u.User == nil {
		return ""
	}
	pass, _ := u.User.Password()
	return u.User.Username() + ":" + pass
}
