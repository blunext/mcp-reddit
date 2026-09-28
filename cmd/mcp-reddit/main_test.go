package main

import (
	"context"
	"runtime"
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
	// Reddit's API rules ask for "<platform>:<app ID>:<version> (by /u/<username>)".
	wantPrefix := runtime.GOOS + ":github.com/blunext/mcp-reddit:dev "
	if want := wantPrefix + "(+https://github.com/blunext/mcp-reddit)"; cfg.UserAgent != want {
		t.Errorf("default user agent = %q, want %q", cfg.UserAgent, want)
	}
	for _, name := range []string{"spez", "u/spez", "/u/spez"} {
		cfg, err = configFromEnv(env(map[string]string{
			"REDDIT_CLIENT_ID": "id", "REDDIT_CLIENT_SECRET": "secret", "REDDIT_USERNAME": name,
		}))
		if want := wantPrefix + "(by /u/spez)"; err != nil || cfg.UserAgent != want {
			t.Errorf("REDDIT_USERNAME=%q: user agent = %q, want %q (err %v)", name, cfg.UserAgent, want, err)
		}
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
