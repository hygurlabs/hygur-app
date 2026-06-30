use std::net::TcpStream;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::menu::{CheckMenuItemBuilder, MenuBuilder, MenuItemBuilder};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Emitter, Manager, RunEvent, WebviewUrl, WebviewWindowBuilder, WindowEvent};
use tauri_plugin_autostart::{ManagerExt, MacosLauncher};
use tauri_plugin_deep_link::DeepLinkExt;
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Modifiers, Shortcut, ShortcutState};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tauri_plugin_opener::OpenerExt;

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

// ── Desktop engine-mode config (~/.hygur-edge/config.json) ───────────────────
// The local↔cloud switch is a desktop-only setting, so it goes through the Tauri
// IPC bridge (no network-reachable endpoint, smaller attack surface) rather than
// a sidecar HTTP route. set_desktop_config writes the file the sidecar reads at
// boot, then restarts the sidecar so the new mode applies. Fields/JSON match the
// Go edge.Config exactly.

fn edge_config_path() -> Option<std::path::PathBuf> {
    std::env::var("HOME")
        .ok()
        .map(|h| std::path::Path::new(&h).join(".hygur-edge/config.json"))
}

#[derive(serde::Serialize, serde::Deserialize, Default)]
struct EdgeFileConfig {
    #[serde(default)]
    mode: String,
    #[serde(default)]
    server: String,
    #[serde(default)]
    token: String,
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
    #[serde(default)]
    backfill_count: u64,
}

fn load_edge_file() -> EdgeFileConfig {
    edge_config_path()
        .and_then(|p| std::fs::read_to_string(p).ok())
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

/// Redacted view of the desktop config: secrets are reported as `*_set` booleans,
/// never echoed back to the frontend.
#[derive(serde::Serialize)]
struct DesktopConfigView {
    mode: String,
    server: String,
    folder: String,
    proton_user: String,
    proton_mailbox: String,
    interval_secs: u64,
    backfill_count: u64,
    token_set: bool,
    proton_password_set: bool,
}

#[tauri::command]
fn get_desktop_config() -> DesktopConfigView {
    let c = load_edge_file();
    DesktopConfigView {
        mode: c.mode,
        server: c.server,
        folder: c.folder,
        proton_user: c.proton_user,
        proton_mailbox: if c.proton_mailbox.is_empty() {
            "All Mail".into()
        } else {
            c.proton_mailbox
        },
        interval_secs: c.interval_secs,
        // Surface the effective value so the field shows the default until set.
        backfill_count: if c.backfill_count == 0 { 200 } else { c.backfill_count },
        token_set: !c.token.is_empty(),
        proton_password_set: !c.proton_password.is_empty(),
    }
}

/// Incoming desktop config from the picker / Settings toggle. Blank `token` /
/// `proton_password` mean "keep the stored secret" (the frontend never receives
/// them, so a round-trip must not wipe them).
#[derive(serde::Deserialize)]
struct DesktopConfigInput {
    mode: String,
    #[serde(default)]
    server: String,
    #[serde(default)]
    token: String,
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
    #[serde(default)]
    backfill_count: u64,
}

#[tauri::command]
fn set_desktop_config(app: AppHandle, cfg: DesktopConfigInput) -> Result<bool, String> {
    let path = edge_config_path().ok_or("no HOME directory")?;
    let mut next = load_edge_file();
    // Proxy-relevant fields are wired once at boot, so a change there requires a
    // sidecar restart. Source fields (folder/proton/interval) are re-read by the
    // running edge loop each cycle — no restart needed.
    let (old_mode, old_server, old_token) =
        (next.mode.clone(), next.server.clone(), next.token.clone());

    next.mode = cfg.mode.trim().to_string();
    next.server = cfg.server.trim().to_string();
    if !cfg.token.trim().is_empty() {
        next.token = cfg.token.trim().to_string(); // blank = keep stored
    }
    next.folder = cfg.folder.trim().to_string();
    next.proton_user = cfg.proton_user.trim().to_string();
    if !cfg.proton_password.is_empty() {
        next.proton_password = cfg.proton_password; // blank = keep stored
    }
    next.proton_mailbox = if cfg.proton_mailbox.trim().is_empty() {
        "All Mail".into()
    } else {
        cfg.proton_mailbox.trim().to_string()
    };
    next.interval_secs = cfg.interval_secs;
    if cfg.backfill_count > 0 {
        next.backfill_count = cfg.backfill_count; // 0 = keep stored (callers may omit it)
    }

    let restart =
        next.mode != old_mode || next.server != old_server || next.token != old_token;

    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(|e| format!("mkdir: {e}"))?;
    }
    let data = serde_json::to_string_pretty(&next).map_err(|e| format!("encode: {e}"))?;
    std::fs::write(&path, data).map_err(|e| format!("write: {e}"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600));
    }

    if restart {
        restart_sidecar(&app);
    }
    Ok(restart)
}

/// Restart the supervised sidecar so it re-reads its mode from the config: reset
/// the rapid-restart guard (a deliberate switch isn't a crash loop), then kill the
/// child — the supervisor's Terminated handler respawns it, which re-reads the
/// config and applies the new mode (proxy + in-process edge push).
fn restart_sidecar(app: &AppHandle) {
    let state = app.state::<Sidecar>();
    state.restarts.lock().unwrap().0 = 0;
    let child = state.child.lock().unwrap().take();
    if let Some(child) = child {
        let _ = child.kill();
    }
}

/// Sign out of the cloud account on desktop: drop the cloud binding (mode/server/
/// token) from the config so the sidecar stops proxying, then restart it. Local
/// source config (folder/proton) is left intact — it's account-independent. With
/// `mode` cleared, the next load lands on the ModePicker (the desktop "login").
#[tauri::command]
fn sign_out_desktop(app: AppHandle) -> Result<(), String> {
    let path = edge_config_path().ok_or("no HOME directory")?;
    let mut next = load_edge_file();
    next.mode = String::new();
    next.server = String::new();
    next.token = String::new();

    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(|e| format!("mkdir: {e}"))?;
    }
    let data = serde_json::to_string_pretty(&next).map_err(|e| format!("encode: {e}"))?;
    std::fs::write(&path, data).map_err(|e| format!("write: {e}"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600));
    }

    restart_sidecar(&app);
    Ok(())
}

/// Open a URL in the user's default SYSTEM browser (not the loopback webview) —
/// used by the desktop passkey sign-in to launch cloud.hygur.ai, where the WebAuthn
/// ceremony can run (it can't on the 127.0.0.1 webview origin).
#[tauri::command]
fn open_external(app: AppHandle, url: String) -> Result<(), String> {
    app.opener().open_url(url, None::<&str>).map_err(|e| e.to_string())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_autostart::init(MacosLauncher::LaunchAgent, None))
        .plugin(tauri_plugin_deep_link::init())
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
        .invoke_handler(tauri::generate_handler![get_desktop_config, set_desktop_config, sign_out_desktop, open_external])
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

            // Passkey deep-link: cloud.hygur.ai redirects to hygur://auth?state=…
            // after a browser passkey login; forward the URL to the webview, which
            // claims the one-time token bundle and applies the cloud config.
            {
                let h = app.handle().clone();
                app.deep_link().on_open_url(move |event| {
                    for url in event.urls() {
                        let _ = h.emit("deeplink-auth", url.to_string());
                    }
                });
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

            // Spawn + supervise the bundled Hygur sidecar (serves the WebUI on
            // :8420). In cloud mode the sidecar reads ~/.hygur-edge/config.json and
            // both proxies data/AI routes to the tenant AND pushes local sources
            // in-process — so there is no separate edge agent to spawn here.
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
            }
            _ => {}
        });
}
