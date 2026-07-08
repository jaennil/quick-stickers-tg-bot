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

After `make install`, `qsg` stores config in `~/.config/qsg/config.yaml`.
On first run it will:
- look for `~/.config/qsg/config.yaml`
- if it is missing, ask for the required values in the terminal and create the file automatically

## Configuration

Create `config.yaml`:

```yaml
telegram:
  api_id: YOUR_API_ID
  api_hash: "YOUR_API_HASH"
  # Optional. Telegram MTProto supports SOCKS5 here; HTTP proxy is not supported.
  proxy_url: "socks5://127.0.0.1:1080"

api:
  url: "https://sb.dubrovskih.ru/api"
  # Leave empty if the backend is not protected by X-API-Key.
  api_key: ""

hotkey: "<ctrl>+<shift>+s"

user_id: YOUR_TELEGRAM_USER_ID
```

Get `api_id` and `api_hash` from https://my.telegram.org

If `telegram.proxy_url` is omitted, qsg also checks `QSG_TELEGRAM_PROXY`,
`ALL_PROXY`, `HTTPS_PROXY`, and `HTTP_PROXY`, but only uses values starting with
`socks5://`.

The shared homelab xray service is internal to Kubernetes. For local qsg usage,
expose its SOCKS port first, for example:

```sh
kubectl -n proxy port-forward svc/xray 1080:1080
```

Then use `socks5://127.0.0.1:1080` as `telegram.proxy_url`.

## Offline mode

The client stores sticker metadata and downloaded thumbnails in `~/.cache/qsg`.
Search, pack filters, sorting, and copying cached images continue to work when the
Sticker API is unavailable. The metadata catalog is refreshed atomically after a
complete successful API sync, so an interrupted refresh keeps the previous catalog.
