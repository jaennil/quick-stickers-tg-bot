package repository

type MediaType string

const (
	MediaTypeSticker   MediaType = "sticker"
	MediaTypePhoto     MediaType = "photo"
	MediaTypeVideo     MediaType = "video"
	MediaTypeVideoFile MediaType = "video_file"
	MediaTypeGIF       MediaType = "gif"
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

	// AIText holds the vision-model transcription plus description. It is
	// populated only by the queries that need it.
	AIText string
	// AITextAttempts counts failed vision calls for this media.
	AITextAttempts int
}

type MediaJob struct {
	ID                int64
	UserID            int64
	ChatID            int64
	ProgressMessageID int
	StickerID         string
	FileID            string
	MediaType         MediaType
	Attempts          int
}

type PackStats struct {
	SetName      string
	Total        int
	ManualEdited int
}

type EmbeddedSticker struct {
	Sticker   *Sticker
	Embedding []byte
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
	GetAITextCandidates(model string, maxAttempts, limit int) ([]*Sticker, error)
	MarkAITextAttempt(userID int64, stickerID string) error
	SaveAIText(userID int64, stickerID, model, aiText, sourceFileID string) error
	GetEmbeddingCandidates(model string, limit int) ([]*Sticker, error)
	MarkEmbeddingAttempt(userID int64, stickerID string) error
	SaveEmbedding(userID int64, stickerID, model, sourceText, sourceFileID string, embedding []byte) error
	GetUserEmbeddings(userID int64, model string) ([]*EmbeddedSticker, error)

	// Media type filtering
	GetUserMediaCount(userID int64, mediaType MediaType) (int, error)
	GetUserMediaByType(userID int64, mediaType MediaType, limit, offset int) ([]*Sticker, error)

	// Durable media processing queue
	EnqueueMediaJob(job *MediaJob) error
	UpdateMediaJobProgressMessage(userID int64, stickerID string, messageID int) error
	RequeueProcessingMediaJobs() error
	ClaimNextMediaJob() (*MediaJob, error)
	RetryMediaJob(id int64, lastError string) error
	CompleteMediaJob(id int64) error

	// Thumbnails
	SaveThumbnail(fileID string, thumbnail []byte) error
	GetThumbnail(fileID string) ([]byte, error)

	Close() error
}
