use eframe::egui;
use std::collections::HashMap;
use std::sync::mpsc::Receiver;
use std::sync::Arc;
use tokio::runtime::Runtime;
use tokio::sync::RwLock;
use tracing::info;

use crate::cache::ThumbnailCache;
use crate::db::Database;
use crate::hotkey::HotkeyEvent;
use crate::models::{ChatInfo, Sticker};
use crate::telegram::TelegramClient;

pub struct StickerApp {
    // State
    search_query: String,
    stickers: Vec<Sticker>,
    selected_sticker: usize,
    chats: Vec<ChatInfo>,
    selected_chat: Option<ChatInfo>,
    chat_filter: String,
    show_chat_dropdown: bool,
    status: String,

    // Thumbnails
    textures: HashMap<String, egui::TextureHandle>,
    pending_thumbs: Arc<RwLock<Vec<String>>>,

    // Backend
    rt: Arc<Runtime>,
    db: Arc<Database>,
    telegram: Arc<TelegramClient>,
    cache: Arc<ThumbnailCache>,
    hotkey_rx: Receiver<HotkeyEvent>,

    // Window visibility
    visible: bool,
}

impl StickerApp {
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
        visuals.widgets.noninteractive.bg_fill = egui::Color32::from_rgb(30, 30, 30);
        cc.egui_ctx.set_visuals(visuals);

        Self {
            search_query: String::new(),
            stickers: Vec::new(),
            selected_sticker: 0,
            chats,
            selected_chat: None,
            chat_filter: String::new(),
            show_chat_dropdown: false,
            status: "Ready - Ctrl+Shift+S to toggle".into(),
            textures: HashMap::new(),
            pending_thumbs: Arc::new(RwLock::new(Vec::new())),
            rt,
            db,
            telegram,
            cache,
            hotkey_rx,
            visible: true,
        }
    }

    fn search_stickers(&mut self) {
        if self.search_query.is_empty() {
            self.stickers.clear();
            return;
        }

        let db = self.db.clone();
        let query = self.search_query.clone();

        info!("Searching for: {:?}", query);
        let stickers = self.rt.block_on(async { db.search_stickers(&query).await });

        match stickers {
            Ok(s) => {
                info!("Found {} stickers", s.len());
                self.stickers = s;
                self.selected_sticker = 0;
            }
            Err(e) => {
                info!("Search error: {}", e);
                self.status = format!("Search error: {}", e);
            }
        }
    }

    fn send_selected_sticker(&mut self) {
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

        self.status = "Sending...".into();

        info!("Sending sticker: set={}, document_id={} to chat={}", set_name, document_id, chat_id);

        let result = self
            .rt
            .block_on(async { telegram.send_sticker(chat_id, &set_name, document_id).await });

        match result {
            Ok(_) => {
                self.status = format!("Sent to {}", chat_name);
            }
            Err(e) => {
                info!("Send error: {}", e);
                self.status = format!("Error: {}", e);
            }
        }
    }

    fn load_thumbnail(&mut self, ctx: &egui::Context, file_id: &str) {
        if self.textures.contains_key(file_id) {
            return;
        }

        let cache = self.cache.clone();
        let db = self.db.clone();
        let file_id_owned = file_id.to_string();

        // Try to load from cache synchronously for immediate display
        let data = self.rt.block_on(async {
            // Try memory/disk cache first
            if let Some(data) = cache.get(&file_id_owned).await {
                return Some(data);
            }

            // Try database
            if let Ok(Some(data)) = db.get_thumbnail(&file_id_owned).await {
                // Save to cache for next time
                let _ = cache.set(&file_id_owned, data.clone()).await;
                return Some(data);
            }

            None
        });

        if let Some(data) = data {
            if let Ok(image) = image::load_from_memory(&data) {
                let size = [image.width() as _, image.height() as _];
                let rgba = image.to_rgba8();
                let pixels = rgba.as_flat_samples();

                let texture = ctx.load_texture(
                    &file_id_owned,
                    egui::ColorImage::from_rgba_unmultiplied(size, pixels.as_slice()),
                    egui::TextureOptions::LINEAR,
                );

                self.textures.insert(file_id_owned, texture);
            }
        }
    }
}

impl eframe::App for StickerApp {
    fn update(&mut self, ctx: &egui::Context, _frame: &mut eframe::Frame) {
        // Handle hotkey events
        while let Ok(event) = self.hotkey_rx.try_recv() {
            match event {
                HotkeyEvent::Toggle => {
                    self.visible = !self.visible;
                    if self.visible {
                        ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
                    }
                }
            }
        }

        // Handle Escape globally
        if ctx.input(|i| i.key_pressed(egui::Key::Escape)) {
            self.visible = false;
        }

        if !self.visible {
            ctx.send_viewport_cmd(egui::ViewportCommand::Minimized(true));
            return;
        }

        egui::CentralPanel::default().show(ctx, |ui| {
            ui.set_min_size(egui::vec2(700.0, 650.0));

            // Chat selector
            ui.horizontal(|ui| {
                ui.label("Chat:");

                let selected_text = self
                    .selected_chat
                    .as_ref()
                    .map(|c| c.to_string())
                    .unwrap_or_else(|| "Select chat...".into());

                egui::ComboBox::from_id_salt("chat_selector")
                    .selected_text(&selected_text)
                    .width(300.0)
                    .show_ui(ui, |ui| {
                        for chat in &self.chats {
                            let is_selected = self.selected_chat.as_ref() == Some(chat);
                            if ui.selectable_label(is_selected, chat.to_string()).clicked() {
                                info!("Selected chat: {:?}", chat);
                                self.selected_chat = Some(chat.clone());
                            }
                        }
                    });
            });

            ui.add_space(8.0);

            // Search input
            let search_response = ui.add(
                egui::TextEdit::singleline(&mut self.search_query)
                    .hint_text("Search stickers... (Enter to send)")
                    .desired_width(ui.available_width()),
            );

            if search_response.changed() {
                info!("searching, query = {:?}", self.search_query);
                self.search_stickers();
            }

            // Handle Enter only when search field has focus
            if search_response.has_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter)) {
                if !self.stickers.is_empty() {
                    self.send_selected_sticker();
                }
            }

            // Handle arrow keys for sticker selection (only when search field has focus)
            if search_response.has_focus() {
                if ui.input(|i| i.key_pressed(egui::Key::ArrowDown) || i.key_pressed(egui::Key::ArrowRight)) {
                    if self.selected_sticker < self.stickers.len().saturating_sub(1) {
                        self.selected_sticker += 1;
                    }
                }
                if ui.input(|i| i.key_pressed(egui::Key::ArrowUp) || i.key_pressed(egui::Key::ArrowLeft)) {
                    if self.selected_sticker > 0 {
                        self.selected_sticker -= 1;
                    }
                }
            }

            ui.add_space(8.0);

            // Collect sticker data first to avoid borrow issues
            let sticker_data: Vec<_> = self
                .stickers
                .iter()
                .enumerate()
                .map(|(idx, s)| (idx, s.file_id.clone()))
                .collect();

            let mut clicked_idx: Option<usize> = None;
            let mut thumbs_to_load: Vec<String> = Vec::new();

            // Results grid
            egui::ScrollArea::vertical()
                .auto_shrink([false, false])
                .show(ui, |ui| {
                    let available_width = ui.available_width();
                    let thumb_size = 100.0;
                    let spacing = 8.0;
                    let cols = ((available_width + spacing) / (thumb_size + spacing)).max(1.0) as usize;

                    egui::Grid::new("sticker_grid")
                        .spacing([spacing, spacing])
                        .show(ui, |ui| {
                            for (idx, file_id) in &sticker_data {
                                let is_selected = *idx == self.selected_sticker;

                                let (rect, response) = ui.allocate_exact_size(
                                    egui::vec2(thumb_size, thumb_size),
                                    egui::Sense::click(),
                                );

                                // Background
                                let bg_color = if is_selected {
                                    egui::Color32::from_rgb(9, 71, 113)
                                } else if response.hovered() {
                                    egui::Color32::from_rgb(42, 45, 46)
                                } else {
                                    egui::Color32::from_rgb(37, 37, 38)
                                };

                                ui.painter().rect_filled(rect, 6.0, bg_color);

                                // Image or placeholder
                                if let Some(texture) = self.textures.get(file_id) {
                                    let image_size = texture.size_vec2();
                                    let scale = (thumb_size / image_size.x.max(image_size.y)).min(1.0);
                                    let scaled_size = image_size * scale;
                                    let offset = (egui::vec2(thumb_size, thumb_size) - scaled_size) / 2.0;

                                    ui.painter().image(
                                        texture.id(),
                                        egui::Rect::from_min_size(rect.min + offset, scaled_size),
                                        egui::Rect::from_min_max(
                                            egui::pos2(0.0, 0.0),
                                            egui::pos2(1.0, 1.0),
                                        ),
                                        egui::Color32::WHITE,
                                    );
                                } else {
                                    // Loading placeholder
                                    ui.painter().text(
                                        rect.center(),
                                        egui::Align2::CENTER_CENTER,
                                        "...",
                                        egui::FontId::proportional(20.0),
                                        egui::Color32::GRAY,
                                    );
                                    thumbs_to_load.push(file_id.clone());
                                }

                                if response.clicked() {
                                    clicked_idx = Some(*idx);
                                }

                                if (*idx + 1) % cols == 0 {
                                    ui.end_row();
                                }
                            }
                        });
                });

            // Load thumbnails after iteration
            for file_id in thumbs_to_load {
                self.load_thumbnail(ctx, &file_id);
            }

            // Handle click after iteration
            if let Some(idx) = clicked_idx {
                self.selected_sticker = idx;
                self.send_selected_sticker();
            }

            // Status bar
            ui.add_space(8.0);
            ui.colored_label(egui::Color32::from_rgb(136, 136, 136), &self.status);
        });

        // Request repaint for animations
        ctx.request_repaint();
    }
}
