use std::fs;
use std::path::PathBuf;

fn read_repo_file(rel: &str) -> String {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join(rel);
    fs::read_to_string(&path).unwrap_or_else(|e| {
        panic!("failed to read '{}': {e}", path.display());
    })
}

#[test]
fn tauri_config_declares_go_sidecar_bundle() {
    let conf = read_repo_file("tauri.conf.json");
    assert!(
        conf.contains("\"externalBin\""),
        "bundle.externalBin must declare the Go sidecar binary"
    );
    assert!(
        conf.contains("binaries/ikuai-bypass-go"),
        "externalBin entry must point at binaries/ikuai-bypass-go"
    );
    assert!(
        !conf.contains("\"withGlobalTauri\": true"),
        "withGlobalTauri must stay off: the window loads the Go server over HTTP and a __TAURI__ global would hijack bridge.ts into broken IPC mode"
    );
    assert!(
        !conf.contains("\"url\""),
        "windows must be created at runtime with the sidecar port, not a static url"
    );
}

#[test]
fn shell_crate_has_no_core_dependency() {
    let manifest = read_repo_file("Cargo.toml");
    assert!(
        !manifest.contains("ikb-core"),
        "the Tauri shell must not depend on ikb-core; business logic lives in the Go sidecar"
    );
    assert!(
        manifest.contains("tauri-plugin-shell"),
        "tauri-plugin-shell is required to spawn the Go sidecar"
    );
}

#[test]
fn tauri_config_file_exists() {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("tauri.conf.json");
    assert!(path.is_file(), "missing tauri config: {}", path.display());
}
