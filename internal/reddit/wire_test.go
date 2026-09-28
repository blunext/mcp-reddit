package reddit

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeThread(t *testing.T) {
	var resp []listing
	if err := json.Unmarshal(loadFixture(t, "comments.json"), &resp); err != nil {
		t.Fatal(err)
	}
	th, err := decodeThread(resp)
	if err != nil {
		t.Fatal(err)
	}

	p := th.Post
	if p.ID != "abc123" || p.Title != "Is Go good for CLI tools?" || p.Subreddit != "golang" ||
		p.Author != "gopher" || p.Score != 350 || p.NumComments != 42 || !p.IsSelf || p.NSFW {
		t.Errorf("post = %+v", p)
	}
	if p.Permalink != "https://www.reddit.com/r/golang/comments/abc123/is_go_good_for_cli_tools/" {
		t.Errorf("permalink = %q", p.Permalink)
	}
	if want := time.Unix(1790000000, 0).UTC(); !p.Created.Equal(want) {
		t.Errorf("created = %v, want %v", p.Created, want)
	}

	if len(th.Comments) != 2 {
		t.Fatalf("top-level comments = %d, want 2", len(th.Comments))
	}
	c1 := th.Comments[0]
	if c1.ID != "c1" || c1.ParentID != "t3_abc123" || c1.Author != "alice" || c1.Score != 120 ||
		c1.Body != "Yes, cobra & stdlib flag are great." {
		t.Errorf("c1 = %+v", c1)
	}
	if len(c1.Replies) != 1 || c1.Replies[0].ID != "c2" {
		t.Fatalf("c1 replies = %+v", c1.Replies)
	}
	c2 := c1.Replies[0]
	if len(c2.Replies) != 0 {
		t.Errorf("c2 replies = %+v, want none", c2.Replies)
	}
	if c2.More == nil || c2.More.Count != 0 || !slices.Equal(c2.More.Tokens, []string{"c:c2"}) {
		t.Errorf("c2 more = %+v, want continue token c:c2", c2.More)
	}
	c3 := th.Comments[1]
	if c3.Author != "[deleted]" || c3.Body != "[removed]" || c3.Score != -2 || c3.Replies != nil {
		t.Errorf("c3 = %+v", c3)
	}
	if th.More == nil || th.More.Count != 3 || !slices.Equal(th.More.Tokens, []string{"m:m1,m2,m3"}) {
		t.Errorf("thread more = %+v", th.More)
	}
}

func TestDecodeThreadEmpty(t *testing.T) {
	if _, err := decodeThread(nil); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestToMore(t *testing.T) {
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("id%d", i)
	}
	m := toMore(moreData{ID: "id0", ParentID: "t3_abc", Count: 250, Children: ids})
	if m == nil || m.Count != 250 || len(m.Tokens) != 3 {
		t.Fatalf("more = %+v, want 3 tokens", m)
	}
	if !strings.HasPrefix(m.Tokens[0], "m:id0,id1,") || strings.Count(m.Tokens[0], ",") != 99 {
		t.Errorf("first token has wrong ids: %.40s…", m.Tokens[0])
	}
	if strings.Count(m.Tokens[2], ",") != 49 {
		t.Errorf("last token should hold 50 ids: %.40s…", m.Tokens[2])
	}
	if got := toMore(moreData{ID: "_", ParentID: "t3_abc"}); got != nil {
		t.Errorf("continue marker under a post = %+v, want nil", got)
	}
}

func TestMergeMore(t *testing.T) {
	a := &More{Count: 2, Tokens: []string{"m:a"}}
	b := &More{Count: 3, Tokens: []string{"m:b"}}
	got := mergeMore(a, b)
	if got.Count != 5 || !slices.Equal(got.Tokens, []string{"m:a", "m:b"}) {
		t.Errorf("merge = %+v", got)
	}
	if mergeMore(nil, b) != b || mergeMore(a, nil) != a {
		t.Error("merge with nil should return the other side")
	}
}
