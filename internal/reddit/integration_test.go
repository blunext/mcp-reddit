package reddit

import (
	"context"
	"os"
	"testing"
)

// liveClient returns a client for the real Reddit API, or skips without credentials.
func liveClient(t *testing.T) *Client {
	t.Helper()
	id, secret := os.Getenv("REDDIT_CLIENT_ID"), os.Getenv("REDDIT_CLIENT_SECRET")
	if id == "" || secret == "" {
		t.Skip("set REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET to run live Reddit tests")
	}
	return New(Config{
		ClientID:     id,
		ClientSecret: secret,
		UserAgent:    "mcp-reddit/integration-test (+https://github.com/blunext/mcp-reddit)",
	})
}

func TestLiveSearchThreadAndExpand(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	page, err := c.SearchPosts(ctx, SearchParams{Query: "cli", Subreddit: "golang", Sort: "comments", Time: "year", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) == 0 {
		t.Fatal("search returned no posts")
	}
	th, err := c.GetThread(ctx, page.Posts[0].ID, ThreadParams{Sort: "top", Limit: 20, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if th.Post.ID != page.Posts[0].ID {
		t.Errorf("thread post = %q, want %q", th.Post.ID, page.Posts[0].ID)
	}
	if th.More != nil {
		if _, err := c.ExpandComments(ctx, th.Post.ID, th.More.Tokens[0], ThreadParams{Sort: "top"}); err != nil {
			t.Errorf("expand %q: %v", th.More.Tokens[0], err)
		}
	}
}

func TestLiveSubredditsAndDuplicates(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	subs, err := c.SearchSubreddits(ctx, "golang", 3)
	if err != nil || len(subs) == 0 {
		t.Fatalf("subs = %v, err = %v", subs, err)
	}
	page, err := c.SearchPosts(ctx, SearchParams{Query: "site:github.com", Limit: 1})
	if err != nil || len(page.Posts) == 0 {
		t.Fatalf("link post search: %v", err)
	}
	if _, _, err := c.Duplicates(ctx, page.Posts[0].ID, 5); err != nil {
		t.Errorf("duplicates: %v", err)
	}
}

// TestLiveShareLink checks the open risk from the spec: resolving /s/ share links.
func TestLiveShareLink(t *testing.T) {
	c := liveClient(t)
	link := os.Getenv("REDDIT_TEST_SHARE_URL")
	if link == "" {
		t.Skip("set REDDIT_TEST_SHARE_URL to a reddit.com/r/<sub>/s/<code> link to test share-link resolution")
	}
	id, err := c.ResolvePostRef(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("share link resolved to post %s", id)
}
