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
