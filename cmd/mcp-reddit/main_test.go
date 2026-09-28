package main

import (
	"context"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigFromEnvRequiresCredentials(t *testing.T) {
	for _, m := range []map[string]string{
		{},
		{"REDDIT_CLIENT_ID": "id"},
		{"REDDIT_CLIENT_SECRET": "secret"},
	} {
		_, err := configFromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), "REDDIT_CLIENT_ID") || !strings.Contains(err.Error(), "README") {
			t.Errorf("env %v: err = %v, want a message naming the variables and the README", m, err)
		}
	}
}

func TestConfigFromEnvUserAgent(t *testing.T) {
	cfg, err := configFromEnv(env(map[string]string{"REDDIT_CLIENT_ID": "id", "REDDIT_CLIENT_SECRET": "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "id" || cfg.ClientSecret != "secret" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !strings.HasPrefix(cfg.UserAgent, "mcp-reddit/") || !strings.Contains(cfg.UserAgent, "github.com/blunext/mcp-reddit") {
		t.Errorf("default user agent = %q", cfg.UserAgent)
	}
	cfg, err = configFromEnv(env(map[string]string{
		"REDDIT_CLIENT_ID": "id", "REDDIT_CLIENT_SECRET": "secret", "REDDIT_USER_AGENT": "custom/1.0 (by /u/me)",
	}))
	if err != nil || cfg.UserAgent != "custom/1.0 (by /u/me)" {
		t.Errorf("custom user agent = %q, err = %v", cfg.UserAgent, err)
	}
}

func TestRunFailsWithoutCredentials(t *testing.T) {
	if err := run(context.Background(), env(nil), nil); err == nil {
		t.Error("run must fail before serving when credentials are missing")
	}
}
