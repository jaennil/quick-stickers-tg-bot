use eframe::egui;

use crate::models::ChatInfo;

pub fn render_chat_selector(
    ui: &mut egui::Ui,
    chats: &[ChatInfo],
    selected_chat: &mut Option<ChatInfo>,
) {
    ui.horizontal(|ui| {
        ui.label("Chat:");

        let selected_text = selected_chat
            .as_ref()
            .map(|c| c.to_string())
            .unwrap_or_else(|| "Select...".into());

        egui::ComboBox::from_id_salt("chat_selector")
            .selected_text(&selected_text)
            .width(300.0)
            .show_ui(ui, |ui| {
                for chat in chats {
                    let is_selected = selected_chat.as_ref() == Some(chat);
                    if ui.selectable_label(is_selected, chat.to_string()).clicked() {
                        *selected_chat = Some(chat.clone());
                    }
                }
            });
    });
}
