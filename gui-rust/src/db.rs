use anyhow::Result;
use sqlx::postgres::PgPoolOptions;
use sqlx::PgPool;

use crate::config::DatabaseConfig;
use crate::models::Sticker;

pub struct Database {
    pool: PgPool,
    user_id: i64,
}

impl Database {
    pub async fn connect(config: &DatabaseConfig, user_id: i64) -> Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(5)
            .connect(&config.connection_string())
            .await?;

        Ok(Self { pool, user_id })
    }

    pub async fn search_stickers(&self, query: &str) -> Result<Vec<Sticker>> {
        let pattern = format!("%{}%", query);

        let rows = sqlx::query_as::<_, StickerRow>(
            r#"
            SELECT sticker_id, file_id, document_id, text, set_name, emoji
            FROM stickers
            WHERE user_id = $1 AND text ILIKE $2
            LIMIT 20
            "#,
        )
        .bind(self.user_id)
        .bind(&pattern)
        .fetch_all(&self.pool)
        .await?;

        Ok(rows
            .into_iter()
            .map(|r| Sticker {
                sticker_id: r.sticker_id,
                file_id: r.file_id,
                document_id: r.document_id.unwrap_or(0),
                text: r.text.unwrap_or_default(),
                set_name: r.set_name.unwrap_or_default(),
                emoji: r.emoji.unwrap_or_default(),
            })
            .collect())
    }

    pub async fn get_thumbnail(&self, file_id: &str) -> Result<Option<Vec<u8>>> {
        let row = sqlx::query_scalar::<_, Vec<u8>>(
            "SELECT thumbnail FROM sticker_thumbnails WHERE file_id = $1",
        )
        .bind(file_id)
        .fetch_optional(&self.pool)
        .await?;

        Ok(row)
    }
}

#[derive(sqlx::FromRow)]
struct StickerRow {
    sticker_id: String,
    file_id: String,
    document_id: Option<i64>,
    text: Option<String>,
    set_name: Option<String>,
    emoji: Option<String>,
}
