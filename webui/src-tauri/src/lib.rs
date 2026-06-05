use std::net::TcpStream;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::menu::{CheckMenuItemBuilder, MenuBuilder, MenuItemBuilder};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, RunEvent, WebviewUrl, WebviewWindowBuilder, WindowEvent};
use tauri_plugin_autostart::{ManagerExt, MacosLauncher};
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
    /// Optional thin-client edge agent (C7-E4); killed on exit like the sidecar.
    edge_child: Mutex<Option<CommandChild>>,
}

/// Edge-agent config read from ~/.hygur-edge/config.json (C7-E4). Absent → no edge.
#[derive(serde::Deserialize)]
struct EdgeConfig {
    server: String,
    #[serde(default)]
    token: String,
    #[serde(default)]
    token_file: String,
    #[serde(default)]
    folder: String,
    #[serde(default)]
    proton_user: String,
    #[serde(default)]
    proton_password: String,
    #[serde(default)]
    proton_mailbox: String,
    #[serde(default)]
    interval_secs: u64,
}

const SIDECAR_URL: &str = "http://127.0.0.1:8420";
const SIDECAR_ADDR: &str = "127.0.0.1:8420";
const MAX_FAST_RESTARTS: u32 = 6;

// Quick-capture palette routes (HashRouter, served by the sidecar at :8420).
const QUICK_URL_ASK: &str = "http://127.0.0.1:8420/#/quick?mode=ask";
const QUICK_URL_NOTE: &str = "http://127.0.0.1:8420/#/quick?mode=note";

/// Bring the main window to the foreground (global shortcut / tray "Show").
fn show_main(app: &AppHandle) {
    if let Some(win) = app.get_webview_window("main") {
        let _ = win.show();
        let _ = win.unminimize();
        let _ = win.set_focus();
    }
}

/// Reveal the quick-capture palette in the requested mode ("note" | "ask").
fn show_quick(app: &AppHandle, mode: &str) {
    if let Some(win) = app.get_webview_window("quick") {
        let url = if mode == "note" { QUICK_URL_NOTE } else { QUICK_URL_ASK };
        if let Ok(u) = url.parse() {
            let _ = win.navigate(u);
        }
        let _ = win.center();
        let _ = win.show();
        let _ = win.set_focus();
    }
}

/// Global-shortcut toggle: hide the palette if it's up, else summon it (ask).
fn toggle_quick(app: &AppHandle) {
    if let Some(win) = app.get_webview_window("quick") {
        if win.is_visible().unwrap_or(false) {
            let _ = win.hide();
        } else {
            show_quick(app, "ask");
        }
    }
}

/// Optional thin-client edge agent (C7-E4). If ~/.hygur-edge/config.json exists,
/// spawn `hygur-sidecar edge …` to push local sources (Files / Proton Bridge) to
/// the configured cloud server. No-op when the config is absent. NOTE: the edge
/// behaviour itself is validated via the `hygur edge` CLI — this is a convenience
/// auto-spawn (its runtime is not exercised by the desktop CI build).
fn maybe_spawn_edge(app: &AppHandle) {
    let home = match std::env::var("HOME") {
        Ok(h) => h,
        Err(_) => return,
    };
    let path = std::path::Path::new(&home).join(".hygur-edge/config.json");
    let raw = match std::fs::read_to_string(&path) {
        Ok(r) => r,
        Err(_) => return, // no config → no edge agent
    };
    let cfg: EdgeConfig = match serde_json::from_str(&raw) {
        Ok(c) => c,
        Err(e) => {
            log::warn!("[edge] bad config {}: {e}", path.display());
            return;
        }
    };
    if cfg.server.is_empty() {
        return;
    }

    let mut args: Vec<String> = vec!["edge".into(), "--server".into(), cfg.server.clone()];
    if !cfg.token_file.is_empty() {
        args.push("--token-file".into());
        args.push(cfg.token_file.clone());
    }
    if !cfg.folder.is_empty() {
        args.push("--folder".into());
        args.push(cfg.folder.clone());
    }
    if !cfg.proton_user.is_empty() {
        args.push("--proton".into());
        args.push("--proton-user".into());
        args.push(cfg.proton_user.clone());
        if !cfg.proton_mailbox.is_empty() {
            args.push("--proton-mailbox".into());
            args.push(cfg.proton_mailbox.clone());
        }
    }
    if cfg.interval_secs > 0 {
        args.push("--interval".into());
        args.push(format!("{}s", cfg.interval_secs));
    }

    let mut envs: std::collections::HashMap<String, String> = std::collections::HashMap::new();
    if !cfg.token.is_empty() {
        envs.insert("HYGUR_EDGE_TOKEN".into(), cfg.token.clone());
    }
    if !cfg.proton_password.is_empty() {
        envs.insert("HYGUR_PROTON_PASSWORD".into(), cfg.proton_password.clone());
    }

    let cmd = match app.shell().sidecar("hygur-sidecar") {
        Ok(c) => c.args(args).envs(envs),
        Err(e) => {
            log::error!("[edge] sidecar command: {e}");
            return;
        }
    };
    match cmd.spawn() {
        Ok((mut rx, child)) => {
            *app.state::<Sidecar>().edge_child.lock().unwrap() = Some(child);
            tauri::async_runtime::spawn(async move {
                while let Some(ev) = rx.recv().await {
                    if let CommandEvent::Stdout(l) | CommandEvent::Stderr(l) = ev {
                        log::info!("[edge] {}", String::from_utf8_lossy(&l).trim_end());
                    }
                }
            });
            log::info!("[edge] agent spawned (server={})", cfg.server);
        }
        Err(e) => log::error!("[edge] spawn failed: {e}"),
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
        .plugin(tauri_plugin_autostart::init(MacosLauncher::LaunchAgent, None))
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, shortcut, event| {
                    if event.state() == ShortcutState::Pressed
                        && shortcut.matches(Modifiers::SUPER | Modifiers::SHIFT, Code::KeyH)
                    {
                        toggle_quick(app);
                    }
                })
                .build(),
        )
        .manage(Sidecar {
            child: Mutex::new(None),
            shutting_down: AtomicBool::new(false),
            restarts: Mutex::new((0, Instant::now())),
            edge_child: Mutex::new(None),
        })
        .setup(|app| {
            if cfg!(debug_assertions) {
                app.handle().plugin(
                    tauri_plugin_log::Builder::default()
                        .level(log::LevelFilter::Info)
                        .build(),
                )?;
            }

            // Global hotkey ⌘⇧H toggles the quick-capture palette (the combo the
            // SwiftUI HotkeyManager used). The main window stays reachable via the
            // tray "Show Hygur" + the dock.
            let summon = Shortcut::new(Some(Modifiers::SUPER | Modifiers::SHIFT), Code::KeyH);
            if let Err(e) = app.global_shortcut().register(summon) {
                log::warn!("global shortcut registration failed: {e}");
            }

            // Frameless, always-on-top quick-capture palette in its own window.
            // Created hidden + navigated to the sidecar route once it binds
            // (below). Spotlight-style: it hides itself when it loses focus.
            let quick = WebviewWindowBuilder::new(
                app.handle(),
                "quick",
                WebviewUrl::App("index.html".into()),
            )
            .title("Hygur")
            .inner_size(660.0, 460.0)
            .decorations(false)
            .always_on_top(true)
            .skip_taskbar(true)
            .resizable(false)
            .center()
            .visible(false)
            .build()?;
            let quick_for_blur = quick.clone();
            quick.on_window_event(move |event| {
                if let WindowEvent::Focused(focused) = event {
                    if !*focused {
                        let _ = quick_for_blur.hide();
                    }
                }
            });

            // Menu bar tray (parity with the SwiftUI "sparkles" menubar icon).
            // A monochrome template image so macOS tints it for light/dark menus.
            let autostart_enabled = app.autolaunch().is_enabled().unwrap_or(false);
            let autostart_i = CheckMenuItemBuilder::with_id("autostart", "Launch at login")
                .checked(autostart_enabled)
                .build(app)?;
            let show_i = MenuItemBuilder::with_id("show", "Show Hygur").build(app)?;
            let quit_i = MenuItemBuilder::with_id("quit", "Quit Hygur").build(app)?;
            // Quick-access actions at the top of the tray menu.
            let note_i = MenuItemBuilder::with_id("quick-note", "Quick note").build(app)?;
            let ask_i = MenuItemBuilder::with_id("quick-ask", "Ask Hygur").build(app)?;
            let menu = MenuBuilder::new(app)
                .items(&[&note_i, &ask_i])
                .separator()
                .items(&[&show_i, &autostart_i])
                .separator()
                .item(&quit_i)
                .build()?;
            let tray_icon = tauri::image::Image::from_bytes(include_bytes!("../icons/tray.png"))?;
            let autostart_check = autostart_i.clone();
            TrayIconBuilder::new()
                .icon(tray_icon)
                .icon_as_template(true)
                .menu(&menu)
                .on_menu_event(move |app, event| match event.id().as_ref() {
                    "autostart" => {
                        let mgr = app.autolaunch();
                        let on = mgr.is_enabled().unwrap_or(false);
                        let _ = if on { mgr.disable() } else { mgr.enable() };
                        let _ = autostart_check.set_checked(!on);
                    }
                    "quick-note" => show_quick(app, "note"),
                    "quick-ask" => show_quick(app, "ask"),
                    "show" => show_main(app),
                    "quit" => app.exit(0),
                    _ => {}
                })
                .build(app)?;

            // Spawn + supervise the bundled Hygur sidecar (serves the WebUI on :8420).
            spawn_sidecar(app.handle().clone());

            // Optional thin-client edge agent (C7-E4): pushes local sources to a
            // cloud server when ~/.hygur-edge/config.json is present (else no-op).
            maybe_spawn_edge(app.handle());

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
                    // Warm the quick palette at the sidecar origin (stays hidden
                    // until summoned by the shortcut or the tray).
                    if let Some(q) = h.get_webview_window("quick") {
                        if let Ok(url) = QUICK_URL_ASK.parse() {
                            let _ = q.navigate(url);
                        }
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
                let edge = state.edge_child.lock().unwrap().take();
                if let Some(edge) = edge {
                    let _ = edge.kill();
                }
            }
            _ => {}
        });
}
