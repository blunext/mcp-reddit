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
	maxShownTokens = 3 // batches of up to 100 ids each; huge threads list thousands of ids

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
	tokens := strings.Join(m.Tokens[:min(len(m.Tokens), maxShownTokens)], " ")
	if hidden := len(m.Tokens) - maxShownTokens; hidden > 0 {
		tokens += fmt.Sprintf(" … (%d more batches not shown; for other comments try another comment_sort)", hidden)
	}
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
