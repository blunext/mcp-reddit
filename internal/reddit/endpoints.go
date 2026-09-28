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
	q := url.Values{"q": {p.Query}, "type": {"link"}, "include_over_18": {"on"}}
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
	q := url.Values{"q": {query}, "include_over_18": {"on"}}
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
		if err := json.Unmarshal(t.Data, &d, jsonOpts); err != nil {
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
