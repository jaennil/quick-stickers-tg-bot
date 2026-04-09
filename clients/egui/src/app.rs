use eframe::egui::{self, RichText};
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
use crate::services::sticker_loader::StickerLoadResult;
use crate::services::thumbnail_loader::ThumbnailResult;
use crate::services::{ChatDetector, StickerLoader, ThumbnailLoader};
use crate::telegram::TelegramClient;
use crate::ui::chat_selector::render_chat_selector;
use crate::ui::grid::{handle_grid_navigation, render_grid, GridState};
use crate::ui::search::{handle_focus, render_search_bar, render_size_slider};
use crate::ui::theme::{
    apply_dark_theme, DEFAULT_THUMB_SIZE, FRAME_TIME_MS, SEARCH_DEBOUNCE_MS, STATUS_TEXT,
};

enum SendResult {
    Success(String),
    Error(String),
}

enum EditResult {
    Success(Sticker),
    Error(String),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SortMode {
    Recent,
    Pack,
    Text,
}

impl SortMode {
    const ALL: [SortMode; 3] = [SortMode::Recent, SortMode::Pack, SortMode::Text];

    fn label(self) -> &'static str {
        match self {
            SortMode::Recent => "Recent",
            SortMode::Pack => "Pack",
            SortMode::Text => "Text",
        }
    }
}

pub struct StickerApp {
    // State
    search_query: String,
    all_stickers: Vec<Sticker>,
    stickers: Vec<Sticker>,
    search_results: Option<Vec<Sticker>>,
    chats: Vec<ChatInfo>,
    selected_chat: Option<ChatInfo>,
    status: String,
    pack_filter: Option<String>,
    sort_mode: SortMode,
    pack_counts: HashMap<String, usize>,
    pack_options: Vec<(String, usize)>,
    top_packs: Vec<(String, usize)>,
    manual_edit_count: usize,
    selected_sticker_id: Option<String>,
    editor_text: String,
    is_saving_text: bool,

    // Grid state
    grid_state: GridState,
    grid_focused: bool,

    // Thumbnails (LRU cache)
    textures: HashMap<String, egui::TextureHandle>,
    texture_order: VecDeque<String>, // LRU order: front = oldest, back = newest
    loading_thumbs: HashSet<String>,
    thumbnail_loader: ThumbnailLoader,

    // Services
    sticker_loader: StickerLoader,
    chat_detector: ChatDetector,
    is_loading_all: bool,

    // Backend
    rt: Arc<Runtime>,
    api: Arc<Api>,
    telegram: Arc<TelegramClient>,
    hotkey_rx: Receiver<HotkeyEvent>,

    // Search
    search_tx: Sender<Vec<Sticker>>,
    search_rx: Receiver<Vec<Sticker>>,
    search_debounce: Option<Instant>,

    // Send sticker
    send_result_rx: Receiver<SendResult>,
    send_result_tx: Sender<SendResult>,
    edit_result_rx: Receiver<EditResult>,
    edit_result_tx: Sender<EditResult>,

    // Window visibility
    visible: bool,

    // Focus
    focus_search: bool,

    // Prevent double-send
    just_sent: bool,

    // Thumbnail size
    thumb_size: f32,

    // Thumbnail cache (for clipboard copy)
    thumbnail_cache: Arc<ThumbnailCache>,
}

impl StickerApp {
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

        // Search channel
        let (search_tx, search_rx) = mpsc::channel();

        // Send result channel
        let (send_result_tx, send_result_rx) = mpsc::channel();
        let (edit_result_tx, edit_result_rx) = mpsc::channel();

        // Start services
        let thumbnail_loader = ThumbnailLoader::start(rt.clone(), api.clone(), cache.clone());
        let sticker_loader = StickerLoader::start(rt.clone(), api.clone());
        let chat_detector = ChatDetector::start(chats.clone());

        // Auto-select first chat
        let selected_chat = chats.first().cloned();

        Self {
            search_query: String::new(),
            all_stickers: Vec::new(),
            stickers: Vec::new(),
            search_results: None,
            chats,
            selected_chat,
            status: "Loading...".into(),
            pack_filter: None,
            sort_mode: SortMode::Recent,
            pack_counts: HashMap::new(),
            pack_options: Vec::new(),
            top_packs: Vec::new(),
            manual_edit_count: 0,
            selected_sticker_id: None,
            editor_text: String::new(),
            is_saving_text: false,
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
            api,
            telegram,
            hotkey_rx,
            search_tx,
            search_rx,
            search_debounce: None,
            send_result_rx,
            send_result_tx,
            edit_result_rx,
            edit_result_tx,
            visible: true,
            focus_search: true,
            just_sent: false,
            thumb_size: DEFAULT_THUMB_SIZE,
            thumbnail_cache: cache,
        }
    }

    fn trigger_search(&mut self) {
        if self.search_query.trim().is_empty() {
            debug!("[search] cleared query, restoring library view");
            self.search_debounce = None;
            self.search_results = None;
            self.rebuild_stickers();
            self.status = self.default_status();
            return;
        }

        debug!("[search] trigger debounce");
        self.status = format!("Searching for {:?}", self.search_query.trim());
        self.search_debounce = Some(Instant::now());
    }

    fn do_search(&mut self) {
        let query = self.search_query.trim().to_string();
        if query.is_empty() {
            self.search_results = None;
            self.rebuild_stickers();
            self.status = self.default_status();
            return;
        }

        info!("[search] searching for: {:?}", query);
        let api = self.api.clone();
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

    fn default_status(&self) -> String {
        if self.search_results.is_some() {
            format!(
                "Found {} stickers • library {}",
                self.stickers.len(),
                self.all_stickers.len()
            )
        } else if self.is_loading_all {
            format!("Loading library... {}", self.all_stickers.len())
        } else if let Some(pack) = &self.pack_filter {
            format!("{} stickers • pack {}", self.stickers.len(), pack)
        } else {
            format!("{} stickers", self.stickers.len())
        }
    }

    fn selected_sticker(&self) -> Option<&Sticker> {
        self.stickers.get(self.grid_state.selected)
    }

    fn select_index(&mut self, idx: usize) {
        if self.stickers.is_empty() {
            self.grid_state.selected = 0;
            self.selected_sticker_id = None;
            self.editor_text.clear();
            return;
        }

        self.grid_state.selected = idx.min(self.stickers.len() - 1);
        self.sync_selection_from_grid();
    }

    fn sync_selection_from_grid(&mut self) {
        let Some(sticker) = self.selected_sticker().cloned() else {
            self.selected_sticker_id = None;
            self.editor_text.clear();
            return;
        };

        let changed = self.selected_sticker_id.as_deref() != Some(sticker.sticker_id.as_str());
        self.selected_sticker_id = Some(sticker.sticker_id.clone());

        if changed {
            self.editor_text = sticker.text.clone();
        }
    }

    fn rebuild_pack_stats(&mut self) {
        self.pack_counts.clear();
        self.pack_options.clear();
        self.top_packs.clear();
        self.manual_edit_count = 0;

        for sticker in &self.all_stickers {
            if sticker.manual_edit {
                self.manual_edit_count += 1;
            }

            if sticker.set_name.is_empty() {
                continue;
            }

            *self
                .pack_counts
                .entry(sticker.set_name.clone())
                .or_default() += 1;
        }

        self.pack_options = self
            .pack_counts
            .iter()
            .map(|(name, count)| (name.clone(), *count))
            .collect();
        self.pack_options.sort_by(|a, b| {
            a.0.to_lowercase()
                .cmp(&b.0.to_lowercase())
                .then_with(|| a.0.cmp(&b.0))
        });

        self.top_packs = self
            .pack_counts
            .iter()
            .map(|(name, count)| (name.clone(), *count))
            .collect();
        self.top_packs
            .sort_by(|a, b| b.1.cmp(&a.1).then_with(|| a.0.cmp(&b.0)));
        self.top_packs.truncate(8);
    }

    fn rebuild_stickers(&mut self) {
        let previous_index = self.grid_state.selected;
        let previous_id = self.selected_sticker_id.clone();

        let mut stickers = match &self.search_results {
            Some(results) => results.clone(),
            None => self.all_stickers.clone(),
        };

        if let Some(pack_filter) = &self.pack_filter {
            stickers.retain(|sticker| sticker.set_name == *pack_filter);
        }

        match self.sort_mode {
            SortMode::Recent => {}
            SortMode::Pack => {
                stickers.sort_by(|a, b| {
                    let a_pack = a.set_name.to_lowercase();
                    let b_pack = b.set_name.to_lowercase();

                    a_pack
                        .cmp(&b_pack)
                        .then_with(|| a.text.to_lowercase().cmp(&b.text.to_lowercase()))
                        .then_with(|| a.sticker_id.cmp(&b.sticker_id))
                });
            }
            SortMode::Text => {
                stickers.sort_by(|a, b| {
                    a.text
                        .to_lowercase()
                        .cmp(&b.text.to_lowercase())
                        .then_with(|| a.set_name.to_lowercase().cmp(&b.set_name.to_lowercase()))
                        .then_with(|| a.sticker_id.cmp(&b.sticker_id))
                });
            }
        }

        self.stickers = stickers;

        if self.stickers.is_empty() {
            self.grid_state.selected = 0;
            self.selected_sticker_id = None;
            self.editor_text.clear();
            return;
        }

        if let Some(selected_id) = previous_id {
            if let Some(idx) = self
                .stickers
                .iter()
                .position(|sticker| sticker.sticker_id == selected_id)
            {
                self.select_index(idx);
                return;
            }
        }

        self.select_index(previous_index.min(self.stickers.len() - 1));
    }

    fn apply_updated_sticker(&mut self, updated: Sticker) -> bool {
        let search_query = self.search_query.trim().to_lowercase();
        let matches_search =
            search_query.is_empty() || updated.text.to_lowercase().contains(&search_query);

        if let Some(sticker) = self
            .all_stickers
            .iter_mut()
            .find(|sticker| sticker.sticker_id == updated.sticker_id)
        {
            *sticker = updated.clone();
        }

        if let Some(results) = &mut self.search_results {
            if let Some(pos) = results
                .iter()
                .position(|sticker| sticker.sticker_id == updated.sticker_id)
            {
                if matches_search {
                    results[pos] = updated.clone();
                } else {
                    results.remove(pos);
                }
            }
        }

        self.is_saving_text = false;
        self.selected_sticker_id = Some(updated.sticker_id.clone());
        self.editor_text = updated.text.clone();
        self.rebuild_pack_stats();
        self.rebuild_stickers();

        !matches_search && !search_query.is_empty()
    }

    fn send_sticker(&mut self) {
        let Some(chat) = &self.selected_chat else {
            warn!("[send] no chat selected");
            self.status = "Select a chat first!".into();
            return;
        };

        let Some(sticker) = self.selected_sticker() else {
            warn!("[send] no sticker selected");
            return;
        };

        let telegram = self.telegram.clone();
        let chat_id = chat.id;
        let set_name = sticker.set_name.clone();
        let document_id = sticker.document_id;
        let chat_name = chat.name.clone();
        let tx = self.send_result_tx.clone();

        info!(
            "[send] sending sticker {} to chat {}",
            document_id, chat_name
        );
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

    fn copy_sticker_to_clipboard(&mut self) {
        let Some(sticker) = self.selected_sticker() else {
            warn!("[clipboard] no sticker selected");
            return;
        };

        let file_id = &sticker.file_id;
        let cache_path = self.thumbnail_cache.cache_path(file_id);

        if !cache_path.exists() {
            warn!(
                "[clipboard] cache file not found for sticker: {}",
                &file_id[..20.min(file_id.len())]
            );
            self.status = "Image not cached yet".into();
            return;
        }

        info!("[clipboard] reading sticker from: {:?}", cache_path);
        match std::fs::read(&cache_path) {
            Ok(data) => match image::load_from_memory(&data) {
                Ok(img) => {
                    let rgba = img.to_rgba8();
                    let (width, height) = rgba.dimensions();
                    let img_data = arboard::ImageData {
                        width: width as usize,
                        height: height as usize,
                        bytes: std::borrow::Cow::Owned(rgba.into_raw()),
                    };
                    match arboard::Clipboard::new() {
                        Ok(mut clipboard) => match clipboard.set_image(img_data) {
                            Ok(_) => {
                                info!(
                                    "[clipboard] copied sticker to clipboard ({}x{})",
                                    width, height
                                );
                                self.status = "Copied to clipboard!".into();
                            }
                            Err(e) => {
                                warn!("[clipboard] failed to set image: {}", e);
                                self.status = format!("Clipboard error: {}", e);
                            }
                        },
                        Err(e) => {
                            warn!("[clipboard] failed to open clipboard: {}", e);
                            self.status = format!("Clipboard error: {}", e);
                        }
                    }
                }
                Err(e) => {
                    warn!("[clipboard] failed to decode image: {}", e);
                    self.status = format!("Decode error: {}", e);
                }
            },
            Err(e) => {
                warn!("[clipboard] failed to read cache file: {}", e);
                self.status = format!("Read error: {}", e);
            }
        }
    }

    fn reset_editor(&mut self) {
        if let Some(sticker) = self.selected_sticker() {
            self.editor_text = sticker.text.clone();
        }
    }

    fn save_current_text(&mut self) {
        let Some(sticker) = self.selected_sticker().cloned() else {
            warn!("[edit] no sticker selected");
            return;
        };

        if self.is_saving_text || sticker.text == self.editor_text {
            return;
        }

        let api = self.api.clone();
        let tx = self.edit_result_tx.clone();
        let sticker_id = sticker.sticker_id;
        let new_text = self.editor_text.clone();

        self.is_saving_text = true;
        self.status = "Saving text...".into();

        self.rt.spawn(async move {
            match api.update_sticker_text(&sticker_id, &new_text).await {
                Ok(updated) => {
                    if tx.send(EditResult::Success(updated)).is_err() {
                        warn!("[edit] result channel closed");
                    }
                }
                Err(e) => {
                    if tx.send(EditResult::Error(e.to_string())).is_err() {
                        warn!("[edit] result channel closed");
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

    fn touch_texture(&mut self, file_id: &str) {
        let Some(pos) = self.texture_order.iter().position(|id| id == file_id) else {
            return;
        };

        if pos + 1 == self.texture_order.len() {
            return;
        }

        if let Some(id) = self.texture_order.remove(pos) {
            self.texture_order.push_back(id);
        }
    }

    fn poll_all(&mut self, ctx: &egui::Context) {
        // Poll sticker loading results
        while let Some(result) = self.sticker_loader.try_recv() {
            match result {
                StickerLoadResult::Append(new_stickers) => {
                    debug!("[poll] appending {} stickers", new_stickers.len());
                    self.all_stickers.extend(new_stickers);
                    self.rebuild_pack_stats();

                    if self.search_results.is_none() {
                        self.rebuild_stickers();
                    }

                    self.status = self.default_status();
                }
                StickerLoadResult::Done => {
                    info!(
                        "[poll] sticker loading complete: {} total",
                        self.all_stickers.len()
                    );
                    self.is_loading_all = false;
                    self.rebuild_stickers();
                    self.status = self.default_status();
                }
            }
        }

        // Poll search results
        while let Ok(stickers) = self.search_rx.try_recv() {
            debug!("[poll] received {} stickers from search", stickers.len());
            self.search_results = Some(stickers);
            self.rebuild_stickers();
            self.status = self.default_status();
        }

        // Poll thumbnail results (limit per frame to avoid decoding hundreds at once)
        let mut thumbs_processed = 0;
        const MAX_THUMBS_PER_FRAME: usize = 10;
        while thumbs_processed < MAX_THUMBS_PER_FRAME {
            let Some(result) = self.thumbnail_loader.try_recv() else {
                break;
            };
            match result {
                ThumbnailResult::Loaded(file_id, data) => {
                    debug!(
                        "[poll] thumbnail loaded: {}",
                        &file_id[..20.min(file_id.len())]
                    );
                    self.loading_thumbs.remove(&file_id);

                    // Skip if already loaded (avoid duplicates)
                    if self.textures.contains_key(&file_id) {
                        continue;
                    }

                    if let Ok(image) = image::load_from_memory(&data) {
                        let size = [image.width() as _, image.height() as _];
                        let rgba = image.to_rgba8();
                        let pixels = rgba.as_flat_samples();
                        let texture = ctx.load_texture(
                            &file_id,
                            egui::ColorImage::from_rgba_unmultiplied(size, pixels.as_slice()),
                            egui::TextureOptions::LINEAR,
                        );

                        // LRU eviction BEFORE inserting new texture
                        while self.textures.len() >= MAX_TEXTURES {
                            if let Some(old_id) = self.texture_order.pop_front() {
                                if self.textures.remove(&old_id).is_some() {
                                    debug!(
                                        "[poll] evicted texture, total: {}",
                                        self.textures.len()
                                    );
                                    break;
                                }
                            } else {
                                break;
                            }
                        }

                        self.textures.insert(file_id.clone(), texture);
                        self.texture_order.push_back(file_id);
                    } else {
                        warn!(
                            "[poll] failed to decode image: {}",
                            &file_id[..20.min(file_id.len())]
                        );
                    }
                }
                ThumbnailResult::NotFound(file_id) => {
                    debug!(
                        "[poll] thumbnail not found: {}",
                        &file_id[..20.min(file_id.len())]
                    );
                    self.loading_thumbs.remove(&file_id);
                }
            }
            thumbs_processed += 1;
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

        while let Ok(result) = self.edit_result_rx.try_recv() {
            match result {
                EditResult::Success(updated) => {
                    let hidden_by_search = self.apply_updated_sticker(updated);
                    if hidden_by_search {
                        self.status = "Text saved; sticker left current search results".into();
                    } else {
                        self.status = "Text saved".into();
                    }
                }
                EditResult::Error(e) => {
                    self.is_saving_text = false;
                    warn!("[poll] edit error: {}", e);
                    self.status = format!("Edit error: {}", e);
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

impl eframe::App for StickerApp {
    fn clear_color(&self, _visuals: &egui::Visuals) -> [f32; 4] {
        [0.0, 0.0, 0.0, 0.0]
    }

    fn update(&mut self, ctx: &egui::Context, _frame: &mut eframe::Frame) {
        // Poll all async results
        self.poll_all(ctx);

        // Check search debounce
        if let Some(start) = self.search_debounce {
            if start.elapsed() > Duration::from_millis(SEARCH_DEBOUNCE_MS) {
                self.search_debounce = None;
                self.do_search();
            }
        }

        // Handle hotkey
        while let Ok(event) = self.hotkey_rx.try_recv() {
            match event {
                HotkeyEvent::Toggle => {
                    self.visible = !self.visible;
                    info!("[hotkey] toggle, visible={}", self.visible);
                    if self.visible {
                        ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
                        self.focus_search = true;
                    }
                }
            }
        }

        // Escape to hide
        if ctx.input(|i| i.key_pressed(egui::Key::Escape)) {
            info!("[key] escape pressed, hiding window");
            self.visible = false;
        }

        if !self.visible {
            ctx.send_viewport_cmd(egui::ViewportCommand::Visible(false));
            return;
        } else {
            ctx.send_viewport_cmd(egui::ViewportCommand::Visible(true));
        }

        let selected_sticker = self.selected_sticker().cloned();
        let selected_texture = selected_sticker
            .as_ref()
            .and_then(|sticker| self.textures.get(&sticker.file_id))
            .cloned();
        let selected_pack_count = selected_sticker
            .as_ref()
            .and_then(|sticker| self.pack_counts.get(&sticker.set_name))
            .copied()
            .unwrap_or(0);
        let pack_options = self.pack_options.clone();
        let top_packs = self.top_packs.clone();
        let current_pack_filter = self.pack_filter.clone();
        let library_total = self.all_stickers.len();
        let visible_total = self.stickers.len();
        let pack_total = self.pack_options.len();
        let manual_edit_total = self.manual_edit_count;
        let selected_position = if self.stickers.is_empty() {
            0
        } else {
            self.grid_state.selected + 1
        };

        let mut apply_pack_filter: Option<Option<String>> = None;
        let mut search_changed = false;
        let mut editor_has_focus = false;
        let mut details_send = false;
        let mut details_copy = false;
        let mut details_save = false;
        let mut details_reset = false;
        let mut details_request_current_pack = false;

        egui::TopBottomPanel::top("toolbar_panel").show(ctx, |ui| {
            render_chat_selector(ui, &self.chats, &mut self.selected_chat);
            ui.add_space(8.0);

            ui.horizontal(|ui| {
                let search_resp = render_search_bar(ui, &mut self.search_query);

                if self.focus_search {
                    ui.memory_mut(|m| m.request_focus(search_resp.id));
                    self.focus_search = false;
                }

                if search_resp.changed {
                    search_changed = true;
                    self.grid_focused = false;
                }

                handle_focus(
                    ui,
                    ctx,
                    search_resp.id,
                    &mut self.focus_search,
                    &mut self.grid_focused,
                    !self.stickers.is_empty(),
                );

                ui.separator();
                ui.label("Sort:");
                egui::ComboBox::from_id_salt("sort_mode")
                    .selected_text(self.sort_mode.label())
                    .show_ui(ui, |ui| {
                        for mode in SortMode::ALL {
                            if ui
                                .selectable_label(self.sort_mode == mode, mode.label())
                                .clicked()
                            {
                                self.sort_mode = mode;
                                self.rebuild_stickers();
                                self.status = self.default_status();
                            }
                        }
                    });
            });

            ui.add_space(6.0);

            ui.horizontal(|ui| {
                ui.label("Pack:");
                egui::ComboBox::from_id_salt("pack_filter")
                    .selected_text(
                        self.pack_filter
                            .clone()
                            .unwrap_or_else(|| "All packs".into()),
                    )
                    .width(260.0)
                    .show_ui(ui, |ui| {
                        if ui
                            .selectable_label(self.pack_filter.is_none(), "All packs")
                            .clicked()
                        {
                            apply_pack_filter = Some(None);
                        }

                        for (pack_name, count) in &pack_options {
                            let label = format!("{} ({})", pack_name, count);
                            if ui
                                .selectable_label(
                                    self.pack_filter.as_deref() == Some(pack_name.as_str()),
                                    label,
                                )
                                .clicked()
                            {
                                apply_pack_filter = Some(Some(pack_name.clone()));
                            }
                        }
                    });

                if self.pack_filter.is_some() && ui.button("Clear").clicked() {
                    apply_pack_filter = Some(None);
                }

                ui.separator();
                render_size_slider(ui, &mut self.thumb_size);

                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    ui.colored_label(STATUS_TEXT, &self.status);
                });
            });
        });

        egui::SidePanel::right("details_panel")
            .resizable(true)
            .default_width(320.0)
            .min_width(280.0)
            .show(ctx, |ui| {
                ui.heading("Selected");
                ui.add_space(8.0);

                if let Some(sticker) = &selected_sticker {
                    if let Some(texture) = &selected_texture {
                        let max_side = ui.available_width().min(220.0);
                        let size = texture.size_vec2();
                        let scale = (max_side / size.x.max(size.y)).min(1.0);
                        let scaled = size * scale;

                        ui.vertical_centered(|ui| {
                            ui.add(egui::Image::new(texture).fit_to_exact_size(scaled));
                        });
                        ui.add_space(8.0);
                    }

                    ui.horizontal(|ui| {
                        if ui.button("Send").clicked() {
                            details_send = true;
                        }
                        if ui.button("Copy").clicked() {
                            details_copy = true;
                        }
                        if !sticker.set_name.is_empty() && ui.button("This pack").clicked() {
                            details_request_current_pack = true;
                        }
                    });

                    ui.add_space(8.0);
                    ui.label(format!("Pack: {}", sticker.pack_label()));
                    ui.label(format!("Pack stickers: {}", selected_pack_count));
                    ui.label(format!(
                        "Visible position: {}/{}",
                        selected_position, visible_total
                    ));
                    ui.label(format!(
                        "Emoji: {}",
                        if sticker.emoji.is_empty() {
                            "-"
                        } else {
                            &sticker.emoji
                        }
                    ));
                    ui.label(format!("Source: {}", sticker.source_label()));
                    ui.label(format!(
                        "Sticker ID: {}",
                        if sticker.sticker_id.len() > 24 {
                            &sticker.sticker_id[..24]
                        } else {
                            &sticker.sticker_id
                        }
                    ));

                    ui.add_space(10.0);
                    ui.label(RichText::new("Text").strong());
                    let edit_response = ui.add_sized(
                        [ui.available_width(), 150.0],
                        egui::TextEdit::multiline(&mut self.editor_text)
                            .desired_rows(7)
                            .hint_text("Editable sticker text"),
                    );
                    editor_has_focus = edit_response.has_focus();

                    let editor_dirty = self.editor_text != sticker.text;
                    if editor_dirty {
                        ui.colored_label(STATUS_TEXT, "Unsaved changes");
                    }

                    ui.horizontal(|ui| {
                        if ui
                            .add_enabled(
                                editor_dirty && !self.is_saving_text,
                                egui::Button::new("Save"),
                            )
                            .clicked()
                        {
                            details_save = true;
                        }
                        if ui
                            .add_enabled(
                                editor_dirty && !self.is_saving_text,
                                egui::Button::new("Reset"),
                            )
                            .clicked()
                        {
                            details_reset = true;
                        }
                        if self.is_saving_text {
                            ui.spinner();
                        }
                    });
                } else {
                    ui.colored_label(STATUS_TEXT, "Select a sticker to inspect and edit it.");
                }

                ui.separator();
                ui.heading("Library");
                ui.label(format!("Loaded: {}", library_total));
                ui.label(format!("Visible: {}", visible_total));
                ui.label(format!("Packs: {}", pack_total));
                ui.label(format!("Manual edits: {}", manual_edit_total));
                ui.label(format!(
                    "Filter: {}",
                    current_pack_filter
                        .clone()
                        .unwrap_or_else(|| "All packs".into())
                ));
                ui.label(format!("Sort: {}", self.sort_mode.label()));

                ui.separator();
                ui.label(RichText::new("Top packs").strong());
                if top_packs.is_empty() {
                    ui.colored_label(STATUS_TEXT, "Pack stats will appear after the first load.");
                } else {
                    for (pack_name, count) in top_packs {
                        let is_active = current_pack_filter.as_deref() == Some(pack_name.as_str());
                        let label = if is_active {
                            format!("[{}] {}", count, pack_name)
                        } else {
                            format!("{} {}", count, pack_name)
                        };

                        if ui.button(label).clicked() {
                            apply_pack_filter = Some(Some(pack_name));
                        }
                    }
                }
            });

        egui::CentralPanel::default().show(ctx, |ui| {
            ui.set_min_size(egui::vec2(720.0, 650.0));

            self.grid_state
                .update_cols(ui.available_width(), self.thumb_size);

            handle_grid_navigation(
                ui,
                &mut self.grid_state,
                self.stickers.len(),
                self.grid_focused,
            );
            self.sync_selection_from_grid();

            let sticker_data: Vec<_> = self
                .stickers
                .iter()
                .enumerate()
                .map(|(i, sticker)| (i, sticker.file_id.clone()))
                .collect();

            let grid_resp = render_grid(
                ui,
                &sticker_data,
                &self.textures,
                self.grid_state.selected,
                self.thumb_size,
                self.grid_state.cols,
            );

            for file_id in grid_resp.needs_thumbnail {
                self.request_thumbnail(&file_id);
            }

            for file_id in grid_resp.visible_file_ids {
                self.touch_texture(&file_id);
            }

            if let Some(idx) = grid_resp.ctrl_clicked {
                self.grid_focused = true;
                self.select_index(idx);
                self.copy_sticker_to_clipboard();
            }

            if let Some(idx) = grid_resp.clicked {
                self.grid_focused = true;
                self.select_index(idx);
            }

            if let Some(idx) = grid_resp.double_clicked {
                self.grid_focused = true;
                self.select_index(idx);
                self.send_sticker();
            }
        });

        if let Some(file_id) = selected_sticker.map(|sticker| sticker.file_id) {
            self.request_thumbnail(&file_id);
            self.touch_texture(&file_id);
        }

        if search_changed {
            self.trigger_search();
        }

        if let Some(filter) = apply_pack_filter {
            self.pack_filter = filter;
            self.rebuild_stickers();
            self.status = self.default_status();
        }

        if details_request_current_pack {
            if let Some(sticker) = self.selected_sticker() {
                self.pack_filter = if sticker.set_name.is_empty() {
                    None
                } else {
                    Some(sticker.set_name.clone())
                };
                self.rebuild_stickers();
                self.status = self.default_status();
            }
        }

        if details_reset {
            self.reset_editor();
        }

        if details_save {
            self.save_current_text();
        }

        if details_copy {
            self.copy_sticker_to_clipboard();
        }

        if details_send {
            self.send_sticker();
        }

        if ctx.input(|i| i.key_released(egui::Key::Enter)) {
            self.just_sent = false;
        }

        let ctrl_enter = ctx.input(|i| i.key_pressed(egui::Key::Enter) && i.modifiers.ctrl);
        let enter = ctx.input(|i| i.key_pressed(egui::Key::Enter));
        let ctrl_s = ctx.input(|i| i.key_pressed(egui::Key::S) && i.modifiers.ctrl);

        if ctrl_s && editor_has_focus {
            self.save_current_text();
        } else if ctrl_enter && !self.stickers.is_empty() && !self.just_sent && !editor_has_focus {
            self.copy_sticker_to_clipboard();
            self.just_sent = true;
        } else if enter && !self.stickers.is_empty() && !self.just_sent && !editor_has_focus {
            self.send_sticker();
            self.just_sent = true;
        }

        ctx.request_repaint_after(Duration::from_millis(FRAME_TIME_MS));
    }
}
