use std::collections::{HashMap, HashSet};

use eframe::egui::{self, TextureHandle};

use super::theme::{
    CELL_DEFAULT, CELL_HOVERED, CELL_PADDING, CELL_ROUNDING, CELL_SELECTED, GRID_SPACING,
};

const PREFETCH_ROWS: usize = 3;

pub struct GridState {
    pub selected: usize,
    pub cols: usize,
}

impl GridState {
    pub fn new() -> Self {
        Self {
            selected: 0,
            cols: 1,
        }
    }

    pub fn navigate_left(&mut self) {
        if self.selected > 0 {
            self.selected -= 1;
        }
    }

    pub fn navigate_right(&mut self, count: usize) {
        if self.selected < count.saturating_sub(1) {
            self.selected += 1;
        }
    }

    pub fn navigate_up(&mut self) {
        if self.selected >= self.cols {
            self.selected -= self.cols;
        }
    }

    pub fn navigate_down(&mut self, count: usize) {
        if self.selected + self.cols < count {
            self.selected += self.cols;
        }
    }

    pub fn update_cols(&mut self, available_width: f32, thumb_size: f32) {
        self.cols =
            ((available_width + GRID_SPACING) / (thumb_size + GRID_SPACING)).max(1.0) as usize;
    }
}

pub struct GridResponse {
    pub clicked: Option<usize>,
    pub double_clicked: Option<usize>,
    pub ctrl_clicked: Option<usize>,
    pub needs_thumbnail: Vec<String>,
    pub prefetch_thumbnails: Vec<String>,
    pub visible_file_ids: Vec<String>,
}

/// Why a sticker showed up in the results. Drawn as an outline in the cell
/// padding rather than a chip on top, so it never covers the picture or the
/// text baked into it.
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum MatchKind {
    None,
    Text,
    Ai,
    Both,
}

impl MatchKind {
    pub fn from_match_type(value: &str) -> Self {
        match value {
            crate::models::MATCH_TEXT => Self::Text,
            crate::models::MATCH_AI => Self::Ai,
            crate::models::MATCH_BOTH => Self::Both,
            _ => Self::None,
        }
    }

    pub fn legend(self) -> &'static str {
        match self {
            Self::Text => "текст",
            Self::Ai => "ИИ",
            Self::Both => "оба",
            Self::None => "",
        }
    }

    pub fn color(self) -> egui::Color32 {
        match self {
            Self::Text => egui::Color32::from_rgb(94, 190, 99),
            Self::Ai => egui::Color32::from_rgb(160, 130, 220),
            Self::Both => egui::Color32::from_rgb(52, 190, 175),
            Self::None => egui::Color32::TRANSPARENT,
        }
    }
}

/// Sits inside CELL_PADDING, so the thumbnail itself is never touched.
const MATCH_OUTLINE_WIDTH: f32 = 2.0;

fn render_match_outline(ui: &egui::Ui, rect: egui::Rect, kind: MatchKind) {
    if kind == MatchKind::None {
        return;
    }
    ui.painter().rect_stroke(
        rect.shrink(MATCH_OUTLINE_WIDTH / 2.0),
        CELL_ROUNDING,
        egui::Stroke::new(MATCH_OUTLINE_WIDTH, kind.color()),
        egui::StrokeKind::Inside,
    );
}

pub fn render_grid(
    ui: &mut egui::Ui,
    file_ids: &[(usize, String, MatchKind)],
    textures: &HashMap<String, TextureHandle>,
    selected: usize,
    thumb_size: f32,
    cols: usize,
    scroll_to_selected: bool,
) -> GridResponse {
    let mut clicked = None;
    let mut double_clicked = None;
    let mut ctrl_clicked = None;
    let mut needs_thumbnail = Vec::new();
    let mut prefetch_thumbnails = Vec::new();
    let mut visible_file_ids = Vec::new();
    let cols = cols.max(1);
    let total_rows = file_ids.len().div_ceil(cols);
    let row_height = thumb_size + GRID_SPACING;
    let mut rendered_rows = None;

    egui::ScrollArea::vertical()
        .auto_shrink([false, false])
        .show_rows(ui, row_height, total_rows, |ui, row_range| {
            rendered_rows = Some(row_range.clone());

            for row in row_range {
                ui.horizontal(|ui| {
                    ui.spacing_mut().item_spacing.x = GRID_SPACING;

                    for col in 0..cols {
                        let item_index = row * cols + col;
                        if item_index >= file_ids.len() {
                            break;
                        }

                        let (idx, file_id, kind) = &file_ids[item_index];
                        let is_selected = *idx == selected;
                        let (rect, resp) = ui.allocate_exact_size(
                            egui::vec2(thumb_size, thumb_size),
                            egui::Sense::click(),
                        );

                        let bg = if is_selected {
                            CELL_SELECTED
                        } else if resp.hovered() {
                            CELL_HOVERED
                        } else {
                            CELL_DEFAULT
                        };

                        if is_selected && scroll_to_selected {
                            ui.scroll_to_rect(rect.expand(GRID_SPACING), None);
                        }

                        ui.painter().rect_filled(rect, CELL_ROUNDING, bg);

                        if let Some(tex) = textures.get(file_id) {
                            render_texture(ui, tex, rect, thumb_size);
                            visible_file_ids.push(file_id.clone());
                        } else {
                            render_placeholder(ui, rect);
                            needs_thumbnail.push(file_id.clone());
                        }

                        render_match_outline(ui, rect, *kind);

                        if resp.clicked() {
                            if ui.input(|i| i.modifiers.ctrl) {
                                ctrl_clicked = Some(*idx);
                            } else {
                                clicked = Some(*idx);
                            }
                        }

                        if resp.double_clicked() && !ui.input(|i| i.modifiers.ctrl) {
                            double_clicked = Some(*idx);
                        }
                    }
                });
            }
        });

    if let Some(row_range) = rendered_rows {
        let prefetch_start = row_range.start.saturating_sub(PREFETCH_ROWS);
        let prefetch_end = (row_range.end + PREFETCH_ROWS).min(total_rows);
        let mut queued = HashSet::new();

        queued.extend(needs_thumbnail.iter().cloned());

        for row in prefetch_start..prefetch_end {
            for col in 0..cols {
                let item_index = row * cols + col;
                if item_index >= file_ids.len() {
                    break;
                }

                let (_, file_id, _) = &file_ids[item_index];
                if textures.contains_key(file_id) || queued.contains(file_id) {
                    continue;
                }

                queued.insert(file_id.clone());
                prefetch_thumbnails.push(file_id.clone());
            }
        }
    }

    GridResponse {
        clicked,
        double_clicked,
        ctrl_clicked,
        needs_thumbnail,
        prefetch_thumbnails,
        visible_file_ids,
    }
}

fn render_texture(ui: &egui::Ui, tex: &TextureHandle, rect: egui::Rect, thumb_size: f32) {
    let inner_size = thumb_size - CELL_PADDING * 2.0;
    let img_size = tex.size_vec2();
    let scale = (inner_size / img_size.x.max(img_size.y)).min(1.0);
    let scaled = img_size * scale;
    let offset = (egui::vec2(thumb_size, thumb_size) - scaled) / 2.0;

    ui.painter().image(
        tex.id(),
        egui::Rect::from_min_size(rect.min + offset, scaled),
        egui::Rect::from_min_max(egui::pos2(0.0, 0.0), egui::pos2(1.0, 1.0)),
        egui::Color32::WHITE,
    );
}

fn render_placeholder(ui: &egui::Ui, rect: egui::Rect) {
    ui.painter().text(
        rect.center(),
        egui::Align2::CENTER_CENTER,
        "...",
        egui::FontId::proportional(16.0),
        egui::Color32::GRAY,
    );
}

pub fn handle_grid_navigation(
    ui: &egui::Ui,
    grid_state: &mut GridState,
    count: usize,
    grid_focused: bool,
) -> bool {
    if !grid_focused || count == 0 {
        return false;
    }

    let previous = grid_state.selected;

    if ui.input(|i| i.key_pressed(egui::Key::H) || i.key_pressed(egui::Key::ArrowLeft)) {
        grid_state.navigate_left();
    }
    if ui.input(|i| i.key_pressed(egui::Key::L) || i.key_pressed(egui::Key::ArrowRight)) {
        grid_state.navigate_right(count);
    }
    if ui.input(|i| i.key_pressed(egui::Key::K) || i.key_pressed(egui::Key::ArrowUp)) {
        grid_state.navigate_up();
    }
    if ui.input(|i| i.key_pressed(egui::Key::J) || i.key_pressed(egui::Key::ArrowDown)) {
        grid_state.navigate_down(count);
    }

    grid_state.selected != previous
}

#[cfg(test)]
mod tests {
    use super::MatchKind;
    use crate::models::{MATCH_AI, MATCH_BOTH, MATCH_TEXT};

    #[test]
    fn match_kind_maps_every_server_match_type() {
        assert!(MatchKind::from_match_type(MATCH_TEXT) == MatchKind::Text);
        assert!(MatchKind::from_match_type(MATCH_AI) == MatchKind::Ai);
        assert!(MatchKind::from_match_type(MATCH_BOTH) == MatchKind::Both);
        assert!(MatchKind::from_match_type("") == MatchKind::None);
        assert!(MatchKind::from_match_type("whatever") == MatchKind::None);
    }

    #[test]
    fn only_the_none_kind_is_invisible() {
        for kind in [MatchKind::Text, MatchKind::Ai, MatchKind::Both] {
            assert!(!kind.legend().is_empty());
            assert!(kind.color().a() > 0, "outline must be visible");
        }
        assert!(MatchKind::None.legend().is_empty());
        assert_eq!(MatchKind::None.color().a(), 0);
    }
}
