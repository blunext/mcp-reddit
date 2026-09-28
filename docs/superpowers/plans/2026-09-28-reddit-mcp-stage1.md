# mcp-reddit Stage 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only Reddit MCP server in Go with five tools (`search_posts`, `get_post`, `expand_comments`, `get_related_posts`, `search_subreddits`) backed by Reddit's OAuth app-only API.

**Architecture:** `internal/reddit` is a thin `net/http` client that owns auth, rate limiting and Reddit's wire format, and exposes only domain types. `internal/tools` registers MCP tools on an official `go-sdk` server; the handlers validate input, call the client through an interface and render compact plain text. `cmd/mcp-reddit` reads the environment and serves over stdio.

**Tech Stack:** Go 1.27; `github.com/modelcontextprotocol/go-sdk/mcp`; `encoding/json/v2` + `encoding/json/jsontext`; stdlib `net/http`, `httptest`; golangci-lint v2; GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-28-reddit-mcp-design.md`

## Global Constraints

- Module path: `github.com/blunext/mcp-reddit`; `go.mod` declares `go 1.27`.
- JSON: `encoding/json/v2` (and `encoding/json/jsontext`) everywhere in our code; never `encoding/json` v1. Custom decoding uses `UnmarshalJSONFrom(*jsontext.Decoder)`.
- Never set `GOEXPERIMENT`. If `encoding/json/v2` does not build with a plain `go build`, stop and report to the user.
- Auth: OAuth application-only (`client_credentials`) only. There is no anonymous or RSS fallback.
- Read-only: no write endpoints and no user login.
- No persistence: nothing is ever written to disk, and there is no cache.
- stdout is the MCP channel. Log only to stderr.
- Tool failures are returned as tool errors (`IsError: true`) with messages written for the model, never as protocol errors.
- On an exhausted rate limit, wait only if the reset is ≤ 5 s away; otherwise fail immediately. Per-request timeout is 15 s. 5xx responses are retried once after 500 ms.
- Output conventions: dates as `YYYY-MM-DD`, NSFW marked `[NSFW]` and never filtered, every item shows its `id`.
- Limits: `limit` defaults to 10 (max 50), `comment_limit` to 50 (max 200), `depth` to 4 (max 10). Post bodies are truncated at 8000 characters, comment bodies at 1500, excerpts at 300. At most 100 ids go into one `morechildren` request.
- Error strings are lowercase and have no trailing punctuation, because golangci-lint's staticcheck enforces ST1005.

## Review Focus

1. **Misspelled or nonexistent subreddit in `search_posts`.** Reddit may answer with a redirect to an HTML page or a 404. The expected result is a clear tool error, not a JSON decode failure. The test is in Task 3 (`TestNonJSONResponse`).
2. **Post links in the forms people actually paste:** tracking query strings (`?utm_source=share…`), comment permalinks (`…/comments/<id>/<slug>/<commentid>/`), `old.`/`m.` hosts, a missing scheme, uppercase hosts and surrounding whitespace. All of them should resolve to the post id. The test is in Task 2 (`TestParsePostRef`).
3. **Multi-line Markdown comments, very long comments and non-ASCII text.** Rendering must keep lines readable, truncate without splitting runes, and state the original length. The tests are in Task 6 (`TestTruncate`, `TestLongCommentTruncated`, and a multi-line golden).
4. **Removed or deleted content and negative scores** (`[deleted]`/`[removed]`, `score: -2`, `replies: ""`, `subscribers: null`). These must decode and render without crashing. The tests are in Task 1 (fixture), Task 4 (`subscribers: null`) and Task 6 (golden).
5. **Malformed or mis-shaped expand tokens.** Examples: `x:abc`, `m:`, `m:a,,b`, or several tokens pasted as one string (`"m:a,b c:x"`). The server should split whitespace-separated tokens and reject malformed ones with an input error naming the token. The tests are in Task 5 (`TestExpandCommentsRejectsBadTokens`) and Task 7 (`TestExpandCommentsFlattensTokens`).

## File Structure

```
go.mod, go.sum
.gitignore
.golangci.yml
.github/workflows/ci.yml
README.md                                  (rewrite)
cmd/mcp-reddit/main.go                     env config, wiring, stdio
cmd/mcp-reddit/main_test.go
internal/reddit/model.go                   domain types (Post, Comment, More, Thread, Fragment, PostPage, Subreddit)
internal/reddit/errors.go                  error vocabulary (ErrNotFound, RateLimitError, …)
internal/reddit/wire.go                    Reddit JSON envelopes + conversion to domain types
internal/reddit/ref.go                     ParsePostRef (id extraction from links)
internal/reddit/client.go                  Config, New, token, rate limiting, get()
internal/reddit/endpoints.go               SearchPosts, GetThread, Duplicates, SearchSubreddits
internal/reddit/expand.go                  ExpandComments, buildFragments, ResolvePostRef
internal/reddit/*_test.go, testdata/*.json
internal/reddit/integration_test.go        live API, skipped without credentials
internal/tools/reddit.go                   Reddit interface consumed by handlers
internal/tools/render.go                   plain-text rendering
internal/tools/tools.go                    input structs, handlers, Register, error mapping
internal/tools/*_test.go, testdata/*.golden
```

Work on a feature branch (for example `feat/stage1`) created from `docs/stage1-design`.

---

### Task 1: Module scaffold, domain types and Reddit wire decoding (json/v2 check)

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `internal/reddit/model.go`, `internal/reddit/errors.go`, `internal/reddit/wire.go`
- Test: `internal/reddit/wire_test.go`, `internal/reddit/testdata/comments.json`

**Interfaces:**
- Consumes: nothing.
- Produces (used by every later task):
  - `type Post struct { ID, Title, Subreddit, Author, SelfText, URL, Permalink string; Score, NumComments int; Created time.Time; IsSelf, NSFW bool }`
  - `type Comment struct { ID, ParentID, Author, Body string; Score int; Created time.Time; Replies []Comment; More *More }`
  - `type More struct { Count int; Tokens []string }`
  - `type Thread struct { Post Post; Comments []Comment; More *More }`
  - `type Fragment struct { ParentID string; Comments []Comment; More *More }`
  - `type PostPage struct { Posts []Post; After string }`
  - `type Subreddit struct { Name, Title, Description string; Subscribers int; NSFW bool }`
  - Errors: `ErrNotFound`, `ErrForbidden`, `ErrUnexpectedResponse`, `*InputError{Msg}`, `*RateLimitError{RetryAfter}` with `Seconds() int`, `*AuthError{Status, Reason}`, `*StatusError{Status}`.
  - Unexported helpers for later tasks:
    - wire types `thing`, `listing`, `linkData`, `commentData`, `moreData`, `subredditData`;
    - `decodePost(thing) (Post, error)`, `decodePosts([]thing) ([]Post, error)`, `toComment(commentData) (Comment, error)`;
    - `convertChildren([]thing) ([]Comment, *More, error)`, `toMore(moreData) *More`, `mergeMore(a, b *More) *More`;
    - `decodeThread([]listing) (Thread, error)`, and the constants `maxMoreIDs = 100` and `redditWeb = "https://www.reddit.com"`.

- [ ] **Step 1: Initialise the module and ignore file**

```bash
go mod init github.com/blunext/mcp-reddit
go mod edit -go=1.27
cat > .gitignore <<'EOF'
.idea/
/mcp-reddit
/dist/
*.test
*.out
EOF
```

- [ ] **Step 2: Write the fixture `internal/reddit/testdata/comments.json`**

This is a trimmed `/comments/{id}` response. It includes a nested reply, a "continue this thread" marker, a removed comment with `replies: ""` and a negative score, a "load more" marker, and a mixed-type `edited` field that must be ignored.

```json
[
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t3", "data": {
      "id": "abc123", "name": "t3_abc123", "title": "Is Go good for CLI tools?",
      "subreddit": "golang", "author": "gopher",
      "selftext": "Thinking about rewriting our tools.\n\nOpinions?",
      "url": "https://www.reddit.com/r/golang/comments/abc123/is_go_good_for_cli_tools/",
      "permalink": "/r/golang/comments/abc123/is_go_good_for_cli_tools/",
      "score": 350, "num_comments": 42, "created_utc": 1790000000.0,
      "is_self": true, "over_18": false, "edited": false
    }}
  ]}},
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t1", "data": {
      "id": "c1", "name": "t1_c1", "parent_id": "t3_abc123", "author": "alice",
      "body": "Yes, cobra & stdlib flag are great.", "score": 120, "created_utc": 1790000100.0,
      "edited": 1790000200.0,
      "replies": {"kind": "Listing", "data": {"after": null, "children": [
        {"kind": "t1", "data": {
          "id": "c2", "name": "t1_c2", "parent_id": "t1_c1", "author": "bob",
          "body": "Agreed.\n\nSingle binary is the killer feature.", "score": 45,
          "created_utc": 1790000300.0, "edited": false,
          "replies": {"kind": "Listing", "data": {"after": null, "children": [
            {"kind": "more", "data": {"id": "_", "name": "t1__", "parent_id": "t1_c2", "count": 0, "depth": 10, "children": []}}
          ]}}
        }}
      ]}}
    }},
    {"kind": "t1", "data": {
      "id": "c3", "name": "t1_c3", "parent_id": "t3_abc123", "author": "[deleted]",
      "body": "[removed]", "score": -2, "created_utc": 1790000400.0, "edited": false, "replies": ""
    }},
    {"kind": "more", "data": {"id": "m1", "name": "t1_m1", "parent_id": "t3_abc123", "count": 3, "depth": 0, "children": ["m1", "m2", "m3"]}}
  ]}}
]
```

- [ ] **Step 3: Write the failing test `internal/reddit/wire_test.go`**

```go
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
```

- [ ] **Step 4: Run the test and confirm it fails for the right reason (json/v2 check)**

Run: `go env GOEXPERIMENT; go test ./internal/reddit/`

Expected: compile errors such as `undefined: listing` / `undefined: decodeThread`.

**If the error instead mentions `encoding/json/v2` with "build constraints exclude all Go files", stop.** Report to the user that json/v2 is not enabled by default in this toolchain. Do not set `GOEXPERIMENT`.

- [ ] **Step 5: Write `internal/reddit/model.go`**

```go
// Package reddit is a small read-only client for Reddit's OAuth Data API.
// It hides Reddit's wire format and exposes only the domain types below.
package reddit

import "time"

// Post is a Reddit submission (kind t3).
type Post struct {
	ID          string
	Title       string
	Subreddit   string
	Author      string
	SelfText    string
	URL         string // link target; for self posts Reddit sets it to the post URL
	Permalink   string // absolute https://www.reddit.com/... URL
	Score       int
	NumComments int
	Created     time.Time
	IsSelf      bool
	NSFW        bool
}

// Comment is a Reddit comment (kind t1) with its loaded replies.
type Comment struct {
	ID       string
	ParentID string // fullname of the parent: "t1_…" or "t3_…"
	Author   string
	Body     string
	Score    int
	Created  time.Time
	Replies  []Comment
	More     *More // collapsed replies at this level, nil if none
}

// More describes collapsed comments. Tokens are opaque values for ExpandComments.
// Count is 0 for "continue this thread" markers.
type More struct {
	Count  int
	Tokens []string
}

// Thread is a post with its comment tree.
type Thread struct {
	Post     Post
	Comments []Comment
	More     *More // collapsed top-level comments
}

// Fragment is a group of expanded comments that share a parent.
type Fragment struct {
	ParentID string // fullname of the comment or post these reply to
	Comments []Comment
	More     *More
}

// PostPage is one page of post results.
type PostPage struct {
	Posts []Post
	After string // cursor for the next page, empty when there are no more results
}

// Subreddit is a community (kind t5).
type Subreddit struct {
	Name        string
	Title       string
	Description string
	Subscribers int
	NSFW        bool
}
```

- [ ] **Step 6: Write `internal/reddit/errors.go`**

```go
package reddit

import (
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrNotFound means the post or subreddit does not exist, was removed, or is banned.
	ErrNotFound = errors.New("reddit: not found")
	// ErrForbidden means the content is private, quarantined or otherwise restricted.
	ErrForbidden = errors.New("reddit: forbidden")
	// ErrUnexpectedResponse means Reddit answered with something other than JSON.
	ErrUnexpectedResponse = errors.New("reddit: unexpected non-JSON response")
)

// InputError reports a caller mistake; Msg is safe to show to the model as is.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// RateLimitError reports an exhausted Reddit rate limit.
type RateLimitError struct{ RetryAfter time.Duration }

// Seconds returns RetryAfter rounded up to whole seconds.
func (e *RateLimitError) Seconds() int { return int(math.Ceil(e.RetryAfter.Seconds())) }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("reddit: rate limit exhausted, retry in %ds", e.Seconds())
}

// AuthError reports rejected or unusable API credentials.
type AuthError struct {
	Status int
	Reason string
}

func (e *AuthError) Error() string {
	s := fmt.Sprintf("reddit: authentication failed (HTTP %d)", e.Status)
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	return s
}

// StatusError reports any other unexpected HTTP status.
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("reddit: unexpected HTTP status %d", e.Status) }
```

- [ ] **Step 7: Write `internal/reddit/wire.go`**

```go
package reddit

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"time"
)

const (
	redditWeb  = "https://www.reddit.com"
	maxMoreIDs = 100 // Reddit's limit for one /api/morechildren call
)

// thing is Reddit's {"kind": ..., "data": {...}} envelope.
type thing struct {
	Kind string         `json:"kind"`
	Data jsontext.Value `json:"data"`
}

type listing struct {
	Data struct {
		After    string  `json:"after"`
		Children []thing `json:"children"`
	} `json:"data"`
}

type linkData struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Subreddit   string  `json:"subreddit"`
	Author      string  `json:"author"`
	SelfText    string  `json:"selftext"`
	URL         string  `json:"url"`
	Permalink   string  `json:"permalink"`
	Score       int     `json:"score"`
	NumComments int     `json:"num_comments"`
	CreatedUTC  float64 `json:"created_utc"`
	IsSelf      bool    `json:"is_self"`
	Over18      bool    `json:"over_18"`
}

type commentData struct {
	ID         string  `json:"id"`
	ParentID   string  `json:"parent_id"`
	Author     string  `json:"author"`
	Body       string  `json:"body"`
	Score      int     `json:"score"`
	CreatedUTC float64 `json:"created_utc"`
	Replies    replies `json:"replies"`
}

type moreData struct {
	ID       string   `json:"id"`
	ParentID string   `json:"parent_id"`
	Count    int      `json:"count"`
	Children []string `json:"children"`
}

type subredditData struct {
	DisplayName       string `json:"display_name"`
	Title             string `json:"title"`
	PublicDescription string `json:"public_description"`
	Subscribers       int    `json:"subscribers"`
	Over18            bool   `json:"over18"`
}

// replies is a listing, or "" when a comment has no replies.
type replies struct{ listing *listing }

func (r *replies) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	v, err := dec.ReadValue()
	if err != nil {
		return err
	}
	if v.Kind() == '"' {
		r.listing = nil
		return nil
	}
	var l listing
	if err := json.Unmarshal(v, &l); err != nil {
		return err
	}
	r.listing = &l
	return nil
}

func unixTime(f float64) time.Time { return time.Unix(int64(f), 0).UTC() }

func toPost(d linkData) Post {
	return Post{
		ID:          d.ID,
		Title:       d.Title,
		Subreddit:   d.Subreddit,
		Author:      d.Author,
		SelfText:    d.SelfText,
		URL:         d.URL,
		Permalink:   redditWeb + d.Permalink,
		Score:       d.Score,
		NumComments: d.NumComments,
		Created:     unixTime(d.CreatedUTC),
		IsSelf:      d.IsSelf,
		NSFW:        d.Over18,
	}
}

func decodePost(t thing) (Post, error) {
	if t.Kind != "t3" {
		return Post{}, fmt.Errorf("reddit: expected a post (t3), got %q", t.Kind)
	}
	var d linkData
	if err := json.Unmarshal(t.Data, &d); err != nil {
		return Post{}, fmt.Errorf("decoding post: %w", err)
	}
	return toPost(d), nil
}

// decodePosts converts the t3 children of a listing and skips everything else.
func decodePosts(ts []thing) ([]Post, error) {
	var posts []Post
	for _, t := range ts {
		if t.Kind != "t3" {
			continue
		}
		p, err := decodePost(t)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func toComment(d commentData) (Comment, error) {
	c := Comment{
		ID:       d.ID,
		ParentID: d.ParentID,
		Author:   d.Author,
		Body:     d.Body,
		Score:    d.Score,
		Created:  unixTime(d.CreatedUTC),
	}
	if d.Replies.listing != nil {
		var err error
		c.Replies, c.More, err = convertChildren(d.Replies.listing.Data.Children)
		if err != nil {
			return Comment{}, err
		}
	}
	return c, nil
}

// convertChildren turns a comment listing into comments plus the collapsed marker at that level.
func convertChildren(ts []thing) ([]Comment, *More, error) {
	var comments []Comment
	var more *More
	for _, t := range ts {
		switch t.Kind {
		case "t1":
			var d commentData
			if err := json.Unmarshal(t.Data, &d); err != nil {
				return nil, nil, fmt.Errorf("decoding comment: %w", err)
			}
			c, err := toComment(d)
			if err != nil {
				return nil, nil, err
			}
			comments = append(comments, c)
		case "more":
			var d moreData
			if err := json.Unmarshal(t.Data, &d); err != nil {
				return nil, nil, fmt.Errorf("decoding more marker: %w", err)
			}
			more = mergeMore(more, toMore(d))
		}
	}
	return comments, more, nil
}

// toMore builds expand tokens: "m:<id>,<id>…" (at most maxMoreIDs ids each) for
// "load more comments", or "c:<comment id>" for "continue this thread".
func toMore(d moreData) *More {
	if len(d.Children) > 0 {
		m := &More{Count: d.Count}
		for i := 0; i < len(d.Children); i += maxMoreIDs {
			end := min(i+maxMoreIDs, len(d.Children))
			m.Tokens = append(m.Tokens, "m:"+strings.Join(d.Children[i:end], ","))
		}
		return m
	}
	if id, ok := strings.CutPrefix(d.ParentID, "t1_"); ok {
		return &More{Count: d.Count, Tokens: []string{"c:" + id}}
	}
	return nil
}

func mergeMore(a, b *More) *More {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	tokens := append(append([]string{}, a.Tokens...), b.Tokens...)
	return &More{Count: a.Count + b.Count, Tokens: tokens}
}

// decodeThread converts the two-listing response of /comments/{id}.
func decodeThread(resp []listing) (Thread, error) {
	if len(resp) < 2 || len(resp[0].Data.Children) == 0 {
		return Thread{}, ErrNotFound
	}
	post, err := decodePost(resp[0].Data.Children[0])
	if err != nil {
		return Thread{}, err
	}
	comments, more, err := convertChildren(resp[1].Data.Children)
	if err != nil {
		return Thread{}, err
	}
	return Thread{Post: post, Comments: comments, More: more}, nil
}
```

- [ ] **Step 8: Run the tests and confirm they pass**

Run: `gofmt -l . ; go vet ./... && go test ./internal/reddit/ -v`

Expected: `gofmt -l` prints nothing, and `TestDecodeThread`, `TestDecodeThreadEmpty`, `TestToMore` and `TestMergeMore` pass. No `GOEXPERIMENT` is set.

- [ ] **Step 9: Commit**

```bash
git add go.mod .gitignore internal/reddit
git commit -m "feat(reddit): add domain types and json/v2 wire decoding"
```

---

### Task 2: Post reference parsing

**Files:**
- Create: `internal/reddit/ref.go`
- Test: `internal/reddit/ref_test.go`

**Interfaces:**
- Consumes: `InputError` (Task 1).
- Produces:
  - `type PostRef struct { ID string; SharePath string }` — exactly one field is set;
  - `func ParsePostRef(s string) (PostRef, error)`;
  - the unexported `idRe` (`^[0-9a-z]{1,13}$`), which Task 5 reuses to validate comment ids.

- [ ] **Step 1: Write the failing test `internal/reddit/ref_test.go`**

```go
package reddit

import (
	"errors"
	"testing"
)

func TestParsePostRef(t *testing.T) {
	tests := []struct {
		in, id, share string
	}{
		{"abc123", "abc123", ""},
		{"  abc123 \n", "abc123", ""},
		{"t3_abc123", "abc123", ""},
		{"https://www.reddit.com/r/golang/comments/abc123/is_go_good/", "abc123", ""},
		{"https://www.reddit.com/r/golang/comments/abc123/is_go_good/?utm_source=share&utm_medium=ios_app", "abc123", ""},
		{"https://www.reddit.com/r/golang/comments/abc123/is_go_good/c9xyz/", "abc123", ""},
		{"https://old.reddit.com/r/golang/comments/abc123/", "abc123", ""},
		{"https://m.reddit.com/r/golang/comments/abc123/x/", "abc123", ""},
		{"https://WWW.Reddit.com/r/golang/comments/abc123/x", "abc123", ""},
		{"https://www.reddit.com/comments/abc123", "abc123", ""},
		{"reddit.com/r/golang/comments/abc123/x", "abc123", ""},
		{"https://www.reddit.com/gallery/abc123", "abc123", ""},
		{"https://redd.it/abc123", "abc123", ""},
		{"redd.it/abc123", "abc123", ""},
		{"https://www.reddit.com/r/golang/s/AbCdEf123", "", "/r/golang/s/AbCdEf123"},
		{"https://www.reddit.com/r/golang/s/AbCdEf123?share_id=x", "", "/r/golang/s/AbCdEf123"},
	}
	for _, tt := range tests {
		got, err := ParsePostRef(tt.in)
		if err != nil {
			t.Errorf("ParsePostRef(%q) error: %v", tt.in, err)
			continue
		}
		if got.ID != tt.id || got.SharePath != tt.share {
			t.Errorf("ParsePostRef(%q) = %+v, want ID %q SharePath %q", tt.in, got, tt.id, tt.share)
		}
	}
}

func TestParsePostRefErrors(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"t3_",
		"not a link at all",
		"https://example.com/r/golang/comments/abc123/",
		"https://notreddit.com/r/golang/comments/abc123/",
		"https://www.reddit.com/r/golang/",
	} {
		_, err := ParsePostRef(in)
		var ie *InputError
		if !errors.As(err, &ie) {
			t.Errorf("ParsePostRef(%q) err = %v, want *InputError", in, err)
		}
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/reddit/ -run TestParsePostRef`
Expected: FAIL to compile with `undefined: ParsePostRef`.

- [ ] **Step 3: Write `internal/reddit/ref.go`**

```go
package reddit

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// idRe matches a base36 Reddit id without its kind prefix.
var idRe = regexp.MustCompile(`^[0-9a-z]{1,13}$`)

// PostRef is a parsed post reference: either a known ID, or a share link path
// (/r/<sub>/s/<code>) that must be resolved through Reddit's redirect.
type PostRef struct {
	ID        string
	SharePath string
}

// ParsePostRef extracts a post id from an id, a t3_ fullname or a Reddit URL.
func ParsePostRef(s string) (PostRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return PostRef{}, &InputError{Msg: "post reference is empty"}
	}
	if id, ok := strings.CutPrefix(s, "t3_"); ok {
		if idRe.MatchString(id) {
			return PostRef{ID: id}, nil
		}
		return PostRef{}, badRef(s)
	}
	if idRe.MatchString(s) {
		return PostRef{ID: s}, nil
	}

	raw := s
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return PostRef{}, badRef(s)
	}
	host := strings.ToLower(u.Hostname())
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })

	switch {
	case host == "redd.it":
		if len(segs) == 1 && idRe.MatchString(segs[0]) {
			return PostRef{ID: segs[0]}, nil
		}
	case host == "reddit.com" || strings.HasSuffix(host, ".reddit.com"):
		for i, seg := range segs {
			if (seg == "comments" || seg == "gallery") && i+1 < len(segs) && idRe.MatchString(segs[i+1]) {
				return PostRef{ID: segs[i+1]}, nil
			}
		}
		if len(segs) == 4 && segs[0] == "r" && segs[2] == "s" {
			return PostRef{SharePath: "/r/" + segs[1] + "/s/" + segs[3]}, nil
		}
	}
	return PostRef{}, badRef(s)
}

func badRef(s string) error {
	return &InputError{Msg: fmt.Sprintf(
		"cannot find a Reddit post id in %q; pass a post id (e.g. abc123), a t3_ fullname or a Reddit post URL", s)}
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/reddit/ -run TestParsePostRef -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/reddit/ref.go internal/reddit/ref_test.go
git commit -m "feat(reddit): parse post ids from ids, fullnames and URLs"
```

---

### Task 3: HTTP core — OAuth token, rate limiting, status mapping

**Files:**
- Create: `internal/reddit/client.go`
- Test: `internal/reddit/client_test.go`

**Interfaces:**
- Consumes: the error types from Task 1.
- Produces:
  - `type Config struct { ClientID, ClientSecret, UserAgent, APIBase, WWWBase string; HTTPClient *http.Client; Now func() time.Time; Sleep func(context.Context, time.Duration) error }`;
  - `const DefaultAPIBase = "https://oauth.reddit.com"`, `const DefaultWWWBase = "https://www.reddit.com"`;
  - `func New(cfg Config) *Client`;
  - unexported `(c *Client) get(ctx, path string, q url.Values, out any) error`, which JSON-decodes a 2xx response into `out`;
  - unexported `discard(*http.Response)` and the `Client` field `noRedirect *http.Client`, both used by Task 5;
  - the test helpers `newTestServer`, `writeJSON` and `fakeClock`, reused by the tests in Tasks 4 and 5.

- [ ] **Step 1: Write the failing test `internal/reddit/client_test.go`**

```go
package reddit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
		if r.URL.Path == "/r/nosuchsub/search" {
			http.Redirect(w, r, "/subreddits/search?q=nosuchsub", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>search page</html>")
	})
	var out okBody
	err := ts.client.get(context.Background(), "/r/nosuchsub/search", nil, &out)
	if !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("err = %v, want ErrUnexpectedResponse", err)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/reddit/ -run 'TestGet|TestToken|TestUnauthorized|TestRateLimit|TestTooMany|TestServerError|TestStatus|TestNonJSON'`
Expected: FAIL to compile with `undefined: New` / `undefined: Config`.

- [ ] **Step 3: Write `internal/reddit/client.go`**

```go
package reddit

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAPIBase = "https://oauth.reddit.com"
	DefaultWWWBase = "https://www.reddit.com"

	requestTimeout = 15 * time.Second
	maxRateWait    = 5 * time.Second
	retryBackoff   = 500 * time.Millisecond
	tokenSlack     = time.Minute
)

// Config configures a Client. Only ClientID, ClientSecret and UserAgent are required.
type Config struct {
	ClientID     string
	ClientSecret string
	UserAgent    string

	APIBase    string // default DefaultAPIBase
	WWWBase    string // default DefaultWWWBase; token endpoint and share links
	HTTPClient *http.Client
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
}

// Client talks to Reddit's OAuth API with application-only credentials.
type Client struct {
	cfg        Config
	http       *http.Client
	noRedirect *http.Client

	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time

	rateMu        sync.Mutex
	rateKnown     bool
	rateRemaining float64
	rateReset     time.Time
}

// New returns a Client, filling in defaults for unset Config fields.
func New(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.WWWBase == "" {
		cfg.WWWBase = DefaultWWWBase
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	noRedirect := *cfg.HTTPClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, http: cfg.HTTPClient, noRedirect: &noRedirect}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get performs an authenticated GET on the API and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if q == nil {
		q = url.Values{}
	}
	q.Set("raw_json", "1")
	endpoint := c.cfg.APIBase + path + "?" + q.Encode()

	authRetried, serverRetried := false, false
	for {
		resp, err := c.doAPI(ctx, endpoint)
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && !authRetried:
			discard(resp)
			c.invalidateToken()
			authRetried = true
			continue
		case resp.StatusCode == http.StatusUnauthorized:
			discard(resp)
			return &AuthError{Status: resp.StatusCode}
		case resp.StatusCode == http.StatusTooManyRequests:
			discard(resp)
			return &RateLimitError{RetryAfter: retryAfter(resp.Header)}
		case resp.StatusCode >= 500 && !serverRetried:
			discard(resp)
			serverRetried = true
			if err := c.cfg.Sleep(ctx, retryBackoff); err != nil {
				return err
			}
			continue
		}
		return decodeResponse(resp, out)
	}
}

func (c *Client) doAPI(ctx context.Context, endpoint string) (*http.Response, error) {
	if err := c.waitRateLimit(ctx); err != nil {
		return nil, err
	}
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reddit request failed: %w", err)
	}
	c.updateRateLimit(resp.Header)
	return resp, nil
}

func decodeResponse(resp *http.Response, out any) error {
	defer discard(resp)
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &StatusError{Status: resp.StatusCode}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return ErrUnexpectedResponse
	}
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("decoding reddit response: %w", err)
	}
	return nil
}

// discard drains and closes the body so the connection can be reused.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && c.cfg.Now().Before(c.tokenExp.Add(-tokenSlack)) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WWWBase+"/api/v1/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting reddit access token: %w", err)
	}
	defer discard(resp)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", &AuthError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Status: resp.StatusCode}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.UnmarshalRead(resp.Body, &tr); err != nil {
		return "", fmt.Errorf("decoding reddit access token: %w", err)
	}
	if tr.AccessToken == "" {
		return "", &AuthError{Status: resp.StatusCode, Reason: tr.Error}
	}
	c.token = tr.AccessToken
	c.tokenExp = c.cfg.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.token, nil
}

func (c *Client) invalidateToken() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = ""
}

func (c *Client) updateRateLimit(h http.Header) {
	remaining, err1 := strconv.ParseFloat(h.Get("X-Ratelimit-Remaining"), 64)
	reset, err2 := strconv.ParseFloat(h.Get("X-Ratelimit-Reset"), 64)
	if err1 != nil || err2 != nil {
		return
	}
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	c.rateKnown = true
	c.rateRemaining = remaining
	c.rateReset = c.cfg.Now().Add(time.Duration(reset * float64(time.Second)))
}

// waitRateLimit sleeps through a short reset window and fails fast on a long one.
func (c *Client) waitRateLimit(ctx context.Context) error {
	c.rateMu.Lock()
	known, remaining, reset := c.rateKnown, c.rateRemaining, c.rateReset
	c.rateMu.Unlock()
	if !known || remaining >= 1 {
		return nil
	}
	d := reset.Sub(c.cfg.Now())
	if d <= 0 {
		return nil
	}
	if d > maxRateWait {
		return &RateLimitError{RetryAfter: d}
	}
	return c.cfg.Sleep(ctx, d)
}

func retryAfter(h http.Header) time.Duration {
	for _, key := range []string{"X-Ratelimit-Reset", "Retry-After"} {
		if s, err := strconv.ParseFloat(h.Get(key), 64); err == nil && s > 0 {
			return time.Duration(s * float64(time.Second))
		}
	}
	return time.Minute
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -l . ; go vet ./... && go test -race ./internal/reddit/ -v`
Expected: every test passes and `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/reddit/client.go internal/reddit/client_test.go
git commit -m "feat(reddit): add OAuth client with rate limiting and status mapping"
```

---

### Task 4: Search, thread, duplicates and subreddit endpoints

**Files:**
- Create: `internal/reddit/endpoints.go`
- Test: `internal/reddit/endpoints_test.go`, `internal/reddit/testdata/search.json`, `internal/reddit/testdata/duplicates.json`, `internal/reddit/testdata/subreddits.json`

**Interfaces:**
- Consumes: `get`, `decodePost`, `decodePosts`, `decodeThread`, `subredditData` and the test helpers (Tasks 1 and 3).
- Produces:
  - `type SearchParams struct { Query, Subreddit, Sort, Time, After string; Limit int }`;
  - `type ThreadParams struct { Sort string; Limit, Depth int }`, where `Sort` is Reddit's API value (`confidence`, `top`, `new`, `controversial`, `old`, `qa`);
  - `func (c *Client) SearchPosts(ctx context.Context, p SearchParams) (PostPage, error)`;
  - `func (c *Client) GetThread(ctx context.Context, postID string, p ThreadParams) (Thread, error)`;
  - `func (c *Client) Duplicates(ctx context.Context, postID string, limit int) (Post, []Post, error)`;
  - `func (c *Client) SearchSubreddits(ctx context.Context, query string, limit int) ([]Subreddit, error)`;
  - unexported `(p ThreadParams) values() url.Values`, which Task 5 uses.

- [ ] **Step 1: Write the fixtures**

`internal/reddit/testdata/search.json`:

```json
{"kind": "Listing", "data": {"after": "t3_next", "children": [
  {"kind": "t3", "data": {"id": "p1", "title": "Go vs Rust for CLIs", "subreddit": "golang", "author": "alice",
    "selftext": "Which one would you pick?", "url": "https://www.reddit.com/r/golang/comments/p1/go_vs_rust_for_clis/",
    "permalink": "/r/golang/comments/p1/go_vs_rust_for_clis/", "score": 210, "num_comments": 88,
    "created_utc": 1789000000.0, "is_self": true, "over_18": false}},
  {"kind": "t3", "data": {"id": "p2", "title": "Benchmark article", "subreddit": "programming", "author": "bob",
    "selftext": "", "url": "https://example.com/bench",
    "permalink": "/r/programming/comments/p2/benchmark_article/", "score": 15, "num_comments": 3,
    "created_utc": 1789500000.0, "is_self": false, "over_18": true}}
]}}
```

`internal/reddit/testdata/duplicates.json`:

```json
[
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t3", "data": {"id": "p2", "title": "Benchmark article", "subreddit": "programming", "author": "bob",
      "selftext": "", "url": "https://example.com/bench", "permalink": "/r/programming/comments/p2/benchmark_article/",
      "score": 15, "num_comments": 3, "created_utc": 1789500000.0, "is_self": false, "over_18": false}}
  ]}},
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t3", "data": {"id": "p3", "title": "Benchmark article", "subreddit": "rust", "author": "carol",
      "selftext": "", "url": "https://example.com/bench", "permalink": "/r/rust/comments/p3/benchmark_article/",
      "score": 40, "num_comments": 12, "created_utc": 1789600000.0, "is_self": false, "over_18": false}}
  ]}}
]
```

`internal/reddit/testdata/subreddits.json`:

```json
{"kind": "Listing", "data": {"after": null, "children": [
  {"kind": "t5", "data": {"display_name": "golang", "title": "The Go Programming Language",
    "public_description": "Ask questions and post articles about the Go programming language.",
    "subscribers": 300000, "over18": false}},
  {"kind": "t5", "data": {"display_name": "gomemes", "title": "Go memes", "public_description": "",
    "subscribers": null, "over18": true}}
]}}
```

- [ ] **Step 2: Write the failing test `internal/reddit/endpoints_test.go`**

```go
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
```

- [ ] **Step 3: Run the test and confirm it fails**

Run: `go test ./internal/reddit/ -run 'TestSearch|TestGetThread|TestDuplicates'`
Expected: FAIL to compile with `undefined: SearchParams`.

- [ ] **Step 4: Write `internal/reddit/endpoints.go`**

```go
package reddit

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SearchParams are the options of a post search. Empty fields use Reddit's defaults.
type SearchParams struct {
	Query     string
	Subreddit string // optional; "golang", "r/golang" and "/r/golang/" are equivalent
	Sort      string // relevance, hot, top, new, comments
	Time      string // hour, day, week, month, year, all
	After     string
	Limit     int
}

// ThreadParams control how much of a comment tree is loaded.
// Sort takes Reddit API values: confidence, top, new, controversial, old, qa.
type ThreadParams struct {
	Sort  string
	Limit int
	Depth int
}

func (p ThreadParams) values() url.Values {
	q := url.Values{}
	setNonEmpty(q, "sort", p.Sort)
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Depth > 0 {
		q.Set("depth", strconv.Itoa(p.Depth))
	}
	return q
}

func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func normalizeSubreddit(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "r/")
	return strings.TrimSuffix(s, "/")
}

// SearchPosts searches submissions, optionally within one subreddit.
func (c *Client) SearchPosts(ctx context.Context, p SearchParams) (PostPage, error) {
	q := url.Values{"q": {p.Query}, "type": {"link"}}
	setNonEmpty(q, "sort", p.Sort)
	setNonEmpty(q, "t", p.Time)
	setNonEmpty(q, "after", p.After)
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	path := "/search"
	if sub := normalizeSubreddit(p.Subreddit); sub != "" {
		path = "/r/" + url.PathEscape(sub) + "/search"
		q.Set("restrict_sr", "1")
	}
	var l listing
	if err := c.get(ctx, path, q, &l); err != nil {
		return PostPage{}, err
	}
	posts, err := decodePosts(l.Data.Children)
	if err != nil {
		return PostPage{}, err
	}
	return PostPage{Posts: posts, After: l.Data.After}, nil
}

// GetThread loads a post and its comment tree.
func (c *Client) GetThread(ctx context.Context, postID string, p ThreadParams) (Thread, error) {
	var resp []listing
	if err := c.get(ctx, "/comments/"+url.PathEscape(postID), p.values(), &resp); err != nil {
		return Thread{}, err
	}
	return decodeThread(resp)
}

// Duplicates returns the post and other submissions of the same link.
func (c *Client) Duplicates(ctx context.Context, postID string, limit int) (Post, []Post, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var resp []listing
	if err := c.get(ctx, "/duplicates/"+url.PathEscape(postID), q, &resp); err != nil {
		return Post{}, nil, err
	}
	if len(resp) < 2 || len(resp[0].Data.Children) == 0 {
		return Post{}, nil, ErrNotFound
	}
	orig, err := decodePost(resp[0].Data.Children[0])
	if err != nil {
		return Post{}, nil, err
	}
	dups, err := decodePosts(resp[1].Data.Children)
	if err != nil {
		return Post{}, nil, err
	}
	return orig, dups, nil
}

// SearchSubreddits finds communities by name or topic.
func (c *Client) SearchSubreddits(ctx context.Context, query string, limit int) ([]Subreddit, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var l listing
	if err := c.get(ctx, "/subreddits/search", q, &l); err != nil {
		return nil, err
	}
	var subs []Subreddit
	for _, t := range l.Data.Children {
		if t.Kind != "t5" {
			continue
		}
		var d subredditData
		if err := json.Unmarshal(t.Data, &d); err != nil {
			return nil, fmt.Errorf("decoding subreddit: %w", err)
		}
		subs = append(subs, Subreddit{
			Name:        d.DisplayName,
			Title:       d.Title,
			Description: d.PublicDescription,
			Subscribers: d.Subscribers,
			NSFW:        d.Over18,
		})
	}
	return subs, nil
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `gofmt -l . ; go vet ./... && go test -race ./internal/reddit/ -v`
Expected: every test passes.

- [ ] **Step 6: Commit**

```bash
git add internal/reddit/endpoints.go internal/reddit/endpoints_test.go internal/reddit/testdata
git commit -m "feat(reddit): add search, thread, duplicates and subreddit endpoints"
```

---

### Task 5: Comment expansion and share-link resolution

**Files:**
- Create: `internal/reddit/expand.go`
- Test: `internal/reddit/expand_test.go`, `internal/reddit/testdata/morechildren.json`, `internal/reddit/testdata/continue.json`

**Interfaces:**
- Consumes: `get`, `ThreadParams.values`, `decodeThread`, `toComment`, `toMore`, `mergeMore`, `idRe`, `maxMoreIDs`, `ParsePostRef`, `discard` and `Client.noRedirect` (Tasks 1–4).
- Produces:
  - `func (c *Client) ExpandComments(ctx context.Context, postID, token string, p ThreadParams) ([]Fragment, error)`;
  - `func (c *Client) ResolvePostRef(ctx context.Context, ref string) (string, error)`.

- [ ] **Step 1: Write the fixtures**

`internal/reddit/testdata/morechildren.json`: a flat `/api/morechildren` response. `m4` replies to `m1` from the same batch, `m5` replies to a comment outside the batch, and a nested `more` marker hangs under `m1`.

```json
{"json": {"errors": [], "data": {"things": [
  {"kind": "t1", "data": {"id": "m1", "parent_id": "t3_abc123", "author": "carol", "body": "Top-level from more.",
    "score": 10, "created_utc": 1790000500.0, "replies": ""}},
  {"kind": "t1", "data": {"id": "m4", "parent_id": "t1_m1", "author": "dave", "body": "Reply to m1.",
    "score": 4, "created_utc": 1790000600.0, "replies": ""}},
  {"kind": "t1", "data": {"id": "m5", "parent_id": "t1_c9", "author": "erin",
    "body": "Reply to a comment outside this batch.", "score": 2, "created_utc": 1790000700.0, "replies": ""}},
  {"kind": "more", "data": {"id": "m6", "parent_id": "t1_m1", "count": 7, "children": ["m6", "m7"]}}
]}}}
```

`internal/reddit/testdata/continue.json`: `/comments/abc123?comment=c2`, rooted at `c2`.

```json
[
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t3", "data": {"id": "abc123", "title": "Is Go good for CLI tools?", "subreddit": "golang",
      "author": "gopher", "selftext": "", "url": "", "permalink": "/r/golang/comments/abc123/x/",
      "score": 350, "num_comments": 42, "created_utc": 1790000000.0, "is_self": true, "over_18": false}}
  ]}},
  {"kind": "Listing", "data": {"after": null, "children": [
    {"kind": "t1", "data": {"id": "c2", "parent_id": "t1_c1", "author": "bob", "body": "Agreed.",
      "score": 45, "created_utc": 1790000300.0,
      "replies": {"kind": "Listing", "data": {"after": null, "children": [
        {"kind": "t1", "data": {"id": "c4", "parent_id": "t1_c2", "author": "frank", "body": "Deep reply one.",
          "score": 3, "created_utc": 1790000800.0, "replies": ""}},
        {"kind": "t1", "data": {"id": "c5", "parent_id": "t1_c2", "author": "grace", "body": "Deep reply two.",
          "score": 1, "created_utc": 1790000900.0, "replies": ""}}
      ]}}
    }}
  ]}}
]
```

- [ ] **Step 2: Write the failing test `internal/reddit/expand_test.go`**

```go
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
```

- [ ] **Step 3: Run the test and confirm it fails**

Run: `go test ./internal/reddit/ -run 'TestExpand|TestResolve'`
Expected: FAIL to compile with `undefined: (*Client).ExpandComments`.

- [ ] **Step 4: Write `internal/reddit/expand.go`**

```go
package reddit

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type moreChildrenResponse struct {
	JSON struct {
		Errors []jsontext.Value `json:"errors"`
		Data   struct {
			Things []thing `json:"things"`
		} `json:"data"`
	} `json:"json"`
}

// ExpandComments loads one collapsed branch identified by a token from a More marker.
func (c *Client) ExpandComments(ctx context.Context, postID, token string, p ThreadParams) ([]Fragment, error) {
	kind, arg, _ := strings.Cut(strings.TrimSpace(token), ":")
	switch kind {
	case "m":
		ids := strings.Split(arg, ",")
		if arg == "" || len(ids) > maxMoreIDs || !allIDs(ids) {
			break
		}
		q := url.Values{"api_type": {"json"}, "link_id": {"t3_" + postID}, "children": {arg}}
		setNonEmpty(q, "sort", p.Sort)
		var resp moreChildrenResponse
		if err := c.get(ctx, "/api/morechildren", q, &resp); err != nil {
			return nil, err
		}
		if len(resp.JSON.Errors) > 0 {
			return nil, fmt.Errorf("reddit: morechildren failed: %s", resp.JSON.Errors[0])
		}
		return buildFragments(resp.JSON.Data.Things)
	case "c":
		if !idRe.MatchString(arg) {
			break
		}
		q := p.values()
		q.Set("comment", arg)
		var resp []listing
		if err := c.get(ctx, "/comments/"+url.PathEscape(postID), q, &resp); err != nil {
			return nil, err
		}
		th, err := decodeThread(resp)
		if err != nil {
			return nil, err
		}
		if len(th.Comments) == 0 {
			return nil, ErrNotFound
		}
		root := th.Comments[0]
		return []Fragment{{ParentID: "t1_" + root.ID, Comments: root.Replies, More: root.More}}, nil
	}
	return nil, &InputError{Msg: fmt.Sprintf(
		"invalid expand token %q; copy tokens exactly from a get_post or expand_comments marker", token)}
}

func allIDs(ids []string) bool {
	for _, id := range ids {
		if !idRe.MatchString(id) {
			return false
		}
	}
	return true
}

type node struct {
	c    Comment
	kids []*node
	more *More
}

func (n *node) comment() Comment {
	c := n.c
	for _, k := range n.kids {
		c.Replies = append(c.Replies, k.comment())
	}
	c.More = mergeMore(c.More, n.more)
	return c
}

// buildFragments rebuilds trees from the flat morechildren list, grouping roots
// by parent in order of first appearance.
func buildFragments(things []thing) ([]Fragment, error) {
	nodes := map[string]*node{}
	var order []*node
	type pendingMore struct {
		parent string
		more   *More
	}
	var mores []pendingMore
	for _, t := range things {
		switch t.Kind {
		case "t1":
			var d commentData
			if err := json.Unmarshal(t.Data, &d); err != nil {
				return nil, fmt.Errorf("decoding comment: %w", err)
			}
			c, err := toComment(d)
			if err != nil {
				return nil, err
			}
			n := &node{c: c}
			nodes["t1_"+c.ID] = n
			order = append(order, n)
		case "more":
			var d moreData
			if err := json.Unmarshal(t.Data, &d); err != nil {
				return nil, fmt.Errorf("decoding more marker: %w", err)
			}
			if m := toMore(d); m != nil {
				mores = append(mores, pendingMore{parent: d.ParentID, more: m})
			}
		}
	}

	type group struct {
		parent string
		roots  []*node
		more   *More
	}
	var groups []*group
	byParent := map[string]*group{}
	groupFor := func(parent string) *group {
		g, ok := byParent[parent]
		if !ok {
			g = &group{parent: parent}
			byParent[parent] = g
			groups = append(groups, g)
		}
		return g
	}
	for _, n := range order {
		if p, ok := nodes[n.c.ParentID]; ok {
			p.kids = append(p.kids, n)
		} else {
			g := groupFor(n.c.ParentID)
			g.roots = append(g.roots, n)
		}
	}
	for _, pm := range mores {
		if p, ok := nodes[pm.parent]; ok {
			p.more = mergeMore(p.more, pm.more)
		} else {
			g := groupFor(pm.parent)
			g.more = mergeMore(g.more, pm.more)
		}
	}

	frags := make([]Fragment, 0, len(groups))
	for _, g := range groups {
		f := Fragment{ParentID: g.parent, More: g.more}
		for _, n := range g.roots {
			f.Comments = append(f.Comments, n.comment())
		}
		frags = append(frags, f)
	}
	return frags, nil
}

// ResolvePostRef returns the post id for any supported reference. Share links
// are resolved with one request that reads the redirect target.
func (c *Client) ResolvePostRef(ctx context.Context, ref string) (string, error) {
	r, err := ParsePostRef(ref)
	if err != nil {
		return "", err
	}
	if r.ID != "" {
		return r.ID, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.WWWBase+r.SharePath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolving share link: %w", err)
	}
	discard(resp)
	loc := resp.Header.Get("Location")
	if strings.HasPrefix(loc, "/") {
		loc = redditWeb + loc
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != "" {
		if target, err := ParsePostRef(loc); err == nil && target.ID != "" {
			return target.ID, nil
		}
	}
	return "", &InputError{Msg: fmt.Sprintf(
		"could not resolve the share link %s (HTTP %d); open it in a browser and pass the full post URL (…/comments/<id>/…) instead",
		ref, resp.StatusCode)}
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `gofmt -l . ; go vet ./... && go test -race ./internal/reddit/ -v`
Expected: every test passes.

- [ ] **Step 6: Commit**

```bash
git add internal/reddit/expand.go internal/reddit/expand_test.go internal/reddit/testdata
git commit -m "feat(reddit): expand collapsed comments and resolve share links"
```

---

### Task 6: Plain-text rendering

**Files:**
- Create: `internal/tools/render.go`
- Test: `internal/tools/render_test.go`, `internal/tools/testdata/*.golden`

**Interfaces:**
- Consumes: the domain types from `internal/reddit` (Task 1).
- Produces:
  - `renderPostList(posts []reddit.Post, after string) string`;
  - `renderThread(t reddit.Thread, sort string) string`;
  - `renderFragments(frags []reddit.Fragment) string`;
  - `renderRelated(orig reddit.Post, dups []reddit.Post) string`;
  - `renderSubreddits(subs []reddit.Subreddit) string`;
  - `truncate(s string, n int) string`;
  - the test helpers `samplePosts()`, `sampleThread()` and `created`, reused by Task 7.

- [ ] **Step 1: Write the failing test `internal/tools/render_test.go`**

```go
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/tools/`
Expected: FAIL to compile with `undefined: renderPostList`.

- [ ] **Step 3: Write `internal/tools/render.go`**

```go
package tools

import (
	"fmt"
	"strings"

	"github.com/blunext/mcp-reddit/internal/reddit"
)

const (
	excerptLen     = 300
	postBodyLen    = 8000
	commentBodyLen = 1500

	noRelated = "No other submissions of this link were found. This tool only finds reposts of the same link; " +
		"for text posts or broader coverage use search_posts with keywords from the title.\n"
)

// truncate cuts s to n runes and notes the original length.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("%s …[truncated, %d chars total]", string(r[:n]), len(r))
}

func excerpt(s string) string {
	return truncate(strings.Join(strings.Fields(s), " "), excerptLen)
}

func date(p reddit.Post) string { return p.Created.UTC().Format("2006-01-02") }

func title(p reddit.Post) string {
	if p.NSFW {
		return p.Title + " [NSFW]"
	}
	return p.Title
}

func postMeta(p reddit.Post) string {
	return fmt.Sprintf("r/%s · %d points · %d comments · %s · u/%s · id: %s",
		p.Subreddit, p.Score, p.NumComments, date(p), p.Author, p.ID)
}

func renderPostList(posts []reddit.Post, after string) string {
	if len(posts) == 0 {
		return "No posts found.\n"
	}
	var b strings.Builder
	for i, p := range posts {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, title(p), postMeta(p), p.Permalink)
		if !p.IsSelf && p.URL != "" {
			fmt.Fprintf(&b, "   link: %s\n", p.URL)
		}
		if ex := excerpt(p.SelfText); ex != "" {
			fmt.Fprintf(&b, "   %s\n", ex)
		}
		b.WriteString("\n")
	}
	if after != "" {
		fmt.Fprintf(&b, "More results: call again with after=%q\n", after)
	}
	return b.String()
}

func renderThread(t reddit.Thread, sort string) string {
	var b strings.Builder
	p := t.Post
	fmt.Fprintf(&b, "%s\n%s\n%s\n", title(p), postMeta(p), p.Permalink)
	if !p.IsSelf && p.URL != "" {
		fmt.Fprintf(&b, "link: %s\n", p.URL)
	}
	if body := strings.TrimSpace(p.SelfText); body != "" {
		fmt.Fprintf(&b, "\n%s\n", truncate(body, postBodyLen))
	}
	fmt.Fprintf(&b, "\n--- Comments (sort: %s) ---\n", sort)
	if len(t.Comments) == 0 && t.More == nil {
		b.WriteString("(no comments)\n")
	}
	writeComments(&b, t.Comments, 0)
	writeMore(&b, t.More, 0)
	return b.String()
}

func writeComments(b *strings.Builder, cs []reddit.Comment, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, c := range cs {
		body := truncate(strings.TrimSpace(c.Body), commentBodyLen)
		lines := strings.Split(body, "\n")
		fmt.Fprintf(b, "%s[%d] u/%s (%s) %s: %s\n", indent, c.Score, c.Author, c.ID,
			c.Created.UTC().Format("2006-01-02"), lines[0])
		for _, l := range lines[1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			fmt.Fprintf(b, "%s    %s\n", indent, l)
		}
		writeComments(b, c.Replies, depth+1)
		writeMore(b, c.More, depth+1)
	}
}

func writeMore(b *strings.Builder, m *reddit.More, depth int) {
	if m == nil || len(m.Tokens) == 0 {
		return
	}
	indent := strings.Repeat("  ", depth)
	tokens := strings.Join(m.Tokens, " ")
	if m.Count > 0 {
		fmt.Fprintf(b, "%s[+%d more replies → expand_comments tokens: %s]\n", indent, m.Count, tokens)
		return
	}
	fmt.Fprintf(b, "%s[continue this thread → expand_comments tokens: %s]\n", indent, tokens)
}

func renderFragments(frags []reddit.Fragment) string {
	if len(frags) == 0 {
		return "No additional comments returned.\n"
	}
	var b strings.Builder
	for i, f := range frags {
		if i > 0 {
			b.WriteString("\n")
		}
		if id, ok := strings.CutPrefix(f.ParentID, "t1_"); ok {
			fmt.Fprintf(&b, "--- Replies to comment %s ---\n", id)
		} else {
			b.WriteString("--- Top-level comments ---\n")
		}
		writeComments(&b, f.Comments, 0)
		writeMore(&b, f.More, 0)
	}
	return b.String()
}

func renderRelated(orig reddit.Post, dups []reddit.Post) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Other submissions of: %s (id: %s)\n", title(orig), orig.ID)
	if !orig.IsSelf && orig.URL != "" {
		fmt.Fprintf(&b, "link: %s\n", orig.URL)
	}
	b.WriteString("\n")
	if len(dups) == 0 {
		b.WriteString(noRelated)
		return b.String()
	}
	b.WriteString(renderPostList(dups, ""))
	return b.String()
}

func renderSubreddits(subs []reddit.Subreddit) string {
	if len(subs) == 0 {
		return "No subreddits found.\n"
	}
	var b strings.Builder
	for i, s := range subs {
		fmt.Fprintf(&b, "%d. r/%s · %d subscribers", i+1, s.Name, s.Subscribers)
		if s.NSFW {
			b.WriteString(" [NSFW]")
		}
		b.WriteString("\n")
		if d := excerpt(s.Description); d != "" {
			fmt.Fprintf(&b, "   %s\n", d)
		}
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Generate the golden files and check them against the expected output**

Run: `go test ./internal/tools/ -run TestRender -update && go test ./internal/tools/ -v`

Then compare each generated file with the content below, byte for byte. Every file ends with a newline. `post_list.golden` and `subreddits.golden` also end with a blank line, and `related.golden` ends with one blank line after the last item. If anything differs, fix `render.go`, not the golden files.

`internal/tools/testdata/post_list.golden`:
```
1. Go vs Rust for CLIs
   r/golang · 210 points · 88 comments · 2026-09-01 · u/alice · id: p1
   https://www.reddit.com/r/golang/comments/p1/go_vs_rust/
   Which one would you pick? Context: small team.

2. Benchmark article [NSFW]
   r/programming · 15 points · 3 comments · 2026-09-01 · u/bob · id: p2
   https://www.reddit.com/r/programming/comments/p2/benchmark_article/
   link: https://example.com/bench

More results: call again with after="t3_next"
```

`internal/tools/testdata/thread.golden`:
```
Is Go good for CLI tools?
r/golang · 350 points · 42 comments · 2026-09-01 · u/gopher · id: abc123
https://www.reddit.com/r/golang/comments/abc123/is_go_good/

Thinking about rewriting our tools.

Opinions?

--- Comments (sort: top) ---
[120] u/alice (c1) 2026-09-01: Yes, cobra & stdlib flag are great.
  [45] u/bob (c2) 2026-09-01: Agreed.
      Single binary is the killer feature.
    [continue this thread → expand_comments tokens: c:c2]
[-2] u/[deleted] (c3) 2026-09-01: [removed]
[+3 more replies → expand_comments tokens: m:m1,m2,m3]
```

`internal/tools/testdata/fragments.golden`:
```
--- Top-level comments ---
[10] u/carol (m1) 2026-09-01: Top-level from more.
  [4] u/dave (m4) 2026-09-01: Reply to m1.
  [+7 more replies → expand_comments tokens: m:m6,m7]

--- Replies to comment c9 ---
[2] u/erin (m5) 2026-09-01: Reply to a comment outside this batch.
```

`internal/tools/testdata/related.golden`:
```
Other submissions of: Benchmark article [NSFW] (id: p2)
link: https://example.com/bench

1. Benchmark article
   r/rust · 40 points · 12 comments · 2026-09-01 · u/carol · id: p3
   https://www.reddit.com/r/rust/comments/p3/benchmark_article/
   link: https://example.com/bench

```

`internal/tools/testdata/subreddits.golden`:
```
1. r/golang · 300000 subscribers
   Ask questions and post articles about the Go programming language.

2. r/gomemes · 0 subscribers [NSFW]

```

Expected: `go test ./internal/tools/ -v` passes without `-update`.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/render.go internal/tools/render_test.go internal/tools/testdata
git commit -m "feat(tools): render posts, threads and subreddits as compact text"
```

---

### Task 7: MCP tool handlers, registration and error mapping

**Files:**
- Create: `internal/tools/reddit.go`, `internal/tools/tools.go`
- Modify: `go.mod`, `go.sum` (add `github.com/modelcontextprotocol/go-sdk`)
- Test: `internal/tools/tools_test.go`, `internal/tools/e2e_test.go`

**Interfaces:**
- Consumes:
  - all `reddit.Client` methods (Tasks 4 and 5): `ResolvePostRef`, `SearchPosts`, `GetThread`, `ExpandComments`, `Duplicates` and `SearchSubreddits`;
  - `reddit.ParsePostRef`;
  - the error types from Task 1;
  - the render functions and test helpers from Task 6.
- Produces:
  - `type Reddit interface` (the six methods above);
  - `func Register(s *mcp.Server, r Reddit)`, which Task 8 calls with `*reddit.Client`.

- [ ] **Step 1: Add the MCP SDK dependency**

Run: `go get github.com/modelcontextprotocol/go-sdk@latest`
Expected: `go.mod` gains a `require github.com/modelcontextprotocol/go-sdk vX.Y.Z` line.

- [ ] **Step 2: Write the failing test `internal/tools/tools_test.go`**

```go
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
```

- [ ] **Step 3: Write the failing end-to-end test `internal/tools/e2e_test.go`**

```go
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
	defer cs.Close()

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
```

- [ ] **Step 4: Run the tests and confirm they fail**

Run: `go test ./internal/tools/`
Expected: FAIL to compile with `undefined: searchPosts` / `undefined: Register`.

- [ ] **Step 5: Write `internal/tools/reddit.go`**

```go
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
```

- [ ] **Step 6: Write `internal/tools/tools.go`**

```go
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
```

- [ ] **Step 7: Run the tests and confirm they pass**

Run: `go mod tidy && gofmt -l . ; go vet ./... && go test -race ./... -v`
Expected: every test passes, including `TestToolsOverMCP`.

If `TestToolsOverMCP` shows that tool calls with `Out = any` still carry an output schema or structured content, check the go-sdk docs for `AddTool` and make sure the handlers return the text result with a nil output. The tool must return only `TextContent`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/tools
git commit -m "feat(tools): register read-only Reddit MCP tools"
```

---

### Task 8: Server entry point and live integration tests

**Files:**
- Create: `cmd/mcp-reddit/main.go`
- Test: `cmd/mcp-reddit/main_test.go`, `internal/reddit/integration_test.go`

**Interfaces:**
- Consumes: `reddit.New`, `reddit.Config` (Task 3) and `tools.Register` (Task 7).
- Produces: the `mcp-reddit` binary, plus the unexported `configFromEnv(getenv func(string) string) (reddit.Config, error)` and `run(ctx context.Context, getenv func(string) string, t mcp.Transport) error`.

- [ ] **Step 1: Write the failing test `cmd/mcp-reddit/main_test.go`**

```go
package main

import (
	"context"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigFromEnvRequiresCredentials(t *testing.T) {
	for _, m := range []map[string]string{
		{},
		{"REDDIT_CLIENT_ID": "id"},
		{"REDDIT_CLIENT_SECRET": "secret"},
	} {
		_, err := configFromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), "REDDIT_CLIENT_ID") || !strings.Contains(err.Error(), "README") {
			t.Errorf("env %v: err = %v, want a message naming the variables and the README", m, err)
		}
	}
}

func TestConfigFromEnvUserAgent(t *testing.T) {
	cfg, err := configFromEnv(env(map[string]string{"REDDIT_CLIENT_ID": "id", "REDDIT_CLIENT_SECRET": "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "id" || cfg.ClientSecret != "secret" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !strings.HasPrefix(cfg.UserAgent, "mcp-reddit/") || !strings.Contains(cfg.UserAgent, "github.com/blunext/mcp-reddit") {
		t.Errorf("default user agent = %q", cfg.UserAgent)
	}
	cfg, err = configFromEnv(env(map[string]string{
		"REDDIT_CLIENT_ID": "id", "REDDIT_CLIENT_SECRET": "secret", "REDDIT_USER_AGENT": "custom/1.0 (by /u/me)",
	}))
	if err != nil || cfg.UserAgent != "custom/1.0 (by /u/me)" {
		t.Errorf("custom user agent = %q, err = %v", cfg.UserAgent, err)
	}
}

func TestRunFailsWithoutCredentials(t *testing.T) {
	if err := run(context.Background(), env(nil), nil); err == nil {
		t.Error("run must fail before serving when credentials are missing")
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./cmd/mcp-reddit/`
Expected: FAIL to compile with `undefined: configFromEnv`.

- [ ] **Step 3: Write `cmd/mcp-reddit/main.go`**

```go
// Command mcp-reddit is a read-only Reddit MCP server that speaks over stdio.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blunext/mcp-reddit/internal/reddit"
	"github.com/blunext/mcp-reddit/internal/tools"
)

func main() {
	// stdout carries the MCP protocol; logs go to stderr only.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp-reddit stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, t mcp.Transport) error {
	cfg, err := configFromEnv(getenv)
	if err != nil {
		return err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-reddit", Version: version()}, nil)
	tools.Register(server, reddit.New(cfg))
	return server.Run(ctx, t)
}

func configFromEnv(getenv func(string) string) (reddit.Config, error) {
	id, secret := getenv("REDDIT_CLIENT_ID"), getenv("REDDIT_CLIENT_SECRET")
	if id == "" || secret == "" {
		return reddit.Config{}, errors.New("REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET must be set; " +
			"see the README (https://github.com/blunext/mcp-reddit#credentials) for how to get Reddit API credentials")
	}
	ua := getenv("REDDIT_USER_AGENT")
	if ua == "" {
		ua = fmt.Sprintf("mcp-reddit/%s (+https://github.com/blunext/mcp-reddit)", version())
	}
	return reddit.Config{ClientID: id, ClientSecret: secret, UserAgent: ua}, nil
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
```

- [ ] **Step 4: Write `internal/reddit/integration_test.go`**

```go
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
```

- [ ] **Step 5: Run the tests and confirm they pass, with the live ones skipped**

Run: `gofmt -l . ; go vet ./... && go test -race ./... -v 2>&1 | grep -E '^(=== RUN|--- (PASS|FAIL|SKIP)|ok|FAIL)'`
Expected: every test passes, and the three `TestLive…` tests SKIP because no credentials are set.

- [ ] **Step 6: Build and check that the server exits cleanly without credentials**

Run: `go build ./... && env -u REDDIT_CLIENT_ID -u REDDIT_CLIENT_SECRET go run ./cmd/mcp-reddit; echo "exit=$?"`
Expected: stderr shows `mcp-reddit stopped` with the README hint, followed by `exit=1`.

- [ ] **Step 7: Commit**

```bash
git add cmd internal/reddit/integration_test.go
git commit -m "feat: add stdio server entry point and live integration tests"
```

---

### Task 9: README, lint configuration and CI

**Files:**
- Modify: `README.md` (full rewrite)
- Create: `.golangci.yml`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the whole project.
- Produces: user documentation, where the `#credentials` anchor is referenced by the error messages from Tasks 7 and 8; CI.

- [ ] **Step 1: Write `.golangci.yml`**

```yaml
version: "2"
linters:
  default: standard
formatters:
  enable:
    - gofmt
```

- [ ] **Step 2: Run the linter locally and fix every finding**

Run: `golangci-lint run ./... || go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: `0 issues.` Fix any finding in the code. Do not disable linters.

- [ ] **Step 3: Write `.github/workflows/ci.yml`**

Before committing, check that these action majors are still current on GitHub Marketplace, and bump them if newer ones exist.

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
      - uses: golangci/golangci-lint-action@v8
        with:
          version: latest
```

- [ ] **Step 4: Rewrite `README.md`**

````markdown
# mcp-reddit

A read-only [Model Context Protocol](https://modelcontextprotocol.io) server that lets AI assistants such as Claude Code and Claude Desktop research Reddit: search posts, read threads with their comments, expand collapsed branches, find other discussions of the same link, and discover subreddits.

It runs locally over stdio, uses Reddit's official OAuth API with your own credentials, never writes anything to Reddit and never stores Reddit data. See [PRIVACY.md](PRIVACY.md).

## Tools

| Tool | What it does |
|---|---|
| `search_posts` | Search posts by keywords, optionally in one subreddit; sort, time window, pagination |
| `get_post` | Read a post and its comment tree; accepts ids, URLs, `redd.it` and mobile share links |
| `expand_comments` | Load collapsed comment branches using tokens from `get_post` output |
| `get_related_posts` | Other submissions of the same link across subreddits |
| `search_subreddits` | Find subreddits by name or topic |

## Install

```sh
go install github.com/blunext/mcp-reddit/cmd/mcp-reddit@latest
```

## Credentials

The server needs Reddit API credentials (a client id and secret) for application-only OAuth access.

Since November 2025 Reddit no longer issues API keys self-service at reddit.com/prefs/apps. To get credentials:

1. Read Reddit's [Responsible Builder Policy](https://support.reddithelp.com/hc/en-us/articles/42728983564564-Responsible-Builder-Policy).
2. Submit a request through [Reddit Developer Support](https://support.reddithelp.com/hc/en-us/requests/new?ticket_form_id=14868593862164). Describe your use case, the data you need and your expected request volume, and link to a privacy policy.
3. Wait for manual review. It can take weeks, and requests can be rejected.

Unauthenticated access is not an option: Reddit has returned HTTP 403 to unauthenticated API requests since May 2026.

## Configuration

| Variable | Required | Description |
|---|---|---|
| `REDDIT_CLIENT_ID` | yes | OAuth client id |
| `REDDIT_CLIENT_SECRET` | yes | OAuth client secret |
| `REDDIT_USER_AGENT` | no | Custom User-Agent; defaults to `mcp-reddit/<version> (+https://github.com/blunext/mcp-reddit)` |

### Claude Code

Plain environment variables (the secret is stored in plain text in your Claude Code config):

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID=your-id -e REDDIT_CLIENT_SECRET=your-secret -- mcp-reddit
```

Recommended: keep the secret in a password manager and fetch it when the server starts.

gopass:

```sh
claude mcp add reddit -- sh -c 'REDDIT_CLIENT_ID="$(gopass show -o reddit/client-id)" REDDIT_CLIENT_SECRET="$(gopass show -o reddit/client-secret)" exec mcp-reddit'
```

The MCP client starts the server without a terminal, so gopass cannot prompt for your GPG passphrase. Make sure `gpg-agent` already has the key unlocked, or configure a graphical pinentry.

1Password CLI:

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID="op://Private/Reddit API/username" -e REDDIT_CLIENT_SECRET="op://Private/Reddit API/credential" -- op run -- mcp-reddit
```

macOS Keychain (store the secret first with `security add-generic-password -s mcp-reddit -a client-secret -w`):

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID=your-id -- sh -c 'REDDIT_CLIENT_SECRET="$(security find-generic-password -s mcp-reddit -a client-secret -w)" exec mcp-reddit'
```

### Claude Desktop

In `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "reddit": {
      "command": "mcp-reddit",
      "env": {
        "REDDIT_CLIENT_ID": "your-id",
        "REDDIT_CLIENT_SECRET": "your-secret"
      }
    }
  }
}
```

The wrapper commands above also work here: set `"command": "sh"` and `"args": ["-c", "…"]`.

## Development

```sh
go test ./...                      # unit tests; live tests skip without credentials
go test ./internal/tools -update   # regenerate golden files after intended output changes
golangci-lint run
```

Live tests run against the real API when `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` are set. Set `REDDIT_TEST_SHARE_URL` to also test share-link resolution.

## License

[MIT](LICENSE)
````

Also create `LICENSE` with the standard MIT License text, `Copyright (c) 2026 Blunext`.

- [ ] **Step 5: Run the full verification**

Run: `gofmt -l . ; go vet ./... && go test -race ./... && (golangci-lint run ./... || go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...)`
Expected: `gofmt -l` prints nothing, the tests pass (live tests skipped) and the linter reports 0 issues.

- [ ] **Step 6: Commit**

```bash
git add README.md .golangci.yml .github LICENSE
git commit -m "docs: add README, lint config and CI"
```
