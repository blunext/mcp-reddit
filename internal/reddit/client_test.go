package reddit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.slept = append(f.slept, d)
	f.now = f.now.Add(d)
	return nil
}

type testServer struct {
	tokenCalls atomic.Int32
	apiCalls   atomic.Int32
	clock      *fakeClock
	client     *Client
	url        string
}

// newTestServer serves Reddit's token endpoint and routes every other request to api.
func newTestServer(t *testing.T, api http.HandlerFunc) *testServer {
	t.Helper()
	ts := &testServer{clock: &fakeClock{now: time.Unix(1790000000, 0)}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/access_token", func(w http.ResponseWriter, r *http.Request) {
		n := ts.tokenCalls.Add(1)
		id, secret, ok := r.BasicAuth()
		if !ok || id != "id" || secret != "secret" || r.FormValue("grant_type") != "client_credentials" ||
			r.Header.Get("User-Agent") != "test-agent/1.0" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, fmt.Sprintf(`{"access_token":"tok%d","token_type":"bearer","expires_in":3600,"scope":"*"}`, n))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ts.apiCalls.Add(1)
		api(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ts.url = srv.URL
	ts.client = New(Config{
		ClientID:     "id",
		ClientSecret: "secret",
		UserAgent:    "test-agent/1.0",
		APIBase:      srv.URL,
		WWWBase:      srv.URL,
		HTTPClient:   srv.Client(),
		Now:          ts.clock.Now,
		Sleep:        ts.clock.Sleep,
	})
	return ts
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_, _ = io.WriteString(w, body)
}

type okBody struct {
	OK bool `json:"ok"`
}

func TestGetSendsTokenUserAgentAndRawJSON(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok1" || r.Header.Get("User-Agent") != "test-agent/1.0" ||
			r.URL.Query().Get("raw_json") != "1" || r.URL.Query().Get("q") != "go" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, `{"ok":true}`)
	})
	for range 2 {
		var out okBody
		if err := ts.client.get(context.Background(), "/ping", map[string][]string{"q": {"go"}}, &out); err != nil {
			t.Fatal(err)
		}
		if !out.OK {
			t.Fatal("response not decoded")
		}
	}
	if n := ts.tokenCalls.Load(); n != 1 {
		t.Errorf("token calls = %d, want 1 (token must be reused)", n)
	}
}

func TestTokenRefreshedShortlyBeforeExpiry(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, `{"ok":true}`) })
	ctx := context.Background()
	var out okBody
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	ts.clock.now = ts.clock.now.Add(3539 * time.Second) // 61 s before expiry: still valid
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if n := ts.tokenCalls.Load(); n != 1 {
		t.Fatalf("token calls = %d, want 1", n)
	}
	ts.clock.now = ts.clock.now.Add(2 * time.Second) // 59 s before expiry: refresh
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if n := ts.tokenCalls.Load(); n != 2 {
		t.Errorf("token calls = %d, want 2", n)
	}
}

func TestUnauthorizedRefreshesTokenOnce(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, `{"ok":true}`)
	})
	var out okBody
	if err := ts.client.get(context.Background(), "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if n := ts.tokenCalls.Load(); n != 2 {
		t.Errorf("token calls = %d, want 2", n)
	}
}

func TestUnauthorizedTwiceIsAuthError(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	var out okBody
	err := ts.client.get(context.Background(), "/ping", nil, &out)
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want AuthError 401", err)
	}
	if n := ts.apiCalls.Load(); n != 2 {
		t.Errorf("api calls = %d, want 2", n)
	}
}

func TestTokenEndpointRejectsCredentials(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, `{"ok":true}`) })
	ts.client.cfg.ClientSecret = "wrong"
	var out okBody
	err := ts.client.get(context.Background(), "/ping", nil, &out)
	var ae *AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AuthError", err)
	}
	if n := ts.apiCalls.Load(); n != 0 {
		t.Errorf("api calls = %d, want 0", n)
	}
}

func TestRateLimitShortWaitSleeps(t *testing.T) {
	var calls atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("X-Ratelimit-Remaining", "0.0")
			w.Header().Set("X-Ratelimit-Reset", "3")
		} else {
			w.Header().Set("X-Ratelimit-Remaining", "99.0")
			w.Header().Set("X-Ratelimit-Reset", "600")
		}
		writeJSON(w, `{"ok":true}`)
	})
	var out okBody
	ctx := context.Background()
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ts.clock.slept, []time.Duration{3 * time.Second}) {
		t.Errorf("slept = %v, want [3s]", ts.clock.slept)
	}
}

func TestRateLimitLongWaitFailsFast(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", "120")
		writeJSON(w, `{"ok":true}`)
	})
	var out okBody
	ctx := context.Background()
	if err := ts.client.get(ctx, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	err := ts.client.get(ctx, "/ping", nil, &out)
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.Seconds() != 120 {
		t.Fatalf("err = %v, want RateLimitError 120s", err)
	}
	if n := ts.apiCalls.Load(); n != 1 {
		t.Errorf("api calls = %d, want 1 (second call must not hit Reddit)", n)
	}
	if len(ts.clock.slept) != 0 {
		t.Errorf("slept = %v, want no sleep", ts.clock.slept)
	}
}

func TestTooManyRequests(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Reset", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	var out okBody
	err := ts.client.get(context.Background(), "/ping", nil, &out)
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.Seconds() != 42 {
		t.Fatalf("err = %v, want RateLimitError 42s", err)
	}
}

func TestServerErrorRetriedOnce(t *testing.T) {
	var calls atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(w, `{"ok":true}`)
	})
	var out okBody
	if err := ts.client.get(context.Background(), "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ts.clock.slept, []time.Duration{500 * time.Millisecond}) {
		t.Errorf("slept = %v, want [500ms]", ts.clock.slept)
	}
}

func TestServerErrorTwiceFails(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	var out okBody
	err := ts.client.get(context.Background(), "/ping", nil, &out)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want StatusError 502", err)
	}
}

func TestStatusMapping(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusForbidden: ErrForbidden,
		http.StatusNotFound:  ErrNotFound,
	} {
		ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
		var out okBody
		if err := ts.client.get(context.Background(), "/ping", nil, &out); !errors.Is(err, want) {
			t.Errorf("status %d: err = %v, want %v", status, err, want)
		}
	}
}

func TestNonJSONResponse(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>search page</html>")
	})
	var out okBody
	err := ts.client.get(context.Background(), "/ping", nil, &out)
	if !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("err = %v, want ErrUnexpectedResponse", err)
	}
}

// Reddit redirects searches in a nonexistent subreddit to a JSON subreddit
// search on the same host; following it would silently return "no posts".
func TestRedirectIsNotFound(t *testing.T) {
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r/nosuchsub/search" {
			http.Redirect(w, r, "/subreddits/search?q=nosuchsub", http.StatusFound)
			return
		}
		writeJSON(w, `{"kind":"Listing","data":{"after":null,"children":[{"kind":"t5","data":{"display_name":"nosuchsubs"}}]}}`)
	})
	_, err := ts.client.SearchPosts(context.Background(), SearchParams{Query: "go", Subreddit: "nosuchsub"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n := ts.apiCalls.Load(); n != 1 {
		t.Errorf("api calls = %d, want 1 (redirect must not be followed)", n)
	}
}

// One malformed string or duplicate key must not make a whole thread unreadable.
func TestLenientJSONDecoding(t *testing.T) {
	body := `[{"kind":"Listing","data":{"after":null,"after":null,"children":[{"kind":"t3","data":{"id":"abc123","title":"t","permalink":"/r/x/comments/abc123/t/"}}]}},` +
		`{"kind":"Listing","data":{"children":[{"kind":"t1","data":{"id":"c1","parent_id":"t3_abc123","author":"a","body":"bad \ud800 text","replies":{"kind":"Listing","data":{"children":[{"kind":"t1","data":{"id":"c2","body":"x \udc00","replies":""}}]}}}}]}}]`
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, body) })
	th, err := ts.client.GetThread(context.Background(), "abc123", ThreadParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Comments) != 1 || !strings.Contains(th.Comments[0].Body, "\uFFFD") && !strings.Contains(th.Comments[0].Body, "�") {
		t.Errorf("comments = %+v, want body with U+FFFD", th.Comments)
	}
	if len(th.Comments[0].Replies) != 1 {
		t.Errorf("nested reply with a lone surrogate was lost: %+v", th.Comments[0].Replies)
	}
}
