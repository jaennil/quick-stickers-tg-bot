use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::io::{self, IsTerminal, Write};
use std::path::{Path, PathBuf};

const DEFAULT_API_URL: &str = "https://sb.dubrovskih.ru/api";

#[derive(Debug, Deserialize, Serialize)]
pub struct Config {
    pub telegram: TelegramConfig,
    pub api: ApiConfig,
    #[serde(default = "default_hotkey")]
    pub hotkey: String,
    pub user_id: i64,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct TelegramConfig {
    pub api_id: i32,
    pub api_hash: String,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct ApiConfig {
    pub url: String,
    pub api_key: String,
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

    pub fn ensure_file<P: AsRef<Path>>(path: P) -> Result<PathBuf> {
        let path = path.as_ref();

        if path.exists() {
            return Ok(path.to_path_buf());
        }

        if !io::stdin().is_terminal() {
            anyhow::bail!(
                "Config file {:?} does not exist and qsg is not running in an interactive terminal.",
                path
            );
        }

        println!(
            "[bootstrap] Config file {:?} not found. Creating it interactively.",
            path
        );
        let config = Self::prompt()?;
        config.save(path)?;
        println!("[bootstrap] Wrote config to {:?}", path);

        Ok(path.to_path_buf())
    }

    fn prompt() -> Result<Self> {
        println!("Enter qsg settings:");

        let api_id = prompt_required_parse::<i32>("Telegram api_id", None)?;
        let api_hash = prompt_required_string("Telegram api_hash", None)?;
        let api_url = prompt_required_string("Sticker API URL", Some(DEFAULT_API_URL))?;
        let api_key = prompt_optional_string("Sticker API key", None)?;
        let hotkey = prompt_required_string("Hotkey", Some(&default_hotkey()))?;
        let user_id = prompt_required_parse::<i64>("Telegram user_id", None)?;

        Ok(Self {
            telegram: TelegramConfig { api_id, api_hash },
            api: ApiConfig {
                url: api_url,
                api_key,
            },
            hotkey,
            user_id,
        })
    }

    fn save<P: AsRef<Path>>(&self, path: P) -> Result<()> {
        let content = serde_yaml::to_string(self)?;
        std::fs::write(&path, content)
            .with_context(|| format!("failed to write config to {:?}", path.as_ref()))?;
        Ok(())
    }
}

fn prompt_required_string(label: &str, default: Option<&str>) -> Result<String> {
    loop {
        let value = prompt_line(label, default)?;
        if !value.trim().is_empty() {
            return Ok(value);
        }
        println!("Value is required.");
    }
}

fn prompt_optional_string(label: &str, default: Option<&str>) -> Result<String> {
    prompt_line(label, default)
}

fn prompt_required_parse<T>(label: &str, default: Option<&str>) -> Result<T>
where
    T: std::str::FromStr,
    T::Err: std::fmt::Display,
{
    loop {
        let raw = prompt_required_string(label, default)?;
        match raw.parse::<T>() {
            Ok(value) => return Ok(value),
            Err(err) => println!("Invalid value: {err}"),
        }
    }
}

fn prompt_line(label: &str, default: Option<&str>) -> Result<String> {
    let mut stdout = io::stdout();
    match default {
        Some(default) => write!(stdout, "{label} [{default}]: ")?,
        None => write!(stdout, "{label}: ")?,
    }
    stdout.flush()?;

    let mut input = String::new();
    io::stdin().read_line(&mut input)?;
    let trimmed = input.trim().to_string();

    if trimmed.is_empty() {
        return Ok(default.unwrap_or_default().to_string());
    }

    Ok(trimmed)
}
