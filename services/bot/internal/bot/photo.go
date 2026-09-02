package bot

import (
	"context"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

const mediaQueueRetryDelay = 30 * time.Second

func (b *Bot) handlePhoto(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	photos := update.Message.Photo
	if len(photos) == 0 {
		return
	}

	photo := photos[len(photos)-1]
	job := &repository.MediaJob{
		UserID:    update.Message.From.ID,
		ChatID:    update.Message.Chat.ID,
		StickerID: photo.FileUniqueID,
		FileID:    photo.FileID,
		MediaType: repository.MediaTypePhoto,
	}

	logger.Log.Infow("[PHOTO] enqueueing",
		"photo", photo.FileUniqueID,
		"user", job.UserID,
		"width", photo.Width,
		"height", photo.Height,
	)
	if err := b.repo.EnqueueMediaJob(job); err != nil {
		logger.Log.Errorw("[PHOTO] failed to enqueue", "photo", photo.FileUniqueID, "error", err)
		_, _ = tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: job.ChatID,
			Text:   "⚠️ Не удалось добавить картинку в очередь. Попробуй ещё раз.",
		})
		return
	}

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: job.ChatID,
		Text:   "⏳ Картинка добавлена в очередь на распознавание.",
	})
	if err != nil {
		logger.Log.Warnw("[PHOTO] failed to send queue message", "photo", photo.FileUniqueID, "error", err)
	} else if err := b.repo.UpdateMediaJobProgressMessage(job.UserID, job.StickerID, progressMsg.ID); err != nil {
		logger.Log.Warnw("[PHOTO] failed to store progress message", "photo", photo.FileUniqueID, "error", err)
	}

	b.notifyMediaQueue()
}

func (b *Bot) notifyMediaQueue() {
	select {
	case b.queueCh <- struct{}{}:
	default:
	}
}

func (b *Bot) runMediaQueue(ctx context.Context) {
	if err := b.repo.RequeueProcessingMediaJobs(); err != nil {
		logger.Log.Errorw("[QUEUE] failed to restore interrupted jobs", "error", err)
	}
	logger.Log.Info("[QUEUE] durable media worker started")

	for {
		job, err := b.repo.ClaimNextMediaJob()
		if err != nil {
			logger.Log.Errorw("[QUEUE] failed to claim job", "error", err)
			if !waitForQueue(ctx, b.queueCh, mediaQueueRetryDelay) {
				return
			}
			continue
		}
		if job == nil {
			if !waitForQueue(ctx, b.queueCh, time.Minute) {
				return
			}
			continue
		}

		if err := b.processMediaJob(ctx, job); err != nil {
			logger.Log.Warnw("[QUEUE] media processing failed",
				"job", job.ID,
				"media", job.StickerID,
				"attempt", job.Attempts,
				"error", err,
			)
			if retryErr := b.repo.RetryMediaJob(job.ID, err.Error()); retryErr != nil {
				logger.Log.Errorw("[QUEUE] failed to requeue job", "job", job.ID, "error", retryErr)
			}
			if !waitForQueue(ctx, b.queueCh, mediaQueueRetryDelay) {
				return
			}
		}
	}
}

func waitForQueue(ctx context.Context, queueCh <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-queueCh:
		return true
	case <-timer.C:
		return true
	}
}

func (b *Bot) processMediaJob(ctx context.Context, job *repository.MediaJob) error {
	switch job.MediaType {
	case repository.MediaTypePhoto:
		return b.processPhotoJob(ctx, job)
	default:
		if err := b.repo.CompleteMediaJob(job.ID); err != nil {
			return err
		}
		logger.Log.Warnw("[QUEUE] discarded unsupported media job", "job", job.ID, "type", job.MediaType)
		return nil
	}
}

func (b *Bot) processPhotoJob(ctx context.Context, job *repository.MediaJob) error {
	file, err := b.bot.GetFile(ctx, &bot.GetFileParams{FileID: job.FileID})
	if err != nil {
		return err
	}
	fileURL := b.bot.FileDownloadLink(file)
	text, err := b.indexer.DownloadAndOCRWithType(ctx, fileURL, service.StickerTypeStatic)
	if err != nil {
		return err
	}

	photo := &repository.Sticker{
		UserID:    job.UserID,
		StickerID: job.StickerID,
		FileID:    job.FileID,
		Text:      text,
		OCREngine: constants.DefaultOCREngine.Name,
		MediaType: repository.MediaTypePhoto,
	}
	if err := b.repo.SaveSticker(photo); err != nil {
		return err
	}
	if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, job.FileID, fileURL, service.StickerTypeStatic); err != nil {
		return err
	}
	if err := b.repo.CompleteMediaJob(job.ID); err != nil {
		return err
	}

	b.state.SetLastSticker(job.UserID, job.StickerID)
	b.finishPhotoJob(ctx, job, text)
	logger.Log.Infow("[QUEUE] photo processed", "job", job.ID, "photo", job.StickerID, "text", text)
	return nil
}

func (b *Bot) finishPhotoJob(ctx context.Context, job *repository.MediaJob, text string) {
	params := &bot.EditMessageTextParams{
		ChatID:    job.ChatID,
		MessageID: job.ProgressMessageID,
		Text:      buildOCRResultMessage("картинку", text, nil),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(job.StickerID)},
		},
	}
	if job.ProgressMessageID != 0 {
		if _, err := b.bot.EditMessageText(ctx, params); err == nil {
			return
		}
	}
	_, _ = b.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      job.ChatID,
		Text:        params.Text,
		ReplyMarkup: params.ReplyMarkup,
	})
}
