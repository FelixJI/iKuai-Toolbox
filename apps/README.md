# Apps

本目录承载 GUI 壳与集成测试（Go 主线的 CLI 本体位于根目录 `cmd/ikuai-bypass` + `internal/`）：

- `apps/gui/`：Tauri v2 GUI（桌面/移动端），独立 crate，业务逻辑由 Go sidecar（`cmd/ikuai-bypass` 交叉编译产物）承载
- `apps/integration-tests-go/`：Go 黑盒集成测试（内置 iKuai 模拟器，CI 与 pre-commit 复用）

Rust 版 `apps/cli/` 与 `apps/integration-tests/` 已归档至 `rust_archive/`；前端单页位于 `frontends/app/`（Astro），CLI WebUI 与 Tauri 共用。
