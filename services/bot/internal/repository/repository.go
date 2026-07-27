package repository

type MediaType string

const (
	MediaTypeSticker MediaType = "sticker"
	MediaTypePhoto   MediaType = "photo"
	MediaTypeVideo   MediaType = "video"
)

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
	MediaType  MediaType
}

type PackStats struct {
	SetName      string
	Total        int
	ManualEdited int
}

type Repository interface {
	// Stickers
	SaveSticker(sticker *Sticker) error
	GetSticker(userID int64, stickerID string) (*Sticker, error)
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

	// Media type filtering
	GetUserMediaCount(userID int64, mediaType MediaType) (int, error)
	GetUserMediaByType(userID int64, mediaType MediaType, limit, offset int) ([]*Sticker, error)

	// Thumbnails
	SaveThumbnail(fileID string, thumbnail []byte) error
	GetThumbnail(fileID string) ([]byte, error)

	Close() error
}
