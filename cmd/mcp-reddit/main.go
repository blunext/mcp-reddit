// Command mcp-reddit is a read-only Reddit MCP server that speaks over stdio.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blunext/mcp-reddit/internal/reddit"
	"github.com/blunext/mcp-reddit/internal/tools"
)

func main() {
	// stdout carries the MCP protocol; logs go to stderr only.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp-reddit stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, t mcp.Transport) error {
	cfg, err := configFromEnv(getenv)
	if err != nil {
		return err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-reddit", Version: version()}, nil)
	tools.Register(server, reddit.New(cfg))
	return server.Run(ctx, t)
}

func configFromEnv(getenv func(string) string) (reddit.Config, error) {
	id, secret := getenv("REDDIT_CLIENT_ID"), getenv("REDDIT_CLIENT_SECRET")
	if id == "" || secret == "" {
		return reddit.Config{}, errors.New("REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET must be set; " +
			"see the README (https://github.com/blunext/mcp-reddit#credentials) for how to get Reddit API credentials")
	}
	ua := getenv("REDDIT_USER_AGENT")
	if ua == "" {
		ua = defaultUserAgent(getenv("REDDIT_USERNAME"))
	}
	return reddit.Config{ClientID: id, ClientSecret: secret, UserAgent: ua}, nil
}

// defaultUserAgent follows Reddit's API rules:
// "<platform>:<app ID>:<version> (by /u/<username>)". Without a username the
// project URL is the contact instead.
func defaultUserAgent(username string) string {
	username = strings.TrimPrefix(strings.TrimPrefix(username, "/"), "u/")
	contact := "+https://github.com/blunext/mcp-reddit"
	if username != "" {
		contact = "by /u/" + username
	}
	return fmt.Sprintf("%s:github.com/blunext/mcp-reddit:%s (%s)", runtime.GOOS, version(), contact)
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
