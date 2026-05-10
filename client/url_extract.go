package client

import "regexp"

// URLMatch describes a URL spotted inside a piece of text. Start/End are byte
// offsets into the original string; `text[Start:End] == URL` always holds, so
// callers can splice the original around the match without re-scanning.
type URLMatch struct {
	Start int
	End   int
	URL   string
}

// urlPattern matches http(s) URLs. The character class is intentionally
// permissive (anything that isn't whitespace or a paren/bracket); a second
// pass strips trailing punctuation we don't want to consider part of the
// link (the .,;:!?)] family that frequently abuts a URL in chat).
var urlPattern = regexp.MustCompile(`https?://[^\s()<>\[\]]+`)

// ExtractURLs returns every URL match in text, in order of appearance.
// Empty input or text without URLs returns a nil slice (callers are
// expected to range over the result; nil ranges fine).
//
// Trailing punctuation (.,;:!? and the `)` that often closes a parenthetical
// reference) is stripped from each match — bookended characters that are
// almost never part of the actual URL but are technically legal characters
// in URI paths/queries. Internal occurrences of those characters survive
// (e.g. query strings and fragments stay intact).
func ExtractURLs(text string) []URLMatch {
	if text == "" {
		return nil
	}
	raw := urlPattern.FindAllStringIndex(text, -1)
	if len(raw) == 0 {
		return nil
	}
	out := make([]URLMatch, 0, len(raw))
	for _, idx := range raw {
		start, end := idx[0], idx[1]
		// Strip trailing punctuation chars one at a time. Conservative:
		// only the chars users typically type around URLs.
		for end > start {
			c := text[end-1]
			if c == '.' || c == ',' || c == ';' || c == ':' ||
				c == '!' || c == '?' || c == ')' {
				end--
				continue
			}
			break
		}
		if end <= start {
			continue
		}
		out = append(out, URLMatch{
			Start: start,
			End:   end,
			URL:   text[start:end],
		})
	}
	return out
}
