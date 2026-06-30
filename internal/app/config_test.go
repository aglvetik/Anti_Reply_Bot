package app

import "testing"

func TestLoadConfigViolationWarningDefaults(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("WEBHOOK_SECRET", "secret")
	t.Setenv("VIOLATION_WARNING_ENABLED", "")
	t.Setenv("VIOLATION_WARNING_MENTION_TARGET", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !cfg.ViolationWarningEnabled {
		t.Fatal("expected VIOLATION_WARNING_ENABLED default to true")
	}
	if !cfg.ViolationWarningMentionTarget {
		t.Fatal("expected VIOLATION_WARNING_MENTION_TARGET default to true")
	}
}

func TestLoadConfigRejectsInvalidViolationWarningBool(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("WEBHOOK_SECRET", "secret")
	t.Setenv("VIOLATION_WARNING_ENABLED", "maybe")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected invalid bool config to return an error")
	}
}
