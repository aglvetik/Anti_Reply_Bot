package rules

import (
	"testing"

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
