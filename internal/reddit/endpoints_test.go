package reddit

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// fixtureAt serves a fixture when the request path and query match, otherwise 400.
func fixtureAt(t *testing.T, path string, query url.Values, fixture string) http.HandlerFunc {
	body := string(loadFixture(t, fixture))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path = %q, want %q", r.URL.Path, path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got := r.URL.Query()
		for k, want := range query {
			if got.Get(k) != want[0] {
				t.Errorf("query %s = %q, want %q", k, got.Get(k), want[0])
			}
		}
		writeJSON(w, body)
	}
}

func TestSearchPostsInSubreddit(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/r/golang/search", url.Values{
		"q": {"cli tools"}, "restrict_sr": {"1"}, "type": {"link"}, "sort": {"top"}, "t": {"year"},
		"limit": {"5"}, "after": {"t3_prev"},
	}, "search.json"))
	page, err := ts.client.SearchPosts(context.Background(), SearchParams{
		Query: "cli tools", Subreddit: "r/golang", Sort: "top", Time: "year", Limit: 5, After: "t3_prev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) != 2 || page.After != "t3_next" {
		t.Fatalf("page = %+v", page)
	}
	p2 := page.Posts[1]
	if p2.ID != "p2" || p2.IsSelf || !p2.NSFW || p2.URL != "https://example.com/bench" {
		t.Errorf("p2 = %+v", p2)
	}
}

func TestSearchPostsEverywhere(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/search", url.Values{"q": {"go"}, "restrict_sr": {""}}, "search.json"))
	if _, err := ts.client.SearchPosts(context.Background(), SearchParams{Query: "go"}); err != nil {
		t.Fatal(err)
	}
}

func TestGetThread(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/comments/abc123", url.Values{
		"sort": {"top"}, "limit": {"50"}, "depth": {"4"},
	}, "comments.json"))
	th, err := ts.client.GetThread(context.Background(), "abc123", ThreadParams{Sort: "top", Limit: 50, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if th.Post.ID != "abc123" || len(th.Comments) != 2 {
		t.Errorf("thread = %+v", th)
	}
}

func TestDuplicates(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/duplicates/p2", url.Values{"limit": {"10"}}, "duplicates.json"))
	orig, dups, err := ts.client.Duplicates(context.Background(), "p2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if orig.ID != "p2" || len(dups) != 1 || dups[0].Subreddit != "rust" {
		t.Errorf("orig = %+v, dups = %+v", orig, dups)
	}
}

func TestSearchSubreddits(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/subreddits/search", url.Values{"q": {"golang"}, "limit": {"10"}}, "subreddits.json"))
	subs, err := ts.client.SearchSubreddits(context.Background(), "golang", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("subs = %+v", subs)
	}
	if subs[0].Name != "golang" || subs[0].Subscribers != 300000 || subs[0].NSFW {
		t.Errorf("subs[0] = %+v", subs[0])
	}
	if subs[1].Name != "gomemes" || subs[1].Subscribers != 0 || !subs[1].NSFW {
		t.Errorf("subs[1] = %+v (null subscribers must decode as 0)", subs[1])
	}
}
