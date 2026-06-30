package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf16"

	"telegram-stop-reply-bot/internal/rules"
	"telegram-stop-reply-bot/internal/storage"
	"telegram-stop-reply-bot/internal/telegram"
)

const (
	telegramRequestTimeout     = 5 * time.Second
	restrictionEnabledText     = "Ограничение включено."
	restrictionDisabledText    = "Ограничение снято."
	restrictionUnavailableText = "Для этого пользователя ограничение недоступно."
	userResolveFailedText      = "Не удалось определить пользователя."
	violationWarningText       = "У вас нет права отвечать на сообщения этого пользователя."
	violationMentionLabel      = "Пользователь"
)

type telegramClient interface {
	GetMe(ctx context.Context) (telegram.User, error)
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	SendMessage(ctx context.Context, chatID int64, text string) (telegram.Message, error)
	SendMessageWithEntities(ctx context.Context, chatID int64, text string, entities []telegram.MessageEntity) (telegram.Message, error)
}

type App struct {
	cfg       Config
	logger    *slog.Logger
	client    telegramClient
	store     *storage.SQLiteStore
	rules     *rules.Service
	afterFunc func(time.Duration, func())
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
		cfg:       cfg,
		logger:    logger,
		client:    client,
		store:     store,
		rules:     service,
		afterFunc: func(delay time.Duration, fn func()) { time.AfterFunc(delay, fn) },
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

	commandMatch := a.rules.MatchStopCommand(msg)
	if commandMatch.IsCommand() {
		a.handleStopCommand(ctx, msg, commandMatch)
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
	a.sendViolationWarning(msg.Chat.ID, violation)
}

func (a *App) handleStopCommand(ctx context.Context, msg *telegram.Message, commandMatch rules.StopCommandMatch) {
	if !commandMatch.IsValid() {
		if commandMatch.Status == rules.StopCommandUnknownTarget {
			a.sendTemporaryMessage(msg.Chat.ID, userResolveFailedText, nil)
		}
		return
	}

	result, err := a.rules.HandleStopCommand(ctx, msg)
	if err != nil {
		a.logger.Warn("handle stop command failed", "error", err, "message_id", msg.MessageID, "chat_id", msg.Chat.ID)
		return
	}

	if _, violation := a.rules.DetectCommandViolation(msg); violation {
		a.deleteUserMessage(msg.Chat.ID, msg.MessageID, "stop_command_violation")
	}

	switch {
	case result.BlockedUserImmune:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionUnavailableText, nil)
	case result.Enabled:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionEnabledText, nil)
	default:
		a.sendTemporaryMessage(msg.Chat.ID, restrictionDisabledText, nil)
	}

	a.logger.Info(
		"rule toggled",
		"chat_id", result.RuleKey.ChatID,
		"protected_user_id", result.RuleKey.ProtectedUserID,
		"blocked_user_id", result.RuleKey.BlockedUserID,
		"enabled", result.Enabled,
		"immune_target", result.BlockedUserImmune,
	)
}

func (a *App) sendViolationWarning(chatID int64, violation rules.Violation) {
	if !a.cfg.ViolationWarningEnabled {
		return
	}

	text, entities := buildViolationWarning(violation, a.cfg.ViolationWarningMentionTarget)
	a.sendTemporaryMessage(chatID, text, entities)
}

func (a *App) sendTemporaryMessage(chatID int64, text string, entities []telegram.MessageEntity) {
	ctx, cancel := context.WithTimeout(context.Background(), telegramRequestTimeout)
	defer cancel()

	var (
		message telegram.Message
		err     error
	)

	if len(entities) == 0 {
		message, err = a.client.SendMessage(ctx, chatID, text)
	} else {
		message, err = a.client.SendMessageWithEntities(ctx, chatID, text, entities)
	}
	if err != nil {
		a.logger.Warn("send temporary message failed", "error", err, "chat_id", chatID)
		return
	}

	a.scheduleMessageDeletion(chatID, message.MessageID, a.cfg.WarningTTL)
}

func (a *App) scheduleMessageDeletion(chatID, messageID int64, delay time.Duration) {
	afterFunc := a.afterFunc
	if afterFunc == nil {
		afterFunc = func(delay time.Duration, fn func()) { time.AfterFunc(delay, fn) }
	}

	afterFunc(delay, func() {
		a.deleteBotMessage(chatID, messageID, "ttl_cleanup")
	})
}

func (a *App) deleteUserMessage(chatID, messageID int64, reason string) {
	a.deleteMessage(chatID, messageID, reason)
}

func (a *App) deleteBotMessage(chatID, messageID int64, reason string) {
	a.deleteMessage(chatID, messageID, reason)
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

func buildViolationWarning(violation rules.Violation, mentionTarget bool) (string, []telegram.MessageEntity) {
	if !mentionTarget || violation.ProtectedUser == nil || violation.ProtectedUser.ID == 0 {
		return violationWarningText, nil
	}

	mentionedUser := *violation.ProtectedUser
	if mentionedUser.FirstName == "" {
		mentionedUser.FirstName = violationMentionLabel
	}

	text := violationMentionLabel + ", " + violationWarningText
	entities := []telegram.MessageEntity{
		{
			Type:   "text_mention",
			Offset: 0,
			Length: utf16Length(violationMentionLabel),
			User:   &mentionedUser,
		},
	}

	return text, entities
}

func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}
