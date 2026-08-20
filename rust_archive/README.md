# iKuai-Toolbox（Rust 归档）

- 本目录为 Rust 版本历史归档（不再作为主线维护）。当前主线为仓库根目录的 Go 实现（`cmd/`、`internal/`），CLI、WebUI 服务与核心业务逻辑均已由 Go 重写。
- 冻结点标记：本地 tag `rust-final` 指向归档前最后一个 Rust 主线提交，可用 `git log rust-final` / `git checkout rust-final` 回溯完整迁移前工作区。
- 目录结构差异（相对归档前的仓库根布局）：
  - `crates/`：原样移入（核心库 `ikb-core`）。
  - `apps/cli/` → `apps-cli/`（CLI + Web 模式，改名避免与现行 `apps/` 目录混淆）。
  - `apps/integration-tests/` → `apps-integration-tests/`（Rust 版黑盒集成测试与 ikuai_simulator；现行版本为 `apps/integration-tests-go/`）。
  - 根 `Cargo.toml` / `Cargo.lock` / `Cross.toml` / `release.toml` 一并移入；workspace members 与 crate 内 path 依赖已按归档后的相对布局修正，但仍为冻结快照，不再更新。
  - `apps/gui/`（Tauri 壳）不在本归档中：仍在原位构建，业务逻辑已由 Go sidecar 接管，并已解耦为独立 crate（自带 `Cargo.lock`）。
