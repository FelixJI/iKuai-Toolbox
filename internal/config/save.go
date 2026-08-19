// save.go 配置安全写盘 / secure config writing
// 行为规格对齐 crates/core/src/config.rs L388-466：
// SaveToPath / validate_and_save_raw_yaml / write_config_file / validate_save_path。
// Behavioral spec mirrors crates/core/src/config.rs L388-466.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 写盘安全错误。错误文案保持英文，与 Rust ConfigError Display 一致。
// Security errors; messages stay English, matching the Rust ConfigError Display strings.
var (
	// ErrInvalidExtension 配置文件后缀必须是 .yml 或 .yaml / extension must be .yml or .yaml.
	ErrInvalidExtension = errors.New("security violation: file extension must be .yml or .yaml")
	// ErrSymlinkDenied 禁止写入符号链接 / writing to a symbolic link is denied.
	ErrSymlinkDenied = errors.New("security violation: cannot write to a symbolic link")
)

// SaveToPath 序列化为 YAML 并安全写盘 / serialize to YAML and write securely.
func (c *Config) SaveToPath(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		// 与 Rust 一致：序列化失败同样归入 parse yaml failed / same variant as Rust for serialize errors.
		return fmt.Errorf("parse yaml failed: %w", err)
	}
	return writeConfigFile(path, data)
}

// ValidateAndSaveRawYAML 解析校验后按原文写盘（rawYaml 是配置编辑的唯一真来源）
// ValidateAndSaveRawYAML validates the raw YAML, then writes it verbatim
// (rawYaml is the single source of truth for config editing).
func ValidateAndSaveRawYAML(raw string, path string) (*Config, error) {
	cfg, err := LoadFromYAMLString(raw)
	if err != nil {
		return nil, err
	}
	if err := writeConfigFile(path, []byte(raw)); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WriteEmbeddedDefaultToPath 校验并按原文写出内嵌默认配置
// WriteEmbeddedDefaultToPath validates then writes the embedded default config verbatim.
func WriteEmbeddedDefaultToPath(path string) error {
	raw := EmbeddedDefaultYAML()
	if _, err := LoadFromYAMLString(raw); err != nil {
		return err
	}
	return writeConfigFile(path, []byte(raw))
}

// writeConfigFile 安全写盘：后缀校验 → 建父目录 → 0600 截断写
// writeConfigFile writes securely: validate extension, create parent dirs,
// then truncate-write with 0600 (permission bits are ignored on Windows).
func writeConfigFile(path string, data []byte) error {
	if err := ValidateSavePath(path); err != nil {
		return err
	}
	if err := ensureParentDir(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write config failed: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write config failed: %w", err)
	}
	return nil
}

// ensureParentDir 自动创建父目录 / ensure the parent directory exists.
func ensureParentDir(path string) error {
	parent := filepath.Dir(path)
	if parent == "" || parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("write config failed: %w", err)
	}
	return nil
}

// ValidateSavePath 写盘路径安全校验：仅 .yml/.yaml（大小写不敏感），拒绝符号链接
// 对齐 config.rs validate_save_path L445-466。
// ValidateSavePath guards write paths: only .yml/.yaml (case-insensitive),
// and symbolic links are rejected.
func ValidateSavePath(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".yml" && ext != ".yaml" {
		return ErrInvalidExtension
	}

	// Lstat 不跟随符号链接，与 Rust symlink_metadata 一致
	// Lstat does not follow symlinks, matching Rust symlink_metadata.
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config failed: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkDenied
	}
	return nil
}
