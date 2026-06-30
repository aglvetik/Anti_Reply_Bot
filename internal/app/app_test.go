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

	if len(client.deletes) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(client.deletes))
	}
	if client.deletes[0].messageID != 100 {
		t.Fatalf("expected violating message to be deleted first, got message %d", client.deletes[0].messageID)
	}
	if len(client.sends) != 0 {
		t.Fatalf("expected no warning message, got %d send calls", len(client.sends))
	}
	if len(*scheduledDelays) != 0 {
		t.Fatalf("expected no scheduled warning deletion, got %d", len(*scheduledDelays))
	}
}

func TestProcessMessageViolationWarningPlain(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       true,
			ViolationWarningMentionTarget: false,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		nil,
	)

	app.processMessage(context.Background(), newViolationReplyMessage())

	if len(client.deletes) != 2 {
		t.Fatalf("expected 2 delete calls, got %d", len(client.deletes))
	}
	if client.deletes[0].messageID != 100 {
		t.Fatalf("expected violating message to be deleted first, got message %d", client.deletes[0].messageID)
	}
	if client.deletes[1].messageID != 900 {
		t.Fatalf("expected warning message to be deleted after ttl, got message %d", client.deletes[1].messageID)
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected 1 warning message, got %d", len(client.sends))
	}
	if client.sends[0].text != violationWarningText {
		t.Fatalf("unexpected warning text: got %q want %q", client.sends[0].text, violationWarningText)
	}
	if len(client.sends[0].entities) != 0 {
		t.Fatalf("expected plain warning without entities, got %d entities", len(client.sends[0].entities))
	}
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected warning deletion scheduled after 5s, got %v", *scheduledDelays)
	}
}

func TestProcessMessageViolationWarningMentionsProtectedUserOnReply(t *testing.T) {
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

	assertWarningMentionsProtectedUser(t, client, 10)
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected warning deletion scheduled after 5s, got %v", *scheduledDelays)
	}
}

func TestProcessMessageViolationWarningMentionsProtectedUserOnMention(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       true,
			ViolationWarningMentionTarget: true,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		[]rules.KnownUser{{UserID: 10, Username: "protected_user", FirstName: "Protected"}},
	)

	app.processMessage(context.Background(), newViolationMentionMessage())

	assertWarningMentionsProtectedUser(t, client, 10)
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected warning deletion scheduled after 5s, got %v", *scheduledDelays)
	}
}

func TestProcessMessageReplyStopCommandNotDeletedWhenUnblocked(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		nil,
	)

	app.processMessage(context.Background(), newReplyStopCommandMessage(10, 20, 30))

	if containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected normal reply stop command message to remain in chat")
	}
	if len(client.sends) != 1 || client.sends[0].text != restrictionEnabledText {
		t.Fatalf("expected enabled confirmation message, got %+v", client.sends)
	}
	if len(client.deletes) != 1 || client.deletes[0].messageID != 900 {
		t.Fatalf("expected only bot confirmation message cleanup, got %+v", client.deletes)
	}
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected only bot confirmation message to be scheduled for ttl cleanup, got %v", *scheduledDelays)
	}
	if !app.rules.HasActiveRule(rules.RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}) {
		t.Fatal("expected reply stop command to enable B -> A rule")
	}
}

func TestProcessMessageMentionStopCommandEnablesRuleAndKeepsMessage(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		[]rules.KnownUser{{UserID: 20, Username: "blocked_user", FirstName: "Blocked"}},
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(10, 30, "@blocked_user бот стоп", "blocked_user", 20))

	if containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected normal mention stop command message to remain in chat")
	}
	if !app.rules.HasActiveRule(rules.RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}) {
		t.Fatal("expected mention stop command to enable B -> A rule")
	}
	if len(client.sends) != 1 || client.sends[0].text != restrictionEnabledText {
		t.Fatalf("expected enabled confirmation message, got %+v", client.sends)
	}
	if len(client.deletes) != 1 || client.deletes[0].messageID != 900 {
		t.Fatalf("expected only bot confirmation message cleanup, got %+v", client.deletes)
	}
	if len(*scheduledDelays) != 1 {
		t.Fatalf("expected confirmation ttl cleanup to be scheduled once, got %v", *scheduledDelays)
	}
}

func TestProcessMessageMentionStopCommandUnknownUsername(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{WarningTTL: 5 * time.Second},
		nil,
		nil,
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(10, 30, "@unknown бот стоп", "unknown", 20))

	if containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected unknown-target mention command message to remain in chat")
	}
	if len(client.sends) != 1 || client.sends[0].text != userResolveFailedText {
		t.Fatalf("expected unknown-user error message, got %+v", client.sends)
	}
	if len(client.deletes) != 1 || client.deletes[0].messageID != 900 {
		t.Fatalf("expected only bot error message cleanup, got %+v", client.deletes)
	}
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected error message ttl cleanup to be scheduled once, got %v", *scheduledDelays)
	}
}

func TestProcessMessageReverseMentionStopCommandDeletesOriginalSilently(t *testing.T) {
	app, client, scheduledDelays := newTestApp(
		Config{
			WarningTTL:                    5 * time.Second,
			ViolationWarningEnabled:       true,
			ViolationWarningMentionTarget: true,
		},
		[]rules.RuleKey{{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}},
		[]rules.KnownUser{{UserID: 10, Username: "protected_user", FirstName: "Protected"}},
	)

	app.processMessage(context.Background(), newMentionStopCommandMessage(20, 30, "@protected_user бот стоп", "protected_user", 10))

	if !app.rules.HasActiveRule(rules.RuleKey{ChatID: 30, ProtectedUserID: 20, BlockedUserID: 10}) {
		t.Fatal("expected reverse mention stop command to enable A -> B rule")
	}
	if !containsDeletedMessage(client.deletes, 100) {
		t.Fatal("expected self-violating reverse command message to be deleted immediately")
	}
	if len(client.sends) != 1 || client.sends[0].text != restrictionEnabledText {
		t.Fatalf("expected only confirmation message, got %+v", client.sends)
	}
	if len(client.sends[0].entities) != 0 {
		t.Fatalf("expected no violation warning mention entities, got %+v", client.sends[0].entities)
	}
	if len(*scheduledDelays) != 1 {
		t.Fatalf("expected only confirmation message ttl cleanup, got %v", *scheduledDelays)
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

func newViolationMentionMessage() *telegram.Message {
	msg := &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: 30,
		},
		From: &telegram.User{
			ID:        20,
			FirstName: "Blocked",
		},
		Text: "привет @protected_user",
	}
	msg.Entities = []telegram.MessageEntity{
		{
			Type:   "mention",
			Offset: utf16Length("привет "),
			Length: utf16Length("@protected_user"),
		},
	}
	return msg
}

func newReplyStopCommandMessage(fromID, replyToID, chatID int64) *telegram.Message {
	msg := &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: chatID,
		},
		From: &telegram.User{
			ID:        fromID,
			FirstName: "Sender",
		},
		Text: "Бот стоп",
		ReplyToMessage: &telegram.Message{
			MessageID: 101,
			From: &telegram.User{
				ID:        replyToID,
				FirstName: "Target",
			},
		},
	}
	return msg
}

func newMentionStopCommandMessage(fromID, chatID int64, text, username string, targetID int64) *telegram.Message {
	msg := &telegram.Message{
		MessageID: 100,
		Chat: &telegram.Chat{
			ID: chatID,
		},
		From: &telegram.User{
			ID:        fromID,
			FirstName: "Sender",
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

	if targetID != 0 {
		msg.Entities[0].User = &telegram.User{ID: targetID, Username: username}
	}

	return msg
}

func assertWarningMentionsProtectedUser(t *testing.T, client *fakeTelegramClient, protectedUserID int64) {
	t.Helper()

	if len(client.deletes) != 2 {
		t.Fatalf("expected 2 delete calls, got %d", len(client.deletes))
	}
	if client.deletes[0].messageID != 100 {
		t.Fatalf("expected violating message to be deleted first, got message %d", client.deletes[0].messageID)
	}
	if client.deletes[1].messageID != 900 {
		t.Fatalf("expected warning message to be deleted after ttl, got message %d", client.deletes[1].messageID)
	}
	if len(client.sends) != 1 {
		t.Fatalf("expected 1 warning message, got %d", len(client.sends))
	}
	if client.sends[0].text != violationMentionLabel+", "+violationWarningText {
		t.Fatalf("unexpected warning text: got %q", client.sends[0].text)
	}
	if len(client.sends[0].entities) != 1 {
		t.Fatalf("expected 1 text_mention entity, got %d", len(client.sends[0].entities))
	}

	entity := client.sends[0].entities[0]
	if entity.Type != "text_mention" {
		t.Fatalf("expected text_mention entity, got %q", entity.Type)
	}
	if entity.Offset != 0 || entity.Length != utf16Length(violationMentionLabel) {
		t.Fatalf("unexpected entity span: offset=%d length=%d", entity.Offset, entity.Length)
	}
	if entity.User == nil || entity.User.ID != protectedUserID {
		t.Fatalf("expected entity user ID %d, got %+v", protectedUserID, entity.User)
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
