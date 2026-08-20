# Tauri 桌面壳（Go sidecar）

- apps/gui 只是一个薄壳：启动 Go 后端（sidecar）并打开指向 `http://127.0.0.1:{port}` 的窗口，业务全部在 Go 侧（`cmd/ikuai-bypass`）。
- 前端零改动：窗口加载 Go 服务地址，`frontends/app/src/lib/bridge.ts` 检测不到 `__TAURI__` 全局时自动走 HTTP 模式（`tauri.conf.json` 因此必须保持 `withGlobalTauri: false`）。
- 窗口在 Rust 侧动态创建（`tauri.conf.json` 不再声明静态 `app.windows`），label 必须保持 `main`（capabilities/default.json 引用该 label）。

## sidecar 二进制放置

- `tauri.conf.json` 的 `bundle.externalBin` 指向 `binaries/ikuai-bypass-go`；文件名必须带目标三元组后缀，例如 Windows x64：
  - `binaries/ikuai-bypass-go-x86_64-pc-windows-msvc.exe`
- 构建命令（仓库根目录执行）：
  - `go build -trimpath -o apps/gui/binaries/ikuai-bypass-go-x86_64-pc-windows-msvc.exe ./cmd/ikuai-bypass`
- 命名说明：sidecar 基名用 `ikuai-bypass-go` 而不是 `ikuai-bypass`，因为打包产物里主程序与 sidecar 落在同一安装目录，且主程序名就是 `ikuai-bypass.exe`，同名会互相覆盖。
- `binaries/` 目录不入库（.gitignore 忽略）；`cargo build` / `tauri dev` 由 tauri-build 自动把它复制到 `target/{profile}/ikuai-bypass-go.exe`，正式打包由 bundler 改名放置。
- 开发前置：`frontends/app` 需先执行 `bun run build` 生成 `dist/`（Go 侧 embed 静态资源）。

## 运行行为

- 首次运行（配置缺失或为空）以内嵌模板（仓库根 `config.yml`，编译期 include）作种子，并清空模板中的 `cron` 与 `webui.user`/`webui.pass`：避免一启动就自动定时，也避免首启弹出 BasicAuth 登录框；模板自带的 `ikuai-url` 占位地址是必要的——Windows 上网关探测恒失败，空 `ikuai-url` 会让 `-r cronAft` 直接退出。
- 启动时选择空闲端口（优先沿用配置中已有 `webui.port`，被占用则从 19091 起扫描），把 `webui.enable: true` 与端口以保留注释的行级方式写回配置文件（已有配置不动 `cron` 与认证字段），再以 `ikuai-bypass-go -c <配置> -r cronAft` 启动 sidecar。
- 就绪探测：轮询 `GET /api/runtime/status`（15 秒超时，任何 HTTP 状态码即视为就绪，BasicAuth 开启时首轮为 401 也算就绪），成功后创建窗口。
- 配置文件启用了 `webui.user` 时窗口会先出现系统的 BasicAuth 登录框（凭据即配置中的 webui 账号），WebView2 会在会话内缓存凭据；不需要登录框可在配置中留空 `webui.user`。
- 退出（窗口关闭或应用退出事件）时杀掉 sidecar 子进程；sidecar stdout/stderr 转发进 tauri-plugin-log。
