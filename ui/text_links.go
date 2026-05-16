package ui

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"altzap/client"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// emojiOnlyCount returns the number of emoji codepoints if the string
// contains ONLY emoji (and optional whitespace/variation selectors).
// Returns 0 if the string contains any non-emoji text. Used to detect
// messages like "👍" or "😂❤️" that should render jumbo.
func emojiOnlyCount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	count := 0
	for len(s) > 0 {
		r, sz := utf8.DecodeRuneInString(s)
		s = s[sz:]
		if r == utf8.RuneError {
			return 0
		}
		// Skip variation selectors (VS15/VS16) and zero-width joiners.
		if r == 0xFE0E || r == 0xFE0F || r == 0x200D {
			continue
		}
		// Skip skin-tone modifiers (U+1F3FB..U+1F3FF).
		if r >= 0x1F3FB && r <= 0x1F3FF {
			continue
		}
		// Regional indicator symbols (flags).
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			count++
			continue
		}
		// Combining enclosing keycap.
		if r == 0x20E3 {
			continue
		}
		if unicode.IsSpace(r) {
			continue
		}
		if !isEmojiRune(r) {
			return 0
		}
		count++
	}
	return count
}

func isEmojiRune(r rune) bool {
	return unicode.Is(unicode.So, r) ||
		(r >= 0x1F600 && r <= 0x1F64F) || // emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // misc symbols & pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // transport & map
		(r >= 0x1F900 && r <= 0x1F9FF) || // supplemental symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // chess symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) || // symbols extended-A
		(r >= 0x2600 && r <= 0x26FF) || // misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // dingbats
		(r >= 0x231A && r <= 0x23F3) || // misc technical
		(r >= 0x2934 && r <= 0x2935) || // arrows
		(r >= 0x25AA && r <= 0x25FE) || // geometric shapes
		r == 0x200D || r == 0xFE0F || r == 0xFE0E ||
		r == 0x2764 || r == 0x2763 || // hearts
		r == 0x270A || r == 0x270B || r == 0x270C || r == 0x270D || // hands
		r == 0x2122 || r == 0x00A9 || r == 0x00AE // TM, ©, ®
}

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
