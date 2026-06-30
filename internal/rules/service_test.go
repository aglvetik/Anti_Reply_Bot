package rules

import (
	"context"
	"testing"

	"telegram-stop-reply-bot/internal/telegram"
)

type fakeStore struct {
	ruleStates       map[RuleKey]bool
	upsertRuleCalls  int
	upsertKnownCalls int
}

func (f *fakeStore) UpsertRule(_ context.Context, key RuleKey, enabled bool) error {
	if f.ruleStates == nil {
		f.ruleStates = make(map[RuleKey]bool)
	}
	f.ruleStates[key] = enabled
	f.upsertRuleCalls++
	return nil
}

func (f *fakeStore) UpsertKnownUsers(_ context.Context, _ []KnownUser) error {
	f.upsertKnownCalls++
	return nil
}

func TestHandleStopCommandEnable(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	service := NewService(store, NewCache(nil, nil, nil))
	msg := newReplyMessage(10, 20, 30, "Бот стоп")

	result, err := service.HandleStopCommand(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleStopCommand() error = %v", err)
	}

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	if !result.Enabled {
		t.Fatal("expected rule to be enabled")
	}
	if !service.cache.IsRuleActive(key) {
		t.Fatal("expected active rule in cache")
	}
	if enabled := store.ruleStates[key]; !enabled {
		t.Fatal("expected store to persist enabled rule")
	}
}

func TestHandleStopCommandDisable(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	service := NewService(store, NewCache(nil, nil, nil))
	msg := newReplyMessage(10, 20, 30, "Бот стоп")

	if _, err := service.HandleStopCommand(context.Background(), msg); err != nil {
		t.Fatalf("initial HandleStopCommand() error = %v", err)
	}
	result, err := service.HandleStopCommand(context.Background(), msg)
	if err != nil {
		t.Fatalf("second HandleStopCommand() error = %v", err)
	}

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	if result.Enabled {
		t.Fatal("expected rule to be disabled on second toggle")
	}
	if service.cache.IsRuleActive(key) {
		t.Fatal("expected rule to be removed from active cache")
	}
	if enabled := store.ruleStates[key]; enabled {
		t.Fatal("expected store to persist disabled rule")
	}
}

func TestHandleStopCommandReverseRuleEnable(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	service := NewService(store, NewCache(nil, nil, nil))

	msgAB := newReplyMessage(10, 20, 30, "Бот стоп")
	msgBA := newReplyMessage(20, 10, 30, "Бот стоп")

	if _, err := service.HandleStopCommand(context.Background(), msgAB); err != nil {
		t.Fatalf("HandleStopCommand(A->B) error = %v", err)
	}
	result, err := service.HandleStopCommand(context.Background(), msgBA)
	if err != nil {
		t.Fatalf("HandleStopCommand(B->A) error = %v", err)
	}

	keyAB := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	keyBA := RuleKey{ChatID: 30, ProtectedUserID: 20, BlockedUserID: 10}

	if !result.Enabled {
		t.Fatal("expected reverse rule to be enabled")
	}
	if !service.cache.IsRuleActive(keyAB) {
		t.Fatal("expected original rule to remain active")
	}
	if !service.cache.IsRuleActive(keyBA) {
		t.Fatal("expected reverse rule to be active")
	}
}

func TestHandleStopCommandRepeatedSameDirectionDisablesOnlyThatDirection(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	service := NewService(store, NewCache(nil, nil, nil))

	msgAB := newReplyMessage(10, 20, 30, "Бот стоп")
	msgBA := newReplyMessage(20, 10, 30, "Бот стоп")

	if _, err := service.HandleStopCommand(context.Background(), msgAB); err != nil {
		t.Fatalf("HandleStopCommand(A->B) error = %v", err)
	}
	if _, err := service.HandleStopCommand(context.Background(), msgBA); err != nil {
		t.Fatalf("HandleStopCommand(B->A) error = %v", err)
	}
	result, err := service.HandleStopCommand(context.Background(), msgAB)
	if err != nil {
		t.Fatalf("HandleStopCommand(A->B second time) error = %v", err)
	}

	keyAB := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	keyBA := RuleKey{ChatID: 30, ProtectedUserID: 20, BlockedUserID: 10}

	if result.Enabled {
		t.Fatal("expected repeated same-direction command to disable the original direction")
	}
	if service.cache.IsRuleActive(keyAB) {
		t.Fatal("expected original direction to be disabled")
	}
	if !service.cache.IsRuleActive(keyBA) {
		t.Fatal("expected reverse direction to remain active")
	}
}

func TestDetectViolationBlockedReply(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, nil))

	msg := newReplyMessage(20, 10, 30, "обычный ответ")

	violation, ok := service.DetectViolation(msg)
	if !ok {
		t.Fatal("expected reply violation to be detected")
	}
	if violation.RuleKey != key {
		t.Fatalf("unexpected violation key: got %+v want %+v", violation.RuleKey, key)
	}
}

func TestDetectViolationBlockedUsernameMention(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	knownUsers := []KnownUser{{UserID: 10, Username: "TargetUser"}}
	service := NewService(nil, NewCache([]RuleKey{key}, knownUsers, nil))

	msg := newMessage(20, 30, "🙂 @targetuser")
	msg.Entities = []telegram.MessageEntity{
		{Type: "mention", Offset: 3, Length: 11},
	}

	violation, ok := service.DetectViolation(msg)
	if !ok {
		t.Fatal("expected username mention violation to be detected")
	}
	if violation.RuleKey != key {
		t.Fatalf("unexpected violation key: got %+v want %+v", violation.RuleKey, key)
	}
}

func TestDetectViolationBlockedTextMention(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, nil))

	msg := newMessage(20, 30, "ping")
	msg.Entities = []telegram.MessageEntity{
		{
			Type:   "text_mention",
			Offset: 0,
			Length: 4,
			User: &telegram.User{
				ID: 10,
			},
		},
	}

	violation, ok := service.DetectViolation(msg)
	if !ok {
		t.Fatal("expected text_mention violation to be detected")
	}
	if violation.RuleKey != key {
		t.Fatalf("unexpected violation key: got %+v want %+v", violation.RuleKey, key)
	}
}

func TestDetectViolationNormalMessageIgnored(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, nil))

	msg := newMessage(20, 30, "обычное сообщение")

	if _, ok := service.DetectViolation(msg); ok {
		t.Fatal("expected normal message without reply or mention to be ignored")
	}
}

func TestDetectViolationStopCommandBypassesViolationCheck(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, nil))

	msg := newReplyMessage(20, 10, 30, " БОТ   СТОП ")

	if !service.IsStopCommand(msg) {
		t.Fatal("expected command to be valid before bypass check")
	}
	if _, ok := service.DetectViolation(msg); ok {
		t.Fatal("expected valid stop command to bypass violation detection")
	}
}

func TestDetectViolationImmuneUserCanAlwaysReply(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	immune := map[int64]struct{}{20: {}}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, immune))

	msg := newReplyMessage(20, 10, 30, "обычный ответ")

	if _, ok := service.DetectViolation(msg); ok {
		t.Fatal("expected immune user reply to be allowed")
	}
}

func TestDetectViolationImmuneUserCanAlwaysMention(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	immune := map[int64]struct{}{20: {}}
	knownUsers := []KnownUser{{UserID: 10, Username: "TargetUser"}}
	service := NewService(nil, NewCache([]RuleKey{key}, knownUsers, immune))

	msg := newMessage(20, 30, "@targetuser")
	msg.Entities = []telegram.MessageEntity{
		{Type: "mention", Offset: 0, Length: 11},
	}

	if _, ok := service.DetectViolation(msg); ok {
		t.Fatal("expected immune user mention to be allowed")
	}
}

func TestHandleStopCommandRuleAgainstImmuneUserIgnored(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	immune := map[int64]struct{}{20: {}}
	service := NewService(store, NewCache(nil, nil, immune))

	msg := newReplyMessage(10, 20, 30, "Бот стоп")

	result, err := service.HandleStopCommand(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleStopCommand() error = %v", err)
	}

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	if !result.BlockedUserImmune {
		t.Fatal("expected immune target result flag")
	}
	if service.cache.IsRuleActive(key) {
		t.Fatal("expected rule against immune user to stay inactive")
	}
	if store.upsertRuleCalls != 0 {
		t.Fatal("expected store not to persist rule against immune user")
	}
}

func TestDetectViolationIgnoresMessagesFromBots(t *testing.T) {
	t.Parallel()

	key := RuleKey{ChatID: 30, ProtectedUserID: 10, BlockedUserID: 20}
	service := NewService(nil, NewCache([]RuleKey{key}, nil, nil))

	msg := newReplyMessage(20, 10, 30, "bot reply")
	msg.From.IsBot = true

	if _, ok := service.DetectViolation(msg); ok {
		t.Fatal("expected bot messages to be ignored")
	}
}
