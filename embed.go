// Package ikuaitoolbox 承载仓库级内嵌资源。
// go:embed 只能引用本包目录下的文件，因此根目录 config.yml 的内嵌必须放在根包；
// internal/config 通过 DefaultConfigYAML 读取，保证 config.yml 单一真来源。
// Package ikuaitoolbox hosts repository-level embedded assets.
// go:embed can only reference files inside the package directory, so embedding
// the root config.yml must live in the root package; internal/config consumes it
// via DefaultConfigYAML so config.yml stays the single source of truth.
package ikuaitoolbox

import _ "embed"

// DefaultConfigYAML 内嵌的默认配置原文（与根目录 config.yml 完全一致）
// DefaultConfigYAML is the embedded raw default config (identical to ./config.yml).
//
//go:embed config.yml
var DefaultConfigYAML string
