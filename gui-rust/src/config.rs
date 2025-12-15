use anyhow::Result;
use serde::Deserialize;
use std::path::Path;

#[derive(Debug, Deserialize)]
pub struct Config {
    pub telegram: TelegramConfig,
    pub database: DatabaseConfig,
    #[serde(default = "default_hotkey")]
    pub hotkey: String,
    pub user_id: i64,
}

#[derive(Debug, Deserialize)]
pub struct TelegramConfig {
    pub api_id: i32,
    pub api_hash: String,
}

#[derive(Debug, Deserialize)]
pub struct DatabaseConfig {
    pub host: String,
    pub port: u16,
    pub user: String,
    pub password: String,
    pub dbname: String,
}

fn default_hotkey() -> String {
    "ctrl+shift+s".to_string()
}

impl Config {
    pub fn load<P: AsRef<Path>>(path: P) -> Result<Self> {
        let content = std::fs::read_to_string(path)?;
        let config: Config = serde_yaml::from_str(&content)?;
        Ok(config)
    }
}

impl DatabaseConfig {
    pub fn connection_string(&self) -> String {
        format!(
            "postgres://{}:{}@{}:{}/{}",
            self.user, self.password, self.host, self.port, self.dbname
        )
    }
}
