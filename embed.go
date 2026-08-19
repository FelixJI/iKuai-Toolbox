// Package ikuaitoolbox 承载仓库级内嵌资源。
// go:embed 只能引用本包目录下的文件，因此根目录 config.yml 与
// frontends/app/dist 的内嵌必须放在根包；internal/config 经
// DefaultConfigYAML 读取配置原文，internal/webserver 经 FrontendFS
// 读取静态产物，保证单一真来源。
// Package ikuaitoolbox hosts repository-level embedded assets.
// go:embed can only reference files inside the package directory, so
// embedding the root config.yml and frontends/app/dist must live in the
// root package; internal/config consumes DefaultConfigYAML for the raw
// config while internal/webserver consumes FrontendFS for the static
// assets, keeping a single source of truth.
//
// FrontendFS 的构建前置：先执行 cd frontends/app && bun install && bun run build
// 产出 dist/，缺失时本包编译失败（dist 被 .gitignore 忽略，embed 只看
// 本地文件，故 CI/新克隆需先构建前端）。
// FrontendFS build prerequisite: run cd frontends/app && bun install &&
// bun run build to produce dist/ first; a missing dist fails compilation of
// this package (dist is gitignored and embed only looks at local files, so
// CI/fresh clones must build the frontend first).
package ikuaitoolbox

import "embed"

// DefaultConfigYAML 内嵌的默认配置原文（与根目录 config.yml 完全一致）
// DefaultConfigYAML is the embedded raw default config (identical to ./config.yml).
//
//go:embed config.yml
var DefaultConfigYAML string

// FrontendFS 内嵌的 WebUI 静态产物；all: 前缀保证 _astro/ 等下划线目录
// 一并嵌入。
// FrontendFS holds the embedded WebUI static assets; the all: prefix keeps
// underscore-prefixed trees such as _astro/ embedded as well.
//
//go:embed all:frontends/app/dist
var FrontendFS embed.FS
