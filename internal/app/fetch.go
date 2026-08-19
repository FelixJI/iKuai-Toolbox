// fetch.go 远程配置/规则文本拉取，行为对齐 crates/core/src/app/fetch.rs（36 行）：
// 走 netx.PlanRuleFetch 规划（smart+ghproxy 命中 github 源时改写 URL 并直连），
// UA "ikb-core"、总 15s 超时。
// Remote config/rule text fetching aligned with app/fetch.rs (36 lines):
// planned through netx.PlanRuleFetch (smart+ghproxy rewrites github sources and
// forces a direct connection) with UA "ikb-core" and a 15s overall timeout.
package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/netx"
)

// FetchRemoteConfig 拉取远程配置文本（fetch.rs L5-36）：空 URL 报错，
// 非 2xx 报 "HTTP <status>"，成功返回响应体文本。
// FetchRemoteConfig fetches remote config text (fetch.rs L5-36): an empty URL
// errors, a non-2xx yields "HTTP <status>", and success returns the body text.
func FetchRemoteConfig(rawURL string, proxy *config.ProxyConfig, githubProxy string) (string, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return "", fmt.Errorf("Remote URL is empty")
	}

	netCfg := netx.NetConfigFromParts(proxy, githubProxy)
	plan := netx.PlanRuleFetch(netCfg, u)

	hc := &http.Client{Timeout: 15 * time.Second}
	netx.ApplyProxyChoice(hc, netCfg, plan.Proxy)

	req, err := http.NewRequest(http.MethodGet, plan.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ikb-core")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
