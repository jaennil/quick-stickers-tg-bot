use anyhow::{anyhow, Result};
use grammers_client::types::{Attribute, Downloadable, Media};
use grammers_client::{Client, Config, FixedReconnect, InitParams, InputMessage, SignInError};
use grammers_session::Session;
use grammers_tl_types as tl;
use std::io::{self, BufRead, Write};
use std::path::Path;
use std::process::Command;
use std::time::Duration;
use tracing::{error, info};

use crate::config::TelegramConfig;
use crate::models::{ChatInfo, ChatType};

const SESSION_FILE: &str = "sticker_gui.session";
const TELEGRAM_RECONNECT_DELAY_SECS: u64 = 2;

// Keep the authenticated session alive across temporary proxy or network outages.
static TELEGRAM_RECONNECT_POLICY: FixedReconnect = FixedReconnect {
    attempts: usize::MAX,
    delay: Duration::from_secs(TELEGRAM_RECONNECT_DELAY_SECS),
};

pub struct TelegramClient {
    client: Client,
}

impl TelegramClient {
    pub async fn connect(config: &TelegramConfig, workdir: &Path) -> Result<Self> {
        let session_path = workdir.join(SESSION_FILE);
        println!("  [telegram] Session path: {:?}", session_path);

        let session = if session_path.exists()
            && std::fs::metadata(&session_path)
                .map(|m| m.len() > 0)
                .unwrap_or(false)
        {
            println!("  [telegram] Loading existing session...");
            let data = std::fs::read(&session_path)
                .map_err(|e| anyhow!("Failed to read session from {:?}: {}", session_path, e))?;
            Session::load(&data)
                .map_err(|e| anyhow!("Failed to parse session from {:?}: {}", session_path, e))?
        } else {
            println!("  [telegram] Creating new session...");
            Session::new()
        };

        println!("  [telegram] Connecting to Telegram servers...");
        let proxy_url = config.proxy_url();
        match &proxy_url {
            Some(proxy_url) => println!("  [telegram] Using SOCKS5 proxy: {proxy_url}"),
            None => println!("  [telegram] Proxy: direct"),
        }
        let client = Client::connect(Config {
            session,
            api_id: config.api_id,
            api_hash: config.api_hash.clone(),
            params: InitParams {
                proxy_url,
                reconnection_policy: &TELEGRAM_RECONNECT_POLICY,
                ..Default::default()
            },
        })
        .await
        .map_err(|e| anyhow!("Failed to connect to Telegram: {}", e))?;

        // Check if we need to sign in
        println!("  [telegram] Checking authorization...");
        if !client.is_authorized().await? {
            Self::sign_in(&client).await?;
        } else {
            println!("  [telegram] Already authorized");
        }

        // Save session
        println!("  [telegram] Saving session to {:?}...", session_path);

        // Try to get session data and write manually
        let session_data = client.session().save();
        std::fs::write(&session_path, &session_data)
            .map_err(|e| anyhow!("Failed to write session to {:?}: {}", session_path, e))?;

        println!("  [telegram] Connected successfully");
        Ok(Self { client })
    }

    async fn sign_in(client: &Client) -> Result<()> {
        println!("Telegram authorization required.");
        print!("Enter phone number: ");
        io::stdout().flush()?;

        let phone = io::stdin()
            .lock()
            .lines()
            .next()
            .ok_or_else(|| anyhow!("no input from stdin"))??;
        let token = client.request_login_code(&phone).await?;

        print!("Enter code: ");
        io::stdout().flush()?;

        let code = io::stdin()
            .lock()
            .lines()
            .next()
            .ok_or_else(|| anyhow!("no input from stdin"))??;

        match client.sign_in(&token, &code).await {
            Ok(_) => {}
            Err(SignInError::PasswordRequired(password_token)) => {
                print!("Enter 2FA password: ");
                io::stdout().flush()?;

                let password = io::stdin()
                    .lock()
                    .lines()
                    .next()
                    .ok_or_else(|| anyhow!("no input from stdin"))??;
                client
                    .check_password(password_token, password.trim())
                    .await?;
            }
            Err(e) => return Err(anyhow!("Sign in failed: {}", e)),
        }

        println!("Signed in successfully!");
        Ok(())
    }

    pub async fn get_dialogs(&self, limit: usize) -> Result<Vec<ChatInfo>> {
        let mut dialogs = self.client.iter_dialogs();
        let mut chats = Vec::new();

        while let Some(dialog) = dialogs.next().await? {
            if chats.len() >= limit {
                break;
            }

            let chat = dialog.chat();
            let chat_type = match chat {
                grammers_client::types::Chat::User(_) => ChatType::Private,
                grammers_client::types::Chat::Group(_) => ChatType::Group,
                grammers_client::types::Chat::Channel(_) => ChatType::Channel,
            };

            chats.push(ChatInfo {
                id: chat.id(),
                name: chat.name().to_string(),
                chat_type,
            });
        }

        // Log first 3 chats for testing dialog order
        info!("[get_dialogs] first 3 chats:");
        for (i, chat) in chats.iter().take(3).enumerate() {
            info!("[get_dialogs]   #{}: {} (id={})", i + 1, chat.name, chat.id);
        }

        Ok(chats)
    }

    pub async fn ping(&self) -> Result<()> {
        let ping_id = rand::random::<i64>();
        self.client
            .invoke(&tl::functions::Ping { ping_id })
            .await
            .map_err(|e| anyhow!("Telegram ping failed: {}", e))?;
        Ok(())
    }

    pub async fn send_sticker(&self, chat_id: i64, set_name: &str, document_id: i64) -> Result<()> {
        info!(
            "[send_sticker] chat_id={}, set_name={}, document_id={}",
            chat_id, set_name, document_id
        );

        // Get the chat by ID
        info!("[send_sticker] resolving chat...");
        let chat = self.resolve_chat(chat_id).await?;
        info!("[send_sticker] chat resolved: {}", chat.name());

        // Get sticker set
        info!("[send_sticker] getting sticker set: {}", set_name);
        let sticker_set = self
            .client
            .invoke(&tl::functions::messages::GetStickerSet {
                stickerset: tl::enums::InputStickerSet::ShortName(
                    tl::types::InputStickerSetShortName {
                        short_name: set_name.to_string(),
                    },
                ),
                hash: 0,
            })
            .await?;
        info!("[send_sticker] got sticker set");

        // Find the sticker in the set
        if let tl::enums::messages::StickerSet::Set(set) = sticker_set {
            let doc_count = set.documents.len();
            info!(
                "[send_sticker] set has {} documents, looking for document_id={}",
                doc_count, document_id
            );

            // Log all document IDs in the set
            let doc_ids: Vec<i64> = set
                .documents
                .iter()
                .filter_map(|doc| {
                    if let tl::enums::Document::Document(d) = doc {
                        Some(d.id)
                    } else {
                        None
                    }
                })
                .collect();
            info!("[send_sticker] available doc_ids: {:?}", doc_ids);

            for doc in set.documents {
                if let tl::enums::Document::Document(d) = doc {
                    if d.id == document_id {
                        info!(
                            "[send_sticker] found matching sticker (doc_id={}), sending...",
                            d.id
                        );
                        let input_doc = tl::types::InputDocument {
                            id: d.id,
                            access_hash: d.access_hash,
                            file_reference: d.file_reference.clone(),
                        };

                        self.client
                            .invoke(&tl::functions::messages::SendMedia {
                                silent: false,
                                background: false,
                                clear_draft: false,
                                noforwards: false,
                                update_stickersets_order: false,
                                invert_media: false,
                                peer: chat.pack().to_input_peer(),
                                reply_to: None,
                                media: tl::enums::InputMedia::Document(
                                    tl::types::InputMediaDocument {
                                        id: tl::enums::InputDocument::Document(input_doc),
                                        ttl_seconds: None,
                                        query: None,
                                        spoiler: false,
                                    },
                                ),
                                message: String::new(),
                                random_id: rand::random(),
                                reply_markup: None,
                                entities: None,
                                schedule_date: None,
                                send_as: None,
                                quick_reply_shortcut: None,
                                effect: None,
                            })
                            .await?;

                        info!("[send_sticker] sent successfully!");
                        return Ok(());
                    }
                }
            }
            error!(
                "[send_sticker] sticker not found in {} documents (document_id={})",
                doc_count, document_id
            );
        } else {
            error!("[send_sticker] unexpected sticker set response");
        }

        error!(
            "[send_sticker] failed: sticker not found in set: {} / {}",
            set_name, document_id
        );
        Err(anyhow!(
            "Sticker not found in set: {} / {}",
            set_name,
            document_id
        ))
    }

    pub async fn download_sticker(
        &self,
        set_name: &str,
        document_id: i64,
        path: &Path,
    ) -> Result<()> {
        let sticker_set = self
            .client
            .invoke(&tl::functions::messages::GetStickerSet {
                stickerset: tl::enums::InputStickerSet::ShortName(
                    tl::types::InputStickerSetShortName {
                        short_name: set_name.to_string(),
                    },
                ),
                hash: 0,
            })
            .await?;

        let tl::enums::messages::StickerSet::Set(set) = sticker_set else {
            return Err(anyhow!("Unexpected sticker set response"));
        };

        let document = set.documents.into_iter().find(|document| {
            matches!(document, tl::enums::Document::Document(doc) if doc.id == document_id)
        });
        let Some(document) = document else {
            return Err(anyhow!(
                "Sticker not found in set: {} / {}",
                set_name,
                document_id
            ));
        };

        let media = Media::from_raw(tl::enums::MessageMedia::Document(
            tl::types::MessageMediaDocument {
                nopremium: false,
                spoiler: false,
                video: false,
                round: false,
                voice: false,
                document: Some(document),
                alt_document: None,
                ttl_seconds: None,
            },
        ))
        .ok_or_else(|| anyhow!("Sticker document cannot be downloaded"))?;
        self.client
            .download_media(&Downloadable::Media(media), path)
            .await
            .map_err(|error| anyhow!("Failed to download sticker: {}", error))
    }

    pub async fn send_photo_file(&self, chat_id: i64, path: &Path) -> Result<()> {
        info!("[send_photo_file] chat_id={}, path={:?}", chat_id, path);

        info!("[send_photo_file] resolving chat...");
        let chat = self.resolve_chat(chat_id).await?;
        info!("[send_photo_file] chat resolved: {}", chat.name());

        let uploaded = self
            .client
            .upload_file(path)
            .await
            .map_err(|e| anyhow!("Failed to upload image {:?}: {}", path, e))?;

        self.client
            .send_message(&chat, InputMessage::text("").photo(uploaded))
            .await?;

        info!("[send_photo_file] sent successfully");
        Ok(())
    }

    pub async fn send_video_file(&self, chat_id: i64, path: &Path) -> Result<()> {
        let chat = self.resolve_chat(chat_id).await?;
        let uploaded = self
            .client
            .upload_file(path)
            .await
            .map_err(|e| anyhow!("Failed to upload video {:?}: {}", path, e))?;
        let (width, height, duration) = probe_video(path)?;
        let message = InputMessage::text("")
            .mime_type("video/mp4")
            .document(uploaded)
            .attribute(Attribute::Video {
                round_message: false,
                supports_streaming: true,
                duration: Duration::from_secs_f64(duration),
                w: width,
                h: height,
            });
        self.client.send_message(&chat, message).await?;
        Ok(())
    }

    pub async fn send_gif_file(&self, chat_id: i64, path: &Path) -> Result<()> {
        let chat = self.resolve_chat(chat_id).await?;
        let uploaded = self
            .client
            .upload_file(path)
            .await
            .map_err(|e| anyhow!("Failed to upload GIF {:?}: {}", path, e))?;
        let message = InputMessage::text("")
            .mime_type("image/gif")
            .document(uploaded);
        self.client.send_message(&chat, message).await?;
        Ok(())
    }

    async fn resolve_chat(&self, chat_id: i64) -> Result<grammers_client::types::Chat> {
        // Try to find in dialogs
        info!("[resolve_chat] looking for chat_id={}", chat_id);
        let mut dialogs = self.client.iter_dialogs();
        while let Some(dialog) = dialogs.next().await? {
            if dialog.chat().id() == chat_id {
                info!("[resolve_chat] found chat: {}", dialog.chat().name());
                return Ok(dialog.chat().clone());
            }
        }

        error!("[resolve_chat] chat not found: {}", chat_id);
        Err(anyhow!("Chat not found: {}", chat_id))
    }
}

fn probe_video(path: &Path) -> Result<(i32, i32, f64)> {
    let output = Command::new("ffprobe")
        .args([
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height,duration",
            "-of",
            "csv=p=0",
        ])
        .arg(path)
        .output()
        .map_err(|error| anyhow!("Failed to run ffprobe: {}", error))?;
    if !output.status.success() {
        return Err(anyhow!("ffprobe could not read video metadata"));
    }
    let metadata = String::from_utf8_lossy(&output.stdout);
    let mut fields = metadata.trim().split(',');
    let width = fields
        .next()
        .and_then(|value| value.parse().ok())
        .ok_or_else(|| anyhow!("Missing video width"))?;
    let height = fields
        .next()
        .and_then(|value| value.parse().ok())
        .ok_or_else(|| anyhow!("Missing video height"))?;
    let duration = fields
        .next()
        .and_then(|value| value.parse().ok())
        .ok_or_else(|| anyhow!("Missing video duration"))?;
    Ok((width, height, duration))
}

#[cfg(test)]
mod tests {
    use std::ops::ControlFlow;

    use grammers_client::ReconnectionPolicy;

    use super::{TELEGRAM_RECONNECT_DELAY_SECS, TELEGRAM_RECONNECT_POLICY};

    #[test]
    fn reconnect_policy_never_gives_up() {
        assert!(matches!(
            TELEGRAM_RECONNECT_POLICY.should_retry(usize::MAX),
            ControlFlow::Continue(delay) if delay.as_secs() == TELEGRAM_RECONNECT_DELAY_SECS
        ));
    }
}
