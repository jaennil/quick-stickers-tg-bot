use std::sync::mpsc::{self, Receiver};
use std::sync::Arc;

use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

use crate::api::Api;
use crate::models::Sticker;
use crate::ui::theme::STICKER_BATCH_SIZE;

pub enum StickerLoadResult {
    Append(Vec<Sticker>),
    Done,
}

pub struct StickerLoader {
    result_rx: Receiver<StickerLoadResult>,
}

impl StickerLoader {
    pub fn start(rt: Arc<Runtime>, api: Arc<Api>) -> Self {
        let (result_tx, result_rx) = mpsc::channel::<StickerLoadResult>();

        info!("[sticker_loader] starting incremental loading");

        rt.spawn(async move {
            let mut offset = 0;
            let mut total_loaded = 0;

            loop {
                debug!(
                    "[sticker_loader] fetching batch: offset={}, limit={}",
                    offset, STICKER_BATCH_SIZE
                );

                match api.get_stickers_page(STICKER_BATCH_SIZE, offset).await {
                    Ok(stickers) => {
                        let count = stickers.len();
                        total_loaded += count;
                        info!(
                            "[sticker_loader] loaded batch: {} stickers (total: {})",
                            count, total_loaded
                        );

                        if count == 0 {
                            info!("[sticker_loader] all stickers loaded: {} total", total_loaded);
                            if result_tx.send(StickerLoadResult::Done).is_err() {
                                warn!("[sticker_loader] result channel closed");
                            }
                            break;
                        }

                        if result_tx.send(StickerLoadResult::Append(stickers)).is_err() {
                            warn!("[sticker_loader] result channel closed");
                            break;
                        }

                        if count < STICKER_BATCH_SIZE {
                            info!(
                                "[sticker_loader] last batch, all stickers loaded: {} total",
                                total_loaded
                            );
                            if result_tx.send(StickerLoadResult::Done).is_err() {
                                warn!("[sticker_loader] result channel closed");
                            }
                            break;
                        }

                        offset += STICKER_BATCH_SIZE;
                    }
                    Err(e) => {
                        warn!("[sticker_loader] batch load failed: {}", e);
                        if result_tx.send(StickerLoadResult::Done).is_err() {
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
