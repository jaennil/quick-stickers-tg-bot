use eframe::egui;

use super::theme::{MAX_THUMB_SIZE, MIN_THUMB_SIZE};

pub struct SearchResponse {
    pub changed: bool,
    pub id: egui::Id,
}

pub fn render_search_bar(ui: &mut egui::Ui, query: &mut String) -> SearchResponse {
    let desired_width = ui.available_width().clamp(240.0, 420.0);
    let response = ui.add(
        egui::TextEdit::singleline(query)
            .hint_text("Search sticker text...")
            .desired_width(desired_width),
    );

    SearchResponse {
        changed: response.changed(),
        id: response.id,
    }
}

pub fn render_size_slider(ui: &mut egui::Ui, thumb_size: &mut f32) {
    ui.horizontal(|ui| {
        ui.label("Size:");
        let slider = ui
            .add(egui::Slider::new(thumb_size, MIN_THUMB_SIZE..=MAX_THUMB_SIZE).show_value(false));
        // Prevent keyboard focus on slider
        if slider.has_focus() {
            slider.surrender_focus();
        }
    });
}

pub fn handle_focus(
    ui: &egui::Ui,
    ctx: &egui::Context,
    search_id: egui::Id,
    focus_search: &mut bool,
    grid_focused: &mut bool,
    has_stickers: bool,
) {
    // Tab to switch focus between search and grid
    if ui.input(|i| i.key_pressed(egui::Key::Tab)) {
        if !*grid_focused && has_stickers {
            *grid_focused = true;
            ctx.memory_mut(|m| m.surrender_focus(search_id));
        } else {
            *grid_focused = false;
            *focus_search = true;
        }
    }
}
