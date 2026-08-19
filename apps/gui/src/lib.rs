#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_log::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .run(tauri::generate_context!())
        .unwrap_or_else(|e| {
            eprintln!("[ERR:启动失败] Failed to run tauri application: {}", e);
        });
}
