package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

type fakeReddit struct {
	err error

	searchParams reddit.SearchParams
	threadID     string
	threadParams reddit.ThreadParams
	tokens       []string
	dupLimit     int
	subQuery     string
	subLimit     int
}

func (f *fakeReddit) ResolvePostRef(_ context.Context, ref string) (string, error) {
	r, err := reddit.ParsePostRef(ref)
	if err != nil {
		return "", err
	}
	if r.SharePath != "" {
		return "shared1", nil
	}
	return r.ID, nil
}

func (f *fakeReddit) SearchPosts(_ context.Context, p reddit.SearchParams) (reddit.PostPage, error) {
	f.searchParams = p
	return reddit.PostPage{Posts: samplePosts()}, f.err
}

func (f *fakeReddit) GetThread(_ context.Context, id string, p reddit.ThreadParams) (reddit.Thread, error) {
	f.threadID, f.threadParams = id, p
	return sampleThread(), f.err
}

func (f *fakeReddit) ExpandComments(_ context.Context, id, token string, p reddit.ThreadParams) ([]reddit.Fragment, error) {
	f.threadID, f.threadParams = id, p
	f.tokens = append(f.tokens, token)
	return []reddit.Fragment{{ParentID: "t3_" + id}}, f.err
}

func (f *fakeReddit) Duplicates(_ context.Context, id string, limit int) (reddit.Post, []reddit.Post, error) {
	f.threadID, f.dupLimit = id, limit
	return samplePosts()[1], nil, f.err
}

func (f *fakeReddit) SearchSubreddits(_ context.Context, q string, limit int) ([]reddit.Subreddit, error) {
	f.subQuery, f.subLimit = q, limit
	return nil, f.err
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func TestSearchPostsDefaults(t *testing.T) {
	f := &fakeReddit{}
	res, _, err := searchPosts(f)(context.Background(), nil, SearchPostsInput{Query: "go cli", Subreddit: "golang"})
	if err != nil {
		t.Fatal(err)
	}
	want := reddit.SearchParams{Query: "go cli", Subreddit: "golang", Sort: "relevance", Time: "all", Limit: 10}
	if f.searchParams != want {
		t.Errorf("params = %+v, want %+v", f.searchParams, want)
	}
	if !strings.Contains(resultText(t, res), "1. Go vs Rust for CLIs") {
		t.Errorf("unexpected text: %s", resultText(t, res))
	}
}

func TestSearchPostsValidation(t *testing.T) {
	for _, in := range []SearchPostsInput{
		{Query: "  "},
		{Query: "go", Sort: "best"},
		{Query: "go", Time: "decade"},
		{Query: "go", Limit: 51},
		{Query: "go", Limit: -1},
	} {
		if _, _, err := searchPosts(&fakeReddit{})(context.Background(), nil, in); err == nil {
			t.Errorf("input %+v: want error", in)
		}
	}
}

func TestGetPostDefaultsAndBestSort(t *testing.T) {
	f := &fakeReddit{}
	if _, _, err := getPost(f)(context.Background(), nil, GetPostInput{Post: "https://redd.it/abc123"}); err != nil {
		t.Fatal(err)
	}
	if f.threadID != "abc123" || f.threadParams != (reddit.ThreadParams{Sort: "top", Limit: 50, Depth: 4}) {
		t.Errorf("id = %q, params = %+v", f.threadID, f.threadParams)
	}
	res, _, err := getPost(f)(context.Background(), nil, GetPostInput{Post: "abc123", CommentSort: "best"})
	if err != nil {
		t.Fatal(err)
	}
	if f.threadParams.Sort != "confidence" {
		t.Errorf("api sort = %q, want confidence", f.threadParams.Sort)
	}
	if !strings.Contains(resultText(t, res), "--- Comments (sort: best) ---") {
		t.Error("rendered sort should use the user-facing name")
	}
}

func TestGetPostValidation(t *testing.T) {
	for _, in := range []GetPostInput{
		{Post: "abc123", CommentLimit: 201},
		{Post: "abc123", Depth: 11},
		{Post: "abc123", CommentSort: "hot"},
		{Post: "https://example.com/x"},
	} {
		if _, _, err := getPost(&fakeReddit{})(context.Background(), nil, in); err == nil {
			t.Errorf("input %+v: want error", in)
		}
	}
}

func TestExpandCommentsFlattensTokens(t *testing.T) {
	f := &fakeReddit{}
	_, _, err := expandComments(f)(context.Background(), nil, ExpandCommentsInput{
		Post: "abc123", Tokens: []string{"m:a,b c:x", " m:c "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.tokens, []string{"m:a,b", "c:x", "m:c"}) {
		t.Errorf("tokens = %q", f.tokens)
	}
	if f.threadParams != (reddit.ThreadParams{Sort: "top", Limit: 50, Depth: 4}) {
		t.Errorf("params = %+v", f.threadParams)
	}
}

func TestExpandCommentsLimits(t *testing.T) {
	for _, tokens := range [][]string{nil, {"  "}, {"m:a", "m:b", "m:c", "m:d", "m:e", "m:f"}} {
		_, _, err := expandComments(&fakeReddit{})(context.Background(), nil, ExpandCommentsInput{Post: "abc123", Tokens: tokens})
		if err == nil {
			t.Errorf("tokens %q: want error", tokens)
		}
	}
}

func TestRelatedAndSubredditDefaults(t *testing.T) {
	f := &fakeReddit{}
	if _, _, err := getRelatedPosts(f)(context.Background(), nil, GetRelatedPostsInput{Post: "t3_p2"}); err != nil {
		t.Fatal(err)
	}
	if f.threadID != "p2" || f.dupLimit != 10 {
		t.Errorf("id = %q, limit = %d", f.threadID, f.dupLimit)
	}
	if _, _, err := searchSubreddits(f)(context.Background(), nil, SearchSubredditsInput{Query: "golang"}); err != nil {
		t.Fatal(err)
	}
	if f.subQuery != "golang" || f.subLimit != 10 {
		t.Errorf("query = %q, limit = %d", f.subQuery, f.subLimit)
	}
	if _, _, err := searchSubreddits(f)(context.Background(), nil, SearchSubredditsInput{Query: ""}); err == nil {
		t.Error("empty query: want error")
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&reddit.RateLimitError{RetryAfter: 42 * time.Second}, "retry in 42 seconds"},
		{&reddit.AuthError{Status: 401}, "REDDIT_CLIENT_ID"},
		{reddit.ErrNotFound, "not found"},
		{reddit.ErrNotFound, "search_subreddits"},
		{fmt.Errorf("wrapped: %w", reddit.ErrForbidden), "private or quarantined"},
		{reddit.ErrUnexpectedResponse, "subreddit name may be wrong"},
		{fmt.Errorf("reddit request failed: %w", context.DeadlineExceeded), "did not respond in time"},
		{&reddit.InputError{Msg: "post reference is empty"}, "post reference is empty"},
		{errors.New("boom"), "reddit request failed: boom"},
	}
	for _, tt := range tests {
		if got := describe(tt.err).Error(); !strings.Contains(got, tt.want) {
			t.Errorf("describe(%v) = %q, want it to contain %q", tt.err, got, tt.want)
		}
	}
}
