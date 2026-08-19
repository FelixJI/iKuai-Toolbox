// url.go 爱快地址归一化，行为对齐 rust_archive/crates/core/src/app/url.rs L1-10。
// （https 前缀版 normalize_url_prefix 已由 internal/netx.NormalizeURLPrefix 提供。）
// URL normalization for iKuai addresses, aligned with url.rs L1-10.
// (The https-flavored normalize_url_prefix already lives in netx.NormalizeURLPrefix.)
package app

import "strings"

// NormalizeBaseURL 空串直通、已含协议直通、否则补 http://（url.rs L1-10）。
// NormalizeBaseURL: empty passthrough, contains a scheme passthrough, else
// prepend http:// (url.rs L1-10).
func NormalizeBaseURL(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "http://" + raw
}
