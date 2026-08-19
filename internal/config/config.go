// config.go 配置结构与解析 / Config structures and parsing
// 行为规格对齐 rust_archive/crates/core/src/config.rs L90-412：
// 字段命名、serde rename/alias、默认值与 apply_defaults 逐条对齐。
// Behavioral spec mirrors rust_archive/crates/core/src/config.rs L90-412:
// field naming, serde rename/alias, defaults, and apply_defaults are aligned item by item.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	ikuaitoolbox "github.com/FelixJI/iKuai-Toolbox"
	"gopkg.in/yaml.v3"
)

// ProxyMode 代理模式 / proxy mode: "custom" | "system" | "smart"
type ProxyMode string

const (
	// ProxyModeCustom 所有外部 HTTP 请求走自定义代理 / all outbound HTTP goes through the custom proxy.
	ProxyModeCustom ProxyMode = "custom"
	// ProxyModeSystem 使用系统/环境代理 / use system/environment proxy settings.
	ProxyModeSystem ProxyMode = "system"
	// ProxyModeSmart 智能模式（默认）/ smart mode (default).
	ProxyModeSmart ProxyMode = "smart"
)

// ParseProxyMode 解析代理模式字符串，兼容历史别名：
// disabled→system；onlyGithubApi/only-github-api/only_github_api→smart。
// ParseProxyMode parses a proxy mode string, accepting legacy aliases:
// disabled→system; onlyGithubApi/only-github-api/only_github_api→smart.
func ParseProxyMode(s string) (ProxyMode, error) {
	switch s {
	case "custom":
		return ProxyModeCustom, nil
	case "system", "disabled":
		return ProxyModeSystem, nil
	case "smart", "onlyGithubApi", "only-github-api", "only_github_api":
		return ProxyModeSmart, nil
	default:
		return "", fmt.Errorf("unknown proxy mode %q (expected custom/system/smart)", s)
	}
}

// UnmarshalYAML 兼容历史别名的 YAML 反序列化 / YAML unmarshal with legacy alias support.
func (m *ProxyMode) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("proxy mode must be a string, got %s", value.Tag)
	}
	parsed, err := ParseProxyMode(value.Value)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// MarshalYAML 序列化为规范名称 / serialize using the canonical name.
func (m ProxyMode) MarshalYAML() (interface{}, error) {
	return string(m), nil
}

// UnmarshalJSON JSON 侧同名归一化（前端两种命名都会发）
// UnmarshalJSON normalizes the same aliases on the JSON side (frontend sends both namings).
func (m *ProxyMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("proxy mode must be a string: %w", err)
	}
	parsed, err := ParseProxyMode(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// MarshalJSON 输出规范名称 / emit the canonical name.
func (m ProxyMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(m))
}

// ProxyConfig 全局 HTTP 代理配置 / global HTTP proxy configuration.
// yaml/json 键与 Rust serde rename 对齐：mode/url/user/pass。
type ProxyConfig struct {
	Mode ProxyMode `yaml:"mode" json:"mode"`
	URL  string    `yaml:"url"  json:"url"`
	User string    `yaml:"user" json:"user"`
	Pass string    `yaml:"pass" json:"pass"`
}

// UnmarshalYAML mode 键缺失时默认 smart（对齐 ProxyConfig::default + #[serde(default)]）
// UnmarshalYAML defaults a missing mode key to smart (matches ProxyConfig::default + #[serde(default)]).
func (p *ProxyConfig) UnmarshalYAML(value *yaml.Node) error {
	// 本地别名类型避免递归调用本方法 / local alias type avoids recursive dispatch.
	type plain ProxyConfig
	raw := plain{Mode: ProxyModeSmart}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Mode == "" {
		// mode 显式为空或 null 时同样落到默认值 / an explicitly empty/null mode falls back too.
		raw.Mode = ProxyModeSmart
	}
	*p = ProxyConfig(raw)
	return nil
}

// UnmarshalJSON 与 YAML 同语义 / same semantics as the YAML side.
func (p *ProxyConfig) UnmarshalJSON(data []byte) error {
	type plain ProxyConfig
	raw := plain{Mode: ProxyModeSmart}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Mode == "" {
		raw.Mode = ProxyModeSmart
	}
	*p = ProxyConfig(raw)
	return nil
}

// CustomIspItem 自定义运营商分片条目 / custom ISP split entry.
type CustomIspItem struct {
	Tag string `yaml:"tag" json:"tag"`
	URL string `yaml:"url" json:"url"`
}

// StreamDomainItem 域名分流条目 / stream-domain entry.
type StreamDomainItem struct {
	Interface         string `yaml:"interface" json:"interface"`
	SrcAddr           string `yaml:"src-addr" json:"src-addr"`
	SrcAddrOptIpGroup string `yaml:"src-addr-opt-ipgroup" json:"src-addr-opt-ipgroup"`
	URL               string `yaml:"url" json:"url"`
	Tag               string `yaml:"tag" json:"tag"`
}

// IpGroupItem IPv4 分组条目 / IPv4 group entry.
type IpGroupItem struct {
	Tag string `yaml:"tag" json:"tag"`
	URL string `yaml:"url" json:"url"`
}

// Ipv6GroupItem IPv6 分组条目 / IPv6 group entry.
type Ipv6GroupItem struct {
	Tag string `yaml:"tag" json:"tag"`
	URL string `yaml:"url" json:"url"`
}

// StreamIpPortItem 端口分流条目 / stream-ipport entry.
type StreamIpPortItem struct {
	OptTagName        string `yaml:"opt-tagname" json:"opt-tagname"`
	Type              string `yaml:"type" json:"type"`
	Interface         string `yaml:"interface" json:"interface"`
	Nexthop           string `yaml:"nexthop" json:"nexthop"`
	SrcAddr           string `yaml:"src-addr" json:"src-addr"`
	SrcAddrOptIpGroup string `yaml:"src-addr-opt-ipgroup" json:"src-addr-opt-ipgroup"`
	SrcAddrInv        int64  `yaml:"src-addr-inv" json:"src-addr-inv"`
	IPGroup           string `yaml:"ip-group" json:"ip-group"`
	DstAddrInv        int64  `yaml:"dst-addr-inv" json:"dst-addr-inv"`
	Prio              int64  `yaml:"prio" json:"prio"`
	Mode              int64  `yaml:"mode" json:"mode"`
	IfaceBand         int64  `yaml:"ifaceband" json:"ifaceband"`
	Protocol          string `yaml:"protocol" json:"protocol"`
}

// WebUiConfig WebUI 管理服务设置 / WebUI management service settings.
type WebUiConfig struct {
	Port      string `yaml:"port" json:"port"`
	User      string `yaml:"user" json:"user"`
	Pass      string `yaml:"pass" json:"pass"`
	Enable    bool   `yaml:"enable" json:"enable"`
	CdnPrefix string `yaml:"cdn-prefix" json:"cdn-prefix"`
}

// MaxNumberOfOneRecordsConfig 单批写入上限 / per-batch write limits.
// 零值在 ApplyDefaults 中回填 5000/1000/1000/5000 / zero values are filled by ApplyDefaults.
type MaxNumberOfOneRecordsConfig struct {
	Isp    int `yaml:"Isp" json:"Isp"`
	Ipv4   int `yaml:"Ipv4" json:"Ipv4"`
	Ipv6   int `yaml:"Ipv6" json:"Ipv6"`
	Domain int `yaml:"Domain" json:"Domain"`
}

// Config 主配置结构 / main configuration structure.
// yaml/json 键与 rust_archive/crates/core/src/config.rs 的 serde rename 一一对应，
// JSON 顶层键是前端 fromBackendMeta 的契约，不得随意改动。
// yaml/json keys map one-to-one to the serde renames in rust_archive/crates/core/src/config.rs;
// the JSON top-level keys are a contract consumed by the frontend fromBackendMeta.
type Config struct {
	IkuaiURL              string                      `yaml:"ikuai-url" json:"ikuai-url"`
	Username              string                      `yaml:"username" json:"username"`
	Password              string                      `yaml:"password" json:"password"`
	Cron                  string                      `yaml:"cron" json:"cron"`
	AddErrRetryWait       Duration                    `yaml:"AddErrRetryWait" json:"AddErrRetryWait"`
	AddWait               Duration                    `yaml:"AddWait" json:"AddWait"`
	RunMode               string                      `yaml:"run-mode" json:"run-mode"`
	Module                string                      `yaml:"mode" json:"mode"`
	GithubProxy           string                      `yaml:"github-proxy" json:"github-proxy"`
	Proxy                 ProxyConfig                 `yaml:"proxy" json:"proxy"`
	CustomIsp             []CustomIspItem             `yaml:"custom-isp" json:"custom-isp"`
	StreamDomain          []StreamDomainItem          `yaml:"stream-domain" json:"stream-domain"`
	IpGroup               []IpGroupItem               `yaml:"ip-group" json:"ip-group"`
	Ipv6Group             []Ipv6GroupItem             `yaml:"ipv6-group" json:"ipv6-group"`
	StreamIpPort          []StreamIpPortItem          `yaml:"stream-ipport" json:"stream-ipport"`
	WebUI                 WebUiConfig                 `yaml:"webui" json:"webui"`
	MaxNumberOfOneRecords MaxNumberOfOneRecordsConfig `yaml:"MaxNumberOfOneRecords" json:"MaxNumberOfOneRecords"`
}

// EmbeddedDefaultYAML 返回内嵌默认配置原文（根目录 config.yml，单一真来源）
// EmbeddedDefaultYAML returns the raw embedded default config (root config.yml, single source of truth).
func EmbeddedDefaultYAML() string {
	return ikuaitoolbox.DefaultConfigYAML
}

// LoadEmbeddedDefault 加载并填充默认值后的内嵌默认配置
// LoadEmbeddedDefault loads the embedded default config with defaults applied.
func LoadEmbeddedDefault() (*Config, error) {
	return LoadFromYAMLString(EmbeddedDefaultYAML())
}

// LoadFromYAMLString 从 YAML 字符串解析配置并填充默认值
// LoadFromYAMLString parses config from a YAML string and applies defaults.
func LoadFromYAMLString(s string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(s), &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml failed: %w", err)
	}
	cfg.ApplyDefaults()
	return &cfg, nil
}

// LoadFromPath 从文件读取并解析配置 / LoadFromPath reads and parses the config file.
func LoadFromPath(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config failed: %w", err)
	}
	return LoadFromYAMLString(string(data))
}

// ApplyDefaults 填充默认值与标准化处理，对齐 config.rs apply_defaults（L296-386）。
// ApplyDefaults fills defaults and normalizes values, mirroring config.rs apply_defaults (L296-386).
func (c *Config) ApplyDefaults() {
	// 仅 1 保持 1，其余值（含 5、-3）归 0 / only 1 stays 1, everything else becomes 0.
	normalizeBinaryFlag := func(value int64) int64 {
		if value == 1 {
			return 1
		}
		return 0
	}

	// WebUI 默认值，与文档/前端默认值保持一致 / WebUI defaults, consistent with docs and frontend.
	if strings.TrimSpace(c.WebUI.Port) == "" {
		c.WebUI.Port = "19001"
	} else {
		c.WebUI.Port = strings.TrimSpace(c.WebUI.Port)
	}
	if c.WebUI.CdnPrefix == "" {
		c.WebUI.CdnPrefix = "https://cdn.jsdelivr.net/npm"
	}

	if c.RunMode == "" {
		c.RunMode = "cronAft"
	}
	if c.Module == "" {
		c.Module = "ispdomain"
	}

	if c.MaxNumberOfOneRecords.Isp == 0 {
		c.MaxNumberOfOneRecords.Isp = 5000
	}
	if c.MaxNumberOfOneRecords.Ipv4 == 0 {
		c.MaxNumberOfOneRecords.Ipv4 = 1000
	}
	if c.MaxNumberOfOneRecords.Ipv6 == 0 {
		c.MaxNumberOfOneRecords.Ipv6 = 1000
	}
	if c.MaxNumberOfOneRecords.Domain == 0 {
		c.MaxNumberOfOneRecords.Domain = 5000
	}

	for i := range c.StreamDomain {
		if c.StreamDomain[i].Tag == "" {
			c.StreamDomain[i].Tag = c.StreamDomain[i].Interface
		}
	}

	for i := range c.StreamIpPort {
		c.StreamIpPort[i].SrcAddrInv = normalizeBinaryFlag(c.StreamIpPort[i].SrcAddrInv)
		c.StreamIpPort[i].DstAddrInv = normalizeBinaryFlag(c.StreamIpPort[i].DstAddrInv)
	}

	// 代理默认值与标准化处理 / proxy defaults and normalization.
	c.Proxy.URL = strings.TrimSpace(c.Proxy.URL)
	c.Proxy.User = strings.TrimSpace(c.Proxy.User)
	c.Proxy.Pass = strings.TrimSpace(c.Proxy.Pass)
	if c.Proxy.Mode == "" {
		// proxy 键整体缺失时 Go 零值为 ""，这里对齐 serde 默认 smart
		// an entirely absent proxy key leaves "" here; align with serde's smart default.
		c.Proxy.Mode = ProxyModeSmart
	}
	if c.Proxy.Mode == ProxyModeCustom && c.Proxy.URL == "" {
		c.Proxy.URL = "http://127.0.0.1:7890"
	}
}

// YAMLHasExplicitMode 判断原始 YAML 是否显式包含顶层 mode 字段。
// 用于 save-raw：当保存的配置完全不包含 mode 字段时，避免覆盖 CLI 启动参数指定的模块。
// YAMLHasExplicitMode reports whether the raw YAML explicitly contains a top-level `mode` field.
// Used by save-raw so a saved config without `mode` never overrides the module
// chosen via CLI args (e.g. `-m ipgroup`).
func YAMLHasExplicitMode(raw string) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return false
	}
	if len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}
	// 只看顶层键，proxy.mode 等嵌套字段不算 / only top-level keys count, not nested ones like proxy.mode.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "mode" {
			return true
		}
	}
	return false
}
