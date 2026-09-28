// Package tools exposes Reddit as read-only MCP tools.
package tools

import (
	"context"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

// Reddit is the subset of *reddit.Client the tools need.
type Reddit interface {
	ResolvePostRef(ctx context.Context, ref string) (string, error)
	SearchPosts(ctx context.Context, p reddit.SearchParams) (reddit.PostPage, error)
	GetThread(ctx context.Context, postID string, p reddit.ThreadParams) (reddit.Thread, error)
	ExpandComments(ctx context.Context, postID, token string, p reddit.ThreadParams) ([]reddit.Fragment, error)
	Duplicates(ctx context.Context, postID string, limit int) (reddit.Post, []reddit.Post, error)
	SearchSubreddits(ctx context.Context, query string, limit int) ([]reddit.Subreddit, error)
}
