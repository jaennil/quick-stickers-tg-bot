use eframe::egui;

use super::theme::{MAX_THUMB_SIZE, MIN_THUMB_SIZE};

pub struct SearchResponse {
    pub changed: bool,
    pub id: egui::Id,
    pub has_focus: bool,
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
        has_focus: response.has_focus(),
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

/// Takes Tab away from egui. egui decides where Tab sends focus before our
/// code runs and hands it to the next focusable widget - the sort combo box -
/// so toggling between search and grid only works once that is cancelled.
/// Must run before any widget of the frame is added.
pub fn take_tab(ctx: &egui::Context) -> bool {
    let pressed = ctx.input_mut(|i| {
        let before = i.events.len();
        i.events.retain(|event| {
            !matches!(
                event,
                egui::Event::Key {
                    key: egui::Key::Tab,
                    pressed: true,
                    ..
                }
            )
        });
        before != i.events.len()
    });
    if pressed {
        ctx.memory_mut(|m| m.move_focus(egui::FocusDirection::None));
    }
    pressed
}

/// Leaves no widget with keyboard focus, so the grid gets every key.
pub fn release_focus(ctx: &egui::Context) {
    ctx.memory_mut(|m| {
        if let Some(id) = m.focused() {
            m.surrender_focus(id);
        }
    });
}

/// Tab switches between the search field and the grid.
pub fn handle_focus(
    ctx: &egui::Context,
    tab_pressed: bool,
    focus_search: &mut bool,
    grid_focused: &mut bool,
    has_stickers: bool,
) {
    if !tab_pressed {
        return;
    }
    if !*grid_focused && has_stickers {
        *grid_focused = true;
        release_focus(ctx);
    } else {
        *grid_focused = false;
        *focus_search = true;
    }
}

#[cfg(test)]
mod tests {
    use super::{handle_focus, take_tab};
    use eframe::egui;

    fn tab() -> egui::Event {
        egui::Event::Key {
            key: egui::Key::Tab,
            physical_key: None,
            pressed: true,
            repeat: false,
            modifiers: egui::Modifiers::NONE,
        }
    }

    /// Runs one frame of a toolbar shaped like the app's: a focused text
    /// field followed by a button standing in for the "Recent" combo box.
    fn frame(ctx: &egui::Context, events: Vec<egui::Event>, intercept: bool) -> bool {
        let mut tab_pressed = false;
        let mut text = String::new();
        let _ = ctx.run(
            egui::RawInput {
                events,
                ..Default::default()
            },
            |ctx| {
                if intercept {
                    tab_pressed = take_tab(ctx);
                }
                egui::CentralPanel::default().show(ctx, |ui| {
                    ui.add(egui::TextEdit::singleline(&mut text).id(egui::Id::new("search")));
                    let _ = ui.button("Recent");
                });
            },
        );
        tab_pressed
    }

    fn focused_search(intercept: bool) -> egui::Context {
        let ctx = egui::Context::default();
        frame(&ctx, vec![], intercept);
        ctx.memory_mut(|m| m.request_focus(egui::Id::new("search")));
        frame(&ctx, vec![], intercept);
        assert_eq!(ctx.memory(|m| m.focused()), Some(egui::Id::new("search")));
        ctx
    }

    #[test]
    fn without_interception_tab_hands_focus_to_the_next_widget() {
        // Reproduces the bug: Tab from search landed on "Recent".
        let ctx = focused_search(false);
        frame(&ctx, vec![tab()], false);
        let focused = ctx.memory(|m| m.focused());
        assert!(focused.is_some() && focused != Some(egui::Id::new("search")));
    }

    #[test]
    fn tab_switches_to_the_grid_and_leaves_no_widget_focused() {
        let ctx = focused_search(true);
        let pressed = frame(&ctx, vec![tab()], true);
        assert!(pressed);

        let (mut focus_search, mut grid_focused) = (false, false);
        handle_focus(&ctx, pressed, &mut focus_search, &mut grid_focused, true);
        assert!(grid_focused);
        assert_eq!(
            ctx.memory(|m| m.focused()),
            None,
            "a focused widget would swallow keys or drag focus off the grid"
        );

        frame(&ctx, vec![], true);
        assert_eq!(
            ctx.memory(|m| m.focused()),
            None,
            "and nothing grabs it next frame"
        );
    }

    #[test]
    fn tab_from_the_grid_goes_back_to_search() {
        let ctx = egui::Context::default();
        let (mut focus_search, mut grid_focused) = (false, true);
        handle_focus(&ctx, true, &mut focus_search, &mut grid_focused, true);
        assert!(!grid_focused);
        assert!(focus_search);
    }
}
