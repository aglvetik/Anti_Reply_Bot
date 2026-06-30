package rules

import (
	"strings"
	"sync"
)

type RuleKey struct {
	ChatID          int64
	ProtectedUserID int64
	BlockedUserID   int64
}

type KnownUser struct {
	UserID    int64
	Username  string
	FirstName string
	LastName  string
	IsBot     bool
}

type Cache struct {
	mu            sync.RWMutex
	activeRules   map[RuleKey]struct{}
	knownUsers    map[int64]KnownUser
	usernameIndex map[string]map[int64]struct{}
	immuneUsers   map[int64]struct{}
}

func NewCache(activeRules []RuleKey, knownUsers []KnownUser, immuneUsers map[int64]struct{}) *Cache {
	cache := &Cache{
		activeRules:   make(map[RuleKey]struct{}, len(activeRules)),
		knownUsers:    make(map[int64]KnownUser, len(knownUsers)),
		usernameIndex: make(map[string]map[int64]struct{}),
		immuneUsers:   copyUserIDSet(immuneUsers),
	}

	for _, key := range activeRules {
		if _, immune := cache.immuneUsers[key.BlockedUserID]; immune {
			continue
		}
		cache.activeRules[key] = struct{}{}
	}

	for _, user := range knownUsers {
		cache.upsertKnownUserLocked(user)
	}

	return cache
}

func (c *Cache) IsImmune(userID int64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.immuneUsers[userID]
	return ok
}

func (c *Cache) IsRuleActive(key RuleKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, immune := c.immuneUsers[key.BlockedUserID]; immune {
		return false
	}
	_, ok := c.activeRules[key]
	return ok
}

func (c *Cache) SetRule(key RuleKey, enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, immune := c.immuneUsers[key.BlockedUserID]; immune {
		delete(c.activeRules, key)
		return
	}

	if enabled {
		c.activeRules[key] = struct{}{}
		return
	}
	delete(c.activeRules, key)
}

func (c *Cache) UpsertKnownUser(user KnownUser) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.upsertKnownUserLocked(user)
}

func (c *Cache) KnownUser(userID int64) (KnownUser, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	user, ok := c.knownUsers[userID]
	return user, ok
}

func (c *Cache) ProtectedIDsByUsername(username string) []int64 {
	normalized := normalizeUsername(username)
	if normalized == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	index := c.usernameIndex[normalized]
	if len(index) == 0 {
		return nil
	}

	result := make([]int64, 0, len(index))
	for userID := range index {
		result = append(result, userID)
	}
	return result
}

func copyUserIDSet(source map[int64]struct{}) map[int64]struct{} {
	result := make(map[int64]struct{}, len(source))
	for userID := range source {
		result[userID] = struct{}{}
	}
	return result
}

func (c *Cache) upsertKnownUserLocked(user KnownUser) {
	if existing, ok := c.knownUsers[user.UserID]; ok {
		oldUsername := normalizeUsername(existing.Username)
		newUsername := normalizeUsername(user.Username)
		if oldUsername != "" && oldUsername != newUsername {
			c.removeUsernameLocked(oldUsername, user.UserID)
		}
	}

	c.knownUsers[user.UserID] = user

	username := normalizeUsername(user.Username)
	if username == "" {
		return
	}
	if c.usernameIndex[username] == nil {
		c.usernameIndex[username] = make(map[int64]struct{})
	}
	c.usernameIndex[username][user.UserID] = struct{}{}
}

func (c *Cache) removeUsernameLocked(username string, userID int64) {
	index := c.usernameIndex[username]
	if len(index) == 0 {
		return
	}
	delete(index, userID)
	if len(index) == 0 {
		delete(c.usernameIndex, username)
	}
}

func normalizeUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return strings.ToLower(username)
}
