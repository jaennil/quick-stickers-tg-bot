use eframe::egui;
use std::collections::{HashMap, HashSet};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};
use tokio::runtime::Runtime;

use crate::cache::ThumbnailCache;
use crate::db::Database;
use crate::hotkey::HotkeyEvent;
use crate::models::{ChatInfo, Sticker};
use crate::telegram::TelegramClient;

// Message types for async operations
enum ThumbnailResult {
    Loaded(String, Vec<u8>),
    NotFound(String),
}

enum SendResult {
    Success(String),
    Error(String),
}

pub struct StickerApp {
    // State
    search_query: String,
    stickers: Vec<Sticker>,
    selected_sticker: usize,
    chats: Vec<ChatInfo>,
    selected_chat: Option<ChatInfo>,
    status: String,

    // Thumbnails
    textures: HashMap<String, egui::TextureHandle>,
    loading_thumbs: HashSet<String>,
    thumb_tx: Sender<String>,
    thumb_result_rx: Receiver<ThumbnailResult>,

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

    // Chat auto-detect
    chat_rx: Receiver<String>,

    // Window visibility
    visible: bool,

    // Focus
    focus_search: bool,
    grid_focused: bool,

    // Prevent double-send
    just_sent: bool,
}

impl StickerApp {
    pub fn new(
        _cc: &eframe::CreationContext<'_>,
        rt: Arc<Runtime>,
        db: Arc<Database>,
        telegram: Arc<TelegramClient>,
        cache: Arc<ThumbnailCache>,
        chats: Vec<ChatInfo>,
        hotkey_rx: Receiver<HotkeyEvent>,
    ) -> Self {
        // Search channel
        let (search_tx, search_rx) = mpsc::channel();

        // Thumbnail loading channel
        let (thumb_tx, thumb_request_rx) = mpsc::channel::<String>();
        let (thumb_result_tx, thumb_result_rx) = mpsc::channel::<ThumbnailResult>();

        // Send result channel
        let (send_result_tx, send_result_rx) = mpsc::channel();

        // Chat detection channel
        let (chat_tx, chat_rx) = mpsc::channel::<String>();

        // Spawn thumbnail loader thread
        let thumb_db = db.clone();
        let thumb_cache = cache.clone();
        let thumb_rt = rt.clone();
        thread::spawn(move || {
            while let Ok(file_id) = thumb_request_rx.recv() {
                let db = thumb_db.clone();
                let cache = thumb_cache.clone();
                let tx = thumb_result_tx.clone();
                let fid = file_id.clone();

                thumb_rt.spawn(async move {
                    // Try cache first
                    if let Some(data) = cache.get(&fid).await {
                        let _ = tx.send(ThumbnailResult::Loaded(fid, data));
                        return;
                    }

                    // Try database
                    if let Ok(Some(data)) = db.get_thumbnail(&fid).await {
                        let _ = cache.set(&fid, data.clone()).await;
                        let _ = tx.send(ThumbnailResult::Loaded(fid, data));
                        return;
                    }

                    let _ = tx.send(ThumbnailResult::NotFound(fid));
                });
            }
        });

        // Spawn chat detector thread (runs xdotool in background)
        let chats_for_detector = chats.clone();
        thread::spawn(move || {
            loop {
                thread::sleep(Duration::from_millis(500));

                if let Some(name) = detect_telegram_chat() {
                    // Only send if chat exists in our list
                    if chats_for_detector.iter().any(|c| c.name == name) {
                        let _ = chat_tx.send(name);
                    }
                }
            }
        });

        // Initial search - load all stickers
        {
            let db = db.clone();
            let tx = search_tx.clone();
            rt.spawn(async move {
                if let Ok(stickers) = db.search_stickers("").await {
                    let _ = tx.send(stickers);
                }
            });
        }

        // Auto-select first chat
        let selected_chat = chats.first().cloned();

        Self {
            search_query: String::new(),
            stickers: Vec::new(),
            selected_sticker: 0,
            chats,
            selected_chat,
            status: "Loading...".into(),
            textures: HashMap::new(),
            loading_thumbs: HashSet::new(),
            thumb_tx,
            thumb_result_rx,
            rt,
            telegram,
            hotkey_rx,
            search_tx,
            search_rx,
            search_debounce: None,
            send_result_rx,
            send_result_tx,
            chat_rx,
            visible: true,
            focus_search: true,
            grid_focused: false,
            just_sent: false,
        }
    }

    fn trigger_search(&mut self) {
        self.search_debounce = Some(Instant::now());
    }

    fn do_search(&mut self, db: &Arc<Database>) {
        let query = self.search_query.clone();
        let db = db.clone();
        let tx = self.search_tx.clone();

        self.rt.spawn(async move {
            if let Ok(stickers) = db.search_stickers(&query).await {
                let _ = tx.send(stickers);
            }
        });
    }

    fn send_sticker(&mut self) {
        let Some(chat) = &self.selected_chat else {
            self.status = "Select a chat first!".into();
            return;
        };

        let Some(sticker) = self.stickers.get(self.selected_sticker) else {
            return;
        };

        let telegram = self.telegram.clone();
        let chat_id = chat.id;
        let set_name = sticker.set_name.clone();
        let document_id = sticker.document_id;
        let chat_name = chat.name.clone();
        let tx = self.send_result_tx.clone();

        self.status = "Sending...".into();

        self.rt.spawn(async move {
            match telegram.send_sticker(chat_id, &set_name, document_id).await {
                Ok(_) => {
                    let _ = tx.send(SendResult::Success(chat_name));
                }
                Err(e) => {
                    let _ = tx.send(SendResult::Error(e.to_string()));
                }
            }
        });
    }

    fn request_thumbnail(&mut self, file_id: &str) {
        if self.textures.contains_key(file_id) || self.loading_thumbs.contains(file_id) {
            return;
        }
        self.loading_thumbs.insert(file_id.to_string());
        let _ = self.thumb_tx.send(file_id.to_string());
    }

    fn poll_all(&mut self, ctx: &egui::Context) {
        // Poll search results
        while let Ok(stickers) = self.search_rx.try_recv() {
            self.stickers = stickers;
            self.selected_sticker = 0;
            self.status = format!("Found {} stickers", self.stickers.len());
        }

        // Poll thumbnail results
        while let Ok(result) = self.thumb_result_rx.try_recv() {
            match result {
                ThumbnailResult::Loaded(file_id, data) => {
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
                        self.textures.insert(file_id, texture);
                    }
                }
                ThumbnailResult::NotFound(file_id) => {
                    self.loading_thumbs.remove(&file_id);
                }
            }
        }

        // Poll send results
        while let Ok(result) = self.send_result_rx.try_recv() {
            match result {
                SendResult::Success(chat_name) => {
                    self.status = format!("Sent to {}", chat_name);
                }
                SendResult::Error(e) => {
                    self.status = format!("Error: {}", e);
                }
            }
        }

        // Poll chat detection
        while let Ok(chat_name) = self.chat_rx.try_recv() {
            if self.selected_chat.as_ref().map(|c| &c.name) != Some(&chat_name) {
                if let Some(chat) = self.chats.iter().find(|c| c.name == chat_name) {
                    self.selected_chat = Some(chat.clone());
                }
            }
        }

    }
}

fn detect_telegram_chat() -> Option<String> {
    use std::process::Command;

    let output = Command::new("xdotool")
        .args(["search", "--class", "TelegramDesktop"])
        .output()
        .ok()?;

    let ids: Vec<&str> = std::str::from_utf8(&output.stdout)
        .ok()?
        .lines()
        .collect();

    for id in ids {
        let name_output = Command::new("xdotool")
            .args(["getwindowname", id])
            .output()
            .ok()?;

        let name = std::str::from_utf8(&name_output.stdout)
            .ok()?
            .trim()
            .to_string();

        if name != "TelegramDesktop" && name != "Media viewer" && name != "Telegram Desktop" {
            let chat_name = if let Some(pos) = name.rfind(" – (") {
                name[..pos].to_string()
            } else {
                name
            };

            let clean: String = chat_name
                .chars()
                .filter(|c| !c.is_control() && *c != '\u{200e}' && *c != '\u{200f}' && *c != '\u{2068}' && *c != '\u{2069}')
                .collect();

            return Some(clean);
        }
    }

    None
}

// Store db in app for async search
pub struct StickerAppWithDb {
    app: StickerApp,
    db: Arc<Database>,
}

impl StickerAppWithDb {
    pub fn new(
        cc: &eframe::CreationContext<'_>,
        rt: Arc<Runtime>,
        db: Arc<Database>,
        telegram: Arc<TelegramClient>,
        cache: Arc<ThumbnailCache>,
        chats: Vec<ChatInfo>,
        hotkey_rx: Receiver<HotkeyEvent>,
    ) -> Self {
        // Dark theme
        let mut visuals = egui::Visuals::dark();
        visuals.widgets.noninteractive.bg_fill = egui::Color32::from_rgba_unmultiplied(30, 30, 30, 180);
        visuals.panel_fill = egui::Color32::from_rgba_unmultiplied(25, 25, 25, 180);
        visuals.window_fill = egui::Color32::from_rgba_unmultiplied(25, 25, 25, 180);
        visuals.extreme_bg_color = egui::Color32::from_rgba_unmultiplied(20, 20, 20, 180);
        cc.egui_ctx.set_visuals(visuals);

        Self {
            app: StickerApp::new(cc, rt, db.clone(), telegram, cache, chats, hotkey_rx),
            db,
        }
    }
}

impl eframe::App for StickerAppWithDb {
    fn clear_color(&self, _visuals: &egui::Visuals) -> [f32; 4] {
        [0.0, 0.0, 0.0, 0.0]
    }

    fn update(&mut self, ctx: &egui::Context, _frame: &mut eframe::Frame) {
        let app = &mut self.app;

        // Poll all async results
        app.poll_all(ctx);

        // Check search debounce
        if let Some(start) = app.search_debounce {
            if start.elapsed() > Duration::from_millis(200) {
                app.search_debounce = None;
                app.do_search(&self.db);
            }
        }

        // Handle hotkey
        while let Ok(event) = app.hotkey_rx.try_recv() {
            match event {
                HotkeyEvent::Toggle => {
                    app.visible = !app.visible;
                    if app.visible {
                        ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
                        app.focus_search = true;
                    }
                }
            }
        }

        // Escape to hide
        if ctx.input(|i| i.key_pressed(egui::Key::Escape)) {
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
            ui.horizontal(|ui| {
                ui.label("Chat:");
                let selected_text = app.selected_chat
                    .as_ref()
                    .map(|c| c.to_string())
                    .unwrap_or_else(|| "Select...".into());

                egui::ComboBox::from_id_salt("chat")
                    .selected_text(&selected_text)
                    .width(300.0)
                    .show_ui(ui, |ui| {
                        for chat in &app.chats {
                            let sel = app.selected_chat.as_ref() == Some(chat);
                            if ui.selectable_label(sel, chat.to_string()).clicked() {
                                app.selected_chat = Some(chat.clone());
                            }
                        }
                    });
            });

            ui.add_space(8.0);

            // Search
            let search = ui.add(
                egui::TextEdit::singleline(&mut app.search_query)
                    .hint_text("Search... (Enter to send)")
                    .desired_width(ui.available_width()),
            );

            if app.focus_search {
                search.request_focus();
                app.focus_search = false;
            }

            if search.changed() {
                app.trigger_search();
                app.grid_focused = false;
            }

            // Tab to switch focus
            if ui.input(|i| i.key_pressed(egui::Key::Tab)) {
                if !app.grid_focused && !app.stickers.is_empty() {
                    app.grid_focused = true;
                    ctx.memory_mut(|m| m.surrender_focus(search.id));
                } else {
                    app.grid_focused = false;
                    app.focus_search = true;
                }
            }

            // Enter released
            if ui.input(|i| i.key_released(egui::Key::Enter)) {
                app.just_sent = false;
            }

            // Enter to send
            if ui.input(|i| i.key_pressed(egui::Key::Enter)) && !app.stickers.is_empty() && !app.just_sent {
                app.send_sticker();
                app.just_sent = true;
            }

            // Grid navigation
            let thumb_size = 100.0;
            let spacing = 8.0;
            let cols = ((ui.available_width() + spacing) / (thumb_size + spacing)).max(1.0) as usize;

            if app.grid_focused && !app.stickers.is_empty() {
                let count = app.stickers.len();
                if ui.input(|i| i.key_pressed(egui::Key::H) || i.key_pressed(egui::Key::ArrowLeft)) {
                    if app.selected_sticker > 0 {
                        app.selected_sticker -= 1;
                    }
                }
                if ui.input(|i| i.key_pressed(egui::Key::L) || i.key_pressed(egui::Key::ArrowRight)) {
                    if app.selected_sticker < count - 1 {
                        app.selected_sticker += 1;
                    }
                }
                if ui.input(|i| i.key_pressed(egui::Key::K) || i.key_pressed(egui::Key::ArrowUp)) {
                    if app.selected_sticker >= cols {
                        app.selected_sticker -= cols;
                    }
                }
                if ui.input(|i| i.key_pressed(egui::Key::J) || i.key_pressed(egui::Key::ArrowDown)) {
                    if app.selected_sticker + cols < count {
                        app.selected_sticker += cols;
                    }
                }
            }

            ui.add_space(8.0);

            // Sticker grid
            let sticker_data: Vec<_> = app.stickers.iter().enumerate()
                .map(|(i, s)| (i, s.file_id.clone()))
                .collect();

            let mut clicked = None;

            egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
                egui::Grid::new("grid").spacing([spacing, spacing]).show(ui, |ui| {
                    for (idx, file_id) in &sticker_data {
                        let selected = *idx == app.selected_sticker;
                        let (rect, resp) = ui.allocate_exact_size(
                            egui::vec2(thumb_size, thumb_size),
                            egui::Sense::click(),
                        );

                        let bg = if selected {
                            egui::Color32::from_rgb(9, 71, 113)
                        } else if resp.hovered() {
                            egui::Color32::from_rgb(42, 45, 46)
                        } else {
                            egui::Color32::from_rgb(37, 37, 38)
                        };

                        ui.painter().rect_filled(rect, 6.0, bg);

                        if let Some(tex) = app.textures.get(file_id) {
                            let img_size = tex.size_vec2();
                            let scale = (thumb_size / img_size.x.max(img_size.y)).min(1.0);
                            let scaled = img_size * scale;
                            let offset = (egui::vec2(thumb_size, thumb_size) - scaled) / 2.0;

                            ui.painter().image(
                                tex.id(),
                                egui::Rect::from_min_size(rect.min + offset, scaled),
                                egui::Rect::from_min_max(egui::pos2(0.0, 0.0), egui::pos2(1.0, 1.0)),
                                egui::Color32::WHITE,
                            );
                        } else {
                            ui.painter().text(
                                rect.center(),
                                egui::Align2::CENTER_CENTER,
                                "...",
                                egui::FontId::proportional(16.0),
                                egui::Color32::GRAY,
                            );
                            app.request_thumbnail(file_id);
                        }

                        if resp.clicked() {
                            clicked = Some(*idx);
                        }

                        if (*idx + 1) % cols == 0 {
                            ui.end_row();
                        }
                    }
                });
            });

            if let Some(idx) = clicked {
                if !app.just_sent {
                    app.selected_sticker = idx;
                    app.send_sticker();
                }
            }

            ui.add_space(8.0);
            ui.colored_label(egui::Color32::from_rgb(136, 136, 136), &app.status);
        });

        ctx.request_repaint_after(Duration::from_millis(16));
    }
}
