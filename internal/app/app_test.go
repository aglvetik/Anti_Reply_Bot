package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"telegram-stop-reply-bot/internal/rules"
	"telegram-stop-reply-bot/internal/telegram"
)

type fakeTelegramClient struct {
	deletes    []deleteCall
	sends      []sendCall
	nextSendID int64
}

type deleteCall struct {
	chatID    int64
	messageID int64
}

type sendCall struct {
	chatID   int64
	text     string
	entities []telegram.MessageEntity
}

func (f *fakeTelegramClient) GetMe(context.Context) (telegram.User, error) {
	return telegram.User{ID: 1, Username: "bot"}, nil
}

func (f *fakeTelegramClient) DeleteMessage(_ context.Context, chatID, messageID int64) error {
	f.deletes = append(f.deletes, deleteCall{chatID: chatID, messageID: messageID})
	return nil
}

func (f *fakeTelegramClient) SendMessage(_ context.Context, chatID int64, text string) (telegram.Message, error) {
	return f.SendMessageWithEntities(context.Background(), chatID, text, nil)
}

func (f *fakeTelegramClient) SendMessageWithEntities(_ context.Context, chatID int64, text string, entities []telegram.MessageEntity) (telegram.Message, error) {
	f.sends = append(f.sends, sendCall{
		chatID:   chatID,
		text:     text,
		entities: cloneEntities(entities),
	})
	if f.nextSendID == 0 {
		f.nextSendID = 900
	}

	message := telegram.Message{MessageID: f.nextSendID}
	f.nextSendID++
	return message, nil
}

func TestProcessMessageViolationWarningDisabled(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       false,
			ViolationWarningMentionTarget: true,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		nil,
	)

	app.processMessage(context.Background(), newViolationReplyMessage())

	if len(client.deletes) != 1 || client.deletes[0].messageID != 100 {
		t.Fatalf("expected only violating message to be deleted, got %+v", client.deletes)
	}
	if len(client.sends) != 0 {
		t.Fatalf("expected no warning message, got %+v", client.sends)
	}
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no scheduled deletion, got %v", *scheduledDelays)
	}
}

func TestProcessMessageViolationWarningTemporary(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       true,
			ViolationWarningMentionTarget: true,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		nil,
	)

	app.processMessage(context.Background(), newViolationReplyMessage())

	if len(client.deletes) != 2 {
		t.Fatalf("expected violation delete plus warning cleanup, got %+v", client.deletes)
	}
	if client.deletes[0].messageID != 100 || client.deletes[1].messageID != 900 {
		t.Fatalf("unexpected delete order: %+v", client.deletes)
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected warning message, got %+v", client.sends)
	}
	if client.sends[0].text != "Ответ пользователю Protected недоступен." {
		t.Fatalf("unexpected warning text: %q", client.sends[0].text)
	}
	if len(client.sends[0].entities) != 1 {
		t.Fatalf("expected protected-user mention in warning, got %+v", client.sends[0].entities)
	}
	assertEntityUserID(t, client.sends[0].entities[0], 10)
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected temporary warning ttl, got %v", *scheduledDelays)
	}
}

func TestProcessMessageNormalReplyCommandSendsPermanentResult(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		nil,
	)

	app.processMessage(context.Background(), newReplyStopCommandMessage(10, 20, 30))

	if containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected original command message to remain in chat")
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected one permanent result message, got %+v", client.sends)
	}
	if client.sends[0].text != "✅ Ограничение включено: Boris больше не может отвечать пользователю Sofiia." {
		t.Fatalf("unexpected command result text: %q", client.sends[0].text)
	}
	assertMessageMentions(t, client.sends[0], 20, 10)
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no ttl cleanup for permanent result message, got %v", *scheduledDelays)
	}
	if containsDeletedMessage(client.deletes, 900) {
		t.Fatal("expected permanent result message to stay in chat")
	}
}

func TestProcessMessageMentionCommandSendsPermanentResult(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		[]rules.KnownUser{{UserID: 20, Username: "blocked_user", FirstName: "Boris"}},
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(10, 30, "@blocked_user бот стоп", "blocked_user"))

	if containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected original mention command message to remain in chat")
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected one permanent result message, got %+v", client.sends)
	}
	if client.sends[0].text != "✅ Ограничение включено: Boris больше не может отвечать пользователю Sofiia." {
		t.Fatalf("unexpected command result text: %q", client.sends[0].text)
	}
	assertMessageMentions(t, client.sends[0], 20, 10)
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no ttl cleanup for permanent result message, got %v", *scheduledDelays)
	}
}

func TestProcessMessageDisableRuleSendsPermanentResult(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		nil,
	)

	app.processMessage(context.Background(), newReplyStopCommandMessage(10, 20, 30))

	if len(client.sends) != 1 {
		t.Fatalf("expected one permanent result message, got %+v", client.sends)
	}
	if client.sends[0].text != "✅ Ограничение снято: Boris снова может отвечать пользователю Sofiia." {
		t.Fatalf("unexpected disabled result text: %q", client.sends[0].text)
	}
	assertMessageMentions(t, client.sends[0], 20, 10)
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no ttl cleanup for permanent disable result, got %v", *scheduledDelays)
	}
}

func TestProcessMessageUnknownMentionCommandRemainsTemporary(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		nil,
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(10, 30, "@unknown бот стоп", "unknown"))

	if len(client.sends) != 1 || client.sends[0].text != userResolveFailedText {
		t.Fatalf("expected temporary unknown-user message, got %+v", client.sends)
	}
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected ttl cleanup for temporary unknown-user message, got %v", *scheduledDelays)
	}
	if !containsDeletedMessage(client.deletes, 900) {
		t.Fatal("expected temporary unknown-user message to be deleted")
	}
}

func TestProcessMessageBlockedUserStopCommandDeletesOriginalButKeepsResult(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       true,
			ViolationWarningMentionTarget: true,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		[]rules.KnownUser{{UserID: 10, Username: "protected_user", FirstName: "Sofiia"}},
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(20, 30, "@protected_user бот стоп", "protected_user"))

	if !containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected self-violating command message to be deleted immediately")
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected one permanent result message, got %+v", client.sends)
	}
	if client.sends[0].text != "✅ Ограничение включено: Sofiia больше не может отвечать пользователю Boris." {
		t.Fatalf("unexpected reverse command result text: %q", client.sends[0].text)
	}
	assertMessageMentions(t, client.sends[0], 10, 20)
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no ttl cleanup for permanent reverse command result, got %v", *scheduledDelays)
	}
	if containsDeletedMessage(client.deletes, 900) {
		t.Fatal("expected permanent reverse command result message to stay in chat")
	}
}

func newTestApp(cfg Config, activeRules []rules.RuleKey, knownUsers []rules.KnownUser) (*App, *fakeTelegramClient, *[]time.Duration) {
	service := rules.NewService(nil, rules.NewCache(activeRules, knownUsers, cfg.ImmuneUserIDs))
	client := &fakeTelegramClient{}
	scheduledDelays := make([]time.Duration, 0, 2)

	app := &App{
		cfg:    cfg,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: client,
		rules:  service,
		afterFunc: func(delay time.Duration, fn func()) {
			scheduledDelays = append(scheduledDelays, delay)
			fn()
		},
	}

	return app, client, &scheduledDelays
}

func newViolationReplyMessage() *telegram.Message {
	return &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: 30,
		},
		From: &telegram.User{
			ID:        20,
			FirstName: "Blocked",
		},
		Text: "обычный ответ",
		ReplyToMessage: &telegram.Message{
			MessageID: 101,
			From: &telegram.User{
				ID:        10,
				FirstName: "Protected",
				Username:  "protected_user",
			},
		},
	}
}

func newReplyStopCommandMessage(fromID, replyToID, chatID int64) *telegram.Message {
	return &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: chatID,
		},
		From: &telegram.User{
			ID:        fromID,
			FirstName: map[int64]string{10: "Sofiia", 20: "Boris"}[fromID],
		},
		Text: "Бот стоп",
		ReplyToMessage: &telegram.Message{
			MessageID: 101,
			From: &telegram.User{
				ID:        replyToID,
				FirstName: map[int64]string{10: "Sofiia", 20: "Boris"}[replyToID],
			},
		},
	}
}

func newMentionStopCommandMessage(fromID, chatID int64, text, username string) *telegram.Message {
	msg := &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: chatID,
		},
		From: &telegram.User{
			ID:        fromID,
			FirstName: map[int64]string{10: "Sofiia", 20: "Boris"}[fromID],
		},
		Text: text,
	}

	mentionText := "@" + username
	offset := utf16Length(text[:indexOf(text, mentionText)])
	msg.Entities = []telegram.MessageEntity{
		{
			Type:   "mention",
			Offset: offset,
			Length: utf16Length(mentionText),
		},
	}

	return msg
}

func assertMessageMentions(t *testing.T, message sendCall, blockedUserID, protectedUserID int64) {
	t.Helper()

	if len(message.entities) != 2 {
		t.Fatalf("expected 2 text_mention entities, got %+v", message.entities)
	}
	assertEntityUserID(t, message.entities[0], blockedUserID)
	assertEntityUserID(t, message.entities[1], protectedUserID)
}

func assertEntityUserID(t *testing.T, entity telegram.MessageEntity, userID int64) {
	t.Helper()

	if entity.Type != "text_mention" {
		t.Fatalf("expected text_mention entity, got %+v", entity)
	}
	if entity.User == nil || entity.User.ID != userID {
		t.Fatalf("expected entity user %d, got %+v", userID, entity.User)
	}
}

func containsDeletedMessage(deletes []deleteCall, messageID int64) bool {
	for _, deleteCall := range deletes {
		if deleteCall.messageID == messageID {
			return true
		}
	}
	return false
}

func indexOf(text, substring string) int {
	for index := range text {
		if len(text[index:]) >= len(substring) && text[index:index+len(substring)] == substring {
			return index
		}
	}
	return -1
}

func cloneEntities(entities []telegram.MessageEntity) []telegram.MessageEntity {
	if len(entities) == 0 {
		return nil
	}

	cloned := make([]telegram.MessageEntity, len(entities))
	for index, entity := range entities {
		cloned[index] = entity
		if entity.User != nil {
			userCopy := *entity.User
			cloned[index].User = &userCopy
		}
	}

	return cloned
}
