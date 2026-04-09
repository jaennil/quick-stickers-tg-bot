#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Sticker {
    pub sticker_id: String,
    pub file_id: String,
    pub document_id: i64,
    pub set_name: String,
    pub text: String,
    pub emoji: String,
    pub ocr_engine: String,
    pub manual_edit: bool,
}

impl Sticker {
    pub fn pack_label(&self) -> &str {
        if self.set_name.is_empty() {
            "No pack"
        } else {
            &self.set_name
        }
    }

    pub fn source_label(&self) -> &str {
        if self.manual_edit {
            "manual"
        } else if self.ocr_engine.is_empty() {
            "unknown"
        } else {
            &self.ocr_engine
        }
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
