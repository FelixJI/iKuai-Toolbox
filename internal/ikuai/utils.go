// utils.go 爱快模块工具函数，行为对齐 rust_archive/crates/core/src/ikuai/utils.rs。
// Utility helpers for the iKuai module, aligned with rust_archive/crates/core/src/ikuai/utils.rs.
package ikuai

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// MD5Hex 小写十六进制 md5 摘要，对齐 utils.rs L3-6。
// MD5Hex returns the lowercase hex md5 digest, mirroring utils.rs L3-6.
func MD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// marshalNoHTMLEscape 对齐 serde_json 的序列化字节：不转义 < > &，紧凑输出且无尾随换行。
// Go 默认 json.Marshal 会做 HTML 转义，导致含特殊字符的请求体与 Rust 产物字节不一致。
// marshalNoHTMLEscape matches serde_json wire bytes: no < > & escaping, compact, no trailing newline.
// Go's default json.Marshal HTML-escapes, which would diverge from Rust bytes for such payloads.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ToStringList 对齐 utils.rs L8-32：null => 空；数组逐项转换；单字符串 => 单元素；
// 对象按 gp_name→name→ip→ipv6 顺序取首个字符串字段，否则回退紧凑 JSON 去引号。
// ToStringList mirrors utils.rs L8-32: null yields empty; arrays convert item by item;
// a lone string wraps into one element; objects pick the first string field among
// gp_name/name/ip/ipv6, otherwise fall back to compact JSON with quotes trimmed.
func ToStringList(v json.RawMessage) []string {
	var parsed any
	if len(v) == 0 || json.Unmarshal(v, &parsed) != nil {
		return []string{}
	}
	switch t := parsed.(type) {
	case nil:
		return []string{}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, valueToString(item))
		}
		return out
	case string:
		return []string{t}
	default:
		return []string{valueToString(parsed)}
	}
}

// valueToString 对齐 utils.rs L17-32 的单值转换规则。
// valueToString mirrors the per-value conversion rules of utils.rs L17-32.
func valueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		// iKuai object references often embed the readable name in `gp_name`.
		// 爱快对象引用通常会把可读名称放在 `gp_name` 字段里。
		for _, key := range []string{"gp_name", "name", "ip", "ipv6"} {
			if s, ok := t[key].(string); ok {
				return s
			}
		}
	}
	b, err := marshalNoHTMLEscape(v)
	if err != nil {
		// 仅可能出现在无法再序列化的值上，Unmarshal 产物不会触达。
		// Only reachable for values that cannot re-marshal; Unmarshal output never hits this.
		return ""
	}
	return strings.Trim(string(b), `"`)
}

// CategorizeAddrs 对齐 utils.rs L34-51：空白跳过并 trim；
// IKB 前缀或不含点/冒/横线 => 对象名（object），其余 => 自定义地址（custom），保持输入顺序。
// CategorizeAddrs mirrors utils.rs L34-51: skip blanks after trimming;
// an IKB prefix or no dot/colon/dash means an object name, anything else is a custom
// address; input order is preserved in both buckets.
func CategorizeAddrs(addrs []string) (custom []string, objects []string) {
	custom = make([]string, 0)
	objects = make([]string, 0)
	for _, raw := range addrs {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		if strings.HasPrefix(addr, NamePrefixIKB) {
			objects = append(objects, addr)
		} else if strings.Contains(addr, ".") || strings.Contains(addr, ":") || strings.Contains(addr, "-") {
			custom = append(custom, addr)
		} else {
			objects = append(objects, addr)
		}
	}
	return custom, objects
}
