// 安全写盘与默认路径测试 / Secure save and default-path tests
// 行为规格对齐 rust_archive/crates/core/src/config.rs L388-466 与 rust_archive/crates/core/src/paths.rs。
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestValidateSavePathExtension 仅允许 .yml/.yaml 后缀（大小写不敏感）
// 对齐 config.rs validate_save_path L445-449。
func TestValidateSavePathExtension(t *testing.T) {
	good := []string{
		filepath.Join(t.TempDir(), "config.yml"),
		filepath.Join(t.TempDir(), "config.yaml"),
		filepath.Join(t.TempDir(), "sub", "dir", "config.YML"), // 大小写不敏感 / case-insensitive
		filepath.Join(t.TempDir(), "config.Yaml"),
	}
	for _, p := range good {
		if err := ValidateSavePath(p); err != nil {
			t.Errorf("ValidateSavePath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{
		filepath.Join(t.TempDir(), "config.txt"),
		filepath.Join(t.TempDir(), "config"),
		filepath.Join(t.TempDir(), "config.json"),
		filepath.Join(t.TempDir(), "config.yml.bak"),
	}
	for _, p := range bad {
		if err := ValidateSavePath(p); err == nil {
			t.Errorf("ValidateSavePath(%q) = nil, want extension error", p)
		}
	}
}

// TestSaveToPathRoundTrip 保存后重新加载应得到等价配置
// TestSaveToPathRoundTrip: saving then reloading yields an equivalent config.
func TestSaveToPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := LoadFromYAMLString(EmbeddedDefaultYAML())
	if err != nil {
		t.Fatal(err)
	}
	cfg.IkuaiURL = "http://10.0.0.1:80"
	if err := cfg.SaveToPath(path); err != nil {
		t.Fatalf("SaveToPath: %v", err)
	}
	loaded, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	if loaded.IkuaiURL != "http://10.0.0.1:80" {
		t.Errorf("IkuaiURL = %q", loaded.IkuaiURL)
	}
	if loaded.Module != "ispdomain" || loaded.RunMode != "cronAft" {
		t.Errorf("module/run-mode = %q/%q", loaded.Module, loaded.RunMode)
	}
	if loaded.AddErrRetryWait != Duration(10*time.Second) {
		t.Errorf("AddErrRetryWait = %v, want 10s", time.Duration(loaded.AddErrRetryWait))
	}
	if len(loaded.CustomIsp) != len(cfg.CustomIsp) {
		t.Errorf("custom-isp entries = %d, want %d", len(loaded.CustomIsp), len(cfg.CustomIsp))
	}
	// 父目录应被自动创建 / parent directories are created automatically.
	nested := filepath.Join(t.TempDir(), "deep", "nest", "config.yaml")
	if err := cfg.SaveToPath(nested); err != nil {
		t.Fatalf("SaveToPath(nested): %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("nested config not written: %v", err)
	}
}

// TestSavedFileMode0600 Unix 下写盘权限必须是 0600（Windows 跳过）
// 对齐 config.rs write_config_file L418-427 的 mode(0o600)。
func TestSavedFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only permission check / permission check is unix-only")
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := LoadFromYAMLString(EmbeddedDefaultYAML())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveToPath(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}

// TestValidateAndSaveRawYAMLRoundTrip 校验通过后必须按原文写盘
// 对齐 config.rs validate_and_save_raw_yaml L393-400：解析校验 → raw 原文写盘。
func TestValidateAndSaveRawYAMLRoundTrip(t *testing.T) {
	raw := "ikuai-url: http://1.2.3.4\nusername: u\npassword: p\ncron: 0 7 * * *\n"
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := ValidateAndSaveRawYAML(raw, path)
	if err != nil {
		t.Fatalf("ValidateAndSaveRawYAML: %v", err)
	}
	if cfg.IkuaiURL != "http://1.2.3.4" {
		t.Errorf("parsed IkuaiURL = %q", cfg.IkuaiURL)
	}
	if cfg.Module != "ispdomain" {
		t.Errorf("defaults not applied, module = %q", cfg.Module)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw {
		t.Errorf("file content = %q, want raw verbatim %q", string(got), raw)
	}
}

// TestValidateAndSaveRawYAMLRejectsBadInput 非法 YAML / 非法路径必须整体失败且不落盘
// Invalid YAML or invalid paths fail entirely without touching disk.
func TestValidateAndSaveRawYAMLRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	// 非法 YAML / malformed YAML.
	badPath := filepath.Join(dir, "bad.yml")
	if _, err := ValidateAndSaveRawYAML(":::not yaml", badPath); err == nil {
		t.Error("malformed yaml should be rejected")
	}
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Error("file must not be created on parse failure")
	}
	// 非法后缀 / bad extension.
	if _, err := ValidateAndSaveRawYAML("a: 1\n", filepath.Join(dir, "bad.txt")); err == nil {
		t.Error("non-yaml extension should be rejected")
	}
}

// TestWriteRejectsSymlink 拒绝写入符号链接 / writing to a symbolic link is denied
// 对齐 config.rs validate_save_path L452-463 的 symlink_metadata 检查。
func TestWriteRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yml")
	if err := os.WriteFile(target, []byte("ikuai-url: http://keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}
	if err := ValidateSavePath(link); err == nil {
		t.Error("ValidateSavePath should reject a symlink")
	}
	cfg := &Config{IkuaiURL: "http://overwritten"}
	if err := cfg.SaveToPath(link); err == nil {
		t.Error("SaveToPath should reject a symlink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ikuai-url: http://keep\n" {
		t.Errorf("symlink target was modified: %q", string(got))
	}
}

// TestWriteEmbeddedDefaultToPath 内嵌默认配置按原文写盘并可再次加载
// The embedded default is written verbatim and reloads cleanly.
func TestWriteEmbeddedDefaultToPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := WriteEmbeddedDefaultToPath(path); err != nil {
		t.Fatalf("WriteEmbeddedDefaultToPath: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != EmbeddedDefaultYAML() {
		t.Error("written default differs from embedded raw yaml")
	}
	if _, err := LoadFromPath(path); err != nil {
		t.Errorf("reload written default: %v", err)
	}
}

// TestYAMLHasExplicitMode 只有顶层 mode 键才算显式声明
// Only a top-level mode key counts as an explicit declaration.
func TestYAMLHasExplicitMode(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"mode: ispdomain\n", true},
		{"run-mode: once\n", false},
		{"proxy:\n  mode: smart\n", false},
		{"ikuai-url: http://x\n", false},
		{":::bad yaml", false},
		{"", false},
	}
	for _, c := range cases {
		if got := YAMLHasExplicitMode(c.raw); got != c.want {
			t.Errorf("YAMLHasExplicitMode(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// TestDefaultConfigPathPerGOOS 用 GOOS 注入法验证各平台默认路径形状
// 对齐 paths.rs default_config_path_with_app_name：
// windows→Roaming AppData；macos→~/Library/Application Support；
// linux/其他 unix→~/.config；android→回退 ./config.yml。
// TestDefaultConfigPathPerGOOS verifies per-platform path shapes via GOOS injection.
func TestDefaultConfigPathPerGOOS(t *testing.T) {
	const home = "/home/tester"
	const ucd = `C:\Users\tester\AppData\Roaming`
	cases := []struct {
		goos string
		want string
	}{
		{"windows", filepath.Join(ucd, "ikuai-bypass", "config.yml")},
		{"darwin", filepath.Join(home, "Library", "Application Support", "ikuai-bypass", "config.yml")},
		{"linux", filepath.Join(home, ".config", "ikuai-bypass", "config.yml")},
		{"freebsd", filepath.Join(home, ".config", "ikuai-bypass", "config.yml")},
		{"android", "./config.yml"},
	}
	for _, c := range cases {
		got := defaultConfigPathFor("ikuai-bypass", c.goos, home, ucd)
		if got != c.want {
			t.Errorf("goos %s => %q, want %q", c.goos, got, c.want)
		}
	}
	// home 不可得时 unix 回退 ./config.yml / unix falls back when no home is available.
	if got := defaultConfigPathFor("ikuai-bypass", "linux", "", ucd); got != "./config.yml" {
		t.Errorf("linux without home => %q, want ./config.yml", got)
	}
	if got := defaultConfigPathFor("ikuai-bypass", "windows", home, ""); got != "./config.yml" {
		t.Errorf("windows without config dir => %q, want ./config.yml", got)
	}
}

// TestDefaultConfigPathCurrentGOOS 当前平台默认路径总是 config.yml 结尾
// The current-platform default path always ends with config.yml.
func TestDefaultConfigPathCurrentGOOS(t *testing.T) {
	p := DefaultConfigPath()
	if filepath.Base(p) != "config.yml" {
		t.Errorf("DefaultConfigPath = %q, want base config.yml", p)
	}
	if filepath.Dir(p) == "." || filepath.Dir(p) == "" {
		t.Errorf("DefaultConfigPath = %q should live in a config dir (except android)", p)
	}
	if !strings.HasSuffix(p, "ikuai-bypass"+string(filepath.Separator)+"config.yml") {
		// android 回退 ./config.yml 是例外 / the android fallback is the exception.
		if runtime.GOOS != "android" {
			t.Errorf("DefaultConfigPath = %q should contain ikuai-bypass/config.yml", p)
		}
	}
}

// TestConfigPathFromBaseDir 指定基础目录的路径拼装 / path assembly from a base dir.
func TestConfigPathFromBaseDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "data")
	got := ConfigPathFromBaseDir(base, "ikuai-bypass")
	want := filepath.Join(base, "ikuai-bypass", "config.yml")
	if got != want {
		t.Errorf("ConfigPathFromBaseDir = %q, want %q", got, want)
	}
}
