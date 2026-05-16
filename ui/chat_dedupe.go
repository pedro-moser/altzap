package ui

import (
	"altzap/client"

	"go.mau.fi/whatsmeow/types"
)

// dedupeLIDPNTwins collapses LID/PN twins in a chat list into a single
// entry per underlying contact. WhatsApp routes a contact's messages
// either via @lid (privacy hash) or @<phone>@s.whatsapp.net depending on
// the contact's settings, so a single person may show up twice. We pick
// whichever twin has the more recent LastMessageTime — that's the chat
// the user is actually engaged in.
//
// resolveLIDPhone returns the phone-number string for a LID JID (typically
// client.PhoneForJID) or "" when no mapping is known yet. LIDs without
// a mapping pass through untouched (we cannot detect their twin).
//
// Order of remaining chats matches the input order.
func dedupeLIDPNTwins(chats []client.Chat, resolveLIDPhone func(types.JID) string) ([]client.Chat, map[string][]string) {
	canonical := func(c client.Chat) string {
		switch c.JID.Server {
		case types.DefaultUserServer:
			return c.JID.User
		case types.HiddenUserServer:
			return resolveLIDPhone(c.JID)
		}
		return ""
	}

	// Group JIDs by canonical phone number.
	phoneJIDs := make(map[string][]string) // phone → all JID strings
	for _, c := range chats {
		phone := canonical(c)
		if phone != "" {
			phoneJIDs[phone] = append(phoneJIDs[phone], c.JID.String())
		}
	}

	// First pass: index by canonical phone, picking the index whose chat
	// has the most recent LastMessageTime among twins.
	winner := make(map[string]int) // phone -> winning index in chats
	for i, c := range chats {
		phone := canonical(c)
		if phone == "" {
			continue
		}
		if best, ok := winner[phone]; !ok || chats[i].LastMessageTime > chats[best].LastMessageTime {
			winner[phone] = i
		}
	}

	// Build sibling map: for each JID that has a twin, list the OTHER
	// JIDs. Both winner and loser get entries so message loading can
	// merge regardless of which JID is opened.
	siblings := make(map[string][]string)
	for _, jids := range phoneJIDs {
		if len(jids) < 2 {
			continue
		}
		for _, jid := range jids {
			others := make([]string, 0, len(jids)-1)
			for _, other := range jids {
				if other != jid {
					others = append(others, other)
				}
			}
			siblings[jid] = others
		}
	}

	// Second pass: keep entries that are either non-deduplicable
	// (groups, LIDs without mapping, etc.) or the winning index for
	// their canonical phone.
	out := make([]client.Chat, 0, len(chats))
	for i, c := range chats {
		phone := canonical(c)
		if phone == "" {
			out = append(out, c)
			continue
		}
		if winner[phone] == i {
			out = append(out, c)
		}
	}
	return out, siblings
}
