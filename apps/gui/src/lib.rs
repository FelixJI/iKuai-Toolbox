// lib.rs Tauri 薄壳：选端口 → 注入 webui 配置 → 启动 Go sidecar（ikuai-bypass-go
// -r cronAft）→ 探测就绪后打开 http://127.0.0.1:{port} 窗口；退出时杀子进程。
// lib.rs Thin Tauri shell: pick a port → inject webui config → spawn the Go
// sidecar (ikuai-bypass-go -r cronAft) → open a window on
// http://127.0.0.1:{port} once ready; kill the child on exit.

use std::net::TcpListener;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use log::Level;
use serde_yaml::Value;
use tauri::Manager;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const SIDECAR_PROGRAM: &str = "ikuai-bypass-go";
const RUN_MODE: &str = "cronAft";
const WINDOW_LABEL: &str = "main";
const WINDOW_TITLE: &str = "iKuai Bypass";
const WINDOW_WIDTH: f64 = 430.0;
const WINDOW_HEIGHT: f64 = 880.0;
const READY_TIMEOUT: Duration = Duration::from_secs(15);
const PROBE_INTERVAL: Duration = Duration::from_millis(250);
const PROBE_CONNECT_TIMEOUT: Duration = Duration::from_millis(800);
const PORT_SCAN_START: u16 = 19091;
const PORT_SCAN_END: u16 = 19110;
const APP_DIR_NAME: &str = "ikuai-bypass";
// 首次运行种子：仓库根 config.yml 模板（唯一真来源）。
// First-run seed: the repo-root config.yml template (single source of truth).
const EMBEDDED_TEMPLATE: &str = include_str!("../../../config.yml");

// sidecar 子进程句柄与存活标记，退出事件据此清理。
// Sidecar child handle plus liveness flag, consumed by the exit cleanup.
#[derive(Default)]
struct SidecarState {
    child: Mutex<Option<CommandChild>>,
    terminated: AtomicBool,
}

// 桌面端与 Go 侧 config.DefaultConfigPath 保持同一路径形状。
// On desktop the path shape mirrors the Go config.DefaultConfigPath.
fn resolve_gui_config_path<R: tauri::Runtime>(app: &tauri::AppHandle<R>) -> PathBuf {
    if cfg!(target_os = "android") || cfg!(target_os = "ios") {
        let path = app.path();
        let base_dir = path
            .app_config_dir()
            .or_else(|_| path.app_data_dir())
            .or_else(|_| path.app_local_data_dir())
            .unwrap_or_else(|_| std::env::temp_dir().join(APP_DIR_NAME));
        return base_dir.join("config.yml");
    }
    dirs::config_dir()
        .map(|d| d.join(APP_DIR_NAME).join("config.yml"))
        .unwrap_or_else(|| PathBuf::from("./config.yml"))
}

fn ensure_config_parent_dir(config_path: &std::path::Path) {
    if let Some(parent) = config_path.parent()
        && !parent.as_os_str().is_empty()
    {
        let _ = std::fs::create_dir_all(parent);
    }
}

// 读取现有 webui 设置：返回 (配置端口, enable)；空文件返回 (None, false)。
// Reads the existing webui settings: (configured port, enable); an empty file
// yields (None, false).
fn parse_webui(raw: &str) -> Result<(Option<u16>, bool), String> {
    if raw.trim().is_empty() {
        return Ok((None, false));
    }
    let value: Value =
        serde_yaml::from_str(raw).map_err(|e| format!("parse yaml failed: {e}"))?;
    let Some(webui) = value.get("webui").filter(|v| !v.is_null()) else {
        return Ok((None, false));
    };
    let port = match webui.get("port") {
        Some(Value::String(s)) => s.trim().parse::<u16>().ok(),
        Some(Value::Number(n)) => n.as_u64().and_then(|p| u16::try_from(p).ok()),
        _ => None,
    };
    let enable = matches!(webui.get("enable"), Some(Value::Bool(true)));
    Ok((port, enable))
}

fn port_bindable(port: u16) -> bool {
    TcpListener::bind(("0.0.0.0", port)).is_ok()
}

// 优先沿用配置中的端口（保持稳定），被占用再从 19091 起扫描。
// Prefer the configured port (keeps it stable); scan from 19091 when busy.
fn choose_port(preferred: Option<u16>) -> Option<u16> {
    if let Some(p) = preferred
        && port_bindable(p)
    {
        return Some(p);
    }
    (PORT_SCAN_START..=PORT_SCAN_END).find(|p| port_bindable(*p))
}

// 顶层键提取：仅匹配列 0 的 “key:” 行，返回 (键, 冒号后的原文)。
// Extracts a top-level key: only column-0 `key:` lines; returns
// (key, raw text after the colon).
fn top_level_key(line: &str) -> Option<(&str, &str)> {
    if line.starts_with([' ', '\t', '#']) || line.trim().is_empty() {
        return None;
    }
    let (key, rest) = line.split_once(':')?;
    let key = key.trim();
    if key.is_empty() || key.contains([' ', '\t', '#']) {
        return None;
    }
    Some((key, rest))
}

// webui 块内子键提取：缩进行里的 “key:” 行，返回 (键, 缩进宽度)。
// Extracts a child key inside the webui block: an indented `key:` line;
// returns (key, indent width).
fn child_key(line: &str) -> Option<(String, usize)> {
    let indent = line.len() - line.trim_start().len();
    let trimmed = line.trim_start();
    if indent == 0 || trimmed.is_empty() || trimmed.starts_with('#') {
        return None;
    }
    let (key, _) = trimmed.split_once(':')?;
    let key = key.trim();
    if key.is_empty() || key.starts_with('-') {
        return None;
    }
    Some((key.to_string(), indent))
}

// 仅替换 “key: value # 注释” 行的值，保留缩进、键与行内注释。
// Replaces only the value of a `key: value # comment` line, keeping the
// indent, key, and trailing comment.
fn replace_scalar_value(line: &str, new_value: &str) -> String {
    let indent_len = line.len() - line.trim_start().len();
    let indent = &line[..indent_len];
    let trimmed = line.trim_start();
    let Some(colon) = trimmed.find(':') else {
        return line.to_string();
    };
    let key = trimmed[..colon].trim_end();
    let rest = trimmed[colon + 1..].trim_start();
    let comment = if let Some(stripped) = rest.strip_prefix('"') {
        stripped
            .find('"')
            .and_then(|close| stripped[close + 1..].find('#').map(|c| close + 1 + c))
            .map(|start| &stripped[start..])
    } else {
        rest.find('#').map(|c| &rest[c..])
    };
    match comment {
        Some(c) => format!("{indent}{key}: {new_value} {c}"),
        None => format!("{indent}{key}: {new_value}"),
    }
}

// 结构化兜底重写（丢失注释），仅在行级手术无法进行时使用。
// Structured fallback rewrite (comments lost), used only when line surgery
// cannot apply.
fn structured_rewrite(raw: &str, port: u16) -> Result<String, String> {
    let mut value: Value =
        serde_yaml::from_str(raw).map_err(|e| format!("parse yaml failed: {e}"))?;
    let mapping = value
        .as_mapping_mut()
        .ok_or_else(|| "config root is not a mapping".to_string())?;
    let webui_key = Value::String("webui".to_string());
    let mut webui = match mapping.get(&webui_key) {
        Some(Value::Mapping(m)) => m.clone(),
        _ => serde_yaml::Mapping::new(),
    };
    webui.insert(Value::String("port".into()), Value::String(port.to_string()));
    webui.insert(Value::String("enable".into()), Value::Bool(true));
    mapping.insert(webui_key, Value::Mapping(webui));
    let out = serde_yaml::to_string(&value).map_err(|e| format!("serialize yaml failed: {e}"))?;
    Ok(out.strip_prefix("---\n").unwrap_or(&out).to_string())
}

// 把 webui.enable/webui.port 写进配置原文：行级手术保留注释，异常形态退回结构化重写。
// Writes webui.enable/webui.port into the raw config: line surgery preserves
// comments; unusual shapes fall back to the structured rewrite.
fn inject_webui(raw: &str, port: u16) -> Result<String, String> {
    let port_value = format!("\"{port}\"");
    if raw.trim().is_empty() {
        return Ok(format!("webui:\n  port: {port_value}\n  enable: true\n"));
    }
    let ends_with_newline = raw.ends_with('\n');
    let newline = if raw.contains("\r\n") { "\r\n" } else { "\n" };
    let mut lines: Vec<String> = raw
        .lines()
        .map(|l| l.trim_end_matches('\r').to_string())
        .collect();

    let finish = |lines: &[String]| -> String {
        let mut out = lines.join(newline);
        if ends_with_newline {
            out.push_str(newline);
        }
        out
    };

    let verify = |out: &str| parse_webui(out) == Ok((Some(port), true));
    let fallback = |raw: &str| -> Result<String, String> {
        let out = structured_rewrite(raw, port)?;
        if verify(&out) {
            Ok(out)
        } else {
            Err("structured rewrite verification failed".to_string())
        }
    };

    let Some(webui_idx) = lines.iter().position(|l| {
        top_level_key(l).is_some_and(|(k, _)| k == "webui")
    }) else {
        lines.push(String::new());
        lines.push("webui:".to_string());
        lines.push(format!("  port: {port_value}"));
        lines.push("  enable: true".to_string());
        let out = finish(&lines);
        return if verify(&out) { Ok(out) } else { fallback(raw) };
    };

    let (_, rest) = top_level_key(&lines[webui_idx]).ok_or("webui line vanished")?;
    let inline = rest.trim();
    if !inline.is_empty() && !inline.starts_with('#') {
        // 行内值（流式映射/标量）无法做安全的行级手术。
        // Inline values (flow mappings/scalars) defeat safe line surgery.
        return fallback(raw);
    }

    let mut port_line: Option<usize> = None;
    let mut enable_line: Option<usize> = None;
    let mut child_indent: Option<String> = None;
    let mut i = webui_idx + 1;
    while i < lines.len() {
        let line = &lines[i];
        if line.trim().is_empty() {
            i += 1;
            continue;
        }
        if !line.starts_with([' ', '\t']) {
            break;
        }
        if let Some((key, indent)) = child_key(line) {
            if child_indent.is_none() {
                child_indent = Some(line[..indent].to_string());
            }
            match key.as_str() {
                "port" => port_line = Some(i),
                "enable" => enable_line = Some(i),
                _ => {}
            }
        }
        i += 1;
    }

    if let Some(p) = port_line {
        lines[p] = replace_scalar_value(&lines[p], &port_value);
    }
    if let Some(e) = enable_line {
        lines[e] = replace_scalar_value(&lines[e], "true");
    }
    let indent = child_indent.unwrap_or_else(|| "  ".to_string());
    if port_line.is_none() {
        lines.insert(webui_idx + 1, format!("{indent}port: {port_value}"));
    }
    if enable_line.is_none() {
        lines.insert(webui_idx + 1, format!("{indent}enable: true"));
    }

    let out = finish(&lines);
    if verify(&out) {
        Ok(out)
    } else {
        fallback(raw)
    }
}

// 种子净化：清空模板里的 cron 与 webui.user/pass——首次运行不自动定时、不出登录框。
// Seed sanitation: blank cron and webui.user/pass from the template, so the
// first run never auto-schedules and never shows a login dialog.
fn sanitize_seed(raw: &str) -> String {
    let newline = if raw.contains("\r\n") { "\r\n" } else { "\n" };
    let mut lines: Vec<String> = raw
        .lines()
        .map(|l| l.trim_end_matches('\r').to_string())
        .collect();
    if let Some(idx) = lines
        .iter()
        .position(|l| top_level_key(l).is_some_and(|(k, _)| k == "cron"))
    {
        lines[idx] = replace_scalar_value(&lines[idx], "\"\"");
    }
    let webui_idx = lines
        .iter()
        .position(|l| top_level_key(l).is_some_and(|(k, _)| k == "webui"));
    if let Some(webui_idx) = webui_idx {
        for key in ["user", "pass"] {
            let mut target: Option<usize> = None;
            let mut i = webui_idx + 1;
            while i < lines.len() {
                let line = &lines[i];
                if !line.trim().is_empty() && !line.starts_with([' ', '\t']) {
                    break;
                }
                if let Some((k, _)) = child_key(line)
                    && k == key
                {
                    target = Some(i);
                    break;
                }
                i += 1;
            }
            if let Some(t) = target {
                lines[t] = replace_scalar_value(&lines[t], "\"\"");
            }
        }
    }
    let mut out = lines.join(newline);
    if raw.ends_with('\n') {
        out.push_str(newline);
    }
    out
}

fn spawn_sidecar<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    config_path: &std::path::Path,
) -> Result<(), String> {
    let sidecar = app
        .shell()
        .sidecar(SIDECAR_PROGRAM)
        .map_err(|e| format!("resolve sidecar binary: {e}"))?;
    let config_arg = config_path.to_string_lossy().to_string();
    let (mut rx, child) = sidecar
        .args(["-c", config_arg.as_str(), "-r", RUN_MODE])
        .spawn()
        .map_err(|e| format!("spawn sidecar: {e}"))?;
    {
        let state = app.state::<SidecarState>();
        let mut guard = state
            .child
            .lock()
            .map_err(|e| format!("sidecar state lock: {e}"))?;
        *guard = Some(child);
    }

    // stdout/stderr 逐行转发进 tauri-plugin-log。
    // Forward stdout/stderr line by line into tauri-plugin-log.
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(bytes) => log_line(Level::Info, &bytes),
                CommandEvent::Stderr(bytes) => log_line(Level::Warn, &bytes),
                CommandEvent::Error(err) => log::error!(target: "sidecar", "{err}"),
                CommandEvent::Terminated(payload) => {
                    handle
                        .state::<SidecarState>()
                        .terminated
                        .store(true, Ordering::SeqCst);
                    log::warn!(
                        target: "sidecar",
                        "sidecar terminated: code={:?} signal={:?}",
                        payload.code,
                        payload.signal
                    );
                }
                _ => {}
            }
        }
    });
    Ok(())
}

fn log_line(level: Level, bytes: &[u8]) {
    let line = String::from_utf8_lossy(bytes).trim_end().to_string();
    if !line.is_empty() {
        log::log!(target: "sidecar", level, "{line}");
    }
}

fn kill_sidecar<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    if let Some(child) = app
        .state::<SidecarState>()
        .child
        .lock()
        .ok()
        .and_then(|mut guard| guard.take())
    {
        match child.kill() {
            Ok(()) => log::info!(target: "sidecar", "sidecar killed on app exit"),
            Err(e) => log::warn!(target: "sidecar", "failed to kill sidecar: {e}"),
        }
    }
}

// 就绪探测：任何 HTTP 响应行（含 BasicAuth 的 401）都视为服务已起。
// Readiness probe: any HTTP status line (including a BasicAuth 401) counts.
async fn probe_ready(port: u16) -> bool {
    let attempt = async {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port)).await.ok()?;
        stream
            .write_all(
                format!("GET /api/runtime/status HTTP/1.0\r\nHost: 127.0.0.1:{port}\r\n\r\n")
                    .as_bytes(),
            )
            .await
            .ok()?;
        let mut buf = [0u8; 64];
        let n = stream.read(&mut buf).await.ok()?;
        std::str::from_utf8(&buf[..n])
            .ok()?
            .starts_with("HTTP/")
            .then_some(())
    };
    tokio::time::timeout(PROBE_CONNECT_TIMEOUT, attempt)
        .await
        .ok()
        .flatten()
        .is_some()
}

async fn wait_until_ready<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    port: u16,
) -> Result<(), String> {
    let deadline = Instant::now() + READY_TIMEOUT;
    loop {
        if app.state::<SidecarState>().terminated.load(Ordering::SeqCst) {
            return Err(format!(
                "sidecar exited before http://127.0.0.1:{port} became reachable (check config yaml / port)"
            ));
        }
        if probe_ready(port).await {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "http://127.0.0.1:{port} not ready within {}s",
                READY_TIMEOUT.as_secs()
            ));
        }
        tokio::time::sleep(PROBE_INTERVAL).await;
    }
}

fn build_window<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    url: tauri::Url,
) -> Result<(), String> {
    tauri::WebviewWindowBuilder::new(
        app,
        WINDOW_LABEL,
        tauri::WebviewUrl::External(url),
    )
    .title(WINDOW_TITLE)
    .inner_size(WINDOW_WIDTH, WINDOW_HEIGHT)
    .resizable(true)
    .build()
    .map(|_| ())
    .map_err(|e| format!("create window: {e}"))
}

// 错误提示窗（data:text/plain），启动失败时至少留下可见信息。
// Error notice window (data:text/plain): startup failures at least leave
// something visible.
fn open_error_window<R: tauri::Runtime>(app: &tauri::AppHandle<R>, message: &str) {
    let mut encoded = String::from("data:text/plain;charset=utf-8,");
    for byte in message.as_bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                encoded.push(*byte as char);
            }
            _ => encoded.push_str(&format!("%{byte:02X}")),
        }
    }
    match encoded.parse::<tauri::Url>() {
        Ok(url) => {
            if let Err(e) = build_window(app, url) {
                log::error!(target: "shell", "{e}");
            }
        }
        Err(e) => log::error!(target: "shell", "encode error window: {e}"),
    }
}

#[cfg(desktop)]
fn launch_desktop_shell<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    let config_path = resolve_gui_config_path(app);
    ensure_config_parent_dir(&config_path);

    // 首次运行（缺失/空配置）以内嵌模板为种子：Windows 上 GetGatewayV4 恒失败，
    // 空 ikuai-url 会让 cronAft 直接退出，模板自带的占位地址保证服务能起。
    // First run (missing/empty config) seeds from the embedded template: on
    // Windows GetGatewayV4 always fails and a blank ikuai-url makes cronAft
    // exit immediately; the template's placeholder URL keeps the server up.
    let existing = std::fs::read_to_string(&config_path).unwrap_or_default();
    let first_run = existing.trim().is_empty();
    let raw = if first_run {
        sanitize_seed(EMBEDDED_TEMPLATE)
    } else {
        existing
    };
    let (preferred, enabled) = parse_webui(&raw).unwrap_or((None, false));
    let Some(port) = choose_port(preferred) else {
        let message = format!(
            "启动失败：{PORT_SCAN_START}-{PORT_SCAN_END} 范围内没有可用端口\nstartup failed: no free port in {PORT_SCAN_START}-{PORT_SCAN_END}"
        );
        log::error!(target: "shell", "{message}");
        open_error_window(app, &message);
        return;
    };
    log::info!(
        target: "shell",
        "config: {} port: {port} first_run: {first_run}",
        config_path.display()
    );

    if first_run || preferred != Some(port) || !enabled {
        match inject_webui(&raw, port) {
            Ok(updated) => {
                if let Err(e) = std::fs::write(&config_path, &updated) {
                    log::warn!(target: "shell", "write config failed: {e}");
                }
            }
            // 注入失败则按原文件启动，让 sidecar 报出具体配置错误。
            // Launch as-is on injection failure and let the sidecar report
            // the concrete config error.
            Err(e) => log::warn!(target: "shell", "webui injection skipped: {e}"),
        }
    }

    if let Err(e) = spawn_sidecar(app, &config_path) {
        let message = format!(
            "启动失败：{e}\n请按 apps/gui/README.md 放置 sidecar 二进制后重试\nstartup failed: {e}"
        );
        log::error!(target: "shell", "{message}");
        open_error_window(app, &message);
        return;
    }

    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        match wait_until_ready(&handle, port).await {
            Ok(()) => {
                let url = format!("http://127.0.0.1:{port}/");
                match url.parse::<tauri::Url>() {
                    Ok(url) => {
                        if let Err(e) = build_window(&handle, url) {
                            log::error!(target: "shell", "{e}");
                        }
                    }
                    Err(e) => log::error!(target: "shell", "parse window url: {e}"),
                }
            }
            Err(e) => {
                log::error!(target: "shell", "startup failed: {e}");
                let log_dir = handle
                    .path()
                    .app_log_dir()
                    .map(|d| d.display().to_string())
                    .unwrap_or_default();
                open_error_window(
                    &handle,
                    &format!(
                        "启动失败：{e}\n配置文件 / config: {}\n日志目录 / log dir: {log_dir}\nsidecar 输出见上述日志",
                        config_path.display()
                    ),
                );
            }
        }
    });
}

// 移动端无法托管 sidecar：远程连接模式是后续任务，这里仅给出提示窗。
// Mobile cannot host the sidecar: the remote-connect mode is a follow-up
// task; only a notice window is shown for now.
#[cfg(mobile)]
fn launch_mobile_notice<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    open_error_window(
        app,
        "移动端远程连接模式尚未实现（本任务仅桌面壳）\nmobile remote-connect mode is not implemented yet (desktop shell only)",
    );
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_log::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .manage(SidecarState::default())
        .setup(|app| {
            let handle = app.handle().clone();
            #[cfg(desktop)]
            launch_desktop_shell(&handle);
            #[cfg(mobile)]
            launch_mobile_notice(&handle);
            Ok(())
        })
        .build(tauri::generate_context!())
        .unwrap_or_else(|e| {
            eprintln!("[ERR:启动失败] Failed to run tauri application: {e}");
            std::process::exit(1);
        });

    app.run(|handle, event| {
        if matches!(
            event,
            tauri::RunEvent::ExitRequested { .. } | tauri::RunEvent::Exit
        ) {
            kill_sidecar(handle);
        }
    });
}
