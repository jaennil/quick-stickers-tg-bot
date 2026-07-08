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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proxy_url: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct ApiConfig {
    pub url: String,
    pub api_key: String,
}

fn default_hotkey() -> String {
    "ctrl+shift+s".to_string()
}

fn normalize_socks5_proxy(value: &str) -> Option<String> {
    let value = value.trim();
    if value.is_empty() {
        return None;
    }

    if value.to_lowercase().starts_with("socks5://") {
        return Some(value.to_string());
    }

    None
}

fn env_socks5_proxy_url() -> Option<String> {
    [
        "QSG_TELEGRAM_PROXY",
        "ALL_PROXY",
        "all_proxy",
        "HTTPS_PROXY",
        "https_proxy",
        "HTTP_PROXY",
        "http_proxy",
    ]
    .into_iter()
    .filter_map(|key| std::env::var(key).ok())
    .find_map(|value| normalize_socks5_proxy(&value))
}

impl TelegramConfig {
    pub fn proxy_url(&self) -> Option<String> {
        self.proxy_url
            .as_deref()
            .and_then(normalize_socks5_proxy)
            .or_else(env_socks5_proxy_url)
    }
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
        let default_proxy = env_socks5_proxy_url();
        let proxy_url =
            prompt_optional_string("Telegram SOCKS5 proxy URL", default_proxy.as_deref())?;
        let api_url = prompt_required_string("Sticker API URL", Some(DEFAULT_API_URL))?;
        let api_key = prompt_optional_string("Sticker API key", None)?;
        let hotkey = prompt_required_string("Hotkey", Some(&default_hotkey()))?;
        let user_id = prompt_required_parse::<i64>("Telegram user_id", None)?;

        Ok(Self {
            telegram: TelegramConfig {
                api_id,
                api_hash,
                proxy_url: normalize_socks5_proxy(&proxy_url),
            },
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

#[cfg(test)]
mod tests {
    use super::{normalize_socks5_proxy, Config};

    #[test]
    fn config_without_proxy_stays_valid() {
        let config: Config = serde_yaml::from_str(
            r#"
telegram:
  api_id: 123
  api_hash: hash
api:
  url: https://example.test/api
  api_key: ""
hotkey: ctrl+shift+s
user_id: 42
"#,
        )
        .unwrap();

        assert_eq!(config.telegram.api_id, 123);
        assert_eq!(config.telegram.proxy_url, None);
    }

    #[test]
    fn config_with_proxy_is_loaded() {
        let config: Config = serde_yaml::from_str(
            r#"
telegram:
  api_id: 123
  api_hash: hash
  proxy_url: socks5://127.0.0.1:1080
api:
  url: https://example.test/api
  api_key: ""
hotkey: ctrl+shift+s
user_id: 42
"#,
        )
        .unwrap();

        assert_eq!(
            config.telegram.proxy_url.as_deref(),
            Some("socks5://127.0.0.1:1080")
        );
    }

    #[test]
    fn only_socks5_proxy_urls_are_normalized() {
        assert_eq!(
            normalize_socks5_proxy(" socks5://127.0.0.1:1080 "),
            Some("socks5://127.0.0.1:1080".to_string())
        );
        assert_eq!(normalize_socks5_proxy("http://127.0.0.1:8080"), None);
        assert_eq!(normalize_socks5_proxy(""), None);
    }
}
