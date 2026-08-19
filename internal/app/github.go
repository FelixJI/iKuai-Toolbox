// github.go GitHub Releases 更新检查，行为对齐 crates/core/src/app/github.rs（60 行）：
// 走 netx.PlanGithubAPI 规划（从不改写 URL、从不使用 ghproxy），
// UA "ikb-core"、connect 8s / 总 15s 超时。
// GitHub Releases update checks aligned with app/github.rs (60 lines):
// planned through netx.PlanGithubAPI (never rewriting the URL, never using
// ghproxy) with UA "ikb-core" and 8s connect / 15s overall timeouts.
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/netx"
)

// githubReleasesURL 生产端点（github.rs L20）。
// githubReleasesURL is the production endpoint (github.rs L20).
const githubReleasesURL = "https://api.github.com/repos/FelixJI/iKuai-Toolbox/releases?per_page=30"

// GithubRelease 单条 release；JSON 键逐字对齐 github.rs L5-17 的 serde 命名
// （name/published_at/created_at 在 Rust 侧为 Option，Go 侧缺省/ null 均落到空串）。
// GithubRelease is one release entry; the JSON keys mirror the serde naming of
// github.rs L5-17 verbatim (name/published_at/created_at are Options in Rust;
// a missing key or null lands on the empty string in Go).
type GithubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	CreatedAt   string `json:"created_at"`
}

// FetchGithubReleases 拉取最近 30 条 release（github.rs L19-60）。
// FetchGithubReleases fetches the latest 30 releases (github.rs L19-60).
func FetchGithubReleases(proxy *config.ProxyConfig) ([]GithubRelease, error) {
	return fetchGithubReleasesFrom(githubReleasesURL, proxy)
}

// fetchGithubReleasesFrom 指定端点拉取，生产入口固定 githubReleasesURL；
// 独立参数仅为 httptest 提供注入点，行为与 github.rs L19-60 逐行对齐。
// fetchGithubReleasesFrom fetches from an explicit endpoint; the production
// entry pins githubReleasesURL, the parameter exists solely as the httptest
// seam, and behavior maps line-by-line to github.rs L19-60.
func fetchGithubReleasesFrom(url string, proxy *config.ProxyConfig) ([]GithubRelease, error) {
	netCfg := netx.NetConfigFromParts(proxy, "")
	plan := netx.PlanGithubAPI(netCfg, url)

	hc := &http.Client{Timeout: 15 * time.Second}
	netx.ApplyProxyChoice(hc, netCfg, plan.Proxy)
	// ApplyProxyChoice 只替换 *http.Transport，补回 8s 连接超时，15s 总超时始终兜底。
	// ApplyProxyChoice only swaps in a *http.Transport; re-attach the 8s dialer
	// while the 15s client timeout stays the safety net.
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.DialContext = (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}

	req, err := http.NewRequest(http.MethodGet, plan.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("Request failed: %s", err)
	}
	req.Header.Set("User-Agent", "ikb-core")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Request failed: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		hint := bodyHint(body, 200)
		return nil, fmt.Errorf("HTTP %s url='%s'%s", resp.Status, url, hint)
	}

	var wire []struct {
		TagName     *string `json:"tag_name"`
		Name        *string `json:"name"`
		Prerelease  *bool   `json:"prerelease"`
		Draft       *bool   `json:"draft"`
		HTMLURL     *string `json:"html_url"`
		PublishedAt *string `json:"published_at"`
		CreatedAt   *string `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("Failed to decode response: %s", err)
	}
	// tag_name/prerelease/draft/html_url 在 Rust 侧为必填字段，缺失须判为解码失败。
	// tag_name/prerelease/draft/html_url are required in Rust; a missing key
	// must count as a decode failure.
	releases := make([]GithubRelease, 0, len(wire))
	for _, w := range wire {
		if w.TagName == nil || w.Prerelease == nil || w.Draft == nil || w.HTMLURL == nil {
			return nil, fmt.Errorf("Failed to decode response: missing required release field")
		}
		rel := GithubRelease{TagName: *w.TagName, Prerelease: *w.Prerelease, Draft: *w.Draft, HTMLURL: *w.HTMLURL}
		if w.Name != nil {
			rel.Name = *w.Name
		}
		if w.PublishedAt != nil {
			rel.PublishedAt = *w.PublishedAt
		}
		if w.CreatedAt != nil {
			rel.CreatedAt = *w.CreatedAt
		}
		releases = append(releases, rel)
	}
	return releases, nil
}

// bodyHint 非 2xx 响应体摘录：trim 后取前 limit 个字符（rune 计数），换行替换为空格，
// 超长追加 "..."，空体返回空串（github.rs L44-53 与 diagnostics.rs L118-126 同源）。
// bodyHint excerpts a non-2xx body: trim, take the first limit runes, replace
// newlines with spaces, append "..." when longer; an empty body yields ""
// (github.rs L44-53 and diagnostics.rs L118-126 share this logic).
func bodyHint(body []byte, limit int) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	out := string(runes)
	if len(runes) > limit {
		out = string(runes[:limit]) + "..."
	}
	return " body='" + strings.ReplaceAll(out, "\n", " ") + "'"
}
