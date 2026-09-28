package reddit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

func TestExpandCommentsLoadMore(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/api/morechildren", url.Values{
		"api_type": {"json"}, "link_id": {"t3_abc123"}, "children": {"m1,m4,m5"}, "sort": {"top"},
	}, "morechildren.json"))
	frags, err := ts.client.ExpandComments(context.Background(), "abc123", "m:m1,m4,m5", ThreadParams{Sort: "top"})
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 2 {
		t.Fatalf("fragments = %+v, want 2", frags)
	}
	f0 := frags[0]
	if f0.ParentID != "t3_abc123" || len(f0.Comments) != 1 || f0.Comments[0].ID != "m1" {
		t.Fatalf("fragment 0 = %+v", f0)
	}
	m1 := f0.Comments[0]
	if len(m1.Replies) != 1 || m1.Replies[0].ID != "m4" {
		t.Errorf("m1 replies = %+v, want [m4]", m1.Replies)
	}
	if m1.More == nil || m1.More.Count != 7 || !slices.Equal(m1.More.Tokens, []string{"m:m6,m7"}) {
		t.Errorf("m1 more = %+v", m1.More)
	}
	f1 := frags[1]
	if f1.ParentID != "t1_c9" || len(f1.Comments) != 1 || f1.Comments[0].ID != "m5" {
		t.Errorf("fragment 1 = %+v", f1)
	}
}

func TestExpandCommentsContinueThread(t *testing.T) {
	ts := newTestServer(t, fixtureAt(t, "/comments/abc123", url.Values{"comment": {"c2"}, "sort": {"top"}}, "continue.json"))
	frags, err := ts.client.ExpandComments(context.Background(), "abc123", "c:c2", ThreadParams{Sort: "top", Limit: 50, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 1 || frags[0].ParentID != "t1_c2" || len(frags[0].Comments) != 2 ||
		frags[0].Comments[0].ID != "c4" || frags[0].Comments[1].ID != "c5" {
		t.Errorf("fragments = %+v", frags)
	}
}

func TestExpandCommentsRejectsBadTokens(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	for _, tok := range []string{"", "x:abc", "m:", "m:a,,b", "c:", "c:Not-An-Id", "m:a b"} {
		_, err := ts.client.ExpandComments(context.Background(), "abc123", tok, ThreadParams{})
		var ie *InputError
		if !errors.As(err, &ie) {
			t.Errorf("token %q: err = %v, want *InputError", tok, err)
		}
	}
}

func TestResolvePostRefLocal(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	id, err := ts.client.ResolvePostRef(context.Background(), "https://redd.it/abc123")
	if err != nil || id != "abc123" {
		t.Errorf("id = %q, err = %v", id, err)
	}
}

func TestResolvePostRefShareLink(t *testing.T) {
	for _, loc := range []string{
		"https://www.reddit.com/r/golang/comments/abc123/is_go_good/?share_id=x&utm_medium=android_app",
		"/r/golang/comments/abc123/is_go_good/",
	} {
		ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/r/golang/s/AbCdEf123" || r.Header.Get("User-Agent") != "test-agent/1.0" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Location", loc)
			w.WriteHeader(http.StatusMovedPermanently)
		})
		id, err := ts.client.ResolvePostRef(context.Background(), "https://www.reddit.com/r/golang/s/AbCdEf123")
		if err != nil || id != "abc123" {
			t.Errorf("location %q: id = %q, err = %v", loc, id, err)
		}
	}
}

func TestResolvePostRefShareLinkBlocked(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	_, err := ts.client.ResolvePostRef(context.Background(), "https://www.reddit.com/r/golang/s/AbCdEf123")
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want *InputError asking for the full URL", err)
	}
}
