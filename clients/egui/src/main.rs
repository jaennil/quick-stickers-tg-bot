mod api;
mod app;
mod cache;
mod config;
mod hotkey;
mod models;
mod services;
mod telegram;
mod ui;

use anyhow::Result;
use std::sync::mpsc;
use std::sync::Arc;
use tokio::runtime::Runtime;

use api::Api;
use app::StickerApp;
use cache::ThumbnailCache;
use config::Config;
use hotkey::{HotkeyEvent, HotkeyListener};
use telegram::TelegramClient;

fn main() -> Result<()> {
    // Initialize logging
    tracing_subscriber::fmt::init();

    // Get config directory
    let config_dir = directories::ProjectDirs::from("", "", "qsg")
        .ok_or_else(|| anyhow::anyhow!("Failed to determine config directory"))?
        .config_dir()
        .to_path_buf();

    // Create config directory if it doesn't exist
    std::fs::create_dir_all(&config_dir)?;

    println!("[1/7] Config directory: {:?}", config_dir);

    // Load config
    let config_path = config_dir.join("config.yaml");
    let config_path = Config::ensure_file(&config_path)?;
    println!("[2/7] Loading config from: {:?}", config_path);
    let config = Config::load(&config_path)
        .map_err(|e| anyhow::anyhow!("Failed to load config {:?}: {}", config_path, e))?;

    // Use config directory as working directory for session files
    let workdir = config_dir;

    println!("Sticker Search GUI (Rust) starting...");
    println!("Press {} to toggle window", config.hotkey);

    // Create tokio runtime
    let rt = Arc::new(Runtime::new()?);

    // Initialize components
    println!("[3/7] Connecting to API...");
    let api = Api::new(&config.api, config.user_id)
        .map_err(|e| anyhow::anyhow!("Failed to create API client: {}", e))?;
    let api = Arc::new(api);

    println!("[4/7] Connecting to Telegram...");
    let telegram = rt
        .block_on(async { TelegramClient::connect(&config.telegram, &workdir).await })
        .map_err(|e| anyhow::anyhow!("Failed to connect to Telegram: {}", e))?;
    let telegram = Arc::new(telegram);

    // Load chats
    println!("[5/7] Loading chats...");
    let chats = rt
        .block_on(async { telegram.get_dialogs(50).await })
        .map_err(|e| anyhow::anyhow!("Failed to load chats: {}", e))?;
    println!("      Loaded {} chats", chats.len());

    // Initialize cache
    let cache_dir = directories::ProjectDirs::from("", "", "qsg")
        .ok_or_else(|| anyhow::anyhow!("Failed to determine cache directory"))?
        .cache_dir()
        .to_path_buf();
    println!("[6/7] Initializing cache at: {:?}", cache_dir);
    let cache = Arc::new(
        ThumbnailCache::new(cache_dir.clone())
            .map_err(|e| anyhow::anyhow!("Failed to create cache at {:?}: {}", cache_dir, e))?,
    );

    // Start hotkey listener
    println!("[7/7] Starting hotkey listener...");
    let (hotkey_tx, hotkey_rx) = mpsc::channel::<HotkeyEvent>();
    let _hotkey_listener = HotkeyListener::start(hotkey_tx, &config.hotkey);

    println!("Starting GUI...");
    // Disable IME completely
    std::env::set_var("GTK_IM_MODULE", "");
    std::env::set_var("QT_IM_MODULE", "");
    std::env::set_var("XMODIFIERS", "");
    std::env::set_var("GLFW_IM_MODULE", "");

    let options = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_inner_size([1080.0, 720.0])
            .with_decorations(false)
            .with_always_on_top()
            .with_transparent(true)
            .with_window_type(egui::X11WindowType::Dialog),
        ..Default::default()
    };

    eframe::run_native(
        "Sticker Search",
        options,
        Box::new(move |cc| {
            Ok(Box::new(StickerApp::new(
                cc, rt, api, telegram, cache, chats, hotkey_rx,
            )))
        }),
    )
    .map_err(|e| anyhow::anyhow!("eframe error: {}", e))
}
