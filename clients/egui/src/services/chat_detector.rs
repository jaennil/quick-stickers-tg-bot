use std::process::Command;
use std::sync::mpsc::{self, Receiver};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use tracing::{debug, info, warn};

use crate::models::ChatInfo;
use crate::ui::theme::CHAT_DETECT_INTERVAL_MS;

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

                if let Some(name) = detect_telegram_chat() {
                    // Only send if chat exists in our list
                    if chats.iter().any(|c| c.name == name) {
                        debug!("[chat_detector] detected active chat: {}", name);
                        if result_tx.send(name).is_err() {
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
fn detect_telegram_chat() -> Option<String> {
    let output = Command::new("xdotool")
        .args(["search", "--class", "TelegramDesktop"])
        .output()
        .ok()?;

    let ids: Vec<&str> = std::str::from_utf8(&output.stdout).ok()?.lines().collect();

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

        let chat_name = extract_chat_name(&name);
        let clean = clean_unicode(&chat_name);

        return Some(clean);
    }

    None
}

/// Checks if window name is a system window (not a chat)
fn is_system_window(name: &str) -> bool {
    matches!(
        name,
        "TelegramDesktop" | "Media viewer" | "Telegram Desktop"
    )
}

/// Extracts chat name from window title (removes unread count suffix)
fn extract_chat_name(name: &str) -> String {
    if let Some(pos) = name.rfind(" – (") {
        name[..pos].to_string()
    } else {
        name.to_string()
    }
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
