package rules

import (
	"strings"
	"unicode/utf16"

	"telegram-stop-reply-bot/internal/telegram"
)

const stopCommandNormalized = "бот стоп"

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
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return false
	}
	if msg.From.IsBot || msg.Text == "" {
		return false
	}
	if NormalizeCommand(msg.Text) != stopCommandNormalized {
		return false
	}
	if msg.ReplyToMessage == nil || msg.ReplyToMessage.From == nil {
		return false
	}
	if msg.ReplyToMessage.From.IsBot {
		return false
	}
	if msg.From.ID == msg.ReplyToMessage.From.ID {
		return false
	}
	return true
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
