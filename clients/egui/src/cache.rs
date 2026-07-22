use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use tokio::sync::RwLock;

use crate::models::Sticker;

const CATALOG_VERSION: u32 = 1;

#[derive(Deserialize, Serialize)]
struct CatalogFile {
    version: u32,
    user_id: i64,
    stickers: Vec<Sticker>,
}

pub struct StickerCatalog {
    path: PathBuf,
    user_id: i64,
    write_lock: Mutex<()>,
}

impl StickerCatalog {
    pub fn new(cache_dir: PathBuf, user_id: i64) -> Result<Self> {
        std::fs::create_dir_all(&cache_dir)?;
        Ok(Self {
            path: cache_dir.join(format!("stickers-{user_id}.yaml")),
            user_id,
            write_lock: Mutex::new(()),
        })
    }

    pub fn load(&self) -> Result<Vec<Sticker>> {
        if !self.path.exists() {
            return Ok(Vec::new());
        }

        let data = std::fs::read_to_string(&self.path)?;
        let catalog: CatalogFile = serde_yaml::from_str(&data)?;
        anyhow::ensure!(
            catalog.version == CATALOG_VERSION,
            "unsupported sticker catalog version {}",
            catalog.version
        );
        anyhow::ensure!(
            catalog.user_id == self.user_id,
            "sticker catalog belongs to another user"
        );
        Ok(catalog.stickers)
    }

    pub fn save(&self, stickers: &[Sticker]) -> Result<()> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| anyhow::anyhow!("sticker catalog lock poisoned"))?;
        let catalog = CatalogFile {
            version: CATALOG_VERSION,
            user_id: self.user_id,
            stickers: stickers.to_vec(),
        };
        let data = serde_yaml::to_string(&catalog)?;
        let temp_path = self.path.with_extension("yaml.tmp");

        std::fs::write(&temp_path, data)?;
        std::fs::rename(&temp_path, &self.path)?;
        Ok(())
    }
}

pub struct ThumbnailCache {
    memory: Arc<RwLock<HashMap<String, Vec<u8>>>>,
    cache_dir: PathBuf,
}

impl ThumbnailCache {
    pub fn new(cache_dir: PathBuf) -> Result<Self> {
        std::fs::create_dir_all(&cache_dir)?;
        Ok(Self {
            memory: Arc::new(RwLock::new(HashMap::new())),
            cache_dir,
        })
    }

    pub async fn get(&self, file_id: &str) -> Option<Vec<u8>> {
        // Check memory cache first
        {
            let cache = self.memory.read().await;
            if let Some(data) = cache.get(file_id) {
                return Some(data.clone());
            }
        }

        // Check disk cache
        let cache_path = self.cache_path(file_id);
        if cache_path.exists() {
            if let Ok(data) = std::fs::read(&cache_path) {
                // Store in memory for faster access next time
                let mut cache = self.memory.write().await;
                cache.insert(file_id.to_string(), data.clone());
                return Some(data);
            }
        }

        None
    }

    pub async fn set(&self, file_id: &str, data: Vec<u8>) -> Result<()> {
        // Save to disk
        let cache_path = self.cache_path(file_id);
        std::fs::write(&cache_path, &data)?;

        // Store in memory
        let mut cache = self.memory.write().await;
        cache.insert(file_id.to_string(), data);

        Ok(())
    }

    pub fn cache_path(&self, file_id: &str) -> PathBuf {
        let hash = format!("{:x}", md5::compute(file_id));
        self.cache_dir.join(format!("{}.png", hash))
    }

    pub fn original_path(&self, file_id: &str, extension: &str) -> PathBuf {
        let hash = format!("{:x}", md5::compute(file_id));
        self.cache_dir.join(format!("{}.{}", hash, extension))
    }
}

#[cfg(test)]
mod tests {
    use super::StickerCatalog;
    use crate::models::Sticker;

    #[test]
    fn catalog_round_trip_preserves_stickers() {
        let cache_dir = std::env::temp_dir().join(format!("qsg-catalog-{}", rand::random::<u64>()));
        let catalog = StickerCatalog::new(cache_dir.clone(), 42).unwrap();
        let stickers = vec![Sticker {
            sticker_id: "sticker-1".into(),
            file_id: "file-1".into(),
            document_id: 123,
            is_animated: true,
            is_video: false,
            set_name: "pack".into(),
            media_type: "sticker".into(),
            text: "searchable text".into(),
            emoji: "emoji".into(),
            ocr_engine: "ocr.space".into(),
            manual_edit: true,
        }];

        catalog.save(&stickers).unwrap();
        assert_eq!(catalog.load().unwrap(), stickers);

        std::fs::remove_dir_all(cache_dir).unwrap();
    }
}
