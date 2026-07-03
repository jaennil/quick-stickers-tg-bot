use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
pub struct Sticker {
    pub sticker_id: String,
    pub file_id: String,
    pub document_id: i64,
    pub set_name: String,
    pub media_type: String,
    pub text: String,
    pub emoji: String,
    pub ocr_engine: String,
    pub manual_edit: bool,
}

pub fn search_stickers(stickers: &[Sticker], query: &str) -> Vec<Sticker> {
    let query = query.trim().to_lowercase();
    if query.is_empty() {
        return stickers.to_vec();
    }

    stickers
        .iter()
        .filter(|sticker| sticker.text.to_lowercase().contains(&query))
        .cloned()
        .collect()
}

impl Sticker {
    pub fn pack_label(&self) -> &str {
        if self.set_name.is_empty() {
            "No pack"
        } else {
            &self.set_name
        }
    }

    pub fn can_send_as_sticker(&self) -> bool {
        self.media_type == "sticker" && self.document_id != 0 && !self.set_name.is_empty()
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct ChatInfo {
    pub id: i64,
    pub name: String,
    pub chat_type: ChatType,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum ChatType {
    Private,
    Group,
    Channel,
}

impl ChatType {
    pub fn icon(&self) -> &'static str {
        match self {
            ChatType::Private => "U",
            ChatType::Group => "G",
            ChatType::Channel => "C",
        }
    }
}

impl std::fmt::Display for ChatInfo {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "[{}] {}", self.chat_type.icon(), self.name)
    }
}

#[cfg(test)]
mod tests {
    use super::{search_stickers, Sticker};

    fn sticker(id: &str, text: &str) -> Sticker {
        Sticker {
            sticker_id: id.into(),
            file_id: format!("file-{id}"),
            document_id: 1,
            set_name: "pack".into(),
            media_type: "sticker".into(),
            text: text.into(),
            emoji: String::new(),
            ocr_engine: String::new(),
            manual_edit: false,
        }
    }

    #[test]
    fn local_search_matches_text_case_insensitively() {
        let stickers = vec![
            sticker("1", "Hello World"),
            sticker("2", "Привет, Мир"),
            sticker("3", "unrelated"),
        ];

        assert_eq!(search_stickers(&stickers, "WORLD")[0].sticker_id, "1");
        assert_eq!(search_stickers(&stickers, "  мир ")[0].sticker_id, "2");
        assert!(search_stickers(&stickers, "missing").is_empty());
    }
}
