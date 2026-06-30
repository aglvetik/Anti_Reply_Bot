package rules

import (
	"strings"
	"unicode/utf16"

	"telegram-stop-reply-bot/internal/telegram"
)

const stopCommandNormalized = "бот стоп"

type StopCommandStatus int

const (
	StopCommandNone StopCommandStatus = iota
	StopCommandValid
	StopCommandUnknownTarget
)

type StopCommandMatch struct {
	Status     StopCommandStatus
	TargetUser *telegram.User
}

func (m StopCommandMatch) IsCommand() bool {
	return m.Status != StopCommandNone
}

func (m StopCommandMatch) IsValid() bool {
	return m.Status == StopCommandValid
}

type entitySource struct {
	text     string
	entities []telegram.MessageEntity
}

func NormalizeCommand(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func IsStopCommand(msg *telegram.Message) bool {
	return matchReplyStopCommand(msg).IsValid()
}

func matchReplyStopCommand(msg *telegram.Message) StopCommandMatch {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return StopCommandMatch{}
	}
	if msg.From.IsBot || msg.Text == "" {
		return StopCommandMatch{}
	}
	if NormalizeCommand(msg.Text) != stopCommandNormalized {
		return StopCommandMatch{}
	}
	if msg.ReplyToMessage == nil || msg.ReplyToMessage.From == nil {
		return StopCommandMatch{}
	}
	if msg.ReplyToMessage.From.IsBot {
		return StopCommandMatch{}
	}
	if msg.From.ID == msg.ReplyToMessage.From.ID {
		return StopCommandMatch{}
	}

	return StopCommandMatch{
		Status:     StopCommandValid,
		TargetUser: cloneUser(msg.ReplyToMessage.From),
	}
}

func matchMentionStopCommand(cache *Cache, msg *telegram.Message) StopCommandMatch {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return StopCommandMatch{}
	}
	if msg.From.IsBot || msg.Text == "" || msg.ReplyToMessage != nil {
		return StopCommandMatch{}
	}
	if len(msg.Entities) == 0 {
		return StopCommandMatch{}
	}

	mentions := make([]telegram.MessageEntity, 0, 1)
	for _, entity := range msg.Entities {
		if entity.Type == "mention" || entity.Type == "text_mention" {
			mentions = append(mentions, entity)
		}
	}
	if len(mentions) != 1 {
		return StopCommandMatch{}
	}

	remainingText, ok := removeUTF16Range(msg.Text, mentions[0].Offset, mentions[0].Length)
	if !ok || NormalizeCommand(remainingText) != stopCommandNormalized {
		return StopCommandMatch{}
	}

	targetUser, status := resolveMentionCommandTarget(cache, msg.Text, mentions[0])
	if status != StopCommandValid {
		return StopCommandMatch{Status: status}
	}
	if targetUser == nil || targetUser.IsBot || targetUser.ID == msg.From.ID {
		return StopCommandMatch{}
	}

	return StopCommandMatch{
		Status:     StopCommandValid,
		TargetUser: targetUser,
	}
}

func resolveMentionCommandTarget(cache *Cache, text string, entity telegram.MessageEntity) (*telegram.User, StopCommandStatus) {
	switch entity.Type {
	case "text_mention":
		if entity.User == nil {
			return nil, StopCommandNone
		}
		return cloneUser(entity.User), StopCommandValid
	case "mention":
		mention, ok := substringByUTF16(text, entity.Offset, entity.Length)
		if !ok {
			return nil, StopCommandNone
		}

		users := cache.KnownUsersByUsername(mention)
		if len(users) != 1 {
			return nil, StopCommandUnknownTarget
		}

		user := users[0]
		return &telegram.User{
			ID:        user.UserID,
			IsBot:     user.IsBot,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Username:  user.Username,
		}, StopCommandValid
	default:
		return nil, StopCommandNone
	}
}

func mentionedProtectedUserIDs(cache *Cache, msg *telegram.Message) []int64 {
	if msg == nil {
		return nil
	}

	seen := make(map[int64]struct{})
	var result []int64

	for _, source := range messageEntitySources(msg) {
		for _, entity := range source.entities {
			switch entity.Type {
			case "mention":
				mention, ok := substringByUTF16(source.text, entity.Offset, entity.Length)
				if !ok {
					continue
				}
				for _, userID := range cache.ProtectedIDsByUsername(mention) {
					if _, exists := seen[userID]; exists {
						continue
					}
					seen[userID] = struct{}{}
					result = append(result, userID)
				}
			case "text_mention":
				if entity.User == nil {
					continue
				}
				if _, exists := seen[entity.User.ID]; exists {
					continue
				}
				seen[entity.User.ID] = struct{}{}
				result = append(result, entity.User.ID)
			}
		}
	}

	return result
}

func messageEntitySources(msg *telegram.Message) []entitySource {
	sources := make([]entitySource, 0, 2)
	if msg.Text != "" && len(msg.Entities) > 0 {
		sources = append(sources, entitySource{
			text:     msg.Text,
			entities: msg.Entities,
		})
	}
	if msg.Caption != "" && len(msg.CaptionEntities) > 0 {
		sources = append(sources, entitySource{
			text:     msg.Caption,
			entities: msg.CaptionEntities,
		})
	}
	return sources
}

func substringByUTF16(text string, offset, length int) (string, bool) {
	if offset < 0 || length <= 0 {
		return "", false
	}

	encoded := utf16.Encode([]rune(text))
	end := offset + length
	if end > len(encoded) {
		return "", false
	}

	return string(utf16.Decode(encoded[offset:end])), true
}

func removeUTF16Range(text string, offset, length int) (string, bool) {
	if offset < 0 || length <= 0 {
		return "", false
	}

	encoded := utf16.Encode([]rune(text))
	end := offset + length
	if end > len(encoded) {
		return "", false
	}

	trimmed := append([]uint16{}, encoded[:offset]...)
	trimmed = append(trimmed, encoded[end:]...)
	return string(utf16.Decode(trimmed)), true
}

func cloneUser(user *telegram.User) *telegram.User {
	if user == nil {
		return nil
	}

	cloned := *user
	return &cloned
}
