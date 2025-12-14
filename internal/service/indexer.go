package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

type IndexProgress struct {
	Current            int64
	Total              int
	Processed          int64
	WithText           int64
	Cancelled          bool
	QuotaExceeded      bool
	LastStickerFileURL string
	RemainingStickers  []models.Sticker
	Report             *IndexReport
}

type IndexReport struct {
	Total          int // всего в паке
	ToProcess      int // нужно обработать
	SkippedAPI     int // пропущено (уже api)
	SkippedManual  int // пропущено (manual_edit)
	Reprocessed    int // переобработано (было local → стало api)
	NewStickers    int // новые стикеры
	WithText       int // с текстом
	APIUnavailable bool
}

type thumbJob struct {
	FileID  string
	FileURL string
}

type stickerJob struct {
	Sticker      models.Sticker
	IsReprocess  bool // переобработка существующего
}

type ProgressCallback func(progress IndexProgress)

type Indexer struct {
	repo repository.Repository
	ocr  *ocr.OCR
}

func NewIndexer(repo repository.Repository, ocr *ocr.OCR) *Indexer {
	return &Indexer{repo: repo, ocr: ocr}
}

func (i *Indexer) IndexPack(
	ctx context.Context,
	tgBot *bot.Bot,
	stickerSet *models.StickerSet,
	userID int64,
	ocrEngine string,
	onProgress ProgressCallback,
) IndexProgress {
	total := len(stickerSet.Stickers)
	setName := stickerSet.Name

	report := &IndexReport{Total: total}

	// Get existing stickers for this pack
	existingStickers, err := i.repo.GetStickersBySetName(userID, setName)
	if err != nil {
		logger.Log.Errorw("[INDEX] failed to get existing stickers", "error", err)
		existingStickers = make(map[string]*repository.Sticker)
	}

	// Filter stickers to process
	var stickersToProcess []stickerJob
	for _, sticker := range stickerSet.Stickers {
		existing, exists := existingStickers[sticker.FileUniqueID]
		if exists {
			// Skip if manually edited
			if existing.ManualEdit {
				report.SkippedManual++
				continue
			}
			// Skip if already processed by api
			if existing.OCREngine == "api" {
				report.SkippedAPI++
				continue
			}
			// Reprocess if processed by local engine
			stickersToProcess = append(stickersToProcess, stickerJob{
				Sticker:     sticker,
				IsReprocess: true,
			})
		} else {
			// New sticker
			stickersToProcess = append(stickersToProcess, stickerJob{
				Sticker:     sticker,
				IsReprocess: false,
			})
			report.NewStickers++
		}
	}

	report.ToProcess = len(stickersToProcess)

	logger.Log.Infow("[INDEX] starting pack indexing",
		"pack", setName,
		"total", total,
		"to_process", report.ToProcess,
		"skipped_api", report.SkippedAPI,
		"skipped_manual", report.SkippedManual,
		"new", report.NewStickers,
		"workers", constants.Workers,
		"user", userID,
	)

	// If nothing to process, return early
	if len(stickersToProcess) == 0 {
		return IndexProgress{
			Total:  total,
			Report: report,
		}
	}

	var processed atomic.Int64
	var withText atomic.Int64
	var reprocessed atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool
	var quotaExceeded atomic.Bool
	var lastStickerURL atomic.Value
	var remainingMu sync.Mutex
	var remainingStickers []models.Sticker

	// Create cancellable context for quota exceeded
	indexCtx, cancelIndex := context.WithCancel(ctx)
	defer cancelIndex()

	jobs := make(chan stickerJob, constants.Workers)
	thumbJobs := make(chan thumbJob, constants.Workers)
	var wg sync.WaitGroup
	var thumbWg sync.WaitGroup

	// Start thumbnail workers
	for tw := 0; tw < constants.Workers; tw++ {
		thumbWg.Add(1)
		go func(workerID int) {
			defer thumbWg.Done()
			for job := range thumbJobs {
				select {
				case <-indexCtx.Done():
					return
				default:
				}
				if err := i.downloadAndSaveThumbnail(indexCtx, job.FileID, job.FileURL); err != nil {
					logger.Log.Debugw("[THUMB] failed to save thumbnail",
						"worker", workerID,
						"file_id", job.FileID[:20]+"...",
						"error", err,
					)
				} else {
					logger.Log.Debugw("[THUMB] thumbnail saved",
						"worker", workerID,
						"file_id", job.FileID[:20]+"...",
					)
				}
			}
		}(tw)
	}

	// Start OCR workers with staggered delay
	for w := 0; w < constants.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*50) * time.Millisecond)
			logger.Log.Debugw("[INDEX] worker started", "worker", workerID)

			for job := range jobs {
				sticker := job.Sticker
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					logger.Log.Warnw("[INDEX] worker cancelled", "worker", workerID)
					return
				default:
				}

				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					logger.Log.Errorw("[INDEX] failed to get file",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				// Always use api engine for reindexing
				text, ocrErr := i.DownloadAndOCR(indexCtx, fileURL, "api")

				// Check for quota exceeded
				if errors.Is(ocrErr, ocr.ErrQuotaExceeded) {
					logger.Log.Warnw("[INDEX] quota exceeded, stopping",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
					)
					quotaExceeded.Store(true)
					lastStickerURL.Store(fileURL)
					cancelIndex() // Stop all workers
					return
				}

				if ocrErr != nil {
					logger.Log.Warnw("[INDEX] OCR failed",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", ocrErr,
					)
				}

				// Skip stickers without text - they won't be searchable anyway
				if text == "" {
					logger.Log.Debugw("[INDEX] skipping sticker (no text)",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"emoji", sticker.Emoji,
					)
					completed.Add(1)
					continue
				}

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
					OCREngine: "api",
				}

				if err := i.repo.SaveSticker(s); err != nil {
					logger.Log.Errorw("[INDEX] failed to save sticker",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
				} else {
					processed.Add(1)
					withText.Add(1)
					if job.IsReprocess {
						reprocessed.Add(1)
					}
					// Queue thumbnail download (non-blocking)
					select {
					case thumbJobs <- thumbJob{FileID: sticker.FileID, FileURL: fileURL}:
					default:
						// Channel full, skip thumbnail
					}
					logger.Log.Infow("[INDEX] sticker saved",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"text", text,
						"emoji", sticker.Emoji,
						"reprocess", job.IsReprocess,
					)
				}
				completed.Add(1)
			}
		}(w)
	}

	// Send jobs in goroutine
	go func() {
		for idx, job := range stickersToProcess {
			select {
			case <-indexCtx.Done():
				// Collect remaining stickers
				remainingMu.Lock()
				for _, j := range stickersToProcess[idx:] {
					remainingStickers = append(remainingStickers, j.Sticker)
				}
				remainingMu.Unlock()
				close(jobs)
				return
			case jobs <- job:
			}
		}
		close(jobs)
	}()

	// Progress updates
	go func() {
		lastUpdate := int64(0)
		toProcess := int64(len(stickersToProcess))
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= toProcess {
				break
			}
			if current-lastUpdate >= constants.ProgressUpdateInterval {
				if onProgress != nil {
					onProgress(IndexProgress{
						Current:       current,
						Total:         int(toProcess),
						Processed:     processed.Load(),
						WithText:      withText.Load(),
						Cancelled:     cancelled.Load(),
						QuotaExceeded: quotaExceeded.Load(),
					})
				}
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	wg.Wait()
	close(thumbJobs)
	thumbWg.Wait()

	// Get last sticker URL if set
	var lastURL string
	if v := lastStickerURL.Load(); v != nil {
		lastURL = v.(string)
	}

	remainingMu.Lock()
	remaining := remainingStickers
	remainingMu.Unlock()

	report.Reprocessed = int(reprocessed.Load())
	report.WithText = int(withText.Load())
	report.APIUnavailable = quotaExceeded.Load()

	result := IndexProgress{
		Current:            completed.Load(),
		Total:              len(stickersToProcess),
		Processed:          processed.Load(),
		WithText:           withText.Load(),
		Cancelled:          cancelled.Load(),
		QuotaExceeded:      quotaExceeded.Load(),
		LastStickerFileURL: lastURL,
		RemainingStickers:  remaining,
		Report:             report,
	}

	logger.Log.Infow("[INDEX] pack indexing completed",
		"pack", setName,
		"total", total,
		"to_process", report.ToProcess,
		"processed", result.Processed,
		"reprocessed", report.Reprocessed,
		"with_text", result.WithText,
		"skipped_api", report.SkippedAPI,
		"skipped_manual", report.SkippedManual,
		"cancelled", result.Cancelled,
		"quota_exceeded", result.QuotaExceeded,
		"remaining", len(result.RemainingStickers),
		"user", userID,
	)

	return result
}

func (i *Indexer) DownloadAndOCR(ctx context.Context, fileURL string, engine string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "sticker-*.webp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", err
	}
	tmpFile.Close()

	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err != nil {
		return i.ocr.RecognizeText(ctx, tmpFile.Name(), engine)
	}

	return i.ocr.RecognizeText(ctx, pngPath, engine)
}

func convertWebPToPNG(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "convert", src, dst)
	return cmd.Run()
}

// CompareOCREngines downloads sticker and compares all OCR engines
func (i *Indexer) CompareOCREngines(ctx context.Context, fileURL string) []ocr.CompareResult {
	resp, err := http.Get(fileURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "sticker-compare-*.webp")
	if err != nil {
		return nil
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return nil
	}
	tmpFile.Close()

	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	imagePath := tmpFile.Name()
	if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err == nil {
		imagePath = pngPath
	}

	return i.ocr.CompareEngines(ctx, imagePath)
}

func (i *Indexer) downloadAndSaveThumbnail(ctx context.Context, fileID, fileURL string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "thumb-*.webp")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return err
	}
	tmpFile.Close()

	// Convert to PNG (keep original size for quality)
	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	cmd := exec.CommandContext(ctx, "convert", tmpFile.Name(), pngPath)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Read PNG file
	pngData, err := os.ReadFile(pngPath)
	if err != nil {
		return err
	}

	return i.repo.SaveThumbnail(fileID, pngData)
}

func ProgressBar(current, total int) string {
	if total == 0 {
		return "[░░░░░░░░░░]"
	}

	filled := current * constants.ProgressBarLength / total
	if filled > constants.ProgressBarLength {
		filled = constants.ProgressBarLength
	}

	bar := "["
	for j := 0; j < constants.ProgressBarLength; j++ {
		if j < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	bar += "]"
	return bar
}
