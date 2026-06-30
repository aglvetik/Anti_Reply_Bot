package rules

import (
	"context"
	"fmt"

	"telegram-stop-reply-bot/internal/telegram"
)

type Store interface {
	UpsertRule(ctx context.Context, key RuleKey, enabled bool) error
	UpsertKnownUsers(ctx context.Context, users []KnownUser) error
}

type Service struct {
	store Store
	cache *Cache
}

type ToggleResult struct {
	RuleKey           RuleKey
	Enabled           bool
	BlockedUserImmune bool
}

type Violation struct {
	RuleKey RuleKey
}

func NewService(store Store, cache *Cache) *Service {
	return &Service{
		store: store,
		cache: cache,
	}
}

func (s *Service) IsStopCommand(msg *telegram.Message) bool {
	return IsStopCommand(msg)
}

func (s *Service) UpdateKnownUsers(ctx context.Context, msg *telegram.Message) error {
	users := CollectKnownUsers(msg)
	if len(users) == 0 {
		return nil
	}

	for _, user := range users {
		s.cache.UpsertKnownUser(user)
	}

	if s.store == nil {
		return nil
	}
	if err := s.store.UpsertKnownUsers(ctx, users); err != nil {
		return fmt.Errorf("upsert known users: %w", err)
	}
	return nil
}

func (s *Service) HandleStopCommand(ctx context.Context, msg *telegram.Message) (ToggleResult, error) {
	if !IsStopCommand(msg) {
		return ToggleResult{}, fmt.Errorf("message is not a valid stop command")
	}

	key := RuleKey{
		ChatID:          msg.Chat.ID,
		ProtectedUserID: msg.From.ID,
		BlockedUserID:   msg.ReplyToMessage.From.ID,
	}

	if s.cache.IsImmune(key.BlockedUserID) {
		return ToggleResult{
			RuleKey:           key,
			BlockedUserImmune: true,
		}, nil
	}

	nextEnabled := !s.cache.IsRuleActive(key)
	if s.store != nil {
		if err := s.store.UpsertRule(ctx, key, nextEnabled); err != nil {
			return ToggleResult{}, fmt.Errorf("upsert rule: %w", err)
		}
	}

	s.cache.SetRule(key, nextEnabled)

	return ToggleResult{
		RuleKey: key,
		Enabled: nextEnabled,
	}, nil
}

func (s *Service) DetectViolation(msg *telegram.Message) (Violation, bool) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return Violation{}, false
	}
	if msg.From.IsBot || s.cache.IsImmune(msg.From.ID) || IsStopCommand(msg) {
		return Violation{}, false
	}

	senderID := msg.From.ID
	chatID := msg.Chat.ID

	if reply := msg.ReplyToMessage; reply != nil && reply.From != nil {
		key := RuleKey{
			ChatID:          chatID,
			ProtectedUserID: reply.From.ID,
			BlockedUserID:   senderID,
		}
		if s.cache.IsRuleActive(key) {
			return Violation{RuleKey: key}, true
		}
	}

	for _, protectedUserID := range mentionedProtectedUserIDs(s.cache, msg) {
		key := RuleKey{
			ChatID:          chatID,
			ProtectedUserID: protectedUserID,
			BlockedUserID:   senderID,
		}
		if s.cache.IsRuleActive(key) {
			return Violation{RuleKey: key}, true
		}
	}

	return Violation{}, false
}

func CollectKnownUsers(msg *telegram.Message) []KnownUser {
	if msg == nil {
		return nil
	}

	seen := make(map[int64]KnownUser)
	order := make([]int64, 0, 4)

	addUser := func(user *telegram.User) {
		if user == nil {
			return
		}
		if _, exists := seen[user.ID]; !exists {
			order = append(order, user.ID)
		}
		seen[user.ID] = KnownUser{
			UserID:    user.ID,
			Username:  user.Username,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			IsBot:     user.IsBot,
		}
	}

	addUser(msg.From)
	if msg.ReplyToMessage != nil {
		addUser(msg.ReplyToMessage.From)
	}
	for _, source := range messageEntitySources(msg) {
		for _, entity := range source.entities {
			if entity.Type == "text_mention" {
				addUser(entity.User)
			}
		}
	}

	users := make([]KnownUser, 0, len(order))
	for _, userID := range order {
		users = append(users, seen[userID])
	}

	return users
}
