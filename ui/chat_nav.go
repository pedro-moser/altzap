package ui

import "altzap/client"

// nextChatJID returns the JID string of the chat that follows currentJID
// in the sidebar list. Empty list → "". Current not found (or empty)
// → first chat's JID. Last chat → "" (no wrap; spec calls for clamp).
func nextChatJID(chats []client.Chat, currentJID string) string {
	if len(chats) == 0 {
		return ""
	}
	for i, c := range chats {
		if c.JID.String() == currentJID {
			if i+1 >= len(chats) {
				return ""
			}
			return chats[i+1].JID.String()
		}
	}
	return chats[0].JID.String()
}

// prevChatJID is the reverse of nextChatJID — first chat clamps to "",
// unknown current falls back to first.
func prevChatJID(chats []client.Chat, currentJID string) string {
	if len(chats) == 0 {
		return ""
	}
	for i, c := range chats {
		if c.JID.String() == currentJID {
			if i == 0 {
				return ""
			}
			return chats[i-1].JID.String()
		}
	}
	return chats[0].JID.String()
}

// latestNonDeletedMessageID returns the ID of the most recent non-
// deleted message. msgs must be in chronological order (oldest first).
// Empty / all-deleted returns "".
func latestNonDeletedMessageID(msgs []*Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].Deleted {
			return msgs[i].ID
		}
	}
	return ""
}

// nextNonDeletedMessageID steps forward from currentID, skipping
// deleted messages. Clamps at the end (returns currentID). When
// currentID isn't in msgs, returns currentID unchanged so callers
// don't accidentally jump.
func nextNonDeletedMessageID(msgs []*Message, currentID string) string {
	idx := indexOfMsg(msgs, currentID)
	if idx < 0 {
		return currentID
	}
	for i := idx + 1; i < len(msgs); i++ {
		if !msgs[i].Deleted {
			return msgs[i].ID
		}
	}
	return currentID
}

// prevNonDeletedMessageID is the symmetric backward step.
func prevNonDeletedMessageID(msgs []*Message, currentID string) string {
	idx := indexOfMsg(msgs, currentID)
	if idx < 0 {
		return currentID
	}
	for i := idx - 1; i >= 0; i-- {
		if !msgs[i].Deleted {
			return msgs[i].ID
		}
	}
	return currentID
}

func indexOfMsg(msgs []*Message, id string) int {
	for i, m := range msgs {
		if m.ID == id {
			return i
		}
	}
	return -1
}
