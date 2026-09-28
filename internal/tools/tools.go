package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

const (
	defaultLimit        = 10
	maxLimit            = 50
	defaultCommentLimit = 50
	maxCommentLimit     = 200
	defaultDepth        = 4
	maxDepth            = 10
	maxTokens           = 5
)

var commentSorts = []string{"top", "best", "new", "controversial", "old", "qa"}

// apiSort maps user-facing comment sorts to Reddit API values.
func apiSort(s string) string {
	if s == "best" {
		return "confidence"
	}
	return s
}

type SearchPostsInput struct {
	Query     string `json:"query" jsonschema:"search terms"`
	Subreddit string `json:"subreddit,omitempty" jsonschema:"restrict the search to this subreddit, e.g. golang"`
	Sort      string `json:"sort,omitempty" jsonschema:"relevance (default), hot, top, new or comments"`
	Time      string `json:"time,omitempty" jsonschema:"time window: hour, day, week, month, year or all (default)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"number of posts, 1-50, default 10"`
	After     string `json:"after,omitempty" jsonschema:"pagination cursor from a previous search_posts result"`
}

type GetPostInput struct {
	Post         string `json:"post" jsonschema:"post id, t3_ fullname, or any Reddit post URL including redd.it and share links"`
	CommentSort  string `json:"comment_sort,omitempty" jsonschema:"top (default), best, new, controversial, old or qa"`
	CommentLimit int    `json:"comment_limit,omitempty" jsonschema:"maximum comments to load, 1-200, default 50"`
	Depth        int    `json:"depth,omitempty" jsonschema:"maximum reply depth, 1-10, default 4"`
}

type ExpandCommentsInput struct {
	Post   string   `json:"post" jsonschema:"the post the tokens belong to (id or URL)"`
	Tokens []string `json:"tokens" jsonschema:"tokens copied exactly from expand_comments markers, at most 5"`
	Sort   string   `json:"sort,omitempty" jsonschema:"top (default), best, new, controversial, old or qa"`
}

type GetRelatedPostsInput struct {
	Post  string `json:"post" jsonschema:"post id or URL of a link post"`
	Limit int    `json:"limit,omitempty" jsonschema:"number of posts, 1-50, default 10"`
}

type SearchSubredditsInput struct {
	Query string `json:"query" jsonschema:"subreddit name or topic"`
	Limit int    `json:"limit,omitempty" jsonschema:"number of subreddits, 1-50, default 10"`
}

// Register adds all read-only Reddit tools to s.
func Register(s *mcp.Server, r Reddit) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_posts",
		Description: "Search Reddit posts by keywords, optionally within one subreddit. Returns titles, stats, ids " +
			"and short excerpts. Opinions live mostly in comments: read promising threads with get_post. " +
			"Reddit offers no comment search.",
		Annotations: readOnly,
	}, searchPosts(r))
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_post",
		Description: "Read a Reddit post and its comment tree. Accepts a post id, a t3_ fullname or any Reddit post " +
			"URL (reddit.com, old.reddit.com, redd.it, mobile share links /r/<sub>/s/<code>). Collapsed branches " +
			"appear as '[+N more replies → expand_comments tokens: …]' or '[continue this thread → …]'; " +
			"pass those tokens to expand_comments to load them.",
		Annotations: readOnly,
	}, getPost(r))
	mcp.AddTool(s, &mcp.Tool{
		Name: "expand_comments",
		Description: "Load collapsed comment branches of a post. Pass the post and tokens copied exactly from " +
			"markers in get_post or expand_comments output. At most 5 tokens per call; each costs one Reddit request.",
		Annotations: readOnly,
	}, expandComments(r))
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_related_posts",
		Description: "Find other submissions of the same link (Reddit's 'other discussions'), e.g. one article " +
			"posted to several subreddits. Only useful for link posts; for text posts or broader coverage use " +
			"search_posts with keywords from the title.",
		Annotations: readOnly,
	}, getRelatedPosts(r))
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_subreddits",
		Description: "Find subreddits by name or topic. Returns names, subscriber counts and descriptions; use a " +
			"name as the subreddit filter of search_posts.",
		Annotations: readOnly,
	}, searchSubreddits(r))
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func oneOf(field, value, def string, allowed ...string) (string, error) {
	if value == "" {
		return def, nil
	}
	if slices.Contains(allowed, value) {
		return value, nil
	}
	return "", fmt.Errorf("invalid %s %q: must be one of %s", field, value, strings.Join(allowed, ", "))
}

func bounded(field string, value, def, maxValue int) (int, error) {
	if value == 0 {
		return def, nil
	}
	if value < 1 || value > maxValue {
		return 0, fmt.Errorf("invalid %s %d: must be between 1 and %d", field, value, maxValue)
	}
	return value, nil
}

func searchPosts(r Reddit) mcp.ToolHandlerFor[SearchPostsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchPostsInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, errors.New("query must not be empty")
		}
		sort, err := oneOf("sort", in.Sort, "relevance", "relevance", "hot", "top", "new", "comments")
		if err != nil {
			return nil, nil, err
		}
		window, err := oneOf("time", in.Time, "all", "hour", "day", "week", "month", "year", "all")
		if err != nil {
			return nil, nil, err
		}
		limit, err := bounded("limit", in.Limit, defaultLimit, maxLimit)
		if err != nil {
			return nil, nil, err
		}
		page, err := r.SearchPosts(ctx, reddit.SearchParams{
			Query: in.Query, Subreddit: in.Subreddit, Sort: sort, Time: window, Limit: limit, After: in.After,
		})
		if err != nil {
			return nil, nil, describe(err)
		}
		return text(renderPostList(page.Posts, page.After)), nil, nil
	}
}

func getPost(r Reddit) mcp.ToolHandlerFor[GetPostInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetPostInput) (*mcp.CallToolResult, any, error) {
		sort, err := oneOf("comment_sort", in.CommentSort, "top", commentSorts...)
		if err != nil {
			return nil, nil, err
		}
		limit, err := bounded("comment_limit", in.CommentLimit, defaultCommentLimit, maxCommentLimit)
		if err != nil {
			return nil, nil, err
		}
		depth, err := bounded("depth", in.Depth, defaultDepth, maxDepth)
		if err != nil {
			return nil, nil, err
		}
		id, err := r.ResolvePostRef(ctx, in.Post)
		if err != nil {
			return nil, nil, describe(err)
		}
		th, err := r.GetThread(ctx, id, reddit.ThreadParams{Sort: apiSort(sort), Limit: limit, Depth: depth})
		if err != nil {
			return nil, nil, describe(err)
		}
		return text(renderThread(th, sort)), nil, nil
	}
}

func expandComments(r Reddit) mcp.ToolHandlerFor[ExpandCommentsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ExpandCommentsInput) (*mcp.CallToolResult, any, error) {
		var tokens []string
		for _, t := range in.Tokens {
			tokens = append(tokens, strings.Fields(t)...)
		}
		if len(tokens) == 0 {
			return nil, nil, errors.New("tokens must not be empty")
		}
		if len(tokens) > maxTokens {
			return nil, nil, fmt.Errorf("at most %d tokens per call, got %d", maxTokens, len(tokens))
		}
		sort, err := oneOf("sort", in.Sort, "top", commentSorts...)
		if err != nil {
			return nil, nil, err
		}
		id, err := r.ResolvePostRef(ctx, in.Post)
		if err != nil {
			return nil, nil, describe(err)
		}
		params := reddit.ThreadParams{Sort: apiSort(sort), Limit: defaultCommentLimit, Depth: defaultDepth}
		var frags []reddit.Fragment
		for _, tok := range tokens {
			f, err := r.ExpandComments(ctx, id, tok, params)
			if err != nil {
				return nil, nil, describe(err)
			}
			frags = append(frags, f...)
		}
		return text(renderFragments(frags)), nil, nil
	}
}

func getRelatedPosts(r Reddit) mcp.ToolHandlerFor[GetRelatedPostsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetRelatedPostsInput) (*mcp.CallToolResult, any, error) {
		limit, err := bounded("limit", in.Limit, defaultLimit, maxLimit)
		if err != nil {
			return nil, nil, err
		}
		id, err := r.ResolvePostRef(ctx, in.Post)
		if err != nil {
			return nil, nil, describe(err)
		}
		orig, dups, err := r.Duplicates(ctx, id, limit)
		if err != nil {
			return nil, nil, describe(err)
		}
		return text(renderRelated(orig, dups)), nil, nil
	}
}

func searchSubreddits(r Reddit) mcp.ToolHandlerFor[SearchSubredditsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchSubredditsInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, errors.New("query must not be empty")
		}
		limit, err := bounded("limit", in.Limit, defaultLimit, maxLimit)
		if err != nil {
			return nil, nil, err
		}
		subs, err := r.SearchSubreddits(ctx, in.Query, limit)
		if err != nil {
			return nil, nil, describe(err)
		}
		return text(renderSubreddits(subs)), nil, nil
	}
}

// describe turns client errors into messages the model can act on.
func describe(err error) error {
	var ie *reddit.InputError
	var rl *reddit.RateLimitError
	var ae *reddit.AuthError
	switch {
	case errors.As(err, &ie):
		return err
	case errors.As(err, &rl):
		return fmt.Errorf("reddit rate limit reached, retry in %d seconds", rl.Seconds())
	case errors.As(err, &ae):
		return errors.New("reddit rejected the API credentials (REDDIT_CLIENT_ID / REDDIT_CLIENT_SECRET); " +
			"the user must fix the server configuration, see https://github.com/blunext/mcp-reddit#credentials; " +
			"retrying will not help")
	case errors.Is(err, reddit.ErrNotFound):
		return errors.New("not found on reddit: the post or subreddit does not exist, was removed or is banned")
	case errors.Is(err, reddit.ErrForbidden):
		return errors.New("access denied by reddit: the subreddit is private or quarantined, or the content is restricted")
	case errors.Is(err, reddit.ErrUnexpectedResponse):
		return errors.New("reddit returned an unexpected non-JSON response; the subreddit name may be wrong")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("reddit did not respond in time; try again")
	default:
		return fmt.Errorf("reddit request failed: %v", err)
	}
}
