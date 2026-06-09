package ui

import "strings"

// extractPhoneDigits returns the digit string of q when q plausibly is a
// phone number typed into a search box: only digits after stripping the
// usual separators ("+ - ( ) . space"), and at least 8 of them (country
// code + local number). Empty string otherwise.
func extractPhoneDigits(q string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(q) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' || r == '-' || r == '(' || r == ')' || r == '.' || r == ' ':
			// separators: skipped, not disqualifying
		default:
			return ""
		}
	}
	if b.Len() < 8 {
		return ""
	}
	return b.String()
}
