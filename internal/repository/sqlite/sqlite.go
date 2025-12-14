package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite/migrations"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

type Repository struct {
	db *sql.DB
}

func New(dbPath string) (*Repository, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	goose.SetBaseFS(embedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}
	migrations.Register()
	if err := goose.Up(db, "migrations"); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	r := &Repository{db: db}
	r.updateTextLower()

	return r, nil
}

func (r *Repository) updateTextLower() {
	rows, err := r.db.Query("SELECT id, text FROM stickers WHERE text_lower IS NULL AND text IS NOT NULL AND text != ''")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var text string
		if rows.Scan(&id, &text) == nil {
			r.db.Exec("UPDATE stickers SET text_lower = ? WHERE id = ?", strings.ToLower(text), id)
		}
	}
}

func (r *Repository) GetUserOCREngine(userID int64) string {
	var engine string
	err := r.db.QueryRow("SELECT ocr_engine FROM user_settings WHERE user_id = ?", userID).Scan(&engine)
	if err != nil {
		return "paddle"
	}
	return engine
}

func (r *Repository) SetUserOCREngine(userID int64, engine string) error {
	query := `
	INSERT INTO user_settings (user_id, ocr_engine) VALUES (?, ?)
	ON CONFLICT(user_id) DO UPDATE SET ocr_engine = excluded.ocr_engine
	`
	_, err := r.db.Exec(query, userID, engine)
	return err
}

func (r *Repository) SaveSticker(sticker *repository.Sticker) error {
	textLower := strings.ToLower(sticker.Text)
	query := `
	INSERT INTO stickers (user_id, sticker_id, set_name, file_id, text, text_lower, emoji)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, sticker_id) DO UPDATE SET
		text = excluded.text,
		text_lower = excluded.text_lower,
		emoji = excluded.emoji
	`
	_, err := r.db.Exec(query, sticker.UserID, sticker.StickerID, sticker.SetName, sticker.FileID, sticker.Text, textLower, sticker.Emoji)
	return err
}

func (r *Repository) SearchByText(userID int64, query string) ([]*repository.Sticker, error) {
	queryLower := strings.ToLower(query)
	sqlQuery := `
	SELECT id, user_id, sticker_id, set_name, file_id, text, emoji
	FROM stickers
	WHERE user_id = ? AND text_lower LIKE ?
	LIMIT 50
	`
	rows, err := r.db.Query(sqlQuery, userID, "%"+queryLower+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*repository.Sticker
	for rows.Next() {
		var st repository.Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (r *Repository) GetUserStickerCount(userID int64) (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM stickers WHERE user_id = ?", userID).Scan(&count)
	return count, err
}

func (r *Repository) GetUserStickers(userID int64, limit, offset int) ([]*repository.Sticker, error) {
	query := `
	SELECT id, user_id, sticker_id, set_name, file_id, text, emoji
	FROM stickers
	WHERE user_id = ?
	ORDER BY id DESC
	LIMIT ? OFFSET ?
	`
	rows, err := r.db.Query(query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*repository.Sticker
	for rows.Next() {
		var st repository.Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (r *Repository) UpdateStickerText(userID int64, stickerID string, text string) error {
	textLower := strings.ToLower(text)
	query := `UPDATE stickers SET text = ?, text_lower = ? WHERE user_id = ? AND sticker_id = ?`
	_, err := r.db.Exec(query, text, textLower, userID, stickerID)
	return err
}

func (r *Repository) Close() error {
	return r.db.Close()
}
