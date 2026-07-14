use std::sync::mpsc::{self, Receiver};
use std::sync::Arc;
use std::time::Duration;

use tokio::runtime::Runtime;
use tracing::{debug, info, warn};

use crate::api::Api;
use crate::cache::StickerCatalog;
use crate::models::Sticker;
use crate::ui::theme::{CATALOG_REFRESH_INTERVAL_SECS, STICKER_BATCH_SIZE};

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
            loop {
                match load_full_catalog(&api).await {
                    Ok(mut stickers_snapshot) => {
                        persist_and_send(&catalog, &result_tx, &stickers_snapshot);

                        loop {
                            tokio::time::sleep(Duration::from_secs(CATALOG_REFRESH_INTERVAL_SECS))
                                .await;

                            match api.get_stickers_page(STICKER_BATCH_SIZE, 0).await {
                                Ok(recent) => {
                                    let refreshed = merge_recent(&stickers_snapshot, recent);
                                    if refreshed != stickers_snapshot {
                                        info!(
                                            "[sticker_loader] catalog refreshed: {} total",
                                            refreshed.len()
                                        );
                                        stickers_snapshot = refreshed;
                                        persist_and_send(&catalog, &result_tx, &stickers_snapshot);
                                    }
                                }
                                Err(e) => {
                                    warn!("[sticker_loader] catalog refresh failed: {}", e);
                                }
                            }
                        }
                    }
                    Err(e) => {
                        warn!("[sticker_loader] full sync failed: {}", e);
                        if result_tx
                            .send(StickerLoadResult::Offline(e.to_string()))
                            .is_err()
                        {
                            warn!("[sticker_loader] result channel closed");
                            break;
                        }
                        tokio::time::sleep(Duration::from_secs(CATALOG_REFRESH_INTERVAL_SECS))
                            .await;
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

async fn load_full_catalog(api: &Api) -> anyhow::Result<Vec<Sticker>> {
    let mut offset = 0;
    let mut stickers = Vec::new();

    loop {
        debug!(
            "[sticker_loader] fetching batch: offset={}, limit={}",
            offset, STICKER_BATCH_SIZE
        );
        let batch = api.get_stickers_page(STICKER_BATCH_SIZE, offset).await?;
        let count = batch.len();
        stickers.extend(batch);
        info!(
            "[sticker_loader] loaded batch: {} stickers (total: {})",
            count,
            stickers.len()
        );

        if count < STICKER_BATCH_SIZE {
            info!(
                "[sticker_loader] all stickers loaded: {} total",
                stickers.len()
            );
            return Ok(stickers);
        }

        offset += STICKER_BATCH_SIZE;
    }
}

fn merge_recent(current: &[Sticker], recent: Vec<Sticker>) -> Vec<Sticker> {
    let recent_ids = recent
        .iter()
        .map(|sticker| sticker.sticker_id.clone())
        .collect::<std::collections::HashSet<_>>();
    let mut merged = recent;
    merged.extend(
        current
            .iter()
            .filter(|sticker| !recent_ids.contains(&sticker.sticker_id))
            .cloned(),
    );
    merged
}

fn persist_and_send(
    catalog: &StickerCatalog,
    result_tx: &mpsc::Sender<StickerLoadResult>,
    stickers: &[Sticker],
) {
    if let Err(e) = catalog.save(stickers) {
        warn!("[sticker_loader] failed to save catalog: {}", e);
    }
    if result_tx
        .send(StickerLoadResult::Synced(stickers.to_vec()))
        .is_err()
    {
        warn!("[sticker_loader] result channel closed");
    }
}

#[cfg(test)]
mod tests {
    use super::merge_recent;
    use crate::models::Sticker;

    fn sticker(id: &str, text: &str) -> Sticker {
        Sticker {
            sticker_id: id.into(),
            file_id: format!("file-{id}"),
            document_id: 1,
            set_name: "pack".into(),
            media_type: "sticker".into(),
            text: text.into(),
            emoji: String::new(),
            ocr_engine: String::new(),
            manual_edit: false,
        }
    }

    #[test]
    fn recent_refresh_prepends_new_and_updates_existing_stickers() {
        let current = vec![sticker("2", "old"), sticker("1", "first")];
        let recent = vec![sticker("3", "new"), sticker("2", "updated")];

        let merged = merge_recent(&current, recent);

        assert_eq!(merged.len(), 3);
        assert_eq!(merged[0].sticker_id, "3");
        assert_eq!(merged[1].text, "updated");
        assert_eq!(merged[2].sticker_id, "1");
    }
}
