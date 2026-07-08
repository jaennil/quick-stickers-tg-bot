use std::process::Command;
use std::sync::mpsc::{self, Receiver};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use tracing::{debug, info, warn};

use crate::models::ChatInfo;
use crate::ui::theme::CHAT_DETECT_INTERVAL_MS;

const MIN_TELEGRAM_WINDOW_WIDTH: u32 = 320;
const MIN_TELEGRAM_WINDOW_HEIGHT: u32 = 240;

pub struct ChatDetector {
    result_rx: Receiver<String>,
    _handle: JoinHandle<()>,
}

impl ChatDetector {
    pub fn start(chats: Vec<ChatInfo>) -> Self {
        let (result_tx, result_rx) = mpsc::channel::<String>();

        let handle = thread::spawn(move || {
            info!("[chat_detector] thread started");

            loop {
                thread::sleep(Duration::from_millis(CHAT_DETECT_INTERVAL_MS));

                if let Some(title) = detect_telegram_chat_title() {
                    // Only send if the detected title matches a known chat.
                    if let Some(chat) = match_chat_title(&chats, &title) {
                        debug!(
                            "[chat_detector] detected active chat: {} from title {:?}",
                            chat.name, title
                        );
                        if result_tx.send(chat.name).is_err() {
                            warn!("[chat_detector] result channel closed, stopping");
                            break;
                        }
                    }
                }
            }

            info!("[chat_detector] thread stopped");
        });

        Self {
            result_rx,
            _handle: handle,
        }
    }

    pub fn try_recv(&self) -> Option<String> {
        self.result_rx.try_recv().ok()
    }
}

/// Detects the currently active Telegram chat by parsing window titles.
/// Returns None if xdotool is not available or no Telegram chat is active.
fn detect_telegram_chat_title() -> Option<String> {
    let output = run_xdotool(&["search", "--onlyvisible", "--class", "TelegramDesktop"])
        .or_else(|| run_xdotool(&["search", "--class", "TelegramDesktop"]))?;

    let ids: Vec<&str> = output.lines().collect();
    let mut best_title = None;
    let mut best_area = 0;

    for id in ids {
        let name_output = Command::new("xdotool")
            .args(["getwindowname", id])
            .output()
            .ok()?;

        let name = std::str::from_utf8(&name_output.stdout)
            .ok()?
            .trim()
            .to_string();

        // Skip non-chat windows
        if is_system_window(&name) {
            continue;
        }

        let Some(area) = window_area(id) else {
            return Some(name);
        };

        if area == 0 {
            continue;
        }

        if area > best_area {
            best_area = area;
            best_title = Some(name);
        }
    }

    best_title
}

pub fn match_chat_title(chats: &[ChatInfo], title: &str) -> Option<ChatInfo> {
    let title = extract_chat_name(&clean_unicode(title));
    if is_system_window(&title) {
        return None;
    }

    let title_norm = normalize_chat_name(&title);
    if title_norm.is_empty() {
        return None;
    }

    if let Some(chat) = chats
        .iter()
        .find(|chat| normalize_chat_name(&chat.name) == title_norm)
    {
        return Some(chat.clone());
    }

    chats
        .iter()
        .filter_map(|chat| {
            if is_system_window(&chat.name) {
                return None;
            }

            let chat_norm = normalize_chat_name(&chat.name);
            if chat_norm.is_empty() || !title_norm.contains(&chat_norm) {
                return None;
            }

            Some((chat_norm.len(), chat))
        })
        .max_by_key(|(len, _)| *len)
        .map(|(_, chat)| chat.clone())
}

/// Checks if window name is a system window (not a chat)
fn is_system_window(name: &str) -> bool {
    matches!(
        name,
        "TelegramDesktop" | "Media viewer" | "Telegram Desktop" | "Notifier"
    )
}

/// Extracts chat name from window title (removes unread count suffix)
fn extract_chat_name(name: &str) -> String {
    let mut name = name.trim().to_string();

    for marker in [" – (", " — (", " - ("] {
        if let Some(pos) = name.rfind(marker) {
            name.truncate(pos);
        }
    }

    for suffix in [
        " – Telegram Desktop",
        " — Telegram Desktop",
        " - Telegram Desktop",
        " – Telegram",
        " — Telegram",
        " - Telegram",
    ] {
        if let Some(stripped) = name.strip_suffix(suffix) {
            name = stripped.trim().to_string();
            break;
        }
    }

    for prefix in [
        "Telegram Desktop – ",
        "Telegram Desktop — ",
        "Telegram Desktop - ",
        "Telegram – ",
        "Telegram — ",
        "Telegram - ",
    ] {
        if let Some(stripped) = name.strip_prefix(prefix) {
            name = stripped.trim().to_string();
            break;
        }
    }

    name
}

/// Removes Unicode control characters that Telegram adds to titles
fn clean_unicode(text: &str) -> String {
    text.chars()
        .filter(|c| {
            !c.is_control()
                && *c != '\u{200e}' // LEFT-TO-RIGHT MARK
                && *c != '\u{200f}' // RIGHT-TO-LEFT MARK
                && *c != '\u{2068}' // FIRST STRONG ISOLATE
                && *c != '\u{2069}' // POP DIRECTIONAL ISOLATE
        })
        .collect()
}

fn normalize_chat_name(text: &str) -> String {
    clean_unicode(text)
        .to_lowercase()
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

fn window_area(id: &str) -> Option<u32> {
    let output = run_xdotool(&["getwindowgeometry", "--shell", id])?;
    parse_window_area(&output)
}

fn run_xdotool(args: &[&str]) -> Option<String> {
    let output = Command::new("xdotool").args(args).output().ok()?;
    if !output.status.success() {
        return None;
    }

    Some(std::str::from_utf8(&output.stdout).ok()?.trim().to_string())
}

fn parse_window_area(geometry: &str) -> Option<u32> {
    let mut width = None;
    let mut height = None;

    for line in geometry.lines() {
        if let Some(raw) = line.strip_prefix("WIDTH=") {
            width = raw.parse::<u32>().ok();
        } else if let Some(raw) = line.strip_prefix("HEIGHT=") {
            height = raw.parse::<u32>().ok();
        }
    }

    let width = width?;
    let height = height?;
    if width < MIN_TELEGRAM_WINDOW_WIDTH || height < MIN_TELEGRAM_WINDOW_HEIGHT {
        return Some(0);
    }

    Some(width.saturating_mul(height))
}

#[cfg(test)]
mod tests {
    use super::{extract_chat_name, is_system_window, match_chat_title, parse_window_area};
    use crate::models::{ChatInfo, ChatType};

    fn chat(name: &str) -> ChatInfo {
        ChatInfo {
            id: 1,
            name: name.into(),
            chat_type: ChatType::Private,
        }
    }

    #[test]
    fn skips_telegram_service_windows() {
        assert!(is_system_window("Notifier"));
        assert!(is_system_window("Telegram Desktop"));
        assert!(!is_system_window("гном с палкой"));
    }

    #[test]
    fn parses_window_area_and_filters_small_windows() {
        assert_eq!(
            parse_window_area("WINDOW=1\nX=0\nY=0\nWIDTH=1200\nHEIGHT=800\nSCREEN=0\n"),
            Some(960000)
        );
        assert_eq!(
            parse_window_area("WINDOW=1\nWIDTH=240\nHEIGHT=120\nSCREEN=0\n"),
            Some(0)
        );
        assert_eq!(parse_window_area("WINDOW=1\nWIDTH=1200\n"), None);
    }

    #[test]
    fn extracts_chat_name_from_unread_title() {
        assert_eq!(extract_chat_name("chat – (2)"), "chat");
        assert_eq!(extract_chat_name("chat — Telegram"), "chat");
        assert_eq!(extract_chat_name("Telegram - chat"), "chat");
        assert_eq!(extract_chat_name("chat"), "chat");
    }

    #[test]
    fn matches_chat_titles_with_telegram_suffixes() {
        let chats = vec![chat("Notifier"), chat("гном с палкой")];

        assert_eq!(
            match_chat_title(&chats, "гном с палкой — Telegram")
                .unwrap()
                .name,
            "гном с палкой"
        );
        assert_eq!(
            match_chat_title(&chats, "Telegram Desktop - гном с палкой")
                .unwrap()
                .name,
            "гном с палкой"
        );
        assert!(match_chat_title(&chats, "Notifier").is_none());
    }
}
