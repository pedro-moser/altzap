package ui

import (
	"net/url"

	"altzap/client"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// buildMessageText returns a renderable widget for a message body, with
// URLs auto-detected and rendered as Fyne hyperlinks (default Hyperlink
// behavior opens in the system browser).
//
// Plain text (no URLs) returns a regular widget.Label — RichText is more
// expensive to layout, and 95% of bubbles in a chat are plain text. The
// caller passes wrap=true when the natural width exceeded the bubble
// max; we honor that on whichever widget we return.
func buildMessageText(text string, wrap bool) fyne.CanvasObject {
	if client.ExtractURLs(text) == nil {
		lbl := widget.NewLabel(text)
		if wrap {
			lbl.Wrapping = fyne.TextWrapWord
		}
		return lbl
	}
	rt := widget.NewRichText(buildTextWithLinks(text)...)
	if wrap {
		rt.Wrapping = fyne.TextWrapWord
	}
	return rt
}

// buildTextWithLinks splits text into RichText segments, materializing any
// URLs found by client.ExtractURLs as HyperlinkSegments and the rest as
// TextSegments. Returned slice is suitable for widget.NewRichText / direct
// assignment to RichText.Segments.
//
// Empty input returns nil. URL-free input returns a single TextSegment.
//
// We keep the surrounding whitespace inside the TextSegments so the
// rendered line preserves spacing exactly as the user typed it; the
// HyperlinkSegment carries only the URL itself.
func buildTextWithLinks(text string) []widget.RichTextSegment {
	if text == "" {
		return nil
	}
	matches := client.ExtractURLs(text)
	if len(matches) == 0 {
		return []widget.RichTextSegment{&widget.TextSegment{Text: text}}
	}

	out := make([]widget.RichTextSegment, 0, 2*len(matches)+1)
	cursor := 0
	for _, m := range matches {
		if m.Start > cursor {
			out = append(out, &widget.TextSegment{Text: text[cursor:m.Start]})
		}
		// Parse defensively — a malformed URL falls back to plain text so
		// we never crash on weird inputs the regex was permissive about.
		u, err := url.Parse(m.URL)
		if err != nil {
			out = append(out, &widget.TextSegment{Text: m.URL})
		} else {
			out = append(out, &widget.HyperlinkSegment{Text: m.URL, URL: u})
		}
		cursor = m.End
	}
	if cursor < len(text) {
		out = append(out, &widget.TextSegment{Text: text[cursor:]})
	}
	return out
}
