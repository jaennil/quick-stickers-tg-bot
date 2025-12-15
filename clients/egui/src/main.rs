mod app;
mod cache;
mod config;
mod db;
mod hotkey;
mod models;
mod telegram;

use anyhow::Result;
use std::path::PathBuf;
use std::sync::mpsc;
use std::sync::Arc;
use tokio::runtime::Runtime;

use app::StickerApp;
use cache::ThumbnailCache;
use config::Config;
use db::Database;
use hotkey::{HotkeyEvent, HotkeyListener};
use telegram::TelegramClient;

fn main() -> Result<()> {
    // Initialize logging
    tracing_subscriber::fmt::init();

    // Use current working directory
    let workdir = std::env::current_dir()?;
    println!("[1/7] Working directory: {:?}", workdir);

    // Load config
    let config_path = workdir.join("config.yaml");
    println!("[2/7] Loading config from: {:?}", config_path);
    let config = Config::load(&config_path)
        .map_err(|e| anyhow::anyhow!("Failed to load config {:?}: {}", config_path, e))?;

    println!("Sticker Search GUI (Rust) starting...");
    println!("Press Ctrl+Shift+S to toggle window");

    // Create tokio runtime
    let rt = Arc::new(Runtime::new()?);

    // Initialize components
    println!("[3/7] Connecting to database...");
    let db = rt.block_on(async { Database::connect(&config.database, config.user_id).await })
        .map_err(|e| anyhow::anyhow!("Failed to connect to database: {}", e))?;
    let db = Arc::new(db);

    println!("[4/7] Connecting to Telegram...");
    let telegram = rt.block_on(async { TelegramClient::connect(&config.telegram, &workdir).await })
        .map_err(|e| anyhow::anyhow!("Failed to connect to Telegram: {}", e))?;
    let telegram = Arc::new(telegram);

    // Load chats
    println!("[5/7] Loading chats...");
    let chats = rt.block_on(async { telegram.get_dialogs(50).await })
        .map_err(|e| anyhow::anyhow!("Failed to load chats: {}", e))?;
    println!("      Loaded {} chats", chats.len());

    // Initialize cache
    let cache_dir = workdir.join(".thumb_cache");
    println!("[6/7] Initializing cache at: {:?}", cache_dir);
    let cache = Arc::new(ThumbnailCache::new(cache_dir.clone())
        .map_err(|e| anyhow::anyhow!("Failed to create cache at {:?}: {}", cache_dir, e))?);

    // Start hotkey listener
    println!("[7/7] Starting hotkey listener...");
    let (hotkey_tx, hotkey_rx) = mpsc::channel::<HotkeyEvent>();
    HotkeyListener::start(hotkey_tx);

    println!("Starting GUI...");
    // Disable IME completely
    std::env::set_var("GTK_IM_MODULE", "");
    std::env::set_var("QT_IM_MODULE", "");
    std::env::set_var("XMODIFIERS", "");
    std::env::set_var("GLFW_IM_MODULE", "");

    let options = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_inner_size([700.0, 650.0])
            .with_decorations(false)
            .with_always_on_top()
            .with_transparent(true),
        ..Default::default()
    };

    eframe::run_native(
        "Sticker Search",
        options,
        Box::new(move |cc| {
            Ok(Box::new(StickerApp::new(
                cc, rt, db, telegram, cache, chats, hotkey_rx,
            )))
        }),
    )
    .map_err(|e| anyhow::anyhow!("eframe error: {}", e))
}
