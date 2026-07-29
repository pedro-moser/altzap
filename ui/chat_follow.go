package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Tail-follow policy for the message list.
//
// The list should chase the newest message while the user sits at the
// bottom of a conversation, and must not move a pixel while they read
// history further up. Fyne's widget.List exposes no OnScrolled hook (the
// scroller that owns it is unexported), so the question "is the newest
// message on screen right now?" is answered by the list itself: a Refresh
// runs UpdateItem over every visible row, so messageListUpdate can flag
// the sighting as the rows go by. That readout is synchronous and can't go
// stale, which a remembered scroll offset can.
//
// The offset is still worth keeping as a fast path: while we're following
// and the viewport hasn't moved since we put it there, the answer is yes
// by construction and the list doesn't need to be asked.
//
// All state here is touched from the UI thread alone — appendMessageBubble
// and refreshMessages run inside fyne.Do, messageListUpdate is called by
// the list's own layout pass — so it needs no locking. Keep it that way:
// the read-receipt flush was deliberately moved off AddMessage's whatsmeow
// goroutine and onto the render path for exactly this reason.

// tailSlackPx is how far the viewport may drift from the offset we last
// set and still count as untouched — about one wheel notch, so a nudge
// doesn't cost a refresh probe.
const tailSlackPx = 40

// parkedAtLiveEdge is the cheap half of the decision: following, and the
// viewport is where we left it, means the newest message is still on
// screen. A false answer is not a verdict — it only means the list has to
// be asked.
func parkedAtLiveEdge(following bool, curOffset, lastSet float32) bool {
	if !following {
		return false
	}
	drift := curOffset - lastSet
	if drift < 0 {
		drift = -drift
	}
	return drift <= tailSlackPx
}

// anchoredRenderLimit widens the render window by one row so a freshly
// appended message doesn't push the oldest visible one out of it.
// messageWindowStart is len(msgs)-renderLimit, so without this every
// arrival slides the whole list up by one bubble and the user's reading
// position drifts even though nothing scrolled. total counts messages
// including the new arrival; the limit only grows once the window is
// actually full.
func anchoredRenderLimit(limit, total int) int {
	if limit <= 0 {
		limit = initialRenderLimit
	}
	if total > limit {
		return limit + 1
	}
	return limit
}

// newMessagesPillLabel captions the floating pill.
func newMessagesPillLabel(n int) string {
	if n == 1 {
		return "↓ 1 new message"
	}
	return fmt.Sprintf("↓ %d new messages", n)
}

// buildNewMessagesPill creates the floating affordance that overlays the
// bottom of the message area while the view is pinned. The layer spans the
// whole area but only materializes the button strip at the bottom, and
// Fyne skips hidden objects when hit-testing, so it never steals clicks
// from the bubbles underneath.
func (cv *ChatView) buildNewMessagesPill() fyne.CanvasObject {
	cv.newMsgPill = widget.NewButton("", cv.scrollToLatest)
	cv.newMsgPill.Importance = widget.HighImportance
	cv.newMsgPillBox = container.NewBorder(
		nil,
		container.NewPadded(container.NewCenter(cv.newMsgPill)),
		nil, nil,
	)
	cv.newMsgPillBox.Hide()
	return cv.newMsgPillBox
}

// syncNewMessagesPill matches the pill to cv.pendingNew. Idempotent: the
// render paths call it unconditionally.
func (cv *ChatView) syncNewMessagesPill() {
	if cv.newMsgPill == nil || cv.newMsgPillBox == nil {
		return
	}
	if cv.pendingNew <= 0 {
		if cv.newMsgPillBox.Visible() {
			cv.newMsgPillBox.Hide()
		}
		return
	}
	cv.newMsgPill.SetText(newMessagesPillLabel(cv.pendingNew))
	if !cv.newMsgPillBox.Visible() {
		cv.newMsgPillBox.Show()
	}
}

// renderMessagesRespectingScroll redraws the message list and reports
// whether it followed the newest message (true) or pinned the viewport
// where the user left it (false).
//
// anchorRow widens the render window before drawing a pinned view — an
// appended message needs the extra row so the window's oldest message
// stays put. The widening is undone when we follow instead, otherwise a
// busy chat would grow its window by a row per message.
func (cv *ChatView) renderMessagesRespectingScroll(anchorRow bool) bool {
	// Nothing laid out yet (first paint): there is no reading position to
	// protect, and an unsized list renders no rows for the probe to see.
	if cv.messageList.Size().Height <= 0 ||
		parkedAtLiveEdge(cv.followTail, cv.messageList.GetScrollOffset(), cv.lastSetOffset) {
		cv.renderFollowingTail()
		return true
	}

	saved := cv.renderLimit
	if anchorRow {
		cv.muMessages.RLock()
		total := len(cv.messages[cv.currentChatJID])
		cv.muMessages.RUnlock()
		cv.renderLimit = anchoredRenderLimit(saved, total)
	}
	pre := cv.messageList.GetScrollOffset()

	// The refresh doubles as the probe — see the note at the top of the
	// file. It is not wasted work: a pinned render needs it anyway. After
	// an append the list already carries the new message, so the row that
	// answers "was the user at the bottom?" is the one before the tail.
	cv.liveEdgeSlack = 0
	if anchorRow {
		cv.liveEdgeSlack = 1
	}
	cv.liveEdgeSeen = false
	cv.messageList.Refresh()
	cv.liveEdgeSlack = 0
	if cv.liveEdgeSeen {
		cv.renderLimit = saved
		cv.renderFollowingTail()
		return true
	}

	cv.followTail = false
	cv.messageList.ScrollToOffset(pre)
	cv.lastSetOffset = cv.messageList.GetScrollOffset()
	cv.syncNewMessagesPill()
	return false
}

// renderFollowingTail redraws and parks the view on the newest message.
// Everything queued for a read receipt is genuinely on screen at that
// point, so this is where the receipts get paid.
func (cv *ChatView) renderFollowingTail() {
	cv.messageList.Refresh()
	cv.messageList.ScrollToBottom()
	cv.lastSetOffset = cv.messageList.GetScrollOffset()
	cv.followTail = true
	cv.pendingNew = 0
	cv.syncNewMessagesPill()
	cv.flushPendingReads(cv.currentChat())
}

// noteTailRowVisible is called from messageListUpdate when the newest row
// materializes. During a probe that just confirms what the probe is about
// to conclude; outside one it means the user scrolled back down to the
// live edge, and the pill should clear immediately rather than wait for
// the next arrival. The early return keeps the steady state — that row
// re-rendering on every refresh while following — free of side effects.
func (cv *ChatView) noteTailRowVisible() {
	if cv.followTail && cv.pendingNew == 0 {
		return
	}
	cv.followTail = true
	cv.pendingNew = 0
	cv.syncNewMessagesPill()
	cv.flushPendingReads(cv.currentChat())
}
