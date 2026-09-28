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
