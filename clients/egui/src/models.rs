use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
pub struct Sticker {
    pub sticker_id: String,
    pub file_id: String,
    pub document_id: i64,
    #[serde(default)]
    pub is_animated: bool,
    #[serde(default)]
    pub is_video: bool,
    pub set_name: String,
    pub media_type: String,
    pub text: String,
    pub emoji: String,
    pub ocr_engine: String,
    pub manual_edit: bool,
    /// How the server found this result: "text", "ai" or "both". Empty when
    /// simply browsing, which is why the badge is hidden in that case.
    #[serde(default)]
    pub match_type: String,
}

pub fn search_stickers(stickers: &[Sticker], query: &str) -> Vec<Sticker> {
    if query.trim().is_empty() {
        return stickers.to_vec();
    }

    stickers
        .iter()
        .filter(|sticker| sticker_matches_query(sticker, query))
        .cloned()
        .map(|mut sticker| {
            // Local search only ever matches text, so label it as such.
            sticker.match_type = MATCH_TEXT.to_string();
            sticker
        })
        .collect()
}

/// Narrows the grid to media that moves or to media that does not.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum MotionFilter {
    #[default]
    All,
    Animated,
    Static,
}

impl MotionFilter {
    pub const ALL: [MotionFilter; 3] = [Self::All, Self::Animated, Self::Static];

    pub fn label(self) -> &'static str {
        match self {
            Self::All => "All",
            Self::Animated => "Animated",
            Self::Static => "Static",
        }
    }

    pub fn matches(self, sticker: &Sticker) -> bool {
        // Lottie stickers count as animated even though the grid shows them
        // still: what the filter answers is what the media is, not what plays.
        let moves = sticker.is_animated_media() || sticker.is_animated;
        match self {
            Self::All => true,
            Self::Animated => moves,
            Self::Static => !moves,
        }
    }
}

pub const MATCH_TEXT: &str = "text";
pub const MATCH_AI: &str = "ai";
pub const MATCH_BOTH: &str = "both";

pub fn sticker_matches_query(sticker: &Sticker, query: &str) -> bool {
    let text = normalize_search_text(&sticker.text);
    normalize_search_text(query)
        .split_whitespace()
        .all(|term| text.contains(term))
}

fn normalize_search_text(value: &str) -> String {
    value
        .chars()
        .flat_map(char::to_lowercase)
        .map(|character| {
            if character.is_alphanumeric() {
                character
            } else {
                ' '
            }
        })
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

    pub fn is_video_media(&self) -> bool {
        self.media_type == "video"
            || self.media_type == "video_file"
            || (self.media_type == "sticker" && self.is_video)
    }

    /// Media that moves and therefore has an animated preview to play.
    pub fn is_animated_media(&self) -> bool {
        self.is_video_media() || self.is_gif_media()
    }

    pub fn is_gif_media(&self) -> bool {
        self.media_type == "gif"
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
    use super::{search_stickers, MotionFilter, Sticker, MATCH_TEXT};

    fn sticker(id: &str, text: &str) -> Sticker {
        Sticker {
            sticker_id: id.into(),
            file_id: format!("file-{id}"),
            document_id: 1,
            is_animated: false,
            is_video: false,
            set_name: "pack".into(),
            media_type: "sticker".into(),
            text: text.into(),
            emoji: String::new(),
            ocr_engine: String::new(),
            manual_edit: false,
            match_type: String::new(),
        }
    }

    #[test]
    fn gif_is_not_classified_as_video_media() {
        let mut gif = sticker("gif", "text");
        gif.media_type = "gif".into();
        gif.is_animated = true;
        gif.is_video = true;

        assert!(gif.is_gif_media());
        assert!(!gif.is_video_media());
    }

    #[test]
    fn video_document_is_classified_as_video_media() {
        let mut video = sticker("video-file", "text");
        video.media_type = "video_file".into();

        assert!(video.is_video_media());
    }

    #[test]
    fn motion_filter_splits_moving_from_still_media() {
        let with = |media_type: &str, is_video: bool, is_animated: bool| {
            let mut s = sticker(media_type, "text");
            s.media_type = media_type.into();
            s.is_video = is_video;
            s.is_animated = is_animated;
            s
        };
        let moving = [
            with("sticker", true, false), // video sticker
            with("sticker", false, true), // lottie
            with("gif", true, false),
            with("video", false, false),
            with("video_file", false, false),
        ];
        let still = [with("sticker", false, false), with("photo", false, false)];

        for s in &moving {
            assert!(
                MotionFilter::Animated.matches(s),
                "{} should be animated",
                s.media_type
            );
            assert!(!MotionFilter::Static.matches(s));
        }
        for s in &still {
            assert!(
                MotionFilter::Static.matches(s),
                "{} should be static",
                s.media_type
            );
            assert!(!MotionFilter::Animated.matches(s));
        }
        assert!(moving
            .iter()
            .chain(&still)
            .all(|s| MotionFilter::All.matches(s)));
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

    #[test]
    fn local_search_matches_words_across_ocr_separators() {
        let stickers = vec![
            sticker("1", "МНЕ ТА ХОЧЕТСЯ, БЛЯ! МНЕ ЧЕТАХОЧЕТСЯ"),
            sticker("2", "мне хочется"),
        ];

        let matches = search_stickers(&stickers, "бля мне");

        assert_eq!(matches.len(), 1);
        assert_eq!(matches[0].sticker_id, "1");
    }

    #[test]
    fn local_search_labels_results_as_text_matches() {
        let stickers = vec![sticker("a", "кот грустит"), sticker("b", "весёлый самолёт")];
        let found = search_stickers(&stickers, "кот");
        assert_eq!(found.len(), 1);
        assert_eq!(found[0].match_type, MATCH_TEXT);
    }

    #[test]
    fn browsing_leaves_results_unlabelled() {
        let stickers = vec![sticker("a", "кот грустит")];
        let all = search_stickers(&stickers, "   ");
        assert!(all[0].match_type.is_empty());
    }
}
