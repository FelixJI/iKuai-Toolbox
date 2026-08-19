// config_meta.go 配置元信息（/api/config 响应体），行为对齐 crates/core/src/app/config_meta.rs（34 行）。
// Config metadata (the /api/config response body), aligned with app/config_meta.rs (34 lines).
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// ConfigMeta 配置展开 + 路径 + 原文：内嵌 config.Config 在 JSON 序列化时键被展平到顶层
// （对齐 config_meta.rs 的 #[serde(flatten)]），conf_path/raw_yaml 随后并列。
// ConfigMeta is the flattened config plus its path and raw text: the embedded
// config.Config promotes its JSON keys to the top level (matching the
// #[serde(flatten)] of config_meta.rs), alongside conf_path/raw_yaml.
type ConfigMeta struct {
	config.Config
	ConfPath string `json:"conf_path"`
	RawYAML  string `json:"raw_yaml"`
}

// BuildConfigMeta 组装配置元信息（config_meta.rs L13-26）：config 先行编码校验，
// conf_path 绝对化（cwd.join 语义），raw_yaml 读原文、读失败或非 UTF-8 回退空串。
// BuildConfigMeta assembles the config metadata (config_meta.rs L13-26): the
// config is encoded up front for validation, conf_path is absolutized (the
// cwd.join semantics), and raw_yaml reads the file verbatim, falling back to
// the empty string on read failure or non-UTF-8 content.
func BuildConfigMeta(cfg *config.Config, configPath string) (ConfigMeta, error) {
	if cfg == nil {
		return ConfigMeta{}, fmt.Errorf("Failed to encode config: config is nil")
	}
	// 前置编码校验，对齐 serde_json::to_value 的即时错误。
	// Encode up front, mirroring the eager serde_json::to_value error.
	if _, err := json.Marshal(cfg); err != nil {
		return ConfigMeta{}, fmt.Errorf("Failed to encode config: %s", err)
	}

	absPath := toAbsPath(configPath)
	rawYAML := ""
	if data, err := os.ReadFile(absPath); err == nil && utf8.Valid(data) {
		rawYAML = string(data)
	}

	return ConfigMeta{Config: *cfg, ConfPath: absPath, RawYAML: rawYAML}, nil
}

// toAbsPath 绝对路径直通，相对路径拼接进程 cwd（config_meta.rs L28-34）。
// toAbsPath passes absolutes through and joins relative paths with the process
// cwd (config_meta.rs L28-34).
func toAbsPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, p)
}
