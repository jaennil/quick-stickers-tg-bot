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
    let output = run_xdotool(&["search", "--onlyvisible", "--class", "TelegramDesktop"])
        .or_else(|| run_xdotool(&["search", "--class", "TelegramDesktop"]))?;

    let ids: Vec<&str> = output.lines().collect();
    let mut best_chat = None;
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
            let chat_name = extract_chat_name(&name);
            let clean = clean_unicode(&chat_name);
            return Some(clean);
        };

        if area == 0 {
            continue;
        }

        let chat_name = extract_chat_name(&name);
        let clean = clean_unicode(&chat_name);
        if area > best_area {
            best_area = area;
            best_chat = Some(clean);
        }
    }

    best_chat
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
    use super::{extract_chat_name, is_system_window, parse_window_area};

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
        assert_eq!(extract_chat_name("chat"), "chat");
    }
}
