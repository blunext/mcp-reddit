package tools

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tools -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

var created = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func samplePosts() []reddit.Post {
	return []reddit.Post{
		{
			ID: "p1", Title: "Go vs Rust for CLIs", Subreddit: "golang", Author: "alice",
			SelfText:  "Which one would you pick?\n\nContext: small team.",
			URL:       "https://www.reddit.com/r/golang/comments/p1/go_vs_rust/",
			Permalink: "https://www.reddit.com/r/golang/comments/p1/go_vs_rust/",
			Score:     210, NumComments: 88, Created: created, IsSelf: true,
		},
		{
			ID: "p2", Title: "Benchmark article", Subreddit: "programming", Author: "bob",
			URL:       "https://example.com/bench",
			Permalink: "https://www.reddit.com/r/programming/comments/p2/benchmark_article/",
			Score:     15, NumComments: 3, Created: created, NSFW: true,
		},
	}
}

func sampleThread() reddit.Thread {
	return reddit.Thread{
		Post: reddit.Post{
			ID: "abc123", Title: "Is Go good for CLI tools?", Subreddit: "golang", Author: "gopher",
			SelfText:  "Thinking about rewriting our tools.\n\nOpinions?",
			URL:       "https://www.reddit.com/r/golang/comments/abc123/is_go_good/",
			Permalink: "https://www.reddit.com/r/golang/comments/abc123/is_go_good/",
			Score:     350, NumComments: 42, Created: created, IsSelf: true,
		},
		Comments: []reddit.Comment{
			{
				ID: "c1", Author: "alice", Body: "Yes, cobra & stdlib flag are great.", Score: 120, Created: created,
				Replies: []reddit.Comment{{
					ID: "c2", Author: "bob", Body: "Agreed.\n\nSingle binary is the killer feature.", Score: 45,
					Created: created, More: &reddit.More{Tokens: []string{"c:c2"}},
				}},
			},
			{ID: "c3", Author: "[deleted]", Body: "[removed]", Score: -2, Created: created},
		},
		More: &reddit.More{Count: 3, Tokens: []string{"m:m1,m2,m3"}},
	}
}

func TestRenderPostList(t *testing.T) {
	golden(t, "post_list", renderPostList(samplePosts(), "t3_next"))
}

func TestRenderPostListEmpty(t *testing.T) {
	if got := renderPostList(nil, ""); got != "No posts found.\n" {
		t.Errorf("got %q", got)
	}
}

func TestRenderThread(t *testing.T) {
	golden(t, "thread", renderThread(sampleThread(), "top"))
}

func TestRenderFragments(t *testing.T) {
	frags := []reddit.Fragment{
		{ParentID: "t3_abc123", Comments: []reddit.Comment{{
			ID: "m1", Author: "carol", Body: "Top-level from more.", Score: 10, Created: created,
			Replies: []reddit.Comment{{ID: "m4", Author: "dave", Body: "Reply to m1.", Score: 4, Created: created}},
			More:    &reddit.More{Count: 7, Tokens: []string{"m:m6,m7"}},
		}}},
		{ParentID: "t1_c9", Comments: []reddit.Comment{{
			ID: "m5", Author: "erin", Body: "Reply to a comment outside this batch.", Score: 2, Created: created,
		}}},
	}
	golden(t, "fragments", renderFragments(frags))
}

func TestRenderRelated(t *testing.T) {
	orig := samplePosts()[1]
	dup := reddit.Post{
		ID: "p3", Title: "Benchmark article", Subreddit: "rust", Author: "carol",
		URL:       "https://example.com/bench",
		Permalink: "https://www.reddit.com/r/rust/comments/p3/benchmark_article/",
		Score:     40, NumComments: 12, Created: created,
	}
	golden(t, "related", renderRelated(orig, []reddit.Post{dup}))
	if got := renderRelated(orig, nil); !strings.Contains(got, "use search_posts") {
		t.Errorf("empty related should point to search_posts, got %q", got)
	}
}

func TestRenderSubreddits(t *testing.T) {
	subs := []reddit.Subreddit{
		{Name: "golang", Description: "Ask questions and post articles about the Go programming language.", Subscribers: 300000},
		{Name: "gomemes", NSFW: true},
	}
	golden(t, "subreddits", renderSubreddits(subs))
	if got := renderSubreddits(nil); got != "No subreddits found.\n" {
		t.Errorf("got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	got := truncate(strings.Repeat("ż", 20), 5)
	if want := "żżżżż …[truncated, 20 chars total]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Error("truncate split a rune")
	}
}

func TestLongCommentTruncated(t *testing.T) {
	th := reddit.Thread{
		Post:     reddit.Post{ID: "x", Title: "t", IsSelf: true},
		Comments: []reddit.Comment{{ID: "c", Author: "a", Body: strings.Repeat("a", 2000), Created: created}},
	}
	out := renderThread(th, "top")
	if !strings.Contains(out, "…[truncated, 2000 chars total]") || strings.Contains(out, strings.Repeat("a", 1501)) {
		t.Errorf("comment not truncated to 1500 chars:\n%.200s", out)
	}
}

func TestLargeMoreMarkerIsCapped(t *testing.T) {
	tokens := make([]string, 20)
	for i := range tokens {
		tokens[i] = "m:" + strings.TrimSuffix(strings.Repeat("abcdefg,", 100), ",")
	}
	th := reddit.Thread{
		Post: reddit.Post{ID: "x", Title: "t", IsSelf: true},
		More: &reddit.More{Count: 2000, Tokens: tokens},
	}
	out := renderThread(th, "top")
	if n := strings.Count(out, "m:abcdefg"); n != 3 {
		t.Errorf("marker shows %d tokens, want 3", n)
	}
	if !strings.Contains(out, "17 more batches not shown") {
		t.Errorf("marker should say how many batches are hidden:\n%.300s", out)
	}
	if len(out) > 4000 {
		t.Errorf("output is %d bytes, want a compact marker", len(out))
	}
}
