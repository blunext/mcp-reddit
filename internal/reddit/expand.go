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
