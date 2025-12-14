package repository

type Sticker struct {
	ID        int64
	UserID    int64
	StickerID string
	SetName   string
	FileID    string
	Text      string
	Emoji     string
}

type Repository interface {
	// Stickers
	SaveSticker(sticker *Sticker) error
	SearchByText(userID int64, query string) ([]*Sticker, error)
	GetUserStickerCount(userID int64) (int, error)
	GetUserStickers(userID int64, limit, offset int) ([]*Sticker, error)
	UpdateStickerText(userID int64, stickerID string, text string) error

	// User settings
	GetUserOCREngine(userID int64) string
	SetUserOCREngine(userID int64, engine string) error

	Close() error
}
