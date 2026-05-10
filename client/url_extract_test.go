package client

import (
	"reflect"
	"testing"
)

func TestExtractURLs_Empty(t *testing.T) {
	if got := ExtractURLs(""); len(got) != 0 {
		t.Fatalf("empty input should yield no matches, got %v", got)
	}
}

func TestExtractURLs_NoURL(t *testing.T) {
	if got := ExtractURLs("hello, no link here"); len(got) != 0 {
		t.Fatalf("plain text should yield no matches, got %v", got)
	}
}

func TestExtractURLs_SingleHTTPS(t *testing.T) {
	got := ExtractURLs("check https://example.com out")
	want := []URLMatch{{Start: 6, End: 25, URL: "https://example.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func TestExtractURLs_HTTPAlsoMatched(t *testing.T) {
	got := ExtractURLs("legacy http://example.org here")
	if len(got) != 1 || got[0].URL != "http://example.org" {
		t.Fatalf("want one http match, got %#v", got)
	}
}

func TestExtractURLs_Multiple(t *testing.T) {
	got := ExtractURLs("first https://a.io and second https://b.io done")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d: %#v", len(got), got)
	}
	if got[0].URL != "https://a.io" || got[1].URL != "https://b.io" {
		t.Fatalf("URLs wrong: %#v", got)
	}
}

func TestExtractURLs_TrimsTrailingPunctuation(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"see https://example.com.", "https://example.com"},
		{"see https://example.com,", "https://example.com"},
		{"see https://example.com)", "https://example.com"},
		{"(see https://example.com)", "https://example.com"},
		{"see https://example.com!", "https://example.com"},
		{"see https://example.com?", "https://example.com"},
		{"end with https://example.com:", "https://example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := ExtractURLs(tc.in)
			if len(got) != 1 || got[0].URL != tc.want {
				t.Fatalf("want %q, got %#v", tc.want, got)
			}
		})
	}
}

func TestExtractURLs_KeepsInternalPunctuation(t *testing.T) {
	// Query strings, fragments, paths with parens — internal chars must
	// stay in the URL even though the same chars are stripped at the tail.
	got := ExtractURLs("see https://example.com/path?q=1&r=2#frag here")
	if len(got) != 1 || got[0].URL != "https://example.com/path?q=1&r=2#frag" {
		t.Fatalf("query/fragment lost: %#v", got)
	}
}

func TestExtractURLs_OffsetsPointToOriginalText(t *testing.T) {
	in := "prefix https://example.com tail"
	got := ExtractURLs(in)
	if len(got) != 1 {
		t.Fatalf("want 1 match: %#v", got)
	}
	if in[got[0].Start:got[0].End] != got[0].URL {
		t.Fatalf("offsets do not slice back to URL: in[%d:%d]=%q, URL=%q",
			got[0].Start, got[0].End, in[got[0].Start:got[0].End], got[0].URL)
	}
}

func TestExtractURLs_AtStringStart(t *testing.T) {
	got := ExtractURLs("https://example.com is the link")
	if len(got) != 1 || got[0].Start != 0 || got[0].URL != "https://example.com" {
		t.Fatalf("want match at start, got %#v", got)
	}
}

func TestExtractURLs_AtStringEnd(t *testing.T) {
	got := ExtractURLs("the link is https://example.com")
	if len(got) != 1 || got[0].URL != "https://example.com" {
		t.Fatalf("want match at end, got %#v", got)
	}
	if got[0].End != len("the link is https://example.com") {
		t.Fatalf("End offset wrong: got %d", got[0].End)
	}
}
