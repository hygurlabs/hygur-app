use std::net::TcpStream;
use std::sync::Mutex;
use std::time::Duration;

use tauri::{Manager, RunEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// Holds the supervised sidecar child so it can be killed when the app exits.
struct Sidecar(Mutex<Option<CommandChild>>);

const SIDECAR_URL: &str = "http://127.0.0.1:8420";
const SIDECAR_ADDR: &str = "127.0.0.1:8420";

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(Sidecar(Mutex::new(None)))
        .setup(|app| {
            if cfg!(debug_assertions) {
                app.handle().plugin(
                    tauri_plugin_log::Builder::default()
                        .level(log::LevelFilter::Info)
                        .build(),
                )?;
            }

            // Spawn the bundled Hygur sidecar (the same Go binary; full-local or
            // --mode=edge for device sources). It serves the WebUI on :8420.
            let (mut rx, child) = app.shell().sidecar("hygur-sidecar")?.spawn()?;
            app.state::<Sidecar>().0.lock().unwrap().replace(child);

            // Drain + log the sidecar's output so the pipe never blocks it.
            tauri::async_runtime::spawn(async move {
                while let Some(event) = rx.recv().await {
                    match event {
                        CommandEvent::Stdout(line) | CommandEvent::Stderr(line) => {
                            log::info!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
                        }
                        CommandEvent::Terminated(payload) => {
                            log::warn!("[sidecar] terminated: {:?}", payload);
                        }
                        _ => {}
                    }
                }
            });

            // Wait for the sidecar to bind, then point the (hidden) main window at
            // the sidecar-served WebUI — same-origin, so the sidecar injects the
            // auth token exactly as in a browser — and reveal it.
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                if let Ok(addr) = SIDECAR_ADDR.parse() {
                    for _ in 0..150 {
                        if TcpStream::connect_timeout(&addr, Duration::from_millis(300)).is_ok() {
                            break;
                        }
                        std::thread::sleep(Duration::from_millis(200));
                    }
                }
                let h = handle.clone();
                let _ = handle.run_on_main_thread(move || {
                    if let Some(win) = h.get_webview_window("main") {
                        if let Ok(url) = SIDECAR_URL.parse() {
                            let _ = win.navigate(url);
                        }
                        let _ = win.show();
                    }
                });
            });

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            if let RunEvent::Exit = event {
                if let Some(child) = app_handle.state::<Sidecar>().0.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        });
}
