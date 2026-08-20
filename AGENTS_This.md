# AGENTS_This.md（iKuai-Toolbox）

本文件只放项目特有内容，通用规范见同目录 `AGENTS.md`。

## 项目定位
- iKuai-Toolbox（基于 joyanhui/ikuai-bypass 的 AGPL-3.0 修改版，版权声明见 `NOTICE`）的 Go 主线版本：仓库根目录即当前可交付版本；Rust 实现已归档到 `rust_archive/`（冻结点 tag `rust-final`），更早的 Go/Fyne 代码归档在 `golang_archive/`。
- 技术栈：Go 1.25（标准库 `net/http` + `robfig/cron/v3` + `gopkg.in/yaml.v3`，CGO_ENABLED=0 静态交叉编译），CLI 与 WebUI 同进程；`apps/gui/` 为 Tauri v2 壳（仓库内唯一保留的 Rust 代码），业务逻辑全部由 Go sidecar（`cmd/ikuai-bypass` 交叉编译产物）承载。
- 除非用户明确要求，不要把新功能继续做进 `rust_archive/` 或 `golang_archive/` 归档目录。

## 文档事实来源
- `docs/`：Jekyll + GitHub Pages 文档站，部署于 `https://joyanhui.github.io/ikuai-bypass/`（子目录）；本地预览执行 `bash script/dev.sh docs:dev`。
- `api-docs/`：爱快 4.x API 抓包记录。
- `dev-docs/`：专题开发说明，含 `openwrt-luci-ipk构建和说明.md`、运行模式和分流模式配置原则、域名/端口/IPv4/IPv6 分组、添加运营商等。
- docs 内部链接必须使用标准 Markdown 相对路径 `](file.md)` 或 `](file.md#锚点)`，禁止 `](/根路径/)` 和 `]({{ site.baseurl }}/path/)` 等 Liquid 写法；`jekyll-relative-links` 插件构建时自动将 `file.md` 转 `/ikuai-bypass/file/`，同时 Obsidian 原生支持 `.md` 相对路径跳转和图谱。

## 开发环境
- 进入仓库目录后执行 `nix develop`（或 direnv），获得 Go/前端/Tauri(Rust)/Jekyll 开发环境。

## 仓库结构
```text
iKuai-Toolbox/
├── cmd/ikuai-bypass/         # CLI 入口（运行模式分发、信号处理、退出码）
├── internal/                 # 核心业务库（config/ikuai/update/runtime/webserver/logger/netx/app）
├── apps/gui/                 # Tauri v2 GUI 壳（独立 crate，Go sidecar 承载业务）
├── apps/integration-tests-go/ # Go 集成测试（simulator/ 内置 iKuai 模拟器，smoke/ 黑盒用例）
├── frontends/app/            # Bun + Astro 单页前端（WebUI 与 Tauri 共用）
├── config.yml                # 示例配置
├── api-docs/                 # 爱快 4.x API 抓包记录
├── docs/                     # Jekyll + GitHub Pages 文档站
├── dev-docs/                 # 专题开发说明
├── packaging/                # 打包相关
├── rust_archive/             # Rust 版本归档（tag rust-final）
└── golang_archive/           # 旧 Go/Fyne 版本归档
```

## 前端技术栈（修正通用 AGENTS.md 的前端规范）
- 本项目前端是 Bun + Astro 4 单页（`frontends/app/`），不是通用 AGENTS.md 的 React Router v7 + Vite 技术栈；通用 AGENTS.md 中 React Router v7、TanStack Query、Zustand、shadcn/ui、Zod、React Hook Form、Lucide、Motion、typesafe-i18n、vite-plugin-checker 等约定不适用。
- 实际技术栈：Astro 4（static output）+ Tailwind v4（@tailwindcss/vite）+ TypeScript + 自定义 i18n（`src/lib/i18n.ts`，zh/en 字典）+ js-yaml/yaml（YAML 解析）+ monaco-editor（懒加载）+ vitest + playwright。
- i18n：用户可见文本必须走 `src/lib/i18n.ts` 字典，禁止硬编码文案；新增词条同步 ZH/EN 两字典。
- 存储：统一走 `src/lib/storage.ts`，禁止组件直接 `setItem` / `getItem`。
- 类型安全沿用通用 AGENTS.md 规则：禁止 `any`、禁止 `@ts-ignore`、禁止 `as any` 绕过类型系统。
- monaco 编辑器仅限 PC 模式可用；Tauri app 移动端禁止使用，会导致 webview 崩溃。
- 单测用 vitest（`src/lib/*.test.ts`），浏览器 e2e 用 playwright。

## 命令防卡死（修正通用 AGENTS.md）
- 通用 AGENTS.md 的 `vite build`（vite-plugin-checker）与 `typesafe-i18n` 卡死约定不适用于本项目：本项目无 vite-plugin-checker 与 typesafe-i18n，前端构建为 `astro build`（`bun run build`），会正常退出。
- 若执行其他不退出进程的命令，不可接 `| tail` / `| head` 等待管道 EOF；截断输出改用 `do sleep x` 或重定向到文件再 tail。

## 配置与编辑模型（修正通用 AGENTS.md 的配置规范）
- 本项目无数据库；配置以 `config.yml`（示例）为事实来源，前端配置编辑唯一真来源是 `rawYaml`。
- 可视化编辑必须通过 YAML AST 定点修改 `rawYaml`（`frontends/app/src/lib/yaml_ast.ts`）；文本编辑直接编辑 `rawYaml`。
- 后端保存必须先解析 YAML 校验，再按 `rawYaml` 原文写盘。
- 配置一致性：新增或修改配置项时，至少同步更新 `config.yml`、`internal/config/config.go`、`frontends/app/src/lib/config_model.ts`、`frontends/app` 相关表单 / YAML AST / 保存逻辑。
- 统一使用 `tag` 字段作为用户标识，不再新增 `name` 字段语义。
- 配置覆写必须做 YAML 后缀、软链接和写入安全校验。

## 通用规范不适用项
- 错误码与 API 规范（OpenAPI 3.0、utoipa、错误码枚举唯一来源、TS 自动生成同步）：本项目是 CLI 为主的 iKuai 分流工具，调用爱快 HTTP API，自身无这套 API 契约体系，通用 AGENTS.md 该节不适用。
- 环境变量规范（`*_env(key, default)` 统一封装、启动时校验）：本项目未采用统一封装，环境变量（如 `IKB_TEST_IKUAI_URL`）按需读取。
- Cloudflare Worker（`bunx wrangler`）：本项目无 Cloudflare Worker，该约定不适用。

## 核心业务逻辑
### 规则标识与命名约定
- 名称前缀：`IKB`；统一备注：`IkuaiBypass`；命名规则：`IKB + tag + 序号`。
- 识别逻辑：名字以 `IKB` 开头或备注包含 `IkuaiBypass`。
- 旧版本兼容：清理/更新模式保留对 `joyanhui/ikuai-bypass` 与 `IKUAI_BYPASS` 的兼容识别。

### 执行与日志规范
- 所有更新任务必须严格顺序执行，禁止并发更新多个规则块。
- 面向用户的日志标签必须使用中文；API 和内部错误信息保持英文，便于定位。

### 更新与安全策略
- 原地更新：匹配则 Edit，不匹配则 Add，保持爱快内部 ID 稳定。
- 自定义运营商分片：同名 `IKB+tag`，通过备注中的分片序号匹配并清理冗余分片。
- Safe-Before：远程资源下载失败或 HTTP 状态异常时，立即终止当前项更新，严禁清理旧规则。
- 清理模式：必须显式指定 `-tag`，不得设置危险默认值。

## 架构约束
- CLI 是完整功能本体，GUI/WebUI 只是可视化入口。
- WebUI 与 Tauri 共用 `frontends/app/` 这一套 Astro 单页；Tauri IPC 语义需要和 Web API 对齐（`frontends/app/src/lib/bridge.ts`）。
- Go 代码约束：错误一律用 `fmt.Errorf("...: %w", err)` 包装透传，不得吞错或裸返回；生产代码禁止 `panic`；通用 AGENTS.md 的 Rust 规范（零 clone/unwrap、tokio 并发等）对本项目不再适用。
- 更新流程严禁并发：所有规则块更新严格顺序执行，WebUI/cron 触发的更新入口必须防重入。

## 注释与文案规范（修正通用 AGENTS.md）
- 本项目明确要求代码注释使用双语文本（中文 + English），优先解释为什么存在，再解释做了什么；这覆盖通用 AGENTS.md 的"除非明确要求，永远不要新增注释"。
- UI 返回文案与 API 错误信息保持英文。
- 测试代码是项目明确要求（见集成测试约定与前端测试），不受通用 AGENTS.md"不得新增测试代码"限制。

## 集成测试约定
- 集成测试位于 `apps/integration-tests-go/`：`simulator/` 为内置 iKuai 模拟器（真实 HTTP 行为），`smoke/` 为黑盒用例，harness 自行 `go build` 被测 CLI；不依赖在线 KVM。
- 单测与 smoke 统一命令：`go test ./...`；根包 go:embed 要求先构建 `frontends/app/dist`（`cd frontends/app && bun install && bun run build`）。
- pre-commit 钩子（`.github/githooks/pre-commit`）本地运行 `go test ./apps/integration-tests-go/... -count=1`。
- `webui` 浏览器 smoke 位于 `frontends/app/tests/e2e/`（playwright，`bun run test:e2e`），通过 `IKB_WEBUI_BASE_URL` / `IKB_WEBUI_USER` / `IKB_WEBUI_PASS` 等环境变量连接自备的运行中 WebUI。

## 安装脚本测试
- `docs/install.sh` 一键安装脚本 CI 测试覆盖 Ubuntu (systemd) 与 OpenWrt (KVM QEMU) 两种环境，验证 OS/arch 检测、版本获取、下载安装、服务文件注册、enable/start/stop/disable 生命周期、保留/删除配置卸载以及进程残留清理。

## CI 约束
- `.github/workflows/release.yml` 只允许 `tag push` 和 `workflow_dispatch` 触发，禁止恢复每日定时构建。
- 发布统一走 Release PR 流程（参考 vibetable 模式）：`release-prepare.yml` 手动选 bump 级别后由 `.github/scripts/release-prepare.py` 自动计算下一版本、同步改写 `internal/app/diagnostics.go` 的 `CoreVersion` 与 `apps/gui/Cargo.toml`、`apps/gui/Cargo.lock`，并维护 `release/auto` 分支的 Release PR；禁止为发版手工编辑版本文件。
- Release PR 合并后由 `release-tag.yml` 自动打 `ikuai-bypass-vX.Y.Z` tag 并以 `trigger_mode=tag`、`build_mode=full` 转发 `release.yml`；GITHUB_TOKEN push 的 tag 不产生 push 事件，必须显式 `workflow_dispatch` 转发。
- tag push 时 `resolve-matrix` 会校验 tag 基版本与三处版本常量一致（`release-prepare.py --verify-tag`），不一致直接失败。
- 手动执行时 `publish_release` 与 `push_docker` 默认勾选；未填写 `release_tag` 但勾选发布时必须自动生成 `manual-release-年月日时分秒` 继续发布；手动执行发布一律标记为 prerelease；选择 `full` 时必须自动包含 nightly MIPS 架构。
- Tag push 仅在 tag 名包含 `manual`、`demo`、`test`、`rc`、`alpha`、`beta`、`pre`、`preview`、`dev`、`nightly` 时发布为 prerelease，否则发布为正式版并推送 Docker `latest`。
- 发布 workflow 注意事项：
  - 发布 workflow 运行期间不要在 main 上 push 新 commit；tag 指向的 commit 不再是默认分支 tip 时，GitHub 平台会拒绝 GITHUB_TOKEN 携带该 commit 作为 target_commitish 创建 release（403），该限制要求 PAT 级权限。
  - Publish Release 步骤使用 `gh release create` 而非 softprops/action-gh-release：tag 已存在时只关联 tag、不传 target_commitish，天然规避上述 403。
  - 上传资产只收集 release/ 第一层归档文件；`.zip.stage/` 等打包中间产物内含同名 `ikuai-bypass`/`README.md`/`config.yml`，上传会触发 asset 重名 422。
