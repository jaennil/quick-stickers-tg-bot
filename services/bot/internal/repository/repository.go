package repository

type Sticker struct {
	ID         int64
	UserID     int64
	StickerID  string
	SetName    string
	FileID     string
	DocumentID int64
	Text       string
	Emoji      string
	OCREngine  string
	ManualEdit bool
	IsAnimated bool
	IsVideo    bool
}

type PackStats struct {
	SetName       string
	Total         int
	ByAPI         int
	ByPaddle      int
	ByEasy        int
	ByTesseract   int
	ManualEdited  int
}

type Repository interface {
	// Stickers
	SaveSticker(sticker *Sticker) error
	SearchByText(userID int64, query string) ([]*Sticker, error)
	GetUserStickerCount(userID int64) (int, error)
	GetUserStickers(userID int64, limit, offset int) ([]*Sticker, error)
	GetStickersBySetName(userID int64, setName string) (map[string]*Sticker, error)
	UpdateStickerText(userID int64, stickerID string, text string) error
	DeleteSticker(userID int64, stickerID string) error
	GetUserPackStats(userID int64) ([]*PackStats, error)
	GetUserStickersByPack(userID int64, setName string, limit, offset int) ([]*Sticker, error)
	GetUserPackStickerCount(userID int64, setName string) (int, error)
	DeleteUserPack(userID int64, setName string) error

	// Thumbnails
	SaveThumbnail(fileID string, thumbnail []byte) error
	GetThumbnail(fileID string) ([]byte, error)

	// User settings
	GetUserOCREngine(userID int64) string
	SetUserOCREngine(userID int64, engine string) error

	Close() error
}
