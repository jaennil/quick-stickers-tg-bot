package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Sticker struct {
	ID          int64
	UserID      int64
	StickerID   string
	SetName     string
	FileID      string
	Text        string
	Emoji       string
}

type Storage struct {
	db *sql.DB
}

func New(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return s, nil
}

func (s *Storage) migrate() error {
	query := `
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
	CREATE INDEX IF NOT EXISTS idx_stickers_text ON stickers(text);
	`
	_, err := s.db.Exec(query)
	return err
}

func (s *Storage) SaveSticker(sticker *Sticker) error {
	query := `
	INSERT INTO stickers (user_id, sticker_id, set_name, file_id, text, emoji)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, sticker_id) DO UPDATE SET
		text = excluded.text,
		emoji = excluded.emoji
	`
	_, err := s.db.Exec(query, sticker.UserID, sticker.StickerID, sticker.SetName, sticker.FileID, sticker.Text, sticker.Emoji)
	return err
}

func (s *Storage) SearchByText(userID int64, query string) ([]*Sticker, error) {
	sqlQuery := `
	SELECT id, user_id, sticker_id, set_name, file_id, text, emoji
	FROM stickers
	WHERE user_id = ? AND text LIKE ?
	LIMIT 50
	`
	rows, err := s.db.Query(sqlQuery, userID, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*Sticker
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (s *Storage) GetUserStickerCount(userID int64) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM stickers WHERE user_id = ?", userID).Scan(&count)
	return count, err
}

func (s *Storage) UpdateStickerText(userID int64, stickerID string, text string) error {
	query := `UPDATE stickers SET text = ? WHERE user_id = ? AND sticker_id = ?`
	_, err := s.db.Exec(query, text, userID, stickerID)
	return err
}

func (s *Storage) Close() error {
	return s.db.Close()
}
