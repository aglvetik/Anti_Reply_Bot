package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"telegram-stop-reply-bot/internal/rules"
	"telegram-stop-reply-bot/internal/storage"
	"telegram-stop-reply-bot/internal/telegram"
)

const (
	telegramRequestTimeout     = 5 * time.Second
	restrictionEnabledText     = "Ограничение включено."
	restrictionDisabledText    = "Ограничение снято."
	restrictionUnavailableText = "Для этого пользователя ограничение недоступно."
	violationWarningText       = "У вас нет права отвечать на сообщения этого пользователя."
)

type App struct {
	cfg    Config
	logger *slog.Logger
	client *telegram.Client
	store  *storage.SQLiteStore
	rules  *rules.Service
}

func New(ctx context.Context, cfg Config, logger *slog.Logger) (*App, error) {
	store, err := storage.Open(ctx, cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	activeRules, err := store.LoadActiveRules(ctx)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("load active rules: %w", err)
	}

	knownUsers, err := store.LoadKnownUsers(ctx)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("load known users: %w", err)
	}

	cache := rules.NewCache(activeRules, knownUsers, cfg.ImmuneUserIDs)
	service := rules.NewService(store, cache)
	client := telegram.NewClient(cfg.BotToken, logger)

	startupCtx, cancel := context.WithTimeout(ctx, telegramRequestTimeout)
	defer cancel()

	me, err := client.GetMe(startupCtx)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("telegram getMe: %w", err)
	}

	logger.Info("telegram bot authenticated", "bot_id", me.ID, "username", me.Username)

	return &App{
		cfg:    cfg,
		logger: logger,
		client: client,
		store:  store,
		rules:  service,
	}, nil
}

func (a *App) Close() error {
	if a.store == nil {
		return nil
	}
	return a.store.Close()
}

func (a *App) ProcessUpdate(ctx context.Context, update telegram.Update) {
	if update.Message == nil {
		return
	}
	a.processMessage(ctx, update.Message)
}

func (a *App) processMessage(ctx context.Context, msg *telegram.Message) {
	if msg == nil || msg.From == nil {
		return
	}

	if err := a.rules.UpdateKnownUsers(ctx, msg); err != nil {
		a.logger.Warn("update known users failed", "error", err, "message_id", msg.MessageID, "chat_id", chatID(msg))
	}

	if a.rules.IsStopCommand(msg) {
		a.handleStopCommand(ctx, msg)
		return
	}

	violation, ok := a.rules.DetectViolation(msg)
	if !ok {
		return
	}

	a.logger.Info(
		"rule violation detected",
		"chat_id", violation.RuleKey.ChatID,
		"protected_user_id", violation.RuleKey.ProtectedUserID,
		"blocked_user_id", violation.RuleKey.BlockedUserID,
		"message_id", msg.MessageID,
	)

	a.deleteMessage(msg.Chat.ID, msg.MessageID, "violation")
	a.sendTemporaryMessage(msg.Chat.ID, violationWarningText)
}

func (a *App) handleStopCommand(ctx context.Context, msg *telegram.Message) {
	result, err := a.rules.HandleStopCommand(ctx, msg)
	if err != nil {
		a.logger.Warn("handle stop command failed", "error", err, "message_id", msg.MessageID, "chat_id", msg.Chat.ID)
		return
	}

	switch {
	case result.BlockedUserImmune:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionUnavailableText)
	case result.Enabled:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionEnabledText)
	default:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionDisabledText)
	}

	a.logger.Info(
		"rule toggled",
		"chat_id", result.RuleKey.ChatID,
		"protected_user_id", result.RuleKey.ProtectedUserID,
		"blocked_user_id", result.RuleKey.BlockedUserID,
		"enabled", result.Enabled,
		"immune_target", result.BlockedUserImmune,
	)

	a.deleteMessage(msg.Chat.ID, msg.MessageID, "stop_command")
}

func (a *App) sendTemporaryMessage(chatID int64, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), telegramRequestTimeout)
	defer cancel()

	message, err := a.client.SendMessage(ctx, chatID, text)
	if err != nil {
		a.logger.Warn("send temporary message failed", "error", err, "chat_id", chatID)
		return
	}

	go a.deleteMessageAfter(chatID, message.MessageID, a.cfg.WarningTTL)
}

func (a *App) deleteMessageAfter(chatID, messageID int64, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	<-timer.C
	a.deleteMessage(chatID, messageID, "ttl_cleanup")
}

func (a *App) deleteMessage(chatID, messageID int64, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), telegramRequestTimeout)
	defer cancel()

	if err := a.client.DeleteMessage(ctx, chatID, messageID); err != nil {
		a.logger.Debug("delete message failed", "error", err, "chat_id", chatID, "message_id", messageID, "reason", reason)
	}
}

func chatID(msg *telegram.Message) int64 {
	if msg == nil || msg.Chat == nil {
		return 0
	}
	return msg.Chat.ID
}
