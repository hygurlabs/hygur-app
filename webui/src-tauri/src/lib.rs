use std::net::TcpStream;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::menu::{MenuBuilder, MenuItemBuilder};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, RunEvent};
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Modifiers, Shortcut, ShortcutState};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// Supervises the bundled sidecar: holds the child to kill on exit, a shutdown
/// flag (so we don't respawn during app quit), and a rapid-restart guard.
struct Sidecar {
    child: Mutex<Option<CommandChild>>,
    shutting_down: AtomicBool,
    /// (consecutive fast restarts, last spawn time) — bounds a crash loop.
    restarts: Mutex<(u32, Instant)>,
}

const SIDECAR_URL: &str = "http://127.0.0.1:8420";
const SIDECAR_ADDR: &str = "127.0.0.1:8420";
const MAX_FAST_RESTARTS: u32 = 6;

/// Bring the main window to the foreground (global shortcut / tray "Show").
fn show_main(app: &AppHandle) {
    if let Some(win) = app.get_webview_window("main") {
        let _ = win.show();
        let _ = win.unminimize();
        let _ = win.set_focus();
    }
}

/// Spawns the sidecar and watches it. On an UNEXPECTED exit — notably the
/// sidecar SIGTERM-ing itself to apply a config change (see config.go), which
/// the supervisor is expected to relaunch — it respawns (bounded backoff).
fn spawn_sidecar(app: AppHandle) {
    let (mut rx, child) = match app.shell().sidecar("hygur-sidecar").and_then(|c| c.spawn()) {
        Ok(v) => v,
        Err(e) => {
            log::error!("[sidecar] spawn failed: {e}");
            return;
        }
    };
    {
        // Scope the State guard so it drops before `app` is moved into the task.
        let state = app.state::<Sidecar>();
        *state.child.lock().unwrap() = Some(child);
        state.restarts.lock().unwrap().1 = Instant::now();
    }

    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(line) | CommandEvent::Stderr(line) => {
                    log::info!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
                }
                CommandEvent::Terminated(payload) => {
                    log::warn!("[sidecar] terminated: {:?}", payload);
                    let state = app.state::<Sidecar>();
                    if state.shutting_down.load(Ordering::SeqCst) {
                        return; // app is quitting — leave it dead
                    }
                    let respawn = {
                        let mut r = state.restarts.lock().unwrap();
                        if r.1.elapsed() > Duration::from_secs(5) {
                            r.0 = 0; // it ran stably; this is a fresh, isolated exit
                        }
                        r.0 += 1;
                        r.0 <= MAX_FAST_RESTARTS
                    };
                    if respawn {
                        log::info!("[sidecar] respawning…");
                        std::thread::sleep(Duration::from_millis(500));
                        spawn_sidecar(app.clone());
                    } else {
                        log::error!("[sidecar] too many rapid restarts — giving up");
                    }
                    return;
                }
                _ => {}
            }
        }
    });
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, shortcut, event| {
                    if event.state() == ShortcutState::Pressed
                        && shortcut.matches(Modifiers::SUPER | Modifiers::SHIFT, Code::KeyH)
                    {
                        show_main(app);
                    }
                })
                .build(),
        )
        .manage(Sidecar {
            child: Mutex::new(None),
            shutting_down: AtomicBool::new(false),
            restarts: Mutex::new((0, Instant::now())),
        })
        .setup(|app| {
            if cfg!(debug_assertions) {
                app.handle().plugin(
                    tauri_plugin_log::Builder::default()
                        .level(log::LevelFilter::Info)
                        .build(),
                )?;
            }

            // Global summon shortcut ⌘⇧H (parity with the SwiftUI HotkeyManager).
            let summon = Shortcut::new(Some(Modifiers::SUPER | Modifiers::SHIFT), Code::KeyH);
            if let Err(e) = app.global_shortcut().register(summon) {
                log::warn!("global shortcut registration failed: {e}");
            }

            // Menu bar tray (parity with the SwiftUI "sparkles" menubar icon).
            // A monochrome template image so macOS tints it for light/dark menus.
            let show_i = MenuItemBuilder::with_id("show", "Show Hygur").build(app)?;
            let quit_i = MenuItemBuilder::with_id("quit", "Quit Hygur").build(app)?;
            let menu = MenuBuilder::new(app).items(&[&show_i, &quit_i]).build()?;
            let tray_icon = tauri::image::Image::from_bytes(include_bytes!("../icons/tray.png"))?;
            TrayIconBuilder::new()
                .icon(tray_icon)
                .icon_as_template(true)
                .menu(&menu)
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "show" => show_main(app),
                    "quit" => app.exit(0),
                    _ => {}
                })
                .build(app)?;

            // Spawn + supervise the bundled Hygur sidecar (serves the WebUI on :8420).
            spawn_sidecar(app.handle().clone());

            // Wait for the sidecar to bind, then point the (hidden) main window at
            // the sidecar-served WebUI — same-origin, so the auth token is injected
            // exactly as in a browser — and reveal it.
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
        .run(|app_handle, event| match event {
            RunEvent::ExitRequested { .. } | RunEvent::Exit => {
                let state = app_handle.state::<Sidecar>();
                state.shutting_down.store(true, Ordering::SeqCst);
                let child = state.child.lock().unwrap().take();
                if let Some(child) = child {
                    let _ = child.kill();
                }
            }
            _ => {}
        });
}
