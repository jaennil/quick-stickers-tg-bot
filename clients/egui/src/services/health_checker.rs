use std::sync::mpsc::{self, Receiver};
use std::sync::Arc;
use std::time::Duration;

use tokio::runtime::Runtime;
use tracing::warn;

use crate::api::Api;
use crate::telegram::TelegramClient;

const HEALTH_CHECK_INTERVAL_SECS: u64 = 15;
const HEALTH_CHECK_TIMEOUT_SECS: u64 = 5;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HealthTarget {
    Api,
    Telegram,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HealthState {
    Checking,
    Online,
    Offline(String),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HealthResult {
    pub target: HealthTarget,
    pub state: HealthState,
}

pub struct HealthChecker {
    result_rx: Receiver<HealthResult>,
}

impl HealthChecker {
    pub fn start(rt: Arc<Runtime>, api: Arc<Api>, telegram: Arc<TelegramClient>) -> Self {
        let (result_tx, result_rx) = mpsc::channel::<HealthResult>();

        {
            let api = api.clone();
            let result_tx = result_tx.clone();
            rt.spawn(async move {
                loop {
                    let state = match api.health_check().await {
                        Ok(()) => HealthState::Online,
                        Err(e) => {
                            warn!("[health] API check failed: {}", e);
                            HealthState::Offline(short_error(&e.to_string()))
                        }
                    };

                    if result_tx
                        .send(HealthResult {
                            target: HealthTarget::Api,
                            state,
                        })
                        .is_err()
                    {
                        break;
                    }

                    tokio::time::sleep(Duration::from_secs(HEALTH_CHECK_INTERVAL_SECS)).await;
                }
            });
        }

        rt.spawn(async move {
            loop {
                let state = match tokio::time::timeout(
                    Duration::from_secs(HEALTH_CHECK_TIMEOUT_SECS),
                    telegram.ping(),
                )
                .await
                {
                    Ok(Ok(())) => HealthState::Online,
                    Ok(Err(e)) => {
                        warn!("[health] Telegram check failed: {}", e);
                        HealthState::Offline(short_error(&e.to_string()))
                    }
                    Err(_) => {
                        warn!("[health] Telegram check timed out");
                        HealthState::Offline("timeout".into())
                    }
                };

                if result_tx
                    .send(HealthResult {
                        target: HealthTarget::Telegram,
                        state,
                    })
                    .is_err()
                {
                    break;
                }

                tokio::time::sleep(Duration::from_secs(HEALTH_CHECK_INTERVAL_SECS)).await;
            }
        });

        Self { result_rx }
    }

    pub fn try_recv(&self) -> Option<HealthResult> {
        self.result_rx.try_recv().ok()
    }
}

fn short_error(error: &str) -> String {
    let lower = error.to_lowercase();

    if lower.contains("read 0 bytes")
        || lower.contains("io failed")
        || lower.contains("read error")
        || lower.contains("connection reset")
        || lower.contains("connection closed")
    {
        return "connection dropped".into();
    }

    if lower.contains("timed out") || lower.contains("timeout") {
        return "timeout".into();
    }

    if lower.contains("dns") || lower.contains("resolve") {
        return "dns failed".into();
    }

    error.chars().take(80).collect()
}

#[cfg(test)]
mod tests {
    use super::short_error;

    #[test]
    fn shortens_common_network_errors() {
        assert_eq!(
            short_error("request error: read error, IO failed: read 0 bytes"),
            "connection dropped"
        );
        assert_eq!(short_error("operation timed out"), "timeout");
    }
}
