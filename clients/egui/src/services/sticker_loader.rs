use std::sync::mpsc::{self, Receiver};
use std::sync::Arc;

use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

use crate::api::Api;
use crate::cache::StickerCatalog;
use crate::models::Sticker;
use crate::ui::theme::STICKER_BATCH_SIZE;

pub enum StickerLoadResult {
    Synced(Vec<Sticker>),
    Offline(String),
}

pub struct StickerLoader {
    result_rx: Receiver<StickerLoadResult>,
}

impl StickerLoader {
    pub fn start(rt: Arc<Runtime>, api: Arc<Api>, catalog: Arc<StickerCatalog>) -> Self {
        let (result_tx, result_rx) = mpsc::channel::<StickerLoadResult>();

        info!("[sticker_loader] starting full catalog sync");

        rt.spawn(async move {
            let mut offset = 0;
            let mut stickers_snapshot = Vec::new();

            loop {
                debug!(
                    "[sticker_loader] fetching batch: offset={}, limit={}",
                    offset, STICKER_BATCH_SIZE
                );

                match api.get_stickers_page(STICKER_BATCH_SIZE, offset).await {
                    Ok(stickers) => {
                        let count = stickers.len();
                        stickers_snapshot.extend(stickers);
                        info!(
                            "[sticker_loader] loaded batch: {} stickers (total: {})",
                            count,
                            stickers_snapshot.len()
                        );

                        if count == 0 || count < STICKER_BATCH_SIZE {
                            info!(
                                "[sticker_loader] all stickers loaded: {} total",
                                stickers_snapshot.len()
                            );
                            if let Err(e) = catalog.save(&stickers_snapshot) {
                                warn!("[sticker_loader] failed to save catalog: {}", e);
                            }
                            if result_tx
                                .send(StickerLoadResult::Synced(stickers_snapshot))
                                .is_err()
                            {
                                warn!("[sticker_loader] result channel closed");
                            }
                            break;
                        }

                        offset += STICKER_BATCH_SIZE;
                    }
                    Err(e) => {
                        warn!("[sticker_loader] batch load failed: {}", e);
                        if result_tx
                            .send(StickerLoadResult::Offline(e.to_string()))
                            .is_err()
                        {
                            warn!("[sticker_loader] result channel closed");
                        }
                        break;
                    }
                }
            }
        });

        Self { result_rx }
    }

    pub fn try_recv(&self) -> Option<StickerLoadResult> {
        self.result_rx.try_recv().ok()
    }
}
