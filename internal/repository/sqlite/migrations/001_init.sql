-- +goose Up
CREATE TABLE IF NOT EXISTS stickers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    sticker_id TEXT NOT NULL,
    set_name TEXT,
    file_id TEXT NOT NULL,
    text TEXT,
    emoji TEXT,
    UNIQUE(user_id, sticker_id)
);

CREATE INDEX IF NOT EXISTS idx_stickers_user_id ON stickers(user_id);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id INTEGER PRIMARY KEY,
    ocr_engine TEXT DEFAULT 'paddle'
);

-- +goose Down
DROP TABLE IF EXISTS user_settings;
DROP INDEX IF EXISTS idx_stickers_user_id;
DROP TABLE IF EXISTS stickers;
