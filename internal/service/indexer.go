package service

import (
	"context"
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
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

type IndexProgress struct {
	Current   int64
	Total     int
	Processed int64
	WithText  int64
	Cancelled bool
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

	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool

	jobs := make(chan models.Sticker, constants.Workers)
	var wg sync.WaitGroup

	// Start workers with staggered delay
	for w := 0; w < constants.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*100) * time.Millisecond)

			for sticker := range jobs {
				select {
				case <-ctx.Done():
					cancelled.Store(true)
					return
				default:
				}

				file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				text, _ := i.DownloadAndOCR(ctx, fileURL, ocrEngine)

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
				}

				if err := i.repo.SaveSticker(s); err == nil {
					processed.Add(1)
					if text != "" {
						withText.Add(1)
					}
				}
				completed.Add(1)
			}
		}(w)
	}

	// Send jobs in goroutine
	go func() {
		for _, sticker := range stickerSet.Stickers {
			select {
			case <-ctx.Done():
				close(jobs)
				return
			case jobs <- sticker:
			}
		}
		close(jobs)
	}()

	// Progress updates
	go func() {
		lastUpdate := int64(0)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= int64(total) {
				break
			}
			if current-lastUpdate >= constants.ProgressUpdateInterval {
				if onProgress != nil {
					onProgress(IndexProgress{
						Current:   current,
						Total:     total,
						Processed: processed.Load(),
						WithText:  withText.Load(),
						Cancelled: cancelled.Load(),
					})
				}
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	wg.Wait()

	return IndexProgress{
		Current:   completed.Load(),
		Total:     total,
		Processed: processed.Load(),
		WithText:  withText.Load(),
		Cancelled: cancelled.Load(),
	}
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
