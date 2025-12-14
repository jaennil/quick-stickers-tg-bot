package storage

import (
	"database/sql"
	"fmt"
	"strings"

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
	// Создаём таблицы
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

	CREATE TABLE IF NOT EXISTS user_settings (
		user_id INTEGER PRIMARY KEY,
		ocr_engine TEXT DEFAULT 'paddle'
	);
	`
	if _, err := s.db.Exec(query); err != nil {
		return err
	}

	// Проверяем есть ли колонка text_lower
	var hasTextLower bool
	rows, err := s.db.Query("PRAGMA table_info(stickers)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "text_lower" {
			hasTextLower = true
			break
		}
	}
	rows.Close()

	// Добавляем колонку text_lower если её нет
	if !hasTextLower {
		if _, err := s.db.Exec("ALTER TABLE stickers ADD COLUMN text_lower TEXT"); err != nil {
			return err
		}
		// Создаём индекс
		s.db.Exec("CREATE INDEX IF NOT EXISTS idx_stickers_text_lower ON stickers(text_lower)")
	}

	// Обновляем text_lower для существующих записей (в Go для поддержки кириллицы)
	updateRows, err := s.db.Query("SELECT id, text FROM stickers WHERE text_lower IS NULL AND text IS NOT NULL AND text != ''")
	if err == nil {
		defer updateRows.Close()
		for updateRows.Next() {
			var id int64
			var text string
			if updateRows.Scan(&id, &text) == nil {
				s.db.Exec("UPDATE stickers SET text_lower = ? WHERE id = ?", strings.ToLower(text), id)
			}
		}
	}

	return nil
}

func (s *Storage) GetUserOCREngine(userID int64) string {
	var engine string
	err := s.db.QueryRow("SELECT ocr_engine FROM user_settings WHERE user_id = ?", userID).Scan(&engine)
	if err != nil {
		return "paddle" // default
	}
	return engine
}

func (s *Storage) SetUserOCREngine(userID int64, engine string) error {
	query := `
	INSERT INTO user_settings (user_id, ocr_engine) VALUES (?, ?)
	ON CONFLICT(user_id) DO UPDATE SET ocr_engine = excluded.ocr_engine
	`
	_, err := s.db.Exec(query, userID, engine)
	return err
}

func (s *Storage) SaveSticker(sticker *Sticker) error {
	textLower := strings.ToLower(sticker.Text)
	query := `
	INSERT INTO stickers (user_id, sticker_id, set_name, file_id, text, text_lower, emoji)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, sticker_id) DO UPDATE SET
		text = excluded.text,
		text_lower = excluded.text_lower,
		emoji = excluded.emoji
	`
	_, err := s.db.Exec(query, sticker.UserID, sticker.StickerID, sticker.SetName, sticker.FileID, sticker.Text, textLower, sticker.Emoji)
	return err
}

func (s *Storage) SearchByText(userID int64, query string) ([]*Sticker, error) {
	queryLower := strings.ToLower(query)
	sqlQuery := `
	SELECT id, user_id, sticker_id, set_name, file_id, text, emoji
	FROM stickers
	WHERE user_id = ? AND text_lower LIKE ?
	LIMIT 50
	`
	rows, err := s.db.Query(sqlQuery, userID, "%"+queryLower+"%")
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

func (s *Storage) GetUserStickers(userID int64, limit, offset int) ([]*Sticker, error) {
	query := `
	SELECT id, user_id, sticker_id, set_name, file_id, text, emoji
	FROM stickers
	WHERE user_id = ?
	ORDER BY id DESC
	LIMIT ? OFFSET ?
	`
	rows, err := s.db.Query(query, userID, limit, offset)
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

func (s *Storage) UpdateStickerText(userID int64, stickerID string, text string) error {
	textLower := strings.ToLower(text)
	query := `UPDATE stickers SET text = ?, text_lower = ? WHERE user_id = ? AND sticker_id = ?`
	_, err := s.db.Exec(query, text, textLower, userID, stickerID)
	return err
}

func (s *Storage) Close() error {
	return s.db.Close()
}
