use global_hotkey::{
    hotkey::{Code, HotKey, Modifiers},
    GlobalHotKeyEvent, GlobalHotKeyManager,
};
use std::sync::mpsc::Sender;
use tracing::{info, warn, error};

pub enum HotkeyEvent {
    Toggle,
}

pub struct HotkeyListener {
    _manager: GlobalHotKeyManager,
    _hotkey_id: u32,
}

fn parse_hotkey(hotkey_str: &str) -> Option<HotKey> {
    let parts: Vec<&str> = hotkey_str.split('+').map(str::trim).collect();
    if parts.is_empty() {
        warn!("[hotkey] empty hotkey string");
        return None;
    }

    let mut modifiers = Modifiers::empty();
    let key_part = parts.last()?;

    for &part in &parts[..parts.len() - 1] {
        match part.to_lowercase().as_str() {
            "ctrl" | "control" => modifiers |= Modifiers::CONTROL,
            "shift" => modifiers |= Modifiers::SHIFT,
            "alt" => modifiers |= Modifiers::ALT,
            "super" | "meta" => modifiers |= Modifiers::SUPER,
            unknown => {
                warn!("[hotkey] unknown modifier: {:?}", unknown);
                return None;
            }
        }
    }

    let code = match key_part.to_lowercase().as_str() {
        "a" => Code::KeyA, "b" => Code::KeyB, "c" => Code::KeyC,
        "d" => Code::KeyD, "e" => Code::KeyE, "f" => Code::KeyF,
        "g" => Code::KeyG, "h" => Code::KeyH, "i" => Code::KeyI,
        "j" => Code::KeyJ, "k" => Code::KeyK, "l" => Code::KeyL,
        "m" => Code::KeyM, "n" => Code::KeyN, "o" => Code::KeyO,
        "p" => Code::KeyP, "q" => Code::KeyQ, "r" => Code::KeyR,
        "s" => Code::KeyS, "t" => Code::KeyT, "u" => Code::KeyU,
        "v" => Code::KeyV, "w" => Code::KeyW, "x" => Code::KeyX,
        "y" => Code::KeyY, "z" => Code::KeyZ,
        "0" => Code::Digit0, "1" => Code::Digit1, "2" => Code::Digit2,
        "3" => Code::Digit3, "4" => Code::Digit4, "5" => Code::Digit5,
        "6" => Code::Digit6, "7" => Code::Digit7, "8" => Code::Digit8,
        "9" => Code::Digit9,
        "space" => Code::Space, "escape" | "esc" => Code::Escape,
        "enter" | "return" => Code::Enter, "tab" => Code::Tab,
        "f1" => Code::F1, "f2" => Code::F2, "f3" => Code::F3,
        "f4" => Code::F4, "f5" => Code::F5, "f6" => Code::F6,
        "f7" => Code::F7, "f8" => Code::F8, "f9" => Code::F9,
        "f10" => Code::F10, "f11" => Code::F11, "f12" => Code::F12,
        unknown => {
            warn!("[hotkey] unknown key: {:?}", unknown);
            return None;
        }
    };

    let mods = if modifiers.is_empty() { None } else { Some(modifiers) };
    Some(HotKey::new(mods, code))
}

impl HotkeyListener {
    pub fn start(tx: Sender<HotkeyEvent>, hotkey_str: &str) -> Option<Self> {
        info!("[hotkey] initializing global hotkey manager");

        let manager = match GlobalHotKeyManager::new() {
            Ok(m) => m,
            Err(e) => {
                error!("[hotkey] failed to create manager: {:?}", e);
                return None;
            }
        };

        let hotkey = match parse_hotkey(hotkey_str) {
            Some(hk) => hk,
            None => {
                error!("[hotkey] failed to parse hotkey: {:?}", hotkey_str);
                return None;
            }
        };
        let hotkey_id = hotkey.id();

        if let Err(e) = manager.register(hotkey) {
            error!("[hotkey] failed to register {:?}: {:?}", hotkey_str, e);
            return None;
        }

        info!("[hotkey] registered {:?} (id={})", hotkey_str, hotkey_id);

        // Spawn event handler thread
        std::thread::spawn(move || {
            info!("[hotkey] event listener thread started");
            loop {
                if let Ok(event) = GlobalHotKeyEvent::receiver().recv() {
                    info!("[hotkey] received event: {:?}", event);
                    if event.id == hotkey_id && event.state == global_hotkey::HotKeyState::Pressed {
                        info!("[hotkey] hotkey pressed, sending Toggle");
                        let _ = tx.send(HotkeyEvent::Toggle);
                    }
                }
            }
        });

        Some(Self {
            _manager: manager,
            _hotkey_id: hotkey_id,
        })
    }
}
