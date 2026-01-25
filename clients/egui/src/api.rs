use anyhow::Result;
use reqwest::Client;
use serde::Deserialize;
use std::time::Duration;

use crate::config::ApiConfig;
use crate::models::Sticker;

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
    text: String,
    set_name: String,
    emoji: String,
}

impl From<StickerResponse> for Sticker {
    fn from(r: StickerResponse) -> Self {
        Sticker {
            sticker_id: r.sticker_id,
            file_id: r.file_id,
            document_id: r.document_id,
            text: r.text,
            set_name: r.set_name,
            emoji: r.emoji,
        }
    }
}

impl Api {
    pub fn new(config: &ApiConfig, user_id: i64) -> Result<Self> {
        let client = Client::builder()
            .pool_max_idle_per_host(100)                    // Allow more idle connections per host
            .timeout(Duration::from_secs(30))               // Overall request timeout
            .connect_timeout(Duration::from_secs(10))       // Connection timeout
            .build()?;

        Ok(Self {
            client,
            base_url: config.url.trim_end_matches('/').to_string(),
            api_key: config.api_key.clone(),
            user_id,
        })
    }

    pub async fn search_stickers(&self, query: &str) -> Result<Vec<Sticker>> {
        let url = format!(
            "{}/stickers?user_id={}&query={}",
            self.base_url,
            self.user_id,
            urlencoding::encode(query)
        );

        self.fetch_stickers(&url).await
    }

    pub async fn get_stickers_page(&self, limit: usize, offset: usize) -> Result<Vec<Sticker>> {
        let url = format!(
            "{}/stickers?user_id={}&limit={}&offset={}",
            self.base_url, self.user_id, limit, offset
        );

        self.fetch_stickers(&url).await
    }

    async fn fetch_stickers(&self, url: &str) -> Result<Vec<Sticker>> {
        let response = self
            .client
            .get(url)
            .header("X-API-Key", &self.api_key)
            .send()
            .await?;

        if !response.status().is_success() {
            anyhow::bail!("API error: {}", response.status());
        }

        let items: Vec<StickerResponse> = response.json().await?;
        Ok(items.into_iter().map(Sticker::from).collect())
    }

    pub async fn get_thumbnail(&self, file_id: &str) -> Result<Option<Vec<u8>>> {
        let url = format!(
            "{}/thumbnails/{}",
            self.base_url,
            urlencoding::encode(file_id)
        );

        let response = self
            .client
            .get(&url)
            .header("X-API-Key", &self.api_key)
            .send()
            .await?;

        if response.status().as_u16() == 404 {
            return Ok(None);
        }

        if !response.status().is_success() {
            anyhow::bail!("API error: {}", response.status());
        }

        let bytes = response.bytes().await?;
        Ok(Some(bytes.to_vec()))
    }
}
