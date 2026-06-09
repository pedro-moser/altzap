package ui

import (
	"log"

	"altzap/client"
)

// Read-receipt plumbing. pendingReads accumulates incoming messages that
// still owe a read receipt, keyed by the REAL chat JID each message arrived
// on — LID/PN twins keep separate buckets because the receipt must be
// addressed to the JID that delivered the message. Session-scoped on
// purpose: history that was already unread before this run is not
// retroactively marked (matches the plan's B1 v1 semantics).

// queuePendingRead records an incoming message that needs a read receipt.
func (cv *ChatView) queuePendingRead(chatJID string, t client.MarkTarget) {
	if chatJID == "" || t.ID == "" {
		return
	}
	cv.muPendingReads.Lock()
	cv.pendingReads[chatJID] = append(cv.pendingReads[chatJID], t)
	cv.muPendingReads.Unlock()
}

// flushPendingReads sends read receipts for chatJID and its LID/PN twins.
// No-op while the window is hidden — OnWindowShown flushes once the user
// can actually see the messages. Network IO runs off the calling thread.
func (cv *ChatView) flushPendingReads(chatJID string) {
	if cv == nil || chatJID == "" || !cv.isWindowVisible() {
		return
	}
	for _, jid := range append([]string{chatJID}, cv.siblingsOf(chatJID)...) {
		cv.muPendingReads.Lock()
		targets := cv.pendingReads[jid]
		delete(cv.pendingReads, jid)
		cv.muPendingReads.Unlock()
		if len(targets) == 0 {
			continue
		}
		jid := jid
		go func() {
			if err := cv.waClient.MarkRead(jid, targets); err != nil {
				log.Printf("mark read %s (%d msgs): %v", jid, len(targets), err)
			}
		}()
	}
}

// dropPendingReads discards queued receipts for chatJID without sending —
// used when the phone already read the chat (its receipts supersede ours).
func (cv *ChatView) dropPendingReads(chatJID string) {
	cv.muPendingReads.Lock()
	delete(cv.pendingReads, chatJID)
	cv.muPendingReads.Unlock()
}
