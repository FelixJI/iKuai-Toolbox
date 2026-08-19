// paths.go 默认配置路径解析 / default config path resolution
// 行为规格对齐 crates/core/src/paths.rs：
// windows→Roaming AppData；macos→~/Library/Application Support；
// linux/其他 unix→~/.config；android 与未知环境→./config.yml。
// Behavioral spec mirrors crates/core/src/paths.rs.
package config

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// DefaultAppName 默认应用目录名 / default application directory name.
const DefaultAppName = "ikuai-bypass"

// DefaultConfigPath 当前平台的默认配置文件路径
// DefaultConfigPath returns the default config file path for the current platform.
func DefaultConfigPath() string {
	home := currentHomeDir()
	ucd := ""
	if dir, err := os.UserConfigDir(); err == nil {
		ucd = dir
	}
	return defaultConfigPathFor(DefaultAppName, runtime.GOOS, home, ucd)
}

// ConfigPathFromBaseDir 在指定基础目录下拼装配置路径
// ConfigPathFromBaseDir assembles a config path under an explicit base directory.
func ConfigPathFromBaseDir(baseDir string, appName string) string {
	return filepath.Join(baseDir, appName, "config.yml")
}

// defaultConfigPathFor 按 GOOS 注入计算默认路径（可测试的核心实现）
// defaultConfigPathFor computes the default path for an injected GOOS
// (the testable core implementation).
//
// home 为用户主目录；userConfigDir 为 windows 的 Roaming AppData 目录。
// 任何必需输入缺失时回退 ./config.yml，对齐 paths.rs 的 Option None 分支。
// home is the user home dir; userConfigDir is the Windows Roaming AppData dir.
// Any missing required input falls back to ./config.yml, matching paths.rs's None branch.
func defaultConfigPathFor(appName string, goos string, home string, userConfigDir string) string {
	var base string
	switch goos {
	case "windows":
		// Rust 使用 SHGetKnownFolderPath(RoamingAppData)；Go 用 os.UserConfigDir 等价取 %AppData%
		// Rust uses SHGetKnownFolderPath(RoamingAppData); Go's os.UserConfigDir reads %AppData% equivalently.
		if userConfigDir == "" {
			return "./config.yml"
		}
		base = userConfigDir
	case "darwin":
		if home == "" {
			return "./config.yml"
		}
		base = filepath.Join(home, "Library", "Application Support")
	case "android":
		// Android 沙箱中 home 探测不可靠，回退 ./config.yml，由上层（Tauri）覆写
		// Home detection is unreliable in the Android sandbox; fall back and let the upper layer override.
		return "./config.yml"
	default:
		// linux 及其他 unix / linux and other unix-like systems.
		if home == "" {
			return "./config.yml"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appName, "config.yml")
}

// currentHomeDir 解析当前用户主目录：优先 HOME 类环境变量，失败再查系统用户数据库
// Rust 刻意绕过环境变量改用 getpwuid_r，Go 标准库无等价无 cgo 实现，
// 这里用 os.UserHomeDir + os/user 兜底，路径形状与 Rust 一致。
// currentHomeDir resolves the current user's home directory: environment first,
// then the system user database as a fallback. Rust deliberately bypasses env vars
// via getpwuid_r; Go's stdlib has no equivalent without cgo, so we use
// os.UserHomeDir with an os/user fallback. The resulting path shape matches Rust.
func currentHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return ""
}
