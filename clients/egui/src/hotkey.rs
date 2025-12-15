use global_hotkey::{
    hotkey::{Code, HotKey, Modifiers},
    GlobalHotKeyEvent, GlobalHotKeyManager,
};
use std::sync::mpsc::Sender;
use tracing::{info, error};

pub enum HotkeyEvent {
    Toggle,
}

pub struct HotkeyListener {
    _manager: GlobalHotKeyManager,
    _hotkey_id: u32,
}

impl HotkeyListener {
    pub fn start(tx: Sender<HotkeyEvent>) -> Option<Self> {
        info!("[hotkey] initializing global hotkey manager");

        let manager = match GlobalHotKeyManager::new() {
            Ok(m) => m,
            Err(e) => {
                error!("[hotkey] failed to create manager: {:?}", e);
                return None;
            }
        };

        // Ctrl+Shift+S
        let hotkey = HotKey::new(Some(Modifiers::CONTROL | Modifiers::SHIFT), Code::KeyS);
        let hotkey_id = hotkey.id();

        if let Err(e) = manager.register(hotkey) {
            error!("[hotkey] failed to register Ctrl+Shift+S: {:?}", e);
            return None;
        }

        info!("[hotkey] registered Ctrl+Shift+S (id={})", hotkey_id);

        // Spawn event handler thread
        std::thread::spawn(move || {
            info!("[hotkey] event listener thread started");
            loop {
                if let Ok(event) = GlobalHotKeyEvent::receiver().recv() {
                    info!("[hotkey] received event: {:?}", event);
                    if event.id == hotkey_id && event.state == global_hotkey::HotKeyState::Pressed {
                        info!("[hotkey] Ctrl+Shift+S pressed, sending Toggle");
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
