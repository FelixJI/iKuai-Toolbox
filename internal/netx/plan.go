// plan.go 网络代理规划层，行为逐行对齐 rust_archive/crates/core/src/net.rs（175 行）。
// 行号引用均指向 net.rs；http.Client 的代理通过替换 Transport 实现（reqwest builder 语义）。
// plan.go proxy planning layer, line-aligned with rust_archive/crates/core/src/net.rs (175 lines).
// Line references cite net.rs; proxies are applied by replacing http.Client.Transport (reqwest builder semantics).
package netx

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// ProxyChoice 代理选择，对齐 net.rs L3-8 的枚举 Direct | System | Custom。
// ProxyChoice proxy selection, mirroring the net.rs L3-8 enum Direct | System | Custom.
type ProxyChoice int

const (
	// ProxyDirect 直连，禁用一切代理 / direct connection, all proxies disabled.
	ProxyDirect ProxyChoice = iota
	// ProxySystem 使用系统/环境代理 / honor system/environment proxy settings.
	ProxySystem
	// ProxyCustom 使用配置的自定义代理 / use the configured custom proxy.
	ProxyCustom
)

// HttpPlan 单次请求的规划结果，对齐 net.rs L10-15。
// HttpPlan the plan for a single request, mirroring net.rs L10-15.
type HttpPlan struct {
	URL             string
	Proxy           ProxyChoice
	UsedGithubProxy bool
}

// NetConfig 网络相关设置的轻量视图，对齐 net.rs L17-26。
// Rust 借用 &str，Go 侧按值持有字符串，语义等价。
// NetConfig a lightweight view of network-related settings, mirroring net.rs L17-26.
// Rust borrows &str while Go holds strings by value; semantically equivalent.
type NetConfig struct {
	Mode        config.ProxyMode
	ProxyURL    string
	ProxyUser   string
	ProxyPass   string
	GithubProxy string
}

// NetConfigFromConfig 对齐 net.rs L29-31：从主配置构造。
// NetConfigFromConfig mirrors net.rs L29-31: build from the main config.
func NetConfigFromConfig(c *config.Config) NetConfig {
	return NetConfigFromParts(&c.Proxy, c.GithubProxy)
}

// NetConfigFromParts 对齐 net.rs L33-41：从代理配置与 github 代理前缀构造，字段原样拷贝。
// NetConfigFromParts mirrors net.rs L33-41: build from a proxy config and ghproxy prefix, copying fields verbatim.
func NetConfigFromParts(p *config.ProxyConfig, githubProxy string) NetConfig {
	return NetConfig{
		Mode:        p.Mode,
		ProxyURL:    p.URL,
		ProxyUser:   p.User,
		ProxyPass:   p.Pass,
		GithubProxy: githubProxy,
	}
}

// IsGithubURLForGhproxy 对齐 net.rs L44-47：trim 后仅认两个 https 前缀。
// IsGithubURLForGhproxy mirrors net.rs L44-47: after trimming, only the two https prefixes match.
func IsGithubURLForGhproxy(raw string) bool {
	u := strings.TrimSpace(raw)
	return strings.HasPrefix(u, "https://raw.githubusercontent.com/") ||
		strings.HasPrefix(u, "https://github.com/")
}

// NormalizeURLPrefix 对齐 net.rs L49-58：空串直通、已含协议直通、否则补 https://。
// NormalizeURLPrefix mirrors net.rs L49-58: empty passthrough, contains a scheme passthrough, else prepend https://.
func NormalizeURLPrefix(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}

// joinPrefix 对齐 net.rs L60-66：前缀 trim 后保证尾斜杠，再拼接 trim 后的 URL。
// joinPrefix mirrors net.rs L60-66: trim the prefix, ensure a trailing slash, then append the trimmed URL.
func joinPrefix(prefix, rawURL string) string {
	p := strings.TrimSpace(prefix)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + strings.TrimSpace(rawURL)
}

// PlanRuleFetch 规划远程规则资源（列表/配置）的下载请求，对齐 net.rs L70-107。
// smart+ghproxy 命中 github 源时改写 URL 并强制直连；smart 未改写时按 proxy_url 有无回退 Custom/System。
// PlanRuleFetch plans a request for downloading remote rule resources, mirroring net.rs L70-107.
// With smart+ghproxy hitting a github source the URL is rewritten and forced direct;
// otherwise smart falls back to Custom/System depending on proxy_url.
func PlanRuleFetch(net NetConfig, originalURL string) HttpPlan {
	u := strings.TrimSpace(originalURL)
	mode := net.Mode
	ghproxy := strings.TrimSpace(net.GithubProxy)

	canUseGhproxy := mode == config.ProxyModeSmart && ghproxy != "" && IsGithubURLForGhproxy(u)
	usedGithubProxy := canUseGhproxy
	finalURL := u
	if canUseGhproxy {
		finalURL = joinPrefix(NormalizeURLPrefix(ghproxy), u)
	}

	var proxy ProxyChoice
	switch mode {
	case config.ProxyModeCustom:
		proxy = ProxyCustom
	case config.ProxyModeSystem:
		proxy = ProxySystem
	case config.ProxyModeSmart:
		if usedGithubProxy {
			// 使用 ghproxy 时必须直连 / when using ghproxy, the request must be direct.
			proxy = ProxyDirect
		} else if strings.TrimSpace(net.ProxyURL) == "" {
			// 未配置自定义代理 -> 回退到系统代理 / no custom proxy -> fall back to the system proxy.
			proxy = ProxySystem
		} else {
			proxy = ProxyCustom
		}
	default:
		// Rust 枚举闭集不可能有未知值；Go 的 ProxyMode 是字符串，
		// 配置解析层（ParseProxyMode/ApplyDefaults）保证不会出现，防御性按系统代理兜底。
		// Rust's closed enum cannot hold unknown values; Go's ProxyMode is a string and the
		// config layer (ParseProxyMode/ApplyDefaults) guarantees none appear, so defensively fall back to system.
		proxy = ProxySystem
	}

	return HttpPlan{URL: finalURL, Proxy: proxy, UsedGithubProxy: usedGithubProxy}
}

// PlanGithubAPI 规划 GitHub API（releases）请求，对齐 net.rs L110-132。
// 从不改写 URL、从不使用 ghproxy；代理选择与 PlanRuleFetch 的未改写分支一致。
// PlanGithubAPI plans a GitHub API (releases) request, mirroring net.rs L110-132.
// The URL is never rewritten and ghproxy is never used; proxy selection matches
// the non-rewritten branch of PlanRuleFetch.
func PlanGithubAPI(net NetConfig, rawURL string) HttpPlan {
	u := strings.TrimSpace(rawURL)

	var proxy ProxyChoice
	switch net.Mode {
	case config.ProxyModeCustom:
		proxy = ProxyCustom
	case config.ProxyModeSystem:
		proxy = ProxySystem
	case config.ProxyModeSmart:
		if strings.TrimSpace(net.ProxyURL) == "" {
			proxy = ProxySystem
		} else {
			proxy = ProxyCustom
		}
	default:
		proxy = ProxySystem
	}

	return HttpPlan{URL: u, Proxy: proxy, UsedGithubProxy: false}
}

// ApplyProxyChoice 把代理策略应用到 http.Client，对齐 net.rs L134-175。
// Direct/Custom 显式禁用系统代理（Proxy=nil / 显式代理）；System 读取环境代理变量；
// Custom 模式代理 URL 缺省 http://127.0.0.1:7890，user 非空时通过 u.User 携带 basic auth。
// ApplyProxyChoice applies a proxy choice to an http.Client, mirroring net.rs L134-175.
// Direct/Custom explicitly disable system proxies (Proxy=nil / an explicit proxy); System honors env
// proxies; Custom defaults the proxy URL to http://127.0.0.1:7890 and carries basic auth via u.User
// when a user is set.
func ApplyProxyChoice(hc *http.Client, net NetConfig, choice ProxyChoice) {
	switch choice {
	case ProxyDirect:
		// Proxy=nil 即显式禁用所有代理，等价 reqwest no_proxy()。
		// Proxy=nil explicitly disables every proxy, equivalent to reqwest no_proxy().
		hc.Transport = &http.Transport{Proxy: nil}
	case ProxySystem:
		// 保持默认行为（读取系统/环境代理变量）。
		// Keep the default behavior (honor system/env proxy variables).
		hc.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	case ProxyCustom:
		hc.Transport = customProxyTransport(net)
	}
}

// customProxyTransport 对齐 net.rs L149-172：解析代理 URL、附加 basic auth、禁用系统代理。
// customProxyTransport mirrors net.rs L149-172: parse the proxy URL, attach basic auth, disable system proxies.
func customProxyTransport(net NetConfig) *http.Transport {
	proxyURL := strings.TrimSpace(net.ProxyURL)
	if proxyURL == "" {
		// custom 模式保留一个合理默认值 / keep a sensible default for custom mode.
		proxyURL = "http://127.0.0.1:7890"
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		// Rust 版本此处向调用方返回 Err；Go 签名无错误返回，解析失败回退直连而非带病代理。
		// The Rust version propagates an Err to the caller; the Go signature returns no error,
		// so an unparseable proxy URL falls back to a direct connection instead of a broken proxy.
		return &http.Transport{Proxy: nil}
	}

	if user := strings.TrimSpace(net.ProxyUser); user != "" {
		// u.User 携带 basic auth，等价 reqwest basic_auth(user, pass)。
		// u.User carries basic auth, equivalent to reqwest basic_auth(user, pass).
		u.User = url.UserPassword(user, strings.TrimSpace(net.ProxyPass))
	}

	// 显式代理本身即排除环境代理，等价 reqwest no_proxy() + proxy(p)。
	// An explicit proxy excludes env proxies, equivalent to reqwest no_proxy() + proxy(p).
	return &http.Transport{Proxy: http.ProxyURL(u)}
}
