package ui

import "altzap/client"

// pinnedFirst returns chats with the pinned ones first, preserving the
// relative (recency) order inside each partition — mirrors how WhatsApp
// anchors pinned chats at the top of the inbox.
func pinnedFirst(chats []client.Chat) []client.Chat {
	if len(chats) == 0 {
		return chats
	}
	out := make([]client.Chat, 0, len(chats))
	for _, c := range chats {
		if c.Pinned {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return chats
	}
	for _, c := range chats {
		if !c.Pinned {
			out = append(out, c)
		}
	}
	return out
}
