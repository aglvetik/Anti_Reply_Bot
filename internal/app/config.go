package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr              = "127.0.0.1:8080"
	defaultSQLitePath            = "./data/bot.sqlite"
	defaultLogLevel              = "info"
	defaultWarningTTLSeconds     = 5
	defaultWebhookMaxConnections = 40
	defaultImmuneUserIDs         = "5300889569"
	defaultViolationWarning      = true
	defaultViolationMention      = true
)

type Config struct {
	BotToken                      string
	WebhookSecret                 string
	PublicWebhookURL              string
	HTTPAddr                      string
	SQLitePath                    string
	LogLevel                      string
	WarningTTL                    time.Duration
	WebhookMaxConnections         int
	ImmuneUserIDs                 map[int64]struct{}
	ViolationWarningEnabled       bool
	ViolationWarningMentionTarget bool
}

func LoadConfig() (Config, error) {
	cfg := Config{
		BotToken:         strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		WebhookSecret:    strings.TrimSpace(os.Getenv("WEBHOOK_SECRET")),
		PublicWebhookURL: strings.TrimSpace(os.Getenv("PUBLIC_WEBHOOK_URL")),
		HTTPAddr:         envOrDefault("HTTP_ADDR", defaultHTTPAddr),
		SQLitePath:       envOrDefault("SQLITE_PATH", defaultSQLitePath),
		LogLevel:         envOrDefault("LOG_LEVEL", defaultLogLevel),
	}

	if cfg.BotToken == "" {
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.WebhookSecret == "" {
		return Config{}, fmt.Errorf("WEBHOOK_SECRET is required")
	}

	warningTTLSeconds, err := parsePositiveInt("WARNING_TTL_SECONDS", envOrDefault("WARNING_TTL_SECONDS", strconv.Itoa(defaultWarningTTLSeconds)))
	if err != nil {
		return Config{}, err
	}
	cfg.WarningTTL = time.Duration(warningTTLSeconds) * time.Second

	maxConnections, err := parsePositiveInt("WEBHOOK_MAX_CONNECTIONS", envOrDefault("WEBHOOK_MAX_CONNECTIONS", strconv.Itoa(defaultWebhookMaxConnections)))
	if err != nil {
		return Config{}, err
	}
	cfg.WebhookMaxConnections = maxConnections

	immuneUsers, err := parseUserIDSet(envOrDefault("IMMUNE_USER_IDS", defaultImmuneUserIDs))
	if err != nil {
		return Config{}, fmt.Errorf("parse IMMUNE_USER_IDS: %w", err)
	}
	cfg.ImmuneUserIDs = immuneUsers

	violationWarningEnabled, err := parseBool(
		"VIOLATION_WARNING_ENABLED",
		envOrDefault("VIOLATION_WARNING_ENABLED", strconv.FormatBool(defaultViolationWarning)),
	)
	if err != nil {
		return Config{}, err
	}
	cfg.ViolationWarningEnabled = violationWarningEnabled

	violationWarningMentionTarget, err := parseBool(
		"VIOLATION_WARNING_MENTION_TARGET",
		envOrDefault("VIOLATION_WARNING_MENTION_TARGET", strconv.FormatBool(defaultViolationMention)),
	)
	if err != nil {
		return Config{}, err
	}
	cfg.ViolationWarningMentionTarget = violationWarningMentionTarget

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parsePositiveInt(envName, raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", envName, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", envName)
	}
	return value, nil
}

func parseBool(envName, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf(
			"%s must be a boolean value: true/false/1/0/yes/no/y/n/on/off",
			envName,
		)
	}
}

func parseUserIDSet(raw string) (map[int64]struct{}, error) {
	result := make(map[int64]struct{})
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}

	parts := strings.Split(raw, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		userID, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid user ID %q: %w", part, err)
		}
		result[userID] = struct{}{}
	}

	return result, nil
}
