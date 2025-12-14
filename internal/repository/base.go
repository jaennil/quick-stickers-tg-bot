package repository

import (
	"strings"

	"github.com/jmoiron/sqlx"
)

type BaseRepository struct {
	db *sqlx.DB
}

func NewBase(db *sqlx.DB) *BaseRepository {
	r := &BaseRepository{db: db}
	r.updateTextLower()
	return r
}

func (r *BaseRepository) updateTextLower() {
	rows, err := r.db.Query("SELECT id, text FROM stickers WHERE text_lower IS NULL AND text IS NOT NULL AND text != ''")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var text string
		if rows.Scan(&id, &text) == nil {
			r.db.Exec(r.db.Rebind("UPDATE stickers SET text_lower = ? WHERE id = ?"), strings.ToLower(text), id)
		}
	}
}

func (r *BaseRepository) GetUserOCREngine(userID int64) string {
	var engine string
	err := r.db.Get(&engine, r.db.Rebind("SELECT ocr_engine FROM user_settings WHERE user_id = ?"), userID)
	if err != nil {
		return "api"
	}
	return engine
}

func (r *BaseRepository) SetUserOCREngine(userID int64, engine string) error {
	query := r.db.Rebind(`
		INSERT INTO user_settings (user_id, ocr_engine) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET ocr_engine = EXCLUDED.ocr_engine
	`)
	_, err := r.db.Exec(query, userID, engine)
	return err
}

func (r *BaseRepository) SaveSticker(sticker *Sticker) error {
	textLower := strings.ToLower(sticker.Text)
	query := r.db.Rebind(`
		INSERT INTO stickers (user_id, sticker_id, set_name, file_id, text, text_lower, emoji, ocr_engine)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, sticker_id) DO UPDATE SET
			text = EXCLUDED.text,
			text_lower = EXCLUDED.text_lower,
			emoji = EXCLUDED.emoji,
			ocr_engine = EXCLUDED.ocr_engine
	`)
	_, err := r.db.Exec(query, sticker.UserID, sticker.StickerID, sticker.SetName, sticker.FileID, sticker.Text, textLower, sticker.Emoji, sticker.OCREngine)
	return err
}

func (r *BaseRepository) SearchByText(userID int64, query string) ([]*Sticker, error) {
	queryLower := strings.ToLower(query)
	sqlQuery := r.db.Rebind(`
		SELECT id, user_id, sticker_id, set_name, file_id, text, emoji, ocr_engine, manual_edit
		FROM stickers
		WHERE user_id = ? AND text_lower LIKE ?
		LIMIT 50
	`)
	rows, err := r.db.Query(sqlQuery, userID, "%"+queryLower+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*Sticker
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji, &st.OCREngine, &st.ManualEdit); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (r *BaseRepository) GetUserStickerCount(userID int64) (int, error) {
	var count int
	err := r.db.Get(&count, r.db.Rebind("SELECT COUNT(*) FROM stickers WHERE user_id = ?"), userID)
	return count, err
}

func (r *BaseRepository) GetUserStickers(userID int64, limit, offset int) ([]*Sticker, error) {
	query := r.db.Rebind(`
		SELECT id, user_id, sticker_id, set_name, file_id, text, emoji, ocr_engine, manual_edit
		FROM stickers
		WHERE user_id = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`)
	rows, err := r.db.Query(query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*Sticker
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji, &st.OCREngine, &st.ManualEdit); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (r *BaseRepository) GetStickersBySetName(userID int64, setName string) (map[string]*Sticker, error) {
	query := r.db.Rebind(`
		SELECT id, user_id, sticker_id, set_name, file_id, text, emoji, ocr_engine, manual_edit
		FROM stickers
		WHERE user_id = ? AND set_name = ?
	`)
	rows, err := r.db.Query(query, userID, setName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stickers := make(map[string]*Sticker)
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji, &st.OCREngine, &st.ManualEdit); err != nil {
			return nil, err
		}
		stickers[st.StickerID] = &st
	}
	return stickers, rows.Err()
}

func (r *BaseRepository) UpdateStickerText(userID int64, stickerID string, text string) error {
	textLower := strings.ToLower(text)
	query := r.db.Rebind("UPDATE stickers SET text = ?, text_lower = ?, manual_edit = TRUE WHERE user_id = ? AND sticker_id = ?")
	_, err := r.db.Exec(query, text, textLower, userID, stickerID)
	return err
}

func (r *BaseRepository) DeleteSticker(userID int64, stickerID string) error {
	query := r.db.Rebind("DELETE FROM stickers WHERE user_id = ? AND sticker_id = ?")
	_, err := r.db.Exec(query, userID, stickerID)
	return err
}

func (r *BaseRepository) GetUserPackStats(userID int64) ([]*PackStats, error) {
	query := r.db.Rebind(`
		SELECT
			set_name,
			COUNT(*) as total,
			SUM(CASE WHEN ocr_engine = 'api' THEN 1 ELSE 0 END) as by_api,
			SUM(CASE WHEN ocr_engine = 'paddle' THEN 1 ELSE 0 END) as by_paddle,
			SUM(CASE WHEN ocr_engine = 'easy' THEN 1 ELSE 0 END) as by_easy,
			SUM(CASE WHEN ocr_engine = 'tesseract' THEN 1 ELSE 0 END) as by_tesseract,
			SUM(CASE WHEN manual_edit = TRUE THEN 1 ELSE 0 END) as manual_edited
		FROM stickers
		WHERE user_id = ? AND set_name != ''
		GROUP BY set_name
		ORDER BY total DESC
	`)
	rows, err := r.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []*PackStats
	for rows.Next() {
		var s PackStats
		if err := rows.Scan(&s.SetName, &s.Total, &s.ByAPI, &s.ByPaddle, &s.ByEasy, &s.ByTesseract, &s.ManualEdited); err != nil {
			return nil, err
		}
		stats = append(stats, &s)
	}
	return stats, rows.Err()
}

func (r *BaseRepository) GetUserStickersByPack(userID int64, setName string, limit, offset int) ([]*Sticker, error) {
	query := r.db.Rebind(`
		SELECT id, user_id, sticker_id, set_name, file_id, text, emoji, ocr_engine, manual_edit
		FROM stickers
		WHERE user_id = ? AND set_name = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`)
	rows, err := r.db.Query(query, userID, setName, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*Sticker
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.UserID, &st.StickerID, &st.SetName, &st.FileID, &st.Text, &st.Emoji, &st.OCREngine, &st.ManualEdit); err != nil {
			return nil, err
		}
		stickers = append(stickers, &st)
	}
	return stickers, rows.Err()
}

func (r *BaseRepository) GetUserPackStickerCount(userID int64, setName string) (int, error) {
	var count int
	err := r.db.Get(&count, r.db.Rebind("SELECT COUNT(*) FROM stickers WHERE user_id = ? AND set_name = ?"), userID, setName)
	return count, err
}

func (r *BaseRepository) DeleteUserPack(userID int64, setName string) error {
	query := r.db.Rebind("DELETE FROM stickers WHERE user_id = ? AND set_name = ?")
	_, err := r.db.Exec(query, userID, setName)
	return err
}

func (r *BaseRepository) SaveThumbnail(fileID string, thumbnail []byte) error {
	query := r.db.Rebind(`
		INSERT INTO sticker_thumbnails (file_id, thumbnail) VALUES (?, ?)
		ON CONFLICT(file_id) DO UPDATE SET thumbnail = EXCLUDED.thumbnail
	`)
	_, err := r.db.Exec(query, fileID, thumbnail)
	return err
}

func (r *BaseRepository) GetThumbnail(fileID string) ([]byte, error) {
	var thumbnail []byte
	err := r.db.Get(&thumbnail, r.db.Rebind("SELECT thumbnail FROM sticker_thumbnails WHERE file_id = ?"), fileID)
	return thumbnail, err
}

func (r *BaseRepository) Close() error {
	return r.db.Close()
}
