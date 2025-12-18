# Sticker Search GUI (egui)

Desktop client for searching and sending stickers via Telegram.

## Dependencies

### Ubuntu/Debian
```bash
sudo apt install libssl-dev pkg-config libxdo-dev
```

### Fedora
```bash
sudo dnf install openssl-devel pkg-config libxdo-devel
```

### Arch
```bash
sudo pacman -S openssl pkg-config xdotool
```

## Build

```bash
cargo build --release
```

## Run

```bash
cargo run --release
```

## Configuration

Create `config.yaml`:

```yaml
telegram:
  api_id: YOUR_API_ID
  api_hash: "YOUR_API_HASH"

api:
  url: "https://k8s.ru.tuna.am/bot-api"
  api_key: "YOUR_API_KEY"

hotkey: "<ctrl>+<shift>+s"

user_id: YOUR_TELEGRAM_USER_ID
```

Get `api_id` and `api_hash` from https://my.telegram.org
