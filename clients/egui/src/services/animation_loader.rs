//! Fetches animated previews and decodes them off the UI thread.

use std::path::PathBuf;
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::Arc;
use std::thread::{self, JoinHandle};

use eframe::egui;
use tokio::runtime::Runtime;
use tokio::sync::Semaphore;
use tracing::{debug, info, warn};

use crate::animation::{self, DecodedFrames, Purpose};
use crate::api::Api;

/// Decoding is CPU-bound; a screen full of video stickers must not starve
/// the rest of the app while it loads.
const MAX_CONCURRENT_DECODES: usize = 4;

pub enum AnimationResult {
    Loaded {
        file_id: String,
        purpose: Purpose,
        frames: DecodedFrames,
    },
    Missing {
        file_id: String,
        purpose: Purpose,
    },
    /// The preview is a single frame: nothing to play.
    Still {
        file_id: String,
        purpose: Purpose,
    },
}

struct Request {
    file_id: String,
    purpose: Purpose,
    side: u32,
}

pub struct AnimationLoader {
    request_tx: Sender<Request>,
    result_rx: Receiver<AnimationResult>,
    _handle: JoinHandle<()>,
}

impl AnimationLoader {
    pub fn start(rt: Arc<Runtime>, api: Arc<Api>, cache_dir: PathBuf, ui: egui::Context) -> Self {
        let (request_tx, request_rx) = mpsc::channel::<Request>();
        let (result_tx, result_rx) = mpsc::channel::<AnimationResult>();
        if let Err(e) = std::fs::create_dir_all(&cache_dir) {
            warn!("[animation_loader] cannot create cache dir: {}", e);
        }
        let permits = Arc::new(Semaphore::new(MAX_CONCURRENT_DECODES));

        let handle = thread::spawn(move || {
            info!("[animation_loader] thread started");
            while let Ok(request) = request_rx.recv() {
                let api = api.clone();
                let tx = result_tx.clone();
                let ui = ui.clone();
                let cache_path = cache_dir.join(cache_name(&request.file_id));
                let permits = permits.clone();

                rt.spawn(async move {
                    let Ok(_permit) = permits.acquire_owned().await else {
                        return;
                    };
                    let result = load(&api, &cache_path, request).await;
                    if tx.send(result).is_err() {
                        warn!("[animation_loader] result channel closed");
                    }
                    // The UI may be idle; wake it so the new animation shows.
                    ui.request_repaint();
                });
            }
            info!("[animation_loader] thread stopped");
        });

        Self {
            request_tx,
            result_rx,
            _handle: handle,
        }
    }

    pub fn request(&self, file_id: &str, purpose: Purpose, side: u32) {
        let request = Request {
            file_id: file_id.to_string(),
            purpose,
            side,
        };
        if self.request_tx.send(request).is_err() {
            warn!("[animation_loader] request channel closed");
        }
    }

    pub fn try_recv(&self) -> Option<AnimationResult> {
        self.result_rx.try_recv().ok()
    }
}

async fn load(api: &Api, cache_path: &PathBuf, request: Request) -> AnimationResult {
    let Request {
        file_id,
        purpose,
        side,
    } = request;
    let missing = |file_id| AnimationResult::Missing { file_id, purpose };

    let bytes = match tokio::fs::read(cache_path).await {
        Ok(bytes) => bytes,
        Err(_) => match api.get_animation(&file_id).await {
            Ok(Some(bytes)) => {
                store(cache_path, &bytes).await;
                bytes
            }
            Ok(None) => return missing(file_id),
            Err(e) => {
                debug!("[animation_loader] fetch failed for {}: {}", file_id, e);
                return missing(file_id);
            }
        },
    };

    match tokio::task::spawn_blocking(move || animation::decode(&bytes, side)).await {
        Ok(Ok(frames)) if frames.frames.len() < 2 => AnimationResult::Still { file_id, purpose },
        Ok(Ok(frames)) => AnimationResult::Loaded {
            file_id,
            purpose,
            frames,
        },
        Ok(Err(e)) => {
            // A corrupt cache entry would otherwise fail forever.
            warn!("[animation_loader] decode failed for {}: {}", file_id, e);
            let _ = tokio::fs::remove_file(cache_path).await;
            missing(file_id)
        }
        Err(e) => {
            warn!("[animation_loader] decode task failed: {}", e);
            missing(file_id)
        }
    }
}

/// Writes through a temporary name so an interrupted write never leaves a
/// truncated preview that later reads would trust.
async fn store(path: &PathBuf, bytes: &[u8]) {
    let partial = path.with_extension("part");
    if tokio::fs::write(&partial, bytes).await.is_ok() {
        if let Err(e) = tokio::fs::rename(&partial, path).await {
            warn!("[animation_loader] cache write failed: {}", e);
        }
    }
}

fn cache_name(file_id: &str) -> String {
    format!("{:x}.webp", md5::compute(file_id))
}
