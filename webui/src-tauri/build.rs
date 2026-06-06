fn main() {
  // Declare the app commands so Tauri generates their ACL permissions
  // (`allow-get-desktop-config` / `allow-set-desktop-config`). Required to grant
  // them to the loopback sidecar origin (a "remote" context to Tauri) in
  // capabilities/default.json — app commands are otherwise denied off-origin.
  tauri_build::try_build(
    tauri_build::Attributes::new().app_manifest(
      tauri_build::AppManifest::new()
        .commands(&["get_desktop_config", "set_desktop_config", "open_external"]),
    ),
  )
  .expect("failed to run tauri-build");
}
