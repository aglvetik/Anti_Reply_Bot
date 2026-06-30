package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf16"

	"telegram-stop-reply-bot/internal/rules"
	"telegram-stop-reply-bot/internal/storage"
	"telegram-stop-reply-bot/internal/telegram"
)

const (
	telegramRequestTimeout      = 5 * time.Second
	restrictionUnavailableText  = "Для этого пользователя ограничение недоступно."
	userResolveFailedText       = "Не удалось определить пользователя."
	violationWarningText        = "Пользователь запретил вам отвечать на его сообщения."
	violationWarningSuffixText  = ", пользователь запретил вам отвечать на его сообщения."
	commandEnabledPrefixText    = "✅ Ограничение включено: "
	commandEnabledSuffixText    = " больше не может отвечать пользователю "
	commandDisabledPrefixText   = "✅ Ограничение снято: "
	commandDisabledSuffixText   = " снова может отвечать пользователю "
	commandResultTerminatorText = "."
	protectedUserFallbackName   = "пользователю"
	blockedUserFallbackName     = "пользователь"
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
		a.sendCommandResultMessage(msg.Chat.ID, true, commandMatch.TargetUser, msg.From)
	default:
		a.sendCommandResultMessage(msg.Chat.ID, false, commandMatch.TargetUser, msg.From)
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

func (a *App) sendCommandResultMessage(chatID int64, enabled bool, blockedUser, protectedUser *telegram.User) {
	text, entities := buildCommandResultMessage(enabled, blockedUser, protectedUser)
	a.sendPersistentMessage(chatID, text, entities)
}

func (a *App) sendTemporaryMessage(chatID int64, text string, entities []telegram.MessageEntity) {
	message, err := a.sendMessage(chatID, text, entities)
	if err != nil {
		a.logger.Warn("send temporary message failed", "error", err, "chat_id", chatID)
		return
	}

	a.scheduleMessageDeletion(chatID, message.MessageID, a.cfg.WarningTTL)
}

func (a *App) sendPersistentMessage(chatID int64, text string, entities []telegram.MessageEntity) {
	if _, err := a.sendMessage(chatID, text, entities); err != nil {
		a.logger.Warn("send persistent message failed", "error", err, "chat_id", chatID)
	}
}

func (a *App) sendMessage(chatID int64, text string, entities []telegram.MessageEntity) (telegram.Message, error) {
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
		return telegram.Message{}, err
	}

	return message, nil
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
	if !mentionTarget || violation.BlockedUser == nil || violation.BlockedUser.ID == 0 {
		return violationWarningText, nil
	}

	builder := newMessageBuilder()
	builder.appendUser(violation.BlockedUser, "Пользователь")
	builder.appendText(violationWarningSuffixText)

	return builder.text(), builder.entities
}

func buildCommandResultMessage(enabled bool, blockedUser, protectedUser *telegram.User) (string, []telegram.MessageEntity) {
	builder := newMessageBuilder()
	if enabled {
		builder.appendText(commandEnabledPrefixText)
	} else {
		builder.appendText(commandDisabledPrefixText)
	}
	builder.appendUser(blockedUser, blockedUserFallbackName)
	if enabled {
		builder.appendText(commandEnabledSuffixText)
	} else {
		builder.appendText(commandDisabledSuffixText)
	}
	builder.appendUser(protectedUser, protectedUserFallbackName)
	builder.appendText(commandResultTerminatorText)

	return builder.text(), builder.entities
}

type messageBuilder struct {
	builder    strings.Builder
	entities   []telegram.MessageEntity
	utf16Count int
}

func newMessageBuilder() *messageBuilder {
	return &messageBuilder{
		entities: make([]telegram.MessageEntity, 0, 2),
	}
}

func (b *messageBuilder) appendText(text string) {
	b.builder.WriteString(text)
	b.utf16Count += utf16Length(text)
}

func (b *messageBuilder) appendUser(user *telegram.User, fallback string) {
	displayName := formatUserDisplayName(user, fallback)
	offset := b.utf16Count
	b.builder.WriteString(displayName)
	b.utf16Count += utf16Length(displayName)

	if user == nil || user.ID == 0 {
		return
	}

	mentionedUser := cloneTelegramUser(user)
	if mentionedUser.FirstName == "" && mentionedUser.LastName == "" && mentionedUser.Username == "" {
		mentionedUser.FirstName = displayName
	}

	b.entities = append(b.entities, telegram.MessageEntity{
		Type:   "text_mention",
		Offset: offset,
		Length: utf16Length(displayName),
		User:   mentionedUser,
	})
}

func (b *messageBuilder) text() string {
	return b.builder.String()
}

func formatUserDisplayName(user *telegram.User, fallback string) string {
	if user == nil {
		return fallback
	}

	fullName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if fullName != "" {
		return fullName
	}
	if firstName := strings.TrimSpace(user.FirstName); firstName != "" {
		return firstName
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		if strings.HasPrefix(username, "@") {
			return username
		}
		return "@" + username
	}
	return fallback
}

func cloneTelegramUser(user *telegram.User) *telegram.User {
	if user == nil {
		return nil
	}

	cloned := *user
	return &cloned
}

func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}
