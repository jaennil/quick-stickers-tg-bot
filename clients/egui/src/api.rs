use anyhow::Result;
use reqwest::Client;
use serde::{Deserialize, Serialize};
use std::time::Duration;
use tracing::{debug, warn};

use crate::config::ApiConfig;
use crate::models::Sticker;

const MAX_RETRIES: u32 = 3;
const INITIAL_BACKOFF_MS: u64 = 200;
const HEALTH_TIMEOUT_SECS: u64 = 5;

pub struct Api {
    client: Client,
    base_url: String,
    api_key: String,
    user_id: i64,
}

#[derive(Debug, Deserialize)]
struct StickerResponse {
    sticker_id: String,
    file_id: String,
    document_id: i64,
    #[serde(default)]
    is_animated: bool,
    #[serde(default)]
    is_video: bool,
    #[serde(default)]
    media_type: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    set_name: String,
    #[serde(default)]
    emoji: String,
    #[serde(default)]
    ocr_engine: String,
    #[serde(default)]
    manual_edit: bool,
}

#[derive(Debug, Serialize)]
struct UpdateStickerRequest<'a> {
    user_id: i64,
    text: &'a str,
}

impl From<StickerResponse> for Sticker {
    fn from(r: StickerResponse) -> Self {
        Sticker {
            sticker_id: r.sticker_id,
            file_id: r.file_id,
            document_id: r.document_id,
            is_animated: r.is_animated,
            is_video: r.is_video,
            set_name: r.set_name,
            media_type: if r.media_type.is_empty() {
                "sticker".into()
            } else {
                r.media_type
            },
            text: r.text,
            emoji: r.emoji,
            ocr_engine: r.ocr_engine,
            manual_edit: r.manual_edit,
        }
    }
}

impl Api {
    pub fn new(config: &ApiConfig, user_id: i64) -> Result<Self> {
        let client = Client::builder()
            .pool_max_idle_per_host(100) // Allow more idle connections per host
            .timeout(Duration::from_secs(30)) // Overall request timeout
            .connect_timeout(Duration::from_secs(10)) // Connection timeout
            .build()?;

        Ok(Self {
            client,
            base_url: config.url.trim_end_matches('/').to_string(),
            api_key: config.api_key.clone(),
            user_id,
        })
    }

    pub async fn update_sticker_text(&self, sticker_id: &str, text: &str) -> Result<Sticker> {
        let url = format!(
            "{}/stickers/{}",
            self.base_url,
            urlencoding::encode(sticker_id)
        );
        let payload = UpdateStickerRequest {
            user_id: self.user_id,
            text,
        };
        let mut last_err = None;

        for attempt in 0..=MAX_RETRIES {
            if attempt > 0 {
                let backoff = Duration::from_millis(INITIAL_BACKOFF_MS * 2u64.pow(attempt - 1));
                warn!(
                    "[api] update_sticker_text retry {}/{} after {:?}",
                    attempt, MAX_RETRIES, backoff
                );
                tokio::time::sleep(backoff).await;
            }

            match self
                .client
                .patch(&url)
                .header("X-API-Key", &self.api_key)
                .json(&payload)
                .send()
                .await
            {
                Ok(response) => {
                    if !response.status().is_success() {
                        let status = response.status();
                        if status.is_client_error() {
                            anyhow::bail!("API error: {}", status);
                        }
                        last_err = Some(anyhow::anyhow!("API error: {}", status));
                        continue;
                    }

                    let sticker: StickerResponse = response.json().await?;
                    debug!(
                        "[api] update_sticker_text success on attempt {}",
                        attempt + 1
                    );
                    return Ok(sticker.into());
                }
                Err(e) => {
                    last_err = Some(e.into());
                }
            }
        }

        Err(last_err.unwrap_or_else(|| anyhow::anyhow!("update_sticker_text failed after retries")))
    }

    pub async fn get_stickers_page(&self, limit: usize, offset: usize) -> Result<Vec<Sticker>> {
        let url = format!(
            "{}/stickers?user_id={}&limit={}&offset={}",
            self.base_url, self.user_id, limit, offset
        );

        self.fetch_stickers(&url).await
    }

    pub async fn health_check(&self) -> Result<()> {
        let url = format!(
            "{}/stickers?user_id={}&limit=1&offset=0",
            self.base_url, self.user_id
        );
        let response = self
            .client
            .get(url)
            .header("X-API-Key", &self.api_key)
            .timeout(Duration::from_secs(HEALTH_TIMEOUT_SECS))
            .send()
            .await?;

        if !response.status().is_success() {
            anyhow::bail!("API error: {}", response.status());
        }

        Ok(())
    }

    async fn fetch_stickers(&self, url: &str) -> Result<Vec<Sticker>> {
        let mut last_err = None;

        for attempt in 0..=MAX_RETRIES {
            if attempt > 0 {
                let backoff = Duration::from_millis(INITIAL_BACKOFF_MS * 2u64.pow(attempt - 1));
                warn!(
                    "[api] fetch_stickers retry {}/{} after {:?}",
                    attempt, MAX_RETRIES, backoff
                );
                tokio::time::sleep(backoff).await;
            }

            match self
                .client
                .get(url)
                .header("X-API-Key", &self.api_key)
                .send()
                .await
            {
                Ok(response) => {
                    if !response.status().is_success() {
                        let status = response.status();
                        // Don't retry client errors (4xx)
                        if status.is_client_error() {
                            anyhow::bail!("API error: {}", status);
                        }
                        last_err = Some(anyhow::anyhow!("API error: {}", status));
                        continue;
                    }
                    let items: Vec<StickerResponse> = response.json().await?;
                    debug!("[api] fetch_stickers success on attempt {}", attempt + 1);
                    return Ok(items.into_iter().map(Sticker::from).collect());
                }
                Err(e) => {
                    last_err = Some(e.into());
                }
            }
        }

        Err(last_err.unwrap_or_else(|| anyhow::anyhow!("fetch_stickers failed after retries")))
    }

    pub async fn get_thumbnail(&self, file_id: &str) -> Result<Option<Vec<u8>>> {
        let url = format!(
            "{}/thumbnails/{}",
            self.base_url,
            urlencoding::encode(file_id)
        );

        let mut last_err = None;

        for attempt in 0..=MAX_RETRIES {
            if attempt > 0 {
                let backoff = Duration::from_millis(INITIAL_BACKOFF_MS * 2u64.pow(attempt - 1));
                warn!(
                    "[api] get_thumbnail retry {}/{} after {:?}",
                    attempt, MAX_RETRIES, backoff
                );
                tokio::time::sleep(backoff).await;
            }

            match self
                .client
                .get(&url)
                .header("X-API-Key", &self.api_key)
                .send()
                .await
            {
                Ok(response) => {
                    if response.status().as_u16() == 404 {
                        return Ok(None);
                    }
                    if !response.status().is_success() {
                        let status = response.status();
                        if status.is_client_error() {
                            anyhow::bail!("API error: {}", status);
                        }
                        last_err = Some(anyhow::anyhow!("API error: {}", status));
                        continue;
                    }
                    let bytes = response.bytes().await?;
                    debug!("[api] get_thumbnail success on attempt {}", attempt + 1);
                    return Ok(Some(bytes.to_vec()));
                }
                Err(e) => {
                    last_err = Some(e.into());
                }
            }
        }

        Err(last_err.unwrap_or_else(|| anyhow::anyhow!("get_thumbnail failed after retries")))
    }
}
