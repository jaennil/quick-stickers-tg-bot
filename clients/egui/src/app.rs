use eframe::egui;
use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

// Limit textures in memory to ~200MB instead of 1.5GB
const MAX_TEXTURES: usize = 200;

use crate::api::Api;
use crate::cache::ThumbnailCache;
use crate::hotkey::HotkeyEvent;
use crate::models::{ChatInfo, Sticker};
use crate::services::{ChatDetector, StickerLoader, ThumbnailLoader};
use crate::services::sticker_loader::StickerLoadResult;
use crate::services::thumbnail_loader::ThumbnailResult;
use crate::telegram::TelegramClient;
use crate::ui::grid::{handle_grid_navigation, render_grid, GridState};
use crate::ui::search::{handle_focus, render_search_bar, render_size_slider};
use crate::ui::chat_selector::render_chat_selector;
use crate::ui::theme::{
    apply_dark_theme, DEFAULT_THUMB_SIZE, FRAME_TIME_MS, SEARCH_DEBOUNCE_MS, STATUS_TEXT,
};

enum SendResult {
    Success(String),
    Error(String),
}

pub struct StickerApp {
    // State
    search_query: String,
    stickers: Vec<Sticker>,
    chats: Vec<ChatInfo>,
    selected_chat: Option<ChatInfo>,
    status: String,

    // Grid state
    grid_state: GridState,
    grid_focused: bool,

    // Thumbnails (LRU cache)
    textures: HashMap<String, egui::TextureHandle>,
    texture_order: VecDeque<String>,  // LRU order: front = oldest, back = newest
    loading_thumbs: HashSet<String>,
    thumbnail_loader: ThumbnailLoader,

    // Services
    sticker_loader: StickerLoader,
    chat_detector: ChatDetector,
    is_loading_all: bool,

    // Backend
    rt: Arc<Runtime>,
    telegram: Arc<TelegramClient>,
    hotkey_rx: Receiver<HotkeyEvent>,

    // Search
    search_tx: Sender<Vec<Sticker>>,
    search_rx: Receiver<Vec<Sticker>>,
    search_debounce: Option<Instant>,

    // Send sticker
    send_result_rx: Receiver<SendResult>,
    send_result_tx: Sender<SendResult>,

    // Window visibility
    visible: bool,

    // Focus
    focus_search: bool,

    // Prevent double-send
    just_sent: bool,

    // Thumbnail size
    thumb_size: f32,
}

impl StickerApp {
    pub fn new(
        _cc: &eframe::CreationContext<'_>,
        rt: Arc<Runtime>,
        api: Arc<Api>,
        telegram: Arc<TelegramClient>,
        cache: Arc<ThumbnailCache>,
        chats: Vec<ChatInfo>,
        hotkey_rx: Receiver<HotkeyEvent>,
    ) -> Self {
        // Search channel
        let (search_tx, search_rx) = mpsc::channel();

        // Send result channel
        let (send_result_tx, send_result_rx) = mpsc::channel();

        // Start services
        let thumbnail_loader = ThumbnailLoader::start(rt.clone(), api.clone(), cache);
        let sticker_loader = StickerLoader::start(rt.clone(), api.clone());
        let chat_detector = ChatDetector::start(chats.clone());

        // Auto-select first chat
        let selected_chat = chats.first().cloned();

        Self {
            search_query: String::new(),
            stickers: Vec::new(),
            chats,
            selected_chat,
            status: "Loading...".into(),
            grid_state: GridState::new(),
            grid_focused: false,
            textures: HashMap::new(),
            texture_order: VecDeque::new(),
            loading_thumbs: HashSet::new(),
            thumbnail_loader,
            sticker_loader,
            chat_detector,
            is_loading_all: true,
            rt,
            telegram,
            hotkey_rx,
            search_tx,
            search_rx,
            search_debounce: None,
            send_result_rx,
            send_result_tx,
            visible: true,
            focus_search: true,
            just_sent: false,
            thumb_size: DEFAULT_THUMB_SIZE,
        }
    }

    fn trigger_search(&mut self) {
        debug!("[search] trigger debounce");
        self.search_debounce = Some(Instant::now());
    }

    fn do_search(&mut self, api: &Arc<Api>) {
        let query = self.search_query.clone();
        info!("[search] searching for: {:?}", query);
        let api = api.clone();
        let tx = self.search_tx.clone();

        self.rt.spawn(async move {
            match api.search_stickers(&query).await {
                Ok(stickers) => {
                    info!("[search] found {} stickers", stickers.len());
                    if tx.send(stickers).is_err() {
                        warn!("[search] result channel closed");
                    }
                }
                Err(e) => {
                    warn!("[search] error: {}", e);
                }
            }
        });
    }

    fn send_sticker(&mut self) {
        let Some(chat) = &self.selected_chat else {
            warn!("[send] no chat selected");
            self.status = "Select a chat first!".into();
            return;
        };

        let Some(sticker) = self.stickers.get(self.grid_state.selected) else {
            warn!("[send] no sticker selected");
            return;
        };

        let telegram = self.telegram.clone();
        let chat_id = chat.id;
        let set_name = sticker.set_name.clone();
        let document_id = sticker.document_id;
        let chat_name = chat.name.clone();
        let tx = self.send_result_tx.clone();

        info!("[send] sending sticker {} to chat {}", document_id, chat_name);
        self.status = "Sending...".into();

        self.rt.spawn(async move {
            match telegram.send_sticker(chat_id, &set_name, document_id).await {
                Ok(_) => {
                    info!("[send] success: sent to {}", chat_name);
                    if tx.send(SendResult::Success(chat_name)).is_err() {
                        warn!("[send] result channel closed");
                    }
                }
                Err(e) => {
                    warn!("[send] error: {}", e);
                    if tx.send(SendResult::Error(e.to_string())).is_err() {
                        warn!("[send] result channel closed");
                    }
                }
            }
        });
    }

    fn request_thumbnail(&mut self, file_id: &str) {
        if self.textures.contains_key(file_id) || self.loading_thumbs.contains(file_id) {
            return;
        }
        debug!("[thumb] requesting: {}", &file_id[..20.min(file_id.len())]);
        self.loading_thumbs.insert(file_id.to_string());
        self.thumbnail_loader.request(file_id);
    }

    fn poll_all(&mut self, ctx: &egui::Context) {
        // Poll sticker loading results
        while let Some(result) = self.sticker_loader.try_recv() {
            match result {
                StickerLoadResult::Append(new_stickers) => {
                    debug!("[poll] appending {} stickers", new_stickers.len());
                    self.stickers.extend(new_stickers);
                    if self.is_loading_all {
                        self.status = format!("Loading... {} stickers", self.stickers.len());
                    }
                }
                StickerLoadResult::Done => {
                    info!("[poll] sticker loading complete: {} total", self.stickers.len());
                    self.is_loading_all = false;
                    self.status = format!("{} stickers", self.stickers.len());
                }
            }
        }

        // Poll search results
        while let Ok(stickers) = self.search_rx.try_recv() {
            debug!("[poll] received {} stickers from search", stickers.len());
            self.stickers = stickers;
            self.grid_state.selected = 0;
            self.status = format!("Found {} stickers", self.stickers.len());
        }

        // Poll thumbnail results
        while let Some(result) = self.thumbnail_loader.try_recv() {
            match result {
                ThumbnailResult::Loaded(file_id, data) => {
                    debug!("[poll] thumbnail loaded: {}", &file_id[..20.min(file_id.len())]);
                    self.loading_thumbs.remove(&file_id);
                    if let Ok(image) = image::load_from_memory(&data) {
                        let size = [image.width() as _, image.height() as _];
                        let rgba = image.to_rgba8();
                        let pixels = rgba.as_flat_samples();
                        let texture = ctx.load_texture(
                            &file_id,
                            egui::ColorImage::from_rgba_unmultiplied(size, pixels.as_slice()),
                            egui::TextureOptions::LINEAR,
                        );
                        self.textures.insert(file_id.clone(), texture);
                        self.texture_order.push_back(file_id);

                        // LRU eviction: remove oldest textures if over limit
                        while self.textures.len() > MAX_TEXTURES {
                            if let Some(old_id) = self.texture_order.pop_front() {
                                self.textures.remove(&old_id);
                                debug!("[poll] evicted old texture: {}", &old_id[..20.min(old_id.len())]);
                            }
                        }
                    } else {
                        warn!("[poll] failed to decode image: {}", &file_id[..20.min(file_id.len())]);
                    }
                }
                ThumbnailResult::NotFound(file_id) => {
                    debug!("[poll] thumbnail not found: {}", &file_id[..20.min(file_id.len())]);
                    self.loading_thumbs.remove(&file_id);
                }
            }
        }

        // Poll send results
        while let Ok(result) = self.send_result_rx.try_recv() {
            match result {
                SendResult::Success(chat_name) => {
                    info!("[poll] send success: {}", chat_name);
                    self.status = format!("Sent to {}", chat_name);
                }
                SendResult::Error(e) => {
                    warn!("[poll] send error: {}", e);
                    self.status = format!("Error: {}", e);
                }
            }
        }

        // Poll chat detection
        while let Some(chat_name) = self.chat_detector.try_recv() {
            if self.selected_chat.as_ref().map(|c| &c.name) != Some(&chat_name) {
                if let Some(chat) = self.chats.iter().find(|c| c.name == chat_name) {
                    info!("[poll] switching to chat: {}", chat_name);
                    self.selected_chat = Some(chat.clone());
                }
            }
        }
    }
}

// Wrapper to hold API reference for async search
pub struct StickerAppWithApi {
    app: StickerApp,
    api: Arc<Api>,
}

impl StickerAppWithApi {
    pub fn new(
        cc: &eframe::CreationContext<'_>,
        rt: Arc<Runtime>,
        api: Arc<Api>,
        telegram: Arc<TelegramClient>,
        cache: Arc<ThumbnailCache>,
        chats: Vec<ChatInfo>,
        hotkey_rx: Receiver<HotkeyEvent>,
    ) -> Self {
        apply_dark_theme(&cc.egui_ctx);

        Self {
            app: StickerApp::new(cc, rt, api.clone(), telegram, cache, chats, hotkey_rx),
            api,
        }
    }
}

impl eframe::App for StickerAppWithApi {
    fn clear_color(&self, _visuals: &egui::Visuals) -> [f32; 4] {
        [0.0, 0.0, 0.0, 0.0]
    }

    fn update(&mut self, ctx: &egui::Context, _frame: &mut eframe::Frame) {
        let app = &mut self.app;

        // Poll all async results
        app.poll_all(ctx);

        // Check search debounce
        if let Some(start) = app.search_debounce {
            if start.elapsed() > Duration::from_millis(SEARCH_DEBOUNCE_MS) {
                app.search_debounce = None;
                app.do_search(&self.api);
            }
        }

        // Handle hotkey
        while let Ok(event) = app.hotkey_rx.try_recv() {
            match event {
                HotkeyEvent::Toggle => {
                    app.visible = !app.visible;
                    info!("[hotkey] toggle, visible={}", app.visible);
                    if app.visible {
                        ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
                        app.focus_search = true;
                    }
                }
            }
        }

        // Escape to hide
        if ctx.input(|i| i.key_pressed(egui::Key::Escape)) {
            info!("[key] escape pressed, hiding window");
            app.visible = false;
        }

        if !app.visible {
            ctx.send_viewport_cmd(egui::ViewportCommand::Visible(false));
            return;
        } else {
            ctx.send_viewport_cmd(egui::ViewportCommand::Visible(true));
        }

        egui::CentralPanel::default().show(ctx, |ui| {
            ui.set_min_size(egui::vec2(700.0, 650.0));

            // Chat selector
            render_chat_selector(ui, &app.chats, &mut app.selected_chat);

            ui.add_space(8.0);

            // Search bar
            let search_resp = render_search_bar(ui, &mut app.search_query);

            if app.focus_search {
                ui.memory_mut(|m| m.request_focus(search_resp.id));
                app.focus_search = false;
            }

            if search_resp.changed {
                app.trigger_search();
                app.grid_focused = false;
            }

            // Handle Tab focus switching
            handle_focus(
                ui,
                ctx,
                search_resp.id,
                &mut app.focus_search,
                &mut app.grid_focused,
                !app.stickers.is_empty(),
            );

            // Enter released
            if ui.input(|i| i.key_released(egui::Key::Enter)) {
                app.just_sent = false;
            }

            // Enter to send
            if ui.input(|i| i.key_pressed(egui::Key::Enter)) && !app.stickers.is_empty() && !app.just_sent {
                app.send_sticker();
                app.just_sent = true;
            }

            // Size slider
            render_size_slider(ui, &mut app.thumb_size);

            ui.add_space(4.0);

            // Update grid columns
            app.grid_state.update_cols(ui.available_width(), app.thumb_size);

            // Grid navigation
            handle_grid_navigation(ui, &mut app.grid_state, app.stickers.len(), app.grid_focused);

            ui.add_space(8.0);

            // Prepare sticker data for grid
            let sticker_data: Vec<_> = app
                .stickers
                .iter()
                .enumerate()
                .map(|(i, s)| (i, s.file_id.clone()))
                .collect();

            // Render grid
            let grid_resp = render_grid(
                ui,
                &sticker_data,
                &app.textures,
                app.grid_state.selected,
                app.thumb_size,
                app.grid_state.cols,
            );

            // Request missing thumbnails
            for file_id in grid_resp.needs_thumbnail {
                app.request_thumbnail(&file_id);
            }

            // Handle click on sticker
            if let Some(idx) = grid_resp.clicked {
                if !app.just_sent {
                    app.grid_state.selected = idx;
                    app.send_sticker();
                }
            }

            ui.add_space(8.0);
            ui.colored_label(STATUS_TEXT, &app.status);
        });

        ctx.request_repaint_after(Duration::from_millis(FRAME_TIME_MS));
    }
}
