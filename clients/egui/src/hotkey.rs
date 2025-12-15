use rdev::{listen, Event, EventType, Key};
use std::sync::mpsc::Sender;
use std::thread;

pub enum HotkeyEvent {
    Toggle,
}

pub struct HotkeyListener {
    ctrl_pressed: bool,
    shift_pressed: bool,
}

impl HotkeyListener {
    pub fn new() -> Self {
        Self {
            ctrl_pressed: false,
            shift_pressed: false,
        }
    }

    pub fn start(tx: Sender<HotkeyEvent>) {
        thread::spawn(move || {
            let mut listener = HotkeyListener::new();

            if let Err(e) = listen(move |event| {
                listener.handle_event(&event, &tx);
            }) {
                eprintln!("Hotkey listener error: {:?}", e);
            }
        });
    }

    fn handle_event(&mut self, event: &Event, tx: &Sender<HotkeyEvent>) {
        match event.event_type {
            EventType::KeyPress(key) => {
                match key {
                    Key::ControlLeft | Key::ControlRight => self.ctrl_pressed = true,
                    Key::ShiftLeft | Key::ShiftRight => self.shift_pressed = true,
                    Key::KeyS => {
                        // Ctrl+Shift+S
                        if self.ctrl_pressed && self.shift_pressed {
                            let _ = tx.send(HotkeyEvent::Toggle);
                        }
                    }
                    _ => {}
                }
            }
            EventType::KeyRelease(key) => match key {
                Key::ControlLeft | Key::ControlRight => self.ctrl_pressed = false,
                Key::ShiftLeft | Key::ShiftRight => self.shift_pressed = false,
                _ => {}
            },
            _ => {}
        }
    }
}
