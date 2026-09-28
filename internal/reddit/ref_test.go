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
