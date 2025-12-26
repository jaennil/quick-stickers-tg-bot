package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/state"
)

type Bot struct {
	bot     *bot.Bot
	repo    repository.Repository
	ocr     *ocr.OCR
	indexer *service.Indexer
	state   *state.Manager
}

func New(token string, repo repository.Repository, ocr *ocr.OCR) (*Bot, error) {
	b := &Bot{
		repo:    repo,
		ocr:     ocr,
		indexer: service.NewIndexer(repo, ocr),
		state:   state.NewManager(constants.StateTTL),
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.defaultHandler),
		bot.WithCallbackQueryDataHandler(CallbackMenu, bot.MatchTypePrefix, b.handleMenuCallback),
		bot.WithCallbackQueryDataHandler(CallbackAddPack, bot.MatchTypePrefix, b.handleAddPackCallback),
		bot.WithCallbackQueryDataHandler(CallbackEdit, bot.MatchTypePrefix, b.handleEditCallback),
		bot.WithCallbackQueryDataHandler(CallbackCancel, bot.MatchTypePrefix, b.handleCancelCallback),
		bot.WithCallbackQueryDataHandler(CallbackOCR, bot.MatchTypePrefix, b.handleOCRCallback),
		bot.WithCallbackQueryDataHandler(CallbackSelectOCR, bot.MatchTypePrefix, b.handleSelectOCRCallback),
		bot.WithCallbackQueryDataHandler(CallbackList, bot.MatchTypePrefix, b.handleListCallback),
		bot.WithCallbackQueryDataHandler(CallbackFallback, bot.MatchTypePrefix, b.handleFallbackCallback),
		bot.WithCallbackQueryDataHandler(CallbackDelete, bot.MatchTypePrefix, b.handleDeleteCallback),
		bot.WithCallbackQueryDataHandler(CallbackAllStickers, bot.MatchTypePrefix, b.handleAllStickersCallback),
		bot.WithCallbackQueryDataHandler(CallbackPack, bot.MatchTypePrefix, b.handlePackCallback),
		bot.WithCallbackQueryDataHandler(CallbackDeletePack, bot.MatchTypePrefix, b.handleDeletePackCallback),
		bot.WithCallbackQueryDataHandler(CallbackMedia, bot.MatchTypePrefix, b.handleMediaCallback),
	}

	tgBot, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	b.bot = tgBot
	b.registerHandlers()

	return b, nil
}

func (b *Bot) registerHandlers() {
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, b.handleStart)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, b.handleHelp)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, b.handleStats)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/search", bot.MatchTypePrefix, b.handleSearch)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/addpack", bot.MatchTypePrefix, b.handleAddPack)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/edit", bot.MatchTypePrefix, b.handleEdit)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/settings", bot.MatchTypeExact, b.handleSettings)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/list", bot.MatchTypePrefix, b.handleList)
}

func (b *Bot) Start(ctx context.Context) {
	logger.Log.Info("Bot started")
	b.bot.Start(ctx)
}
