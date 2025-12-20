use eframe::egui;

// Window colors (with transparency for overlay effect)
pub const PANEL_BG: egui::Color32 = egui::Color32::from_rgba_premultiplied(25, 25, 25, 180);
pub const WIDGET_BG: egui::Color32 = egui::Color32::from_rgba_premultiplied(30, 30, 30, 180);
pub const EXTREME_BG: egui::Color32 = egui::Color32::from_rgba_premultiplied(20, 20, 20, 180);

// Grid cell colors
pub const CELL_SELECTED: egui::Color32 = egui::Color32::from_rgb(9, 71, 113);
pub const CELL_HOVERED: egui::Color32 = egui::Color32::from_rgb(42, 45, 46);
pub const CELL_DEFAULT: egui::Color32 = egui::Color32::from_rgb(37, 37, 38);

// Text colors
pub const STATUS_TEXT: egui::Color32 = egui::Color32::from_rgb(136, 136, 136);

// Layout constants
pub const GRID_SPACING: f32 = 8.0;
pub const CELL_PADDING: f32 = 4.0;
pub const CELL_ROUNDING: f32 = 6.0;

// Default values
pub const DEFAULT_THUMB_SIZE: f32 = 100.0;
pub const MIN_THUMB_SIZE: f32 = 50.0;
pub const MAX_THUMB_SIZE: f32 = 200.0;

// Timing
pub const SEARCH_DEBOUNCE_MS: u64 = 200;
pub const CHAT_DETECT_INTERVAL_MS: u64 = 500;
pub const FRAME_TIME_MS: u64 = 16;

// Loading
pub const STICKER_BATCH_SIZE: usize = 50;

pub fn apply_dark_theme(ctx: &egui::Context) {
    let mut visuals = egui::Visuals::dark();
    visuals.widgets.noninteractive.bg_fill = WIDGET_BG;
    visuals.panel_fill = PANEL_BG;
    visuals.window_fill = PANEL_BG;
    visuals.extreme_bg_color = EXTREME_BG;
    ctx.set_visuals(visuals);
}
