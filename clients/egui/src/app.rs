use eframe::egui::{self, RichText};
use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

// Limit textures in memory to ~200MB instead of 1.5GB
const MAX_TEXTURES: usize = 200;
const MIN_WINDOW_WIDTH: f32 = 760.0;
const MIN_WINDOW_HEIGHT: f32 = 560.0;
const MAX_WINDOW_WIDTH: f32 = 1800.0;
const MAX_WINDOW_HEIGHT: f32 = 1100.0;
const TARGET_WINDOW_WIDTH_RATIO: f32 = 0.78;
const TARGET_WINDOW_HEIGHT_RATIO: f32 = 0.82;
const MAX_MONITOR_WIDTH_RATIO: f32 = 0.94;
const MAX_MONITOR_HEIGHT_RATIO: f32 = 0.92;

use crate::api::Api;
use crate::cache::{StickerCatalog, ThumbnailCache};
use crate::hotkey::HotkeyEvent;
use crate::models::{search_stickers, sticker_matches_query, ChatInfo, Sticker};
use crate::services::chat_detector::match_chat_title;
use crate::services::health_checker::{HealthState, HealthTarget};
use crate::services::sticker_loader::StickerLoadResult;
use crate::services::thumbnail_loader::ThumbnailResult;
use crate::services::{ChatDetector, HealthChecker, StickerLoader, ThumbnailLoader};
use crate::telegram::TelegramClient;
use crate::ui::chat_selector::render_chat_selector;
use crate::ui::grid::{handle_grid_navigation, render_grid, GridState};
use crate::ui::search::{handle_focus, render_search_bar, render_size_slider};
use crate::ui::theme::{
    apply_dark_theme, DEFAULT_THUMB_SIZE, FRAME_TIME_MS, SEARCH_DEBOUNCE_MS, STATUS_ERROR,
    STATUS_OK, STATUS_TEXT, STATUS_WARN,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SendMode {
    Sticker,
    Image,
}

enum SendResult {
    Success { chat_name: String, mode: SendMode },
    Error(String),
}

enum EditResult {
    Success(Sticker),
    Error(String),
}

fn is_sticker_send_error(error: &str) -> bool {
    let error = error.to_lowercase();
    error.contains("item cannot be sent as a sticker")
        || error.contains("stickerset_invalid")
        || error.contains("sticker not found in set")
        || error.contains("document_invalid")
        || error.contains("media_invalid")
}

fn should_fallback_to_image(error: &anyhow::Error) -> bool {
    is_sticker_send_error(&error.to_string())
}

fn friendly_send_error(error: &str) -> String {
    let lower = error.to_lowercase();

    if lower.contains("image is not cached yet") {
        return "Telegram send failed: image is not cached yet".into();
    }

    if lower.contains("read 0 bytes")
        || lower.contains("io failed")
        || lower.contains("read error")
        || lower.contains("connection reset")
        || lower.contains("connection closed")
    {
        return "Telegram send failed: connection dropped, check proxy".into();
    }

    if lower.contains("timed out") || lower.contains("timeout") {
        return "Telegram send failed: timeout, check proxy".into();
    }

    if is_sticker_send_error(error) {
        return "Telegram send failed: sticker rejected and image fallback failed".into();
    }

    format!("Telegram send failed: {error}")
}

pub struct AppResources {
    pub rt: Arc<Runtime>,
    pub api: Arc<Api>,
    pub telegram: Arc<TelegramClient>,
    pub thumbnail_cache: Arc<ThumbnailCache>,
    pub sticker_catalog: Arc<StickerCatalog>,
    pub cached_stickers: Vec<Sticker>,
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
    is_offline: bool,

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
    health_checker: HealthChecker,
    is_loading_all: bool,

    // Backend
    rt: Arc<Runtime>,
    api: Arc<Api>,
    telegram: Arc<TelegramClient>,
    api_health: HealthState,
    telegram_health: HealthState,
    hotkey_rx: Receiver<HotkeyEvent>,

    // Search
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
    sticker_catalog: Arc<StickerCatalog>,

    // Window sizing
    window_sized_for_monitor: Option<egui::Vec2>,
}

impl StickerApp {
    pub fn new(
        cc: &eframe::CreationContext<'_>,
        resources: AppResources,
        chats: Vec<ChatInfo>,
        hotkey_rx: Receiver<HotkeyEvent>,
    ) -> Self {
        apply_dark_theme(&cc.egui_ctx);

        let AppResources {
            rt,
            api,
            telegram,
            thumbnail_cache,
            sticker_catalog,
            cached_stickers,
        } = resources;

        // Send result channel
        let (send_result_tx, send_result_rx) = mpsc::channel();
        let (edit_result_tx, edit_result_rx) = mpsc::channel();

        // Start services
        let thumbnail_loader =
            ThumbnailLoader::start(rt.clone(), api.clone(), thumbnail_cache.clone());
        let sticker_loader = StickerLoader::start(rt.clone(), api.clone(), sticker_catalog.clone());
        let chat_detector = ChatDetector::start(chats.clone());
        let health_checker = HealthChecker::start(rt.clone(), api.clone(), telegram.clone());

        let mut app = Self {
            search_query: String::new(),
            all_stickers: cached_stickers.clone(),
            stickers: cached_stickers,
            search_results: None,
            chats,
            selected_chat: None,
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
            is_offline: false,
            grid_state: GridState::new(),
            grid_focused: false,
            textures: HashMap::new(),
            texture_order: VecDeque::new(),
            loading_thumbs: HashSet::new(),
            thumbnail_loader,
            sticker_loader,
            chat_detector,
            health_checker,
            is_loading_all: true,
            rt,
            api,
            telegram,
            api_health: HealthState::Checking,
            telegram_health: HealthState::Checking,
            hotkey_rx,
            search_debounce: None,
            send_result_rx,
            send_result_tx,
            edit_result_rx,
            edit_result_tx,
            visible: true,
            focus_search: true,
            just_sent: false,
            thumb_size: DEFAULT_THUMB_SIZE,
            thumbnail_cache,
            sticker_catalog,
            window_sized_for_monitor: None,
        };
        app.rebuild_pack_stats();
        app.select_index(0);
        app.status = app.default_status();
        app
    }

    fn desired_window_axis(
        monitor_axis: f32,
        target_ratio: f32,
        min_axis: f32,
        max_axis: f32,
        max_monitor_ratio: f32,
    ) -> f32 {
        let available = (monitor_axis * max_monitor_ratio).max(320.0);
        let min_axis = min_axis.min(available);
        (monitor_axis * target_ratio)
            .min(max_axis)
            .clamp(min_axis, available)
    }

    fn desired_window_size(monitor_size: egui::Vec2) -> egui::Vec2 {
        egui::vec2(
            Self::desired_window_axis(
                monitor_size.x,
                TARGET_WINDOW_WIDTH_RATIO,
                MIN_WINDOW_WIDTH,
                MAX_WINDOW_WIDTH,
                MAX_MONITOR_WIDTH_RATIO,
            ),
            Self::desired_window_axis(
                monitor_size.y,
                TARGET_WINDOW_HEIGHT_RATIO,
                MIN_WINDOW_HEIGHT,
                MAX_WINDOW_HEIGHT,
                MAX_MONITOR_HEIGHT_RATIO,
            ),
        )
    }

    fn min_window_size(monitor_size: egui::Vec2) -> egui::Vec2 {
        egui::vec2(
            MIN_WINDOW_WIDTH.min((monitor_size.x * MAX_MONITOR_WIDTH_RATIO).max(480.0)),
            MIN_WINDOW_HEIGHT.min((monitor_size.y * MAX_MONITOR_HEIGHT_RATIO).max(420.0)),
        )
    }

    fn max_window_size(monitor_size: egui::Vec2) -> egui::Vec2 {
        egui::vec2(
            (monitor_size.x * MAX_MONITOR_WIDTH_RATIO).max(480.0),
            (monitor_size.y * MAX_MONITOR_HEIGHT_RATIO).max(420.0),
        )
    }

    fn sync_window_size(&mut self, ctx: &egui::Context) {
        let monitor_size = ctx.input(|i| i.viewport().monitor_size);
        let Some(monitor_size) = monitor_size.filter(|size| size.x > 1.0 && size.y > 1.0) else {
            return;
        };

        let monitor_changed = self
            .window_sized_for_monitor
            .map(|prev| {
                (prev.x - monitor_size.x).abs() > 1.0 || (prev.y - monitor_size.y).abs() > 1.0
            })
            .unwrap_or(true);

        if !monitor_changed {
            return;
        }

        ctx.send_viewport_cmd(egui::ViewportCommand::MinInnerSize(Self::min_window_size(
            monitor_size,
        )));
        ctx.send_viewport_cmd(egui::ViewportCommand::MaxInnerSize(Self::max_window_size(
            monitor_size,
        )));
        ctx.send_viewport_cmd(egui::ViewportCommand::InnerSize(Self::desired_window_size(
            monitor_size,
        )));

        self.window_sized_for_monitor = Some(monitor_size);
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

        info!("[search] searching local catalog for: {:?}", query);
        let stickers = search_stickers(&self.all_stickers, &query);
        info!("[search] found {} stickers locally", stickers.len());
        self.search_results = Some(stickers);
        self.selected_sticker_id = None;
        self.grid_state.selected = 0;
        self.rebuild_stickers();
        self.status = self.default_status();
    }

    fn default_status(&self) -> String {
        let status = if self.search_results.is_some() {
            format!(
                "Found {} stickers • library {}",
                self.stickers.len(),
                self.all_stickers.len()
            )
        } else if self.is_loading_all {
            if self.all_stickers.is_empty() {
                "Loading library...".into()
            } else {
                format!("Refreshing library... {} cached", self.all_stickers.len())
            }
        } else if let Some(pack) = &self.pack_filter {
            format!("{} stickers • pack {}", self.stickers.len(), pack)
        } else {
            format!("{} stickers", self.stickers.len())
        };

        if self.is_offline {
            format!("Offline • {status}")
        } else {
            status
        }
    }

    fn health_indicator(prefix: &str, state: &HealthState) -> (String, egui::Color32) {
        match state {
            HealthState::Checking => (format!("{prefix}: Checking"), STATUS_WARN),
            HealthState::Online => (format!("{prefix}: Online"), STATUS_OK),
            HealthState::Offline(error) => {
                if error.is_empty() {
                    (format!("{prefix}: Offline"), STATUS_ERROR)
                } else {
                    (format!("{prefix}: Offline ({error})"), STATUS_ERROR)
                }
            }
        }
    }

    fn api_indicator(&self) -> (String, egui::Color32) {
        if self.is_loading_all && matches!(self.api_health, HealthState::Checking) {
            ("API: Syncing".into(), STATUS_WARN)
        } else {
            Self::health_indicator("API", &self.api_health)
        }
    }

    fn telegram_indicator(&self) -> (String, egui::Color32) {
        Self::health_indicator("TG", &self.telegram_health)
    }

    fn select_chat_from_title(&mut self, title: &str) {
        let Some(chat) = match_chat_title(&self.chats, title) else {
            debug!("[chat_detector] no chat matched window title {:?}", title);
            self.status = format!("Chat not detected from window: {}", title);
            return;
        };

        if self.selected_chat.as_ref() != Some(&chat) {
            info!(
                "[chat_detector] switching to chat from active window: {}",
                chat.name
            );
            self.selected_chat = Some(chat);
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

    fn apply_updated_sticker(&mut self, updated: Sticker) -> Result<bool, String> {
        let search_query = self.search_query.trim();
        let has_search_query = !search_query.is_empty();
        let matches_search = !has_search_query || sticker_matches_query(&updated, search_query);

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

        if let Err(e) = self.sticker_catalog.save(&self.all_stickers) {
            warn!("[edit] failed to update local catalog: {}", e);
            return Err(e.to_string());
        }

        Ok(!matches_search && has_search_query)
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
        let file_id = sticker.file_id.clone();
        let can_send_as_sticker = sticker.can_send_as_sticker();
        let thumbnail_cache = self.thumbnail_cache.clone();
        let chat_name = chat.name.clone();
        let tx = self.send_result_tx.clone();

        if can_send_as_sticker {
            info!(
                "[send] sending sticker {} to chat {}",
                document_id, chat_name
            );
            self.status = "Sending...".into();
        } else {
            info!(
                "[send] sending cached image fallback to chat {}: set_name={:?}, document_id={}, media_type={}",
                chat_name, set_name, document_id, sticker.media_type
            );
            self.status = "Sending as image...".into();
        }

        self.rt.spawn(async move {
            let result: anyhow::Result<SendMode> = if can_send_as_sticker {
                match telegram.send_sticker(chat_id, &set_name, document_id).await {
                    Ok(()) => Ok(SendMode::Sticker),
                    Err(sticker_error) if should_fallback_to_image(&sticker_error) => {
                        warn!(
                            "[send] sticker send rejected, trying image fallback: {}",
                            sticker_error
                        );
                        let cache_path = thumbnail_cache.cache_path(&file_id);
                        if thumbnail_cache.get(&file_id).await.is_none() {
                            Err(anyhow::anyhow!(
                                "sticker send failed ({}); image is not cached yet",
                                sticker_error
                            ))
                        } else {
                            telegram
                                .send_photo_file(chat_id, &cache_path)
                                .await
                                .map(|()| SendMode::Image)
                                .map_err(|image_error| {
                                    anyhow::anyhow!(
                                        "sticker send failed ({}); image fallback failed ({})",
                                        sticker_error,
                                        image_error
                                    )
                                })
                        }
                    }
                    Err(sticker_error) => Err(sticker_error),
                }
            } else {
                let cache_path = thumbnail_cache.cache_path(&file_id);
                if thumbnail_cache.get(&file_id).await.is_none() {
                    Err(anyhow::anyhow!("Image is not cached yet"))
                } else {
                    telegram
                        .send_photo_file(chat_id, &cache_path)
                        .await
                        .map(|()| SendMode::Image)
                }
            };

            match result {
                Ok(mode) => {
                    info!("[send] success: sent to {}", chat_name);
                    if tx.send(SendResult::Success { chat_name, mode }).is_err() {
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
                StickerLoadResult::Synced(stickers) => {
                    info!("[poll] sticker sync complete: {} total", stickers.len());
                    self.all_stickers = stickers;
                    self.is_loading_all = false;
                    self.is_offline = false;
                    self.api_health = HealthState::Online;
                    self.rebuild_pack_stats();
                    if self.search_query.trim().is_empty() {
                        self.search_results = None;
                        self.rebuild_stickers();
                    } else {
                        self.search_results = Some(search_stickers(
                            &self.all_stickers,
                            self.search_query.trim(),
                        ));
                        self.rebuild_stickers();
                    }
                    self.status = self.default_status();
                }
                StickerLoadResult::Offline(error) => {
                    warn!("[poll] API unavailable, using local catalog: {}", error);
                    self.is_loading_all = false;
                    self.is_offline = true;
                    self.api_health = HealthState::Offline("catalog refresh failed".into());
                    self.rebuild_pack_stats();
                    self.rebuild_stickers();
                    self.status = self.default_status();
                }
            }
        }

        while let Some(result) = self.health_checker.try_recv() {
            match result.target {
                HealthTarget::Api => {
                    self.api_health = result.state;
                    self.is_offline = matches!(self.api_health, HealthState::Offline(_));
                }
                HealthTarget::Telegram => {
                    self.telegram_health = result.state;
                }
            }
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
                SendResult::Success { chat_name, mode } => {
                    info!("[poll] send success: {}", chat_name);
                    self.telegram_health = HealthState::Online;
                    self.status = match mode {
                        SendMode::Sticker => format!("Sent to {}", chat_name),
                        SendMode::Image => format!("Sent as image to {}", chat_name),
                    };
                }
                SendResult::Error(e) => {
                    warn!("[poll] send error: {}", e);
                    self.telegram_health = HealthState::Offline("send failed, check proxy".into());
                    self.status = friendly_send_error(&e);
                }
            }
        }

        while let Ok(result) = self.edit_result_rx.try_recv() {
            match result {
                EditResult::Success(updated) => match self.apply_updated_sticker(updated) {
                    Ok(true) => {
                        self.status = "Text saved; sticker left current search results".into();
                    }
                    Ok(false) => self.status = "Text saved".into(),
                    Err(e) => {
                        self.status = format!("Text saved, local cache error: {}", e);
                    }
                },
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
        self.sync_window_size(ctx);

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
                HotkeyEvent::Toggle {
                    active_window_title,
                } => {
                    self.visible = !self.visible;
                    info!("[hotkey] toggle, visible={}", self.visible);
                    if self.visible {
                        match active_window_title {
                            Some(title) => self.select_chat_from_title(&title),
                            None => self.status = "Chat detection unavailable".into(),
                        }
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

        let mut apply_pack_filter: Option<Option<String>> = None;
        let mut search_changed = false;
        let mut search_has_focus = false;
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

                search_has_focus = search_resp.has_focus;

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
                    ui.separator();
                    let (tg_label, tg_color) = self.telegram_indicator();
                    ui.colored_label(tg_color, tg_label);
                    let (api_label, api_color) = self.api_indicator();
                    ui.colored_label(api_color, api_label);
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
                        if ui
                            .add_enabled(self.selected_chat.is_some(), egui::Button::new("Send"))
                            .clicked()
                        {
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
                        "Emoji: {}",
                        if sticker.emoji.is_empty() {
                            "-"
                        } else {
                            &sticker.emoji
                        }
                    ));

                    ui.add_space(10.0);
                    let editor_dirty = self.editor_text != sticker.text;
                    ui.horizontal(|ui| {
                        ui.label(RichText::new("Text").strong());
                        if editor_dirty {
                            ui.colored_label(STATUS_WARN, "Unsaved");
                        }
                        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                            if ui
                                .add_enabled(
                                    editor_dirty && !self.is_saving_text,
                                    egui::Button::new("Save"),
                                )
                                .on_disabled_hover_text("No text changes")
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
                    });
                    let edit_response = ui.add_sized(
                        [
                            ui.available_width(),
                            ui.available_height().clamp(96.0, 150.0),
                        ],
                        egui::TextEdit::multiline(&mut self.editor_text)
                            .desired_rows(7)
                            .hint_text("Editable sticker text"),
                    );
                    editor_has_focus = edit_response.has_focus();
                    ui.colored_label(STATUS_TEXT, "Ctrl+S saves text");
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
            self.grid_state
                .update_cols(ui.available_width(), self.thumb_size);

            let navigated_with_keyboard = handle_grid_navigation(
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
                navigated_with_keyboard,
            );

            for file_id in grid_resp.needs_thumbnail {
                self.request_thumbnail(&file_id);
            }

            for file_id in grid_resp.prefetch_thumbnails {
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

        let copy_key = ctx.input(|i| i.key_pressed(egui::Key::C));
        let enter = ctx.input(|i| i.key_pressed(egui::Key::Enter));
        let ctrl_s = ctx.input(|i| i.key_pressed(egui::Key::S) && i.modifiers.ctrl);
        let text_input_focused = editor_has_focus || search_has_focus;

        if ctrl_s && editor_has_focus {
            self.save_current_text();
        } else if copy_key && !self.stickers.is_empty() && !text_input_focused {
            self.copy_sticker_to_clipboard();
        } else if enter && !self.stickers.is_empty() && !self.just_sent && !text_input_focused {
            self.send_sticker();
            self.just_sent = true;
        }

        ctx.request_repaint_after(Duration::from_millis(FRAME_TIME_MS));
    }
}

#[cfg(test)]
mod tests {
    use super::{friendly_send_error, should_fallback_to_image};

    #[test]
    fn falls_back_to_image_only_for_sticker_rejections() {
        assert!(should_fallback_to_image(&anyhow::anyhow!(
            "rpc error 400: STICKERSET_INVALID caused by messages.getStickerSet"
        )));
        assert!(should_fallback_to_image(&anyhow::anyhow!(
            "The item cannot be sent as a sticker"
        )));
        assert!(!should_fallback_to_image(&anyhow::anyhow!(
            "request error: read error, IO failed: read 0 bytes"
        )));
    }

    #[test]
    fn telegram_network_errors_are_human_readable() {
        assert_eq!(
            friendly_send_error("request error: read error, IO failed: read 0 bytes"),
            "Telegram send failed: connection dropped, check proxy"
        );
    }
}
