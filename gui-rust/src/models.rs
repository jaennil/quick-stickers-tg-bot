#[derive(Debug, Clone)]
pub struct Sticker {
    pub sticker_id: String,
    pub file_id: String,
    pub document_id: i64,
    pub text: String,
    pub set_name: String,
    pub emoji: String,
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
    Supergroup,
    Channel,
}

impl ChatType {
    pub fn icon(&self) -> &'static str {
        match self {
            ChatType::Private => "U",
            ChatType::Group | ChatType::Supergroup => "G",
            ChatType::Channel => "C",
        }
    }
}

impl std::fmt::Display for ChatInfo {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "[{}] {}", self.chat_type.icon(), self.name)
    }
}
