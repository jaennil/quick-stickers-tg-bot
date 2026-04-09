use std::collections::HashMap;

use eframe::egui::{self, TextureHandle};

use super::theme::{
    CELL_DEFAULT, CELL_HOVERED, CELL_PADDING, CELL_ROUNDING, CELL_SELECTED, GRID_SPACING,
};

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
    pub ctrl_clicked: Option<usize>,
    pub needs_thumbnail: Vec<String>,
    pub visible_file_ids: Vec<String>,
}

pub fn render_grid(
    ui: &mut egui::Ui,
    file_ids: &[(usize, String)],
    textures: &HashMap<String, TextureHandle>,
    selected: usize,
    thumb_size: f32,
    cols: usize,
) -> GridResponse {
    let mut clicked = None;
    let mut ctrl_clicked = None;
    let mut needs_thumbnail = Vec::new();
    let mut visible_file_ids = Vec::new();
    let cols = cols.max(1);
    let total_rows = file_ids.len().div_ceil(cols);
    let row_height = thumb_size + GRID_SPACING;

    egui::ScrollArea::vertical()
        .auto_shrink([false, false])
        .show_rows(ui, row_height, total_rows, |ui, row_range| {
            for row in row_range {
                ui.horizontal(|ui| {
                    ui.spacing_mut().item_spacing.x = GRID_SPACING;

                    for col in 0..cols {
                        let item_index = row * cols + col;
                        if item_index >= file_ids.len() {
                            break;
                        }

                        let (idx, file_id) = &file_ids[item_index];
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

                        ui.painter().rect_filled(rect, CELL_ROUNDING, bg);

                        if let Some(tex) = textures.get(file_id) {
                            render_texture(ui, tex, rect, thumb_size);
                            visible_file_ids.push(file_id.clone());
                        } else {
                            render_placeholder(ui, rect);
                            needs_thumbnail.push(file_id.clone());
                        }

                        if resp.clicked() {
                            if ui.input(|i| i.modifiers.ctrl) {
                                ctrl_clicked = Some(*idx);
                            } else {
                                clicked = Some(*idx);
                            }
                        }
                    }
                });
            }
        });

    GridResponse {
        clicked,
        ctrl_clicked,
        needs_thumbnail,
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
) {
    if !grid_focused || count == 0 {
        return;
    }

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
}
