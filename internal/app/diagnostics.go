// diagnostics.go 登录/代理连通性测试与诊断报告，行为对齐 crates/core/src/app/diagnostics.rs（491 行）。
// 密码只以 (set,len=N) 形式出现在报告中，绝不回传明文；cron 归一化后给出下次触发时刻；
// 规则 URL 以 Range bytes=0-2047 抽样探测。
// Login/proxy connectivity probes and the diagnostics report, aligned with
// crates/core/src/app/diagnostics.rs (491 lines). Passwords only ever appear as
// (set,len=N); the cron expression is normalized with its next fire time; rule
// URLs are sampled with a Range bytes=0-2047 probe.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
	"github.com/FelixJI/iKuai-Toolbox/internal/netx"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
	"github.com/FelixJI/iKuai-Toolbox/internal/update"
)

// TestResult 连通性测试结果，JSON 键 ok/message 为前端契约（diagnostics.rs L12-16）。
// TestResult is a connectivity probe result; the ok/message JSON keys are a
// frontend contract (diagnostics.rs L12-16).
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// TestIkuaiLoginRequest 登录测试入参；JSON 同时接受 baseUrl 与 base_url
// （前端 Tauri 走 camelCase、HTTP 走 snake_case，diagnostics.rs L18-24 的 serde alias）。
// TestIkuaiLoginRequest is the login-probe input; JSON accepts both baseUrl and
// base_url (the Tauri frontend sends camelCase while HTTP sends snake_case,
// mirroring the serde alias of diagnostics.rs L18-24).
type TestIkuaiLoginRequest struct {
	BaseURL  string
	Username string
	Password string
}

// UnmarshalJSON 双命名直解：base_url 与 baseUrl 指向同一字段，二者同时出现视为
// 重复字段，任一必填字段缺失均报错（对齐 serde alias + 无默认值 String 的拒绝语义）。
// UnmarshalJSON decodes both spellings directly: base_url and baseUrl target the
// same field, both present counts as a duplicate, and any missing required
// field errors out (mirroring serde aliases plus the no-default String rejection).
func (r *TestIkuaiLoginRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		BaseURLSnake *string `json:"base_url"`
		BaseURLCamel *string `json:"baseUrl"`
		Username     *string `json:"username"`
		Password     *string `json:"password"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.BaseURLSnake != nil && wire.BaseURLCamel != nil {
		return fmt.Errorf("duplicate field base_url")
	}
	if wire.BaseURLSnake == nil && wire.BaseURLCamel == nil {
		return fmt.Errorf("missing field base_url")
	}
	if wire.Username == nil {
		return fmt.Errorf("missing field username")
	}
	if wire.Password == nil {
		return fmt.Errorf("missing field password")
	}
	if wire.BaseURLSnake != nil {
		r.BaseURL = *wire.BaseURLSnake
	} else {
		r.BaseURL = *wire.BaseURLCamel
	}
	r.Username = *wire.Username
	r.Password = *wire.Password
	return nil
}

// TestGithubProxyRequest ghproxy 连通性测试入参；接受 githubProxy 与 github_proxy 两种命名。
// TestGithubProxyRequest is the ghproxy probe input; both githubProxy and
// github_proxy spellings are accepted.
type TestGithubProxyRequest struct {
	GithubProxy string
}

// UnmarshalJSON 双命名直解，语义与 TestIkuaiLoginRequest 一致（diagnostics.rs L26-30）。
// UnmarshalJSON decodes both spellings directly with the same semantics as
// TestIkuaiLoginRequest (diagnostics.rs L26-30).
func (r *TestGithubProxyRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Snake *string `json:"github_proxy"`
		Camel *string `json:"githubProxy"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Snake != nil && wire.Camel != nil {
		return fmt.Errorf("duplicate field github_proxy")
	}
	if wire.Snake == nil && wire.Camel == nil {
		return fmt.Errorf("missing field github_proxy")
	}
	if wire.Snake != nil {
		r.GithubProxy = *wire.Snake
	} else {
		r.GithubProxy = *wire.Camel
	}
	return nil
}

// githubProxyProbeURL ghproxy 探测目标（diagnostics.rs L71-72）：仓库 main 分支 .gitignore。
// githubProxyProbeURL is the ghproxy probe target (diagnostics.rs L71-72): the
// repository's main-branch .gitignore.
const githubProxyProbeURL = "https://raw.githubusercontent.com/FelixJI/iKuai-Toolbox/refs/heads/main/.gitignore"

// ghProxyProbeUA ghproxy 站点可能限制非常见 UA，使用桌面 Chrome UA（diagnostics.rs L89-90）。
// ghProxyProbeUA: some ghproxy sites restrict uncommon user agents, so a
// desktop Chrome UA is used (diagnostics.rs L89-90).
const ghProxyProbeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// CoreVersion 报告中的 ikb-core 版本行；Rust 侧取 env!("CARGO_PKG_VERSION")
// （crates/core 4.4.109），Go 主线版本对齐 cmd/ikuai-bypass 占位值，
// Task 9 CLI 落地时统一为单一来源。
// CoreVersion feeds the ikb-core report line; the Rust side reads
// env!("CARGO_PKG_VERSION") (crates/core 4.4.109) while the Go line matches the
// cmd/ikuai-bypass placeholder, to be unified into one source in Task 9.
const CoreVersion = "5.0.0-go.1"

// TestIkuaiLogin 登录连通性测试（diagnostics.rs L32-68）：URL 归一化与用户名
// 前置校验后真实登录一次，错误文案英文原样透出。
// TestIkuaiLogin probes iKuai login connectivity (diagnostics.rs L32-68):
// after URL normalization and the username guard it performs one real login,
// surfacing English error texts verbatim.
func TestIkuaiLogin(req TestIkuaiLoginRequest) TestResult {
	baseURL := NormalizeBaseURL(req.BaseURL)
	username := strings.TrimSpace(req.Username)
	if baseURL == "" {
		return TestResult{OK: false, Message: "Empty iKuai URL"}
	}
	if username == "" {
		return TestResult{OK: false, Message: "Empty username"}
	}

	api, err := ikuai.NewIKuaiClient(baseURL)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	if err := api.Login(username, req.Password); err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	return TestResult{OK: true, Message: "OK"}
}

// TestGithubProxy ghproxy 直连探测（diagnostics.rs L70-168）：前缀拼接探测 URL、
// 强制直连（no_proxy，避免其他代理干扰）、空响应/HTML 误回/非 2xx 均判失败。
// TestGithubProxy probes a ghproxy prefix over a direct connection
// (diagnostics.rs L70-168): the probe URL joins behind the prefix, the client
// forces direct (no_proxy, avoiding proxy interference), and empty bodies,
// HTML misdetections and non-2xx all count as failures.
func TestGithubProxy(req TestGithubProxyRequest) TestResult {
	ghproxy := netx.NormalizeURLPrefix(req.GithubProxy)
	if ghproxy == "" {
		return TestResult{OK: false, Message: "Empty github proxy"}
	}

	finalURL := ghproxy + githubProxyProbeURL
	if !strings.HasSuffix(ghproxy, "/") {
		finalURL = ghproxy + "/" + githubProxyProbeURL
	}

	// ghproxy 测试必须直连，避免被其他代理干扰。
	// The ghproxy test must stay direct to avoid proxy interference.
	hc := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	hreq, err := http.NewRequest(http.MethodGet, finalURL, nil)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	hreq.Header.Set("User-Agent", ghProxyProbeUA)
	resp, err := hc.Do(hreq)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return TestResult{OK: false, Message: fmt.Sprintf("HTTP %s url='%s'%s",
			resp.Status, finalURL, bodyHint(body, 200))}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return TestResult{OK: false, Message: fmt.Sprintf("Empty response url='%s'", finalURL)}
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		runes := []rune(trimmed)
		out := trimmed
		if len(runes) > 200 {
			out = string(runes[:200]) + "..."
		}
		return TestResult{OK: false, Message: fmt.Sprintf("Unexpected HTML url='%s' body='%s'",
			finalURL, strings.ReplaceAll(out, "\n", " "))}
	}
	return TestResult{OK: true, Message: fmt.Sprintf("OK url='%s'", finalURL)}
}

// DiagnosticsReport 诊断报告（diagnostics.rs L170-174）。
// DiagnosticsReport is the diagnostics report (diagnostics.rs L170-174).
type DiagnosticsReport struct {
	GeneratedAt string `json:"generated_at"`
	Text        string `json:"text"`
}

// maskSecret 密码脱敏（diagnostics.rs L176-184）：空 => (empty)，非空 => (set,len=rune 数)，
// 永不回传明文。
// maskSecret masks secrets (diagnostics.rs L176-184): blank => (empty),
// otherwise (set,len=rune count); plaintext never round-trips.
func maskSecret(s string) string {
	v := strings.TrimSpace(s)
	if v == "" {
		return "(empty)"
	}
	return fmt.Sprintf("(set,len=%d)", utf8.RuneCountInString(v))
}

// reportCronParser 报告侧 cron 解析器：与 internal/runtime 同配置的
// 6 段含秒 + 描述符解析器（Rust 侧直接使用 cron crate 的 Schedule）。
// reportCronParser is the report-side cron parser: the same 6-field
// second-capable descriptor parser as internal/runtime (the Rust side just
// uses the cron crate's Schedule).
var reportCronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// normalizeCronExprForReport 报告侧 cron 归一化（diagnostics.rs L186-207）：
// 依次尝试 raw、"0 raw"（5 段）、"0 raw *"（5 段补年份）、"raw *"（6 段补年份），
// 首个可解析者胜出。
// normalizeCronExprForReport normalizes cron for the report (diagnostics.rs
// L186-207): try raw, "0 raw" (5 fields), "0 raw *" (5 fields plus year) and
// "raw *" (6 fields plus year) in order; the first parseable candidate wins.
func normalizeCronExprForReport(expr string) (string, error) {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return "", errors.New("cron expression is empty")
	}
	parts := strings.Fields(raw)
	candidates := []string{raw}
	if len(parts) == 5 {
		candidates = append(candidates, "0 "+raw, "0 "+raw+" *")
	}
	if len(parts) == 6 {
		candidates = append(candidates, raw+" *")
	}
	for _, c := range candidates {
		if _, err := reportCronParser.Parse(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("Invalid cron expression")
}

// urlProbe 一条规则 URL 抽样探测（diagnostics.rs L209-213）。
// urlProbe is one sampled rule-URL probe (diagnostics.rs L209-213).
type urlProbe struct {
	label string
	url   string
}

// urlProbeResult 探测结果（diagnostics.rs L215-223）。
// urlProbeResult is the probe outcome (diagnostics.rs L215-223).
type urlProbeResult struct {
	ok              bool
	status          string
	via             string
	usedGithubProxy bool
	bytes           int
	errMsg          string
}

// probeRuleUrl 规则 URL Range 抽样探测（diagnostics.rs L225-320）：走 PlanRuleFetch
// 规划，Range bytes=0-2047，UA ikb-core，connect 8s / 总 15s。
// probeRuleUrl samples a rule URL with a Range request (diagnostics.rs
// L225-320): planned through PlanRuleFetch with Range bytes=0-2047, UA
// ikb-core, 8s connect / 15s overall.
func probeRuleUrl(cfg *config.Config, originalURL string) urlProbeResult {
	netCfg := netx.NetConfigFromConfig(cfg)
	plan := netx.PlanRuleFetch(netCfg, originalURL)
	via := ""
	switch plan.Proxy {
	case netx.ProxyDirect:
		via = "direct"
	case netx.ProxySystem:
		via = "system"
	case netx.ProxyCustom:
		via = "custom"
	}

	hc := &http.Client{Timeout: 15 * time.Second}
	netx.ApplyProxyChoice(hc, netCfg, plan.Proxy)
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.DialContext = (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}

	req, err := http.NewRequest(http.MethodGet, plan.URL, nil)
	if err != nil {
		return urlProbeResult{via: via, usedGithubProxy: plan.UsedGithubProxy, errMsg: err.Error()}
	}
	req.Header.Set("User-Agent", "ikb-core")
	req.Header.Set("Range", "bytes=0-2047")
	resp, err := hc.Do(req)
	if err != nil {
		return urlProbeResult{via: via, usedGithubProxy: plan.UsedGithubProxy, errMsg: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return urlProbeResult{
			via: via, usedGithubProxy: plan.UsedGithubProxy,
			status: resp.Status,
			errMsg: fmt.Sprintf("HTTP %s%s", resp.Status, bodyHint(body, 160)),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return urlProbeResult{via: via, usedGithubProxy: plan.UsedGithubProxy, status: resp.Status, errMsg: err.Error()}
	}
	return urlProbeResult{
		ok: true, status: resp.Status, via: via,
		usedGithubProxy: plan.UsedGithubProxy, bytes: len(body),
	}
}

// proxyModeLabel 对齐 Rust {:?} 的枚举 Debug 输出（Custom/System/Smart）。
// proxyModeLabel mirrors the Rust {:?} Debug rendering (Custom/System/Smart).
func proxyModeLabel(m config.ProxyMode) string {
	switch m {
	case config.ProxyModeCustom:
		return "Custom"
	case config.ProxyModeSystem:
		return "System"
	case config.ProxyModeSmart:
		return "Smart"
	default:
		return string(m)
	}
}

// loginSourceLabel 登录参数来源标签，对齐 LoginSource 枚举 Debug 输出
// （Cli/Config/Gateway；session.rs 三级优先的复述）。
// loginSourceLabel renders the login-params source like the Rust LoginSource
// Debug output (Cli/Config/Gateway; restating the session.rs priority).
func loginSourceLabel(cliLogin string, cfg *config.Config) string {
	if strings.TrimSpace(cliLogin) != "" {
		return "Cli"
	}
	if strings.TrimSpace(cfg.IkuaiURL) != "" {
		return "Config"
	}
	return "Gateway"
}

// BuildDiagnosticsReport 生成纯文本诊断报告（diagnostics.rs L322-491）：
// 段落顺序 config→rules→cron→runtime（可选）→checks→url-probe；
// 密码一律脱敏，规则 URL 抽样探测验证 proxy/ghproxy 行为。
// BuildDiagnosticsReport renders the plain-text diagnostics report
// (diagnostics.rs L322-491): sections run config→rules→cron→runtime
// (optional)→checks→url-probe; secrets are always masked and rule URLs are
// sampled to validate proxy/ghproxy behavior.
func BuildDiagnosticsReport(cfg *config.Config, configPath string, rt *runtime.RuntimeStatus, cliLogin string) DiagnosticsReport {
	now := time.Now()
	var out strings.Builder
	out.WriteString("IKB Diagnostics Report\n")
	fmt.Fprintf(&out, "generated_at: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&out, "ikb-core: %s\n", CoreVersion)
	out.WriteByte('\n')

	out.WriteString("[config]\n")
	fmt.Fprintf(&out, "path: %s\n", configPath)
	fmt.Fprintf(&out, "ikuai_url: %s\n", strings.TrimSpace(cfg.IkuaiURL))
	fmt.Fprintf(&out, "username: %s\n", strings.TrimSpace(cfg.Username))
	fmt.Fprintf(&out, "password: %s\n", maskSecret(cfg.Password))
	fmt.Fprintf(&out, "cron: %s\n", strings.TrimSpace(cfg.Cron))
	fmt.Fprintf(&out, "run-mode: %s\n", strings.TrimSpace(cfg.RunMode))
	fmt.Fprintf(&out, "mode: %s\n", strings.TrimSpace(cfg.Module))
	fmt.Fprintf(&out, "proxy.mode: %s\n", proxyModeLabel(cfg.Proxy.Mode))
	fmt.Fprintf(&out, "proxy.url: %s\n", strings.TrimSpace(cfg.Proxy.URL))
	fmt.Fprintf(&out, "proxy.user: %s\n", strings.TrimSpace(cfg.Proxy.User))
	fmt.Fprintf(&out, "proxy.pass: %s\n", maskSecret(cfg.Proxy.Pass))
	fmt.Fprintf(&out, "github-proxy: %s\n", strings.TrimSpace(cfg.GithubProxy))
	fmt.Fprintf(&out, "webui.enable: %v\n", cfg.WebUI.Enable)
	fmt.Fprintf(&out, "webui.port: %s\n", strings.TrimSpace(cfg.WebUI.Port))
	fmt.Fprintf(&out, "webui.user: %s\n", strings.TrimSpace(cfg.WebUI.User))
	fmt.Fprintf(&out, "webui.pass: %s\n", maskSecret(cfg.WebUI.Pass))
	out.WriteByte('\n')

	out.WriteString("[rules]\n")
	fmt.Fprintf(&out, "custom-isp: %d\n", len(cfg.CustomIsp))
	fmt.Fprintf(&out, "stream-domain: %d\n", len(cfg.StreamDomain))
	fmt.Fprintf(&out, "ip-group: %d\n", len(cfg.IpGroup))
	fmt.Fprintf(&out, "ipv6-group: %d\n", len(cfg.Ipv6Group))
	fmt.Fprintf(&out, "stream-ipport: %d\n", len(cfg.StreamIpPort))
	out.WriteByte('\n')

	out.WriteString("[cron]\n")
	if norm, err := normalizeCronExprForReport(cfg.Cron); err != nil {
		fmt.Fprintf(&out, "error: %s\n", err)
	} else {
		fmt.Fprintf(&out, "normalized: %s\n", norm)
		if sched, perr := reportCronParser.Parse(norm); perr == nil {
			fmt.Fprintf(&out, "next: %s\n", sched.Next(time.Now()).Format(time.RFC3339))
		}
	}
	out.WriteByte('\n')

	if rt != nil {
		out.WriteString("[runtime]\n")
		fmt.Fprintf(&out, "running: %v\n", rt.Running)
		fmt.Fprintf(&out, "cron_running: %v\n", rt.CronRunning)
		fmt.Fprintf(&out, "module: %s\n", strings.TrimSpace(rt.Module))
		fmt.Fprintf(&out, "cron_expr: %s\n", strings.TrimSpace(rt.CronExpr))
		fmt.Fprintf(&out, "last_run_at: %s\n", strings.TrimSpace(rt.LastRunAt))
		fmt.Fprintf(&out, "next_run_at: %s\n", strings.TrimSpace(rt.NextRunAt))
		out.WriteByte('\n')
	}

	out.WriteString("[checks]\n")
	baseURL, username, password, err := update.ParseLoginParams(cliLogin, cfg)
	if err != nil {
		// 只透出底层文案（对齐 Rust 直接打印 LoginParamsError 的 Display）。
		// Surface the inner text only (Rust prints the LoginParamsError Display directly).
		fmt.Fprintf(&out, "login.resolve: FAIL (%s)\n", err.Msg)
	} else {
		fmt.Fprintf(&out, "login.source: %s\n", loginSourceLabel(cliLogin, cfg))
		fmt.Fprintf(&out, "login.base_url: %s\n", strings.TrimSpace(baseURL))
		fmt.Fprintf(&out, "login.username: %s\n", strings.TrimSpace(username))
		fmt.Fprintf(&out, "login.password: %s\n", maskSecret(password))

		r := TestIkuaiLogin(TestIkuaiLoginRequest{BaseURL: baseURL, Username: username, Password: password})
		verdict := "FAIL"
		if r.OK {
			verdict = "OK"
		}
		fmt.Fprintf(&out, "ikuai.login: %s\n", verdict)
		if !r.OK {
			fmt.Fprintf(&out, "ikuai.login.error: %s\n", r.Message)
		}
	}

	if cfg.Proxy.Mode == config.ProxyModeSmart && strings.TrimSpace(cfg.GithubProxy) != "" {
		r := TestGithubProxy(TestGithubProxyRequest{GithubProxy: cfg.GithubProxy})
		verdict := "FAIL"
		if r.OK {
			verdict = "OK"
		}
		fmt.Fprintf(&out, "github-proxy.test: %s\n", verdict)
		if !r.OK {
			fmt.Fprintf(&out, "github-proxy.error: %s\n", r.Message)
		}
	}

	// 抽样探测少量 URL，用于验证 proxy/ghproxy 行为。
	// Probe a small set of URLs to validate proxy/ghproxy behavior.
	probes := make([]urlProbe, 0, 6)
	for _, it := range cfg.CustomIsp[:min(2, len(cfg.CustomIsp))] {
		probes = append(probes, urlProbe{label: "custom-isp:" + strings.TrimSpace(it.Tag), url: it.URL})
	}
	for _, it := range cfg.StreamDomain[:min(2, len(cfg.StreamDomain))] {
		probes = append(probes, urlProbe{label: "stream-domain:" + strings.TrimSpace(it.Tag), url: it.URL})
	}
	for _, it := range cfg.IpGroup[:min(1, len(cfg.IpGroup))] {
		probes = append(probes, urlProbe{label: "ip-group:" + strings.TrimSpace(it.Tag), url: it.URL})
	}
	for _, it := range cfg.Ipv6Group[:min(1, len(cfg.Ipv6Group))] {
		probes = append(probes, urlProbe{label: "ipv6-group:" + strings.TrimSpace(it.Tag), url: it.URL})
	}

	if len(probes) > 0 {
		out.WriteString("\n[url-probe]\n")
	}
	for _, p := range probes {
		res := probeRuleUrl(cfg, p.url)
		if res.ok {
			fmt.Fprintf(&out, "%s: OK status=%s via=%s ghproxy=%d bytes=%d url='%s'\n",
				p.label, res.status, res.via, b01(res.usedGithubProxy), res.bytes, p.url)
		} else {
			fmt.Fprintf(&out, "%s: FAIL via=%s ghproxy=%d url='%s' error='%s'\n",
				p.label, res.via, b01(res.usedGithubProxy), p.url, strings.ReplaceAll(res.errMsg, "\n", " "))
		}
	}

	return DiagnosticsReport{GeneratedAt: now.Format(time.RFC3339), Text: out.String()}
}

// b01 布尔转报告里的 1/0 标记。
// b01 renders a bool as the report's 1/0 marker.
func b01(b bool) int {
	if b {
		return 1
	}
	return 0
}
