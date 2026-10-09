use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use eframe::egui::{self, TextureHandle};

use super::theme::{
    CELL_DEFAULT, CELL_HOVERED, CELL_PADDING, CELL_ROUNDING, CELL_SELECTED, GRID_SPACING,
};

const PREFETCH_ROWS: usize = 3;

/// The second `g` of `gg` must follow the first within this.
const GG_TIMEOUT: Duration = Duration::from_millis(600);

pub struct GridState {
    pub selected: usize,
    pub cols: usize,
    // Scroll position and viewport height from the last frame. The grid is
    // virtualised - only visible rows are drawn - so revealing the selection
    // has to be computed from these, not left to the selected cell's drawing.
    scroll_offset: f32,
    viewport_height: f32,
    /// Selection the view was last brought in line with.
    revealed: Option<usize>,
    pending_g: Option<Instant>,
}

impl GridState {
    pub fn new() -> Self {
        Self {
            selected: 0,
            cols: 1,
            scroll_offset: 0.0,
            viewport_height: 0.0,
            revealed: None,
            pending_g: None,
        }
    }

    /// Selects `index` and brings it into view on the next frame, even when
    /// the index is unchanged but the list under it was replaced.
    pub fn select(&mut self, index: usize) {
        self.selected = index;
        self.revealed = None;
    }

    pub fn select_first(&mut self) {
        self.select(0);
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
        if count == 0 {
            return;
        }
        if self.selected + self.cols < count {
            self.selected += self.cols;
        } else if self.row_of(count - 1) > self.row_of(self.selected) {
            // The last row is shorter and has nothing straight below: land on
            // its last item, the way vim clamps the column on a short line.
            self.selected = count - 1;
        }
    }

    /// Feeds one press of `g` (`G` with shift). Both move vertically only:
    /// `gg` to the top row and `G` to the bottom row, keeping the column.
    pub fn press_g(&mut self, shift: bool, count: usize, now: Instant) {
        if count == 0 {
            self.pending_g = None;
            return;
        }
        let cols = self.cols.max(1);
        let col = self.selected % cols;
        if shift {
            self.pending_g = None;
            // The bottom row may be short; then stop at its last item, the
            // way `j` does.
            self.selected = (self.row_of(count - 1) * cols + col).min(count - 1);
            return;
        }
        match self.pending_g.take() {
            Some(first) if now.duration_since(first) <= GG_TIMEOUT => self.selected = col,
            _ => self.pending_g = Some(now),
        }
    }

    /// Any other key breaks a half-typed `gg`.
    pub fn cancel_pending(&mut self) {
        self.pending_g = None;
    }

    fn row_of(&self, index: usize) -> usize {
        index / self.cols.max(1)
    }

    pub fn update_cols(&mut self, available_width: f32, thumb_size: f32) {
        self.cols =
            ((available_width + GRID_SPACING) / (thumb_size + GRID_SPACING)).max(1.0) as usize;
    }
}

/// Scroll offset that brings `row` fully into view, or `None` if it already is.
fn offset_to_reveal(
    row: usize,
    row_height: f32,
    stride: f32,
    offset: f32,
    viewport: f32,
) -> Option<f32> {
    let top = row as f32 * stride;
    let bottom = top + row_height;
    if top < offset - 0.5 || viewport < row_height {
        return Some(top);
    }
    if bottom > offset + viewport + 0.5 {
        return Some(bottom - viewport);
    }
    None
}

/// After a manual scroll: the selection moved onto the nearest fully visible
/// row, keeping its column, or `None` when it is still on screen.
fn follow_scroll(
    selected: usize,
    cols: usize,
    count: usize,
    row_height: f32,
    stride: f32,
    offset: f32,
    viewport: f32,
) -> Option<usize> {
    if count == 0 || viewport < row_height {
        return None;
    }
    let cols = cols.max(1);
    let (row, col) = (selected / cols, selected % cols);
    let last_row = (count - 1) / cols;
    // The half-pixel slack keeps rows that sit exactly on an edge counted in.
    let first = ((offset - 0.5) / stride).ceil().max(0.0) as usize;
    let last = (((offset + viewport + 0.5 - row_height) / stride)
        .floor()
        .max(0.0) as usize)
        .min(last_row);
    if first > last {
        return None;
    }
    let target = if row < first {
        first
    } else if row > last {
        last
    } else {
        return None;
    };
    Some((target * cols + col).min(count - 1))
}

pub struct GridResponse {
    pub clicked: Option<usize>,
    pub double_clicked: Option<usize>,
    pub ctrl_clicked: Option<usize>,
    pub needs_thumbnail: Vec<String>,
    pub prefetch_thumbnails: Vec<String>,
    pub visible_file_ids: Vec<String>,
    /// Where the selection would move to keep up with a manual scroll. Left
    /// to the caller, who knows whether moving it would lose unsaved edits.
    pub follow_selection: Option<usize>,
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

/// Everything the grid can draw a cell with.
pub struct GridTextures<'a> {
    pub thumbnails: &'a HashMap<String, TextureHandle>,
    /// Current frame of each playing animation. Preferred over the thumbnail,
    /// which stays the fallback until the animation has loaded.
    pub animated: &'a HashMap<String, TextureHandle>,
}

impl GridTextures<'_> {
    fn get(&self, file_id: &str) -> Option<&TextureHandle> {
        self.animated
            .get(file_id)
            .or_else(|| self.thumbnails.get(file_id))
    }
}

pub fn render_grid(
    ui: &mut egui::Ui,
    file_ids: &[(usize, String, MatchKind)],
    textures: GridTextures<'_>,
    state: &mut GridState,
    thumb_size: f32,
) -> GridResponse {
    let selected = state.selected;
    let cols = state.cols;
    let mut clicked = None;
    let mut double_clicked = None;
    let mut ctrl_clicked = None;
    let mut needs_thumbnail = Vec::new();
    let mut prefetch_thumbnails = Vec::new();
    let mut visible_file_ids = Vec::new();
    let cols = cols.max(1);
    let total_rows = file_ids.len().div_ceil(cols);
    // Rows are laid out with GRID_SPACING between them, and the scroll area
    // is told exactly that. Passing the spacing inside the row height while
    // egui added its own on top made every modelled row taller than the real
    // one, so the drawn block drifted against the scroll position.
    let stride = thumb_size + GRID_SPACING;
    let mut rendered_rows = None;

    let mut area = egui::ScrollArea::vertical().auto_shrink([false, false]);
    let mut scrolled_by_us = false;
    if state.revealed != Some(selected) && state.viewport_height > 0.0 {
        if let Some(offset) = offset_to_reveal(
            selected / cols.max(1),
            thumb_size,
            stride,
            state.scroll_offset,
            state.viewport_height,
        ) {
            area = area.vertical_scroll_offset(offset);
            scrolled_by_us = true;
        }
        state.revealed = Some(selected);
    }

    let output = ui
        .scope(|ui| {
            ui.spacing_mut().item_spacing.y = GRID_SPACING;
            area.show_rows(ui, thumb_size, total_rows, |ui, row_range| {
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
            })
        })
        .inner;

    let offset = output.state.offset.y;
    let viewport = output.inner_rect.height();
    let scrolled_by_user = !scrolled_by_us && (offset - state.scroll_offset).abs() > 0.5;
    state.scroll_offset = offset;
    state.viewport_height = viewport;
    let follow_selection = if scrolled_by_user {
        follow_scroll(
            selected,
            cols,
            file_ids.len(),
            thumb_size,
            stride,
            offset,
            viewport,
        )
    } else {
        None
    };

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
                if textures.thumbnails.contains_key(file_id) || queued.contains(file_id) {
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
        follow_selection,
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
    // A focused text field owns the keyboard. Without this, typing "j" or "G"
    // into the caption editor moved the grid and replaced the editor's text
    // with another sticker's, since clicking the editor leaves the grid flag set.
    if !grid_focused || count == 0 || ui.ctx().wants_keyboard_input() {
        grid_state.cancel_pending();
        return;
    }
    let now = Instant::now();
    let events = ui.input(|i| i.events.clone());
    for event in events {
        let egui::Event::Key {
            key,
            pressed: true,
            repeat,
            modifiers,
            ..
        } = event
        else {
            continue;
        };
        match key {
            // Holding g must not turn into gg.
            egui::Key::G if !repeat => grid_state.press_g(modifiers.shift, count, now),
            egui::Key::G => {}
            egui::Key::H | egui::Key::ArrowLeft => {
                grid_state.cancel_pending();
                grid_state.navigate_left();
            }
            egui::Key::L | egui::Key::ArrowRight => {
                grid_state.cancel_pending();
                grid_state.navigate_right(count);
            }
            egui::Key::K | egui::Key::ArrowUp => {
                grid_state.cancel_pending();
                grid_state.navigate_up();
            }
            egui::Key::J | egui::Key::ArrowDown => {
                grid_state.cancel_pending();
                grid_state.navigate_down(count);
            }
            _ => grid_state.cancel_pending(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{follow_scroll, offset_to_reveal, GridState, MatchKind};
    use crate::models::{MATCH_AI, MATCH_BOTH, MATCH_TEXT};
    use std::time::{Duration, Instant};

    fn grid(cols: usize, selected: usize) -> GridState {
        let mut state = GridState::new();
        state.cols = cols;
        state.selected = selected;
        state
    }

    #[test]
    fn down_onto_a_short_last_row_lands_on_its_last_item() {
        // 3 columns, 7 items: the last row holds only index 6.
        let mut state = grid(3, 4);
        state.navigate_down(7);
        assert_eq!(state.selected, 6, "nothing straight below index 4");
        state.navigate_down(7);
        assert_eq!(state.selected, 6, "already on the last row");
        let mut state = grid(3, 1);
        state.navigate_down(7);
        assert_eq!(state.selected, 4, "a full row below moves straight down");
    }

    #[test]
    fn gg_and_capital_g_move_vertically_keeping_the_column() {
        let now = Instant::now();
        // 4 columns, 30 items: rows 0..=7, the last row holds 28 and 29.
        let mut state = grid(4, 13); // row 3, column 1
        state.press_g(false, 30, now);
        assert_eq!(state.selected, 13, "a single g does nothing yet");
        state.press_g(false, 30, now + Duration::from_millis(200));
        assert_eq!(state.selected, 1, "gg: top row, same column");

        state.press_g(true, 30, now);
        assert_eq!(state.selected, 29, "G: bottom row, same column");
    }

    #[test]
    fn capital_g_stops_at_the_end_of_a_short_bottom_row() {
        // Column 2 does not exist in the last row (28, 29).
        let mut state = grid(4, 10);
        state.press_g(true, 30, Instant::now());
        assert_eq!(state.selected, 29);
    }

    #[test]
    fn gg_needs_two_presses_close_together_and_uninterrupted() {
        let now = Instant::now();
        let mut slow = grid(4, 9);
        slow.press_g(false, 30, now);
        slow.press_g(false, 30, now + Duration::from_secs(2));
        assert_eq!(slow.selected, 9, "too far apart is not gg");

        let mut broken = grid(4, 9);
        broken.press_g(false, 30, now);
        broken.cancel_pending();
        broken.press_g(false, 30, now + Duration::from_millis(100));
        assert_eq!(broken.selected, 9, "another key in between breaks gg");

        let mut empty = grid(4, 0);
        empty.press_g(true, 0, now);
        assert_eq!(empty.selected, 0, "G on an empty list must not underflow");
    }

    #[test]
    fn reveals_rows_that_are_off_screen_in_either_direction() {
        // 100px rows, 108px stride, 500px viewport scrolled to 1000px.
        let (h, stride, viewport) = (100.0, 108.0, 500.0);
        assert_eq!(offset_to_reveal(12, h, stride, 1000.0, viewport), None);
        assert_eq!(
            offset_to_reveal(5, h, stride, 1000.0, viewport),
            Some(540.0),
            "above: align top"
        );
        assert_eq!(
            offset_to_reveal(20, h, stride, 1000.0, viewport),
            Some(20.0 * stride + h - viewport),
            "below: align bottom"
        );
        assert_eq!(offset_to_reveal(0, h, stride, 0.0, viewport), None);
    }

    #[test]
    fn the_selection_follows_a_manual_scroll_keeping_its_column() {
        let (h, stride, viewport) = (100.0, 108.0, 500.0);
        // 5 columns, 100 items, selection in row 1 column 3, scrolled to row 10.
        let to = follow_scroll(8, 5, 100, h, stride, 10.0 * stride, viewport);
        assert_eq!(to, Some(10 * 5 + 3), "first fully visible row, same column");

        // Scrolled back up past a selection far below.
        let to = follow_scroll(90, 5, 100, h, stride, 0.0, viewport);
        let last_visible = ((viewport - h) / stride).floor() as usize;
        assert_eq!(to, Some(last_visible * 5));

        assert_eq!(
            follow_scroll(52, 5, 100, h, stride, 10.0 * stride, viewport),
            None,
            "still on screen"
        );
    }

    #[test]
    fn following_a_scroll_never_points_past_the_last_item() {
        let (h, stride) = (100.0, 108.0);
        // 3 columns, 7 items; selection in column 2 of row 0, scrolled so only
        // the short last row is visible: column 2 does not exist there.
        let to = follow_scroll(2, 3, 7, h, stride, 2.0 * stride, 120.0);
        assert_eq!(to, Some(6));
    }

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
