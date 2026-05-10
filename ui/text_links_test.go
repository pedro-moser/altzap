package ui

import (
	"testing"

	"fyne.io/fyne/v2/widget"
)

func TestBuildTextWithLinks_NoURLReturnsSingleTextSegment(t *testing.T) {
	got := buildTextWithLinks("just plain text")
	if len(got) != 1 {
		t.Fatalf("want 1 segment, got %d: %#v", len(got), got)
	}
	ts, ok := got[0].(*widget.TextSegment)
	if !ok {
		t.Fatalf("want *TextSegment, got %T", got[0])
	}
	if ts.Text != "just plain text" {
		t.Fatalf("text wrong: %q", ts.Text)
	}
}

func TestBuildTextWithLinks_URLBecomesHyperlinkSegment(t *testing.T) {
	got := buildTextWithLinks("see https://example.com today")
	if len(got) != 3 {
		t.Fatalf("want 3 segments (text/link/text), got %d: %#v", len(got), got)
	}
	if pre, ok := got[0].(*widget.TextSegment); !ok || pre.Text != "see " {
		t.Fatalf("first segment wrong: %T %v", got[0], got[0])
	}
	link, ok := got[1].(*widget.HyperlinkSegment)
	if !ok {
		t.Fatalf("middle segment must be HyperlinkSegment, got %T", got[1])
	}
	if link.Text != "https://example.com" {
		t.Fatalf("link text wrong: %q", link.Text)
	}
	if link.URL == nil || link.URL.String() != "https://example.com" {
		t.Fatalf("link URL wrong: %v", link.URL)
	}
	if post, ok := got[2].(*widget.TextSegment); !ok || post.Text != " today" {
		t.Fatalf("trailing segment wrong: %T %v", got[2], got[2])
	}
}

func TestBuildTextWithLinks_AdjacentURLs(t *testing.T) {
	got := buildTextWithLinks("https://a.io https://b.io")
	// link, " ", link
	if len(got) != 3 {
		t.Fatalf("want 3 segments, got %d", len(got))
	}
	if _, ok := got[0].(*widget.HyperlinkSegment); !ok {
		t.Fatalf("first should be link, got %T", got[0])
	}
	if _, ok := got[2].(*widget.HyperlinkSegment); !ok {
		t.Fatalf("third should be link, got %T", got[2])
	}
}

func TestBuildTextWithLinks_EmptyReturnsEmpty(t *testing.T) {
	got := buildTextWithLinks("")
	if len(got) != 0 {
		t.Fatalf("empty input should yield no segments, got %#v", got)
	}
}
