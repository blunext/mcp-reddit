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

// jsonOpts relaxes json/v2's strict defaults so that one malformed string
// (e.g. a lone surrogate in a comment) or a duplicate key cannot make a whole
// response unreadable, as encoding/json v1 would have tolerated.
var jsonOpts = json.JoinOptions(jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true))

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
	if err := json.Unmarshal(v, &l, jsonOpts); err != nil {
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
	if err := json.Unmarshal(t.Data, &d, jsonOpts); err != nil {
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
			if err := json.Unmarshal(t.Data, &d, jsonOpts); err != nil {
				return nil, nil, fmt.Errorf("decoding comment: %w", err)
			}
			c, err := toComment(d)
			if err != nil {
				return nil, nil, err
			}
			comments = append(comments, c)
		case "more":
			var d moreData
			if err := json.Unmarshal(t.Data, &d, jsonOpts); err != nil {
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
