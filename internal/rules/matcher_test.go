package rules

import (
	"testing"
	"unicode/utf16"

	"telegram-stop-reply-bot/internal/telegram"
)

func TestNormalizeCommand(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		text string
		want string
	}{
		{name: "exact", text: "Бот стоп", want: "бот стоп"},
		{name: "lowercase", text: "бот стоп", want: "бот стоп"},
		{name: "uppercase", text: "БОТ СТОП", want: "бот стоп"},
		{name: "extra spaces", text: "  бот   стоп  ", want: "бот стоп"},
		{name: "empty", text: "   ", want: ""},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := NormalizeCommand(testCase.text)
			if got != testCase.want {
				t.Fatalf("NormalizeCommand(%q) = %q, want %q", testCase.text, got, testCase.want)
			}
		})
	}
}

func TestIsStopCommandValidReply(t *testing.T) {
	t.Parallel()

	msg := newReplyMessage(100, 200, 300, " БОТ   СТОП ")

	if !IsStopCommand(msg) {
		t.Fatal("expected valid stop command to be accepted")
	}
}

func TestIsStopCommandInvalidWithoutReply(t *testing.T) {
	t.Parallel()

	msg := newMessage(100, 300, "Бот стоп")

	if IsStopCommand(msg) {
		t.Fatal("expected stop command without reply to be rejected")
	}
}

func TestIsStopCommandRejectsSelfReply(t *testing.T) {
	t.Parallel()

	msg := newReplyMessage(100, 100, 300, "Бот стоп")

	if IsStopCommand(msg) {
		t.Fatal("expected self-reply stop command to be rejected")
	}
}

func TestMatchStopCommandValidMentionPrefix(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewCache(nil, []KnownUser{{UserID: 200, Username: "someuser"}}, nil))
	msg := newMentionCommandMessage(100, 300, "@someuser БОТ   СТОП", "@someuser", 200)

	match := service.MatchStopCommand(msg)
	if !match.IsValid() {
		t.Fatal("expected mention stop command to be accepted")
	}
	if match.TargetUser == nil || match.TargetUser.ID != 200 {
		t.Fatalf("expected mention target user 200, got %+v", match.TargetUser)
	}
}

func TestMatchStopCommandValidMentionSuffix(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewCache(nil, []KnownUser{{UserID: 200, Username: "someuser"}}, nil))
	msg := newMentionCommandMessage(100, 300, "БОТ СТОП @someuser", "@someuser", 200)

	match := service.MatchStopCommand(msg)
	if !match.IsValid() {
		t.Fatal("expected suffix mention stop command to be accepted")
	}
}

func TestMatchStopCommandUnknownMention(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewCache(nil, nil, nil))
	msg := newMentionCommandMessage(100, 300, "@unknown бот стоп", "@unknown", 0)

	match := service.MatchStopCommand(msg)
	if match.Status != StopCommandUnknownTarget {
		t.Fatalf("expected unknown-target mention status, got %v", match.Status)
	}
}

func TestMatchStopCommandMultipleMentionsInvalid(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewCache(nil, []KnownUser{
		{UserID: 200, Username: "one"},
		{UserID: 201, Username: "two"},
	}, nil))
	msg := newMessage(100, 300, "@one @two бот стоп")
	msg.Entities = []telegram.MessageEntity{
		{Type: "mention", Offset: 0, Length: utf16Len("@one")},
		{Type: "mention", Offset: utf16Len("@one "), Length: utf16Len("@two")},
	}

	match := service.MatchStopCommand(msg)
	if match.IsCommand() {
		t.Fatalf("expected multiple mention command to be invalid, got %+v", match)
	}
}

func TestMatchStopCommandSupportsTextMention(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewCache(nil, nil, nil))
	msg := newMessage(100, 300, "Пользователь бот стоп")
	msg.Entities = []telegram.MessageEntity{
		{
			Type:   "text_mention",
			Offset: 0,
			Length: utf16Len("Пользователь"),
			User: &telegram.User{
				ID:        200,
				FirstName: "Some",
			},
		},
	}

	match := service.MatchStopCommand(msg)
	if !match.IsValid() {
		t.Fatal("expected text_mention stop command to be accepted")
	}
	if match.TargetUser == nil || match.TargetUser.ID != 200 {
		t.Fatalf("expected text_mention target user 200, got %+v", match.TargetUser)
	}
}

func newMessage(fromID, chatID int64, text string) *telegram.Message {
	return &telegram.Message{
		MessageID: 1,
		Chat: &telegram.Chat{
			ID: chatID,
		},
		From: &telegram.User{
			ID: fromID,
		},
		Text: text,
	}
}

func newReplyMessage(fromID, replyToID, chatID int64, text string) *telegram.Message {
	msg := newMessage(fromID, chatID, text)
	msg.ReplyToMessage = &telegram.Message{
		MessageID: 2,
		From: &telegram.User{
			ID: replyToID,
		},
	}
	return msg
}

func newMentionCommandMessage(fromID, chatID int64, text, mention string, targetID int64) *telegram.Message {
	msg := newMessage(fromID, chatID, text)
	offset := utf16Len(text[:findSubstring(text, mention)])
	msg.Entities = []telegram.MessageEntity{
		{
			Type:   "mention",
			Offset: offset,
			Length: utf16Len(mention),
		},
	}
	if targetID != 0 {
		msg.Entities[0].User = &telegram.User{ID: targetID}
	}
	return msg
}

func findSubstring(text, substring string) int {
	for index := range text {
		if len(text[index:]) >= len(substring) && text[index:index+len(substring)] == substring {
			return index
		}
	}
	return -1
}

func utf16Len(text string) int {
	return len(utf16.Encode([]rune(text)))
}
