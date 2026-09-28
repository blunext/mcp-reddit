package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

func TestToolsOverMCP(t *testing.T) {
	ctx := context.Background()
	f := &fakeReddit{}
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-reddit", Version: "test"}, nil)
	Register(server, f)

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	lt, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range lt.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s is not marked read-only", tool.Name)
		}
		if tool.OutputSchema != nil {
			t.Errorf("tool %s declares an output schema; tools must return plain text only", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"expand_comments", "get_post", "get_related_posts", "search_posts", "search_subreddits"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"query": "go cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(resultText(t, res), "Go vs Rust for CLIs") {
		t.Errorf("search_posts result = %+v", res)
	}
	if res.StructuredContent != nil || len(res.Content) != 1 {
		t.Errorf("search_posts must return exactly one text content and no structured content: %+v", res)
	}

	f.err = reddit.ErrNotFound
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_post", Arguments: map[string]any{"post": "abc123"}})
	if err != nil {
		t.Fatalf("reddit errors must be tool errors, not protocol errors: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "not found") {
		t.Errorf("get_post error result = %+v", res)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{}})
	if err == nil && !res.IsError {
		t.Error("missing required query must be rejected")
	}
}
