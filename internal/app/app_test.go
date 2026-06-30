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
	app, client, scheduledDelays := newViolationTestApp(Config{
		WarningTTL:                    5 * time.Second,
		ViolationWarningEnabled:       false,
		ViolationWarningMentionTarget: true,
	})

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
	app, client, scheduledDelays := newViolationTestApp(Config{
		WarningTTL:                    5 * time.Second,
		ViolationWarningEnabled:       true,
		ViolationWarningMentionTarget: false,
	})

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

func TestProcessMessageViolationWarningMentionsProtectedUser(t *testing.T) {
	app, client, scheduledDelays := newViolationTestApp(Config{
		WarningTTL:                    5 * time.Second,
		ViolationWarningEnabled:       true,
		ViolationWarningMentionTarget: true,
	})

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
	if entity.User == nil || entity.User.ID != 10 {
		t.Fatalf("expected entity user ID 10, got %+v", entity.User)
	}
	if len(*scheduledDelays) != 1 || (*scheduledDelays)[0] != 5*time.Second {
		t.Fatalf("expected warning deletion scheduled after 5s, got %v", *scheduledDelays)
	}
}

func newViolationTestApp(cfg Config) (*App, *fakeTelegramClient, *[]time.Duration) {
	activeRule := rules.RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := rules.NewService(nil, rules.NewCache([]rules.RuleKey{activeRule}, nil, nil))
	client := &fakeTelegramClient{}
	scheduledDelays := make([]time.Duration, 0, 1)

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
