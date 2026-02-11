use anyhow::Result;
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::RwLock;

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
}
