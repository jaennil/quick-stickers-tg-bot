use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::Arc;
use std::thread::{self, JoinHandle};

use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

use crate::api::Api;
use crate::cache::ThumbnailCache;

pub enum ThumbnailResult {
    Loaded(String, Vec<u8>),
    NotFound(String),
}

pub struct ThumbnailLoader {
    request_tx: Sender<String>,
    result_rx: Receiver<ThumbnailResult>,
    _handle: JoinHandle<()>,
}

impl ThumbnailLoader {
    pub fn start(rt: Arc<Runtime>, api: Arc<Api>, cache: Arc<ThumbnailCache>) -> Self {
        let (request_tx, request_rx) = mpsc::channel::<String>();
        let (result_tx, result_rx) = mpsc::channel::<ThumbnailResult>();

        let handle = thread::spawn(move || {
            info!("[thumbnail_loader] thread started");

            while let Ok(file_id) = request_rx.recv() {
                let api = api.clone();
                let cache = cache.clone();
                let tx = result_tx.clone();
                let fid = file_id.clone();

                rt.spawn(async move {
                    let short_id = &fid[..20.min(fid.len())];
                    debug!("[thumbnail_loader] loading: {}", short_id);

                    // Try cache first
                    if let Some(data) = cache.get(&fid).await {
                        debug!("[thumbnail_loader] cache hit: {}", short_id);
                        if tx.send(ThumbnailResult::Loaded(fid, data)).is_err() {
                            warn!("[thumbnail_loader] result channel closed");
                        }
                        return;
                    }

                    // Try API
                    match api.get_thumbnail(&fid).await {
                        Ok(Some(data)) => {
                            debug!("[thumbnail_loader] API success: {}", short_id);
                            let _ = cache.set(&fid, data.clone()).await;
                            if tx.send(ThumbnailResult::Loaded(fid, data)).is_err() {
                                warn!("[thumbnail_loader] result channel closed");
                            }
                        }
                        Ok(None) => {
                            debug!("[thumbnail_loader] not found: {}", short_id);
                            if tx.send(ThumbnailResult::NotFound(fid)).is_err() {
                                warn!("[thumbnail_loader] result channel closed");
                            }
                        }
                        Err(e) => {
                            warn!("[thumbnail_loader] API error for {}: {}", short_id, e);
                            if tx.send(ThumbnailResult::NotFound(fid)).is_err() {
                                warn!("[thumbnail_loader] result channel closed");
                            }
                        }
                    }
                });
            }

            info!("[thumbnail_loader] thread stopped");
        });

        Self {
            request_tx,
            result_rx,
            _handle: handle,
        }
    }

    pub fn request(&self, file_id: &str) {
        if self.request_tx.send(file_id.to_string()).is_err() {
            warn!("[thumbnail_loader] request channel closed");
        }
    }

    pub fn try_recv(&self) -> Option<ThumbnailResult> {
        self.result_rx.try_recv().ok()
    }
}
