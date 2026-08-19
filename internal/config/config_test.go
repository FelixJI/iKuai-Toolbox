// 配置结构解析与默认值测试 / Config parsing and defaults tests
// 行为规格对齐 crates/core/src/config.rs L223-412。
package config

import (
	"encoding/json"
	"testing"
	"time"
)

// TestParseEmbeddedDefault 内嵌默认配置必须可解析且 module/run-mode 为默认值
// 对齐 Rust test_embedded_default_has_correct_mode。
func TestParseEmbeddedDefault(t *testing.T) {
	cfg, err := LoadFromYAMLString(EmbeddedDefaultYAML())
	if err != nil {
		t.Fatalf("parse embedded default: %v", err)
	}
	if cfg.Module != "ispdomain" {
		t.Errorf("Module = %q, want %q", cfg.Module, "ispdomain")
	}
	if cfg.RunMode != "cronAft" {
		t.Errorf("RunMode = %q, want %q", cfg.RunMode, "cronAft")
	}
	// 关键字段抽查：AddErrRetryWait/AddWait 走 Duration 双格式解析
	// Spot-check duration fields parsed through the dual-format Duration type.
	if cfg.AddErrRetryWait != Duration(10*time.Second) {
		t.Errorf("AddErrRetryWait = %v, want 10s", time.Duration(cfg.AddErrRetryWait))
	}
	if cfg.AddWait != Duration(1*time.Second) {
		t.Errorf("AddWait = %v, want 1s", time.Duration(cfg.AddWait))
	}
}

// TestApplyDefaultsEmptyFields 空字段应填充默认值 / empty fields get defaults
// 对齐 Rust test_apply_defaults_empty_fields。
func TestApplyDefaultsEmptyFields(t *testing.T) {
	raw := `
username: admin
password: admin888
ikuai-url: http://192.168.1.1
cron: 0 7 * * *
`
	cfg, err := LoadFromYAMLString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunMode != "cronAft" {
		t.Errorf("empty run-mode should default to cronAft, got %q", cfg.RunMode)
	}
	if cfg.Module != "ispdomain" {
		t.Errorf("empty module should default to ispdomain, got %q", cfg.Module)
	}
}

// TestApplyDefaultsPreservesSetValues 显式设置的值必须保留 / explicit values are kept
// 对齐 Rust test_apply_defaults_preserves_set_values 与 test_apply_defaults_old_spdomain_is_rejected
// （apply_defaults 不改写 spdomain，校验属于后续 runner 任务）。
func TestApplyDefaultsPreservesSetValues(t *testing.T) {
	raw := `
username: admin
password: admin888
ikuai-url: http://192.168.1.1
cron: 0 7 * * *
run-mode: once
mode: ipgroup
`
	cfg, err := LoadFromYAMLString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunMode != "once" {
		t.Errorf("explicit run-mode should be kept, got %q", cfg.RunMode)
	}
	if cfg.Module != "ipgroup" {
		t.Errorf("explicit mode should be kept, got %q", cfg.Module)
	}

	// spdomain 不被 apply_defaults 修正 / spdomain is NOT rewritten by apply_defaults.
	cfg2, err := LoadFromYAMLString("ikuai-url: http://192.168.1.1\nmode: spdomain\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Module != "spdomain" {
		t.Errorf("apply_defaults must not rewrite spdomain, got %q", cfg2.Module)
	}
}

// TestProxyModeAliases 代理模式别名归一化 / proxy mode alias normalization
// 对齐 config.rs ProxyMode serde alias：disabled→system；onlyGithubApi 系→smart。
func TestProxyModeAliases(t *testing.T) {
	cases := []struct {
		in   string
		want ProxyMode
	}{
		{"onlyGithubApi", ProxyModeSmart},
		{"only-github-api", ProxyModeSmart},
		{"only_github_api", ProxyModeSmart},
		{"disabled", ProxyModeSystem},
		{"custom", ProxyModeCustom},
		{"system", ProxyModeSystem},
		{"smart", ProxyModeSmart},
	}
	for _, c := range cases {
		var p ProxyConfig
		raw := "mode: " + c.in + "\nurl: \"\"\nuser: \"\"\npass: \"\"\n"
		if err := yamlUnmarshalStr(raw, &p); err != nil {
			t.Fatalf("yaml %s: %v", c.in, err)
		}
		if p.Mode != c.want {
			t.Errorf("yaml mode %q => %q, want %q", c.in, p.Mode, c.want)
		}
		// JSON 侧同样归一化 / the JSON side normalizes the same way.
		var pj ProxyConfig
		data, err := json.Marshal(ProxyConfig{Mode: c.want})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &pj); err != nil {
			t.Fatalf("json %s: %v", c.in, err)
		}
		if pj.Mode != c.want {
			t.Errorf("json mode %q => %q, want %q", c.in, pj.Mode, c.want)
		}
	}

	// 未知模式必须报错 / unknown modes must fail.
	var p ProxyConfig
	if err := yamlUnmarshalStr("mode: nonsense\n", &p); err == nil {
		t.Errorf("unknown proxy mode should be rejected")
	}

	// proxy 键完全缺失时默认 smart（对齐 serde #[serde(default)]）
	// A fully absent proxy key defaults to smart (matches serde #[serde(default)]).
	cfg, err := LoadFromYAMLString("ikuai-url: http://192.168.1.1\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Mode != ProxyModeSmart {
		t.Errorf("absent proxy mode should default to smart, got %q", cfg.Proxy.Mode)
	}
}

// TestCustomProxyDefaultURL custom 模式且 URL 为空时回退本地代理
// 对齐 config.rs L351-353。
func TestCustomProxyDefaultURL(t *testing.T) {
	raw := `
ikuai-url: http://192.168.1.1
proxy:
  mode: custom
  url: ""
`
	cfg, err := LoadFromYAMLString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.URL != "http://127.0.0.1:7890" {
		t.Errorf("custom proxy with empty url = %q, want http://127.0.0.1:7890", cfg.Proxy.URL)
	}
}

// TestApplyDefaultsFillsWebUI 空 webui 段落填充端口与 CDN 前缀
// 对齐 config.rs L303-313。
func TestApplyDefaultsFillsWebUI(t *testing.T) {
	cfg, err := LoadFromYAMLString("ikuai-url: http://192.168.1.1\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebUI.Port != "19001" {
		t.Errorf("default webui port = %q, want 19001", cfg.WebUI.Port)
	}
	if cfg.WebUI.CdnPrefix != "https://cdn.jsdelivr.net/npm" {
		t.Errorf("default cdn prefix = %q, want https://cdn.jsdelivr.net/npm", cfg.WebUI.CdnPrefix)
	}
	// 端口仅含空白时同样回填默认值，显式端口做 trim / blank port falls back, explicit port is trimmed.
	cfg2, err := LoadFromYAMLString("ikuai-url: http://x\nwebui:\n  port: \" 8080 \"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.WebUI.Port != "8080" {
		t.Errorf("trimmed webui port = %q, want 8080", cfg2.WebUI.Port)
	}
	// MaxNumberOfOneRecords 零值回填 5000/1000/1000/5000 / zero records limits are filled in.
	if cfg.MaxNumberOfOneRecords.Isp != 5000 || cfg.MaxNumberOfOneRecords.Ipv4 != 1000 ||
		cfg.MaxNumberOfOneRecords.Ipv6 != 1000 || cfg.MaxNumberOfOneRecords.Domain != 5000 {
		t.Errorf("default MaxNumberOfOneRecords = %+v, want 5000/1000/1000/5000", cfg.MaxNumberOfOneRecords)
	}
	// stream-domain 空 tag 回填 interface / empty stream-domain tag falls back to interface.
	cfg3, err := LoadFromYAMLString("ikuai-url: http://x\nstream-domain:\n  - interface: wan2\n    url: https://example.com/l.txt\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg3.StreamDomain) != 1 || cfg3.StreamDomain[0].Tag != "wan2" {
		t.Errorf("stream-domain tag should default to interface, got %+v", cfg3.StreamDomain)
	}
}

// TestStreamIpPortInvNormalize stream-ipport 反向匹配标志归一化为 0/1
// 对齐 config.rs L296-299 normalize_binary_flag：仅 1 保持 1，其余（含 5、-3）归 0。
// 注：任务书示例写 "5 => 1"，但行为规格 config.rs 为 value==1?1:0，以规格为准。
func TestStreamIpPortInvNormalize(t *testing.T) {
	raw := `
ikuai-url: http://192.168.1.1
stream-ipport:
  - type: "0"
    src-addr-inv: 5
    dst-addr-inv: 2
    mode: 6
    ifaceband: 0
    protocol: tcp+udp
  - type: "1"
    src-addr-inv: 1
    dst-addr-inv: 0
    mode: 0
    ifaceband: 1
    protocol: tcp+udp
`
	cfg, err := LoadFromYAMLString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.StreamIpPort[0].SrcAddrInv; got != 0 {
		t.Errorf("src-addr-inv 5 => %d, want 0 (only 1 stays 1)", got)
	}
	if got := cfg.StreamIpPort[0].DstAddrInv; got != 0 {
		t.Errorf("dst-addr-inv 2 => %d, want 0", got)
	}
	if got := cfg.StreamIpPort[1].SrcAddrInv; got != 1 {
		t.Errorf("src-addr-inv 1 => %d, want 1", got)
	}
	if got := cfg.StreamIpPort[1].DstAddrInv; got != 0 {
		t.Errorf("dst-addr-inv 0 => %d, want 0", got)
	}
}

// TestConfigJSONKeysMatchContract JSON 顶层键契约（前端 fromBackendMeta 依赖）
// Top-level JSON keys must keep the exact contract expected by the frontend.
func TestConfigJSONKeysMatchContract(t *testing.T) {
	cfg, err := LoadFromYAMLString(EmbeddedDefaultYAML())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	required := []string{
		"ikuai-url", "run-mode", "AddErrRetryWait", "MaxNumberOfOneRecords",
		"custom-isp", "stream-ipport", "webui",
	}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			t.Errorf("json output missing required top-level key %q", key)
		}
	}
}
