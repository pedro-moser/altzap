package ui

import (
	"fmt"
	"testing"
	"time"

	"altzap/client"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// Behavioural cover for the tail-follow policy, driven through a real
// rendered widget.List — the probe reads back what the list actually put
// on screen, so nothing short of a live list proves it works.
//
// No WhatsApp client is needed: the render path only reaches for one when
// there are read receipts queued, and these views never queue any.

func newFollowTestView(t *testing.T, n int) (*ChatView, fyne.Window) {
	t.Helper()
	test.NewApp()

	cv := &ChatView{
		currentChatJID: "history@g.us",
		followTail:     true,
		messages:       make(map[string][]*Message),
		pendingReads:   make(map[string][]client.MarkTarget),
		bubbleHeights:  make(map[string]float32),
	}
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	msgs := make([]*Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, &Message{
			ID:        fmt.Sprintf("old%d", i),
			Sender:    "Alice",
			Text:      fmt.Sprintf("history line %d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	cv.messages[cv.currentChatJID] = msgs

	content := container.NewStack(cv.buildMessageArea(), cv.buildNewMessagesPill())
	w := test.NewWindow(content)
	w.Resize(fyne.NewSize(420, 320))

	// Land on the newest message, the way opening a chat does.
	cv.scrollToLatest()
	if cv.messageList.Size().Height <= 0 {
		t.Fatal("message list never got laid out; the policy would short-circuit")
	}
	return cv, w
}

// arrive appends an incoming message and renders it, mirroring what
// AddMessage does on the UI thread.
func arrive(cv *ChatView, text string) {
	msgs := cv.messages[cv.currentChatJID]
	msg := &Message{
		ID:     fmt.Sprintf("new%d", len(msgs)),
		Sender: "Alice",
		Text:   text,
		Timestamp: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC).
			Add(time.Duration(len(msgs)) * time.Second),
	}
	cv.messages[cv.currentChatJID] = append(msgs, msg)
	cv.appendMessageBubble(msg)
}

func TestArrivalFollowsWhileParkedAtTheNewest(t *testing.T) {
	cv, w := newFollowTestView(t, 60)
	defer w.Close()

	before := cv.messageList.GetScrollOffset()
	arrive(cv, "landed while watching")

	if got := cv.messageList.GetScrollOffset(); got <= before {
		t.Fatalf("list should have followed the new message: offset %v -> %v", before, got)
	}
	if !cv.followTail {
		t.Fatal("followTail should stay set while parked at the newest message")
	}
	if cv.newMsgPillBox.Visible() {
		t.Fatal("pill must stay hidden while the newest message is on screen")
	}
}

func TestArrivalPinsTheViewWhileReadingHistory(t *testing.T) {
	cv, w := newFollowTestView(t, 60)
	defer w.Close()

	cv.messageList.ScrollToOffset(120) // user scrolls up into the backlog
	before := cv.messageList.GetScrollOffset()

	arrive(cv, "first interruption")
	if got := cv.messageList.GetScrollOffset(); got != before {
		t.Fatalf("reading position moved: offset %v -> %v", before, got)
	}
	if cv.followTail {
		t.Fatal("followTail should have dropped once the user scrolled away")
	}
	if !cv.newMsgPillBox.Visible() {
		t.Fatal("pill should announce the message the user hasn't reached")
	}
	if got, want := cv.newMsgPill.Text, "↓ 1 new message"; got != want {
		t.Fatalf("pill text = %q, want %q", got, want)
	}

	arrive(cv, "second interruption")
	if got := cv.messageList.GetScrollOffset(); got != before {
		t.Fatalf("reading position moved on the second arrival: %v -> %v", before, got)
	}
	if got, want := cv.newMsgPill.Text, "↓ 2 new messages"; got != want {
		t.Fatalf("pill text = %q, want %q", got, want)
	}
}

func TestOwnMessageFollowsEvenFromHistory(t *testing.T) {
	cv, w := newFollowTestView(t, 60)
	defer w.Close()

	cv.messageList.ScrollToOffset(120)
	arrive(cv, "someone else wrote")
	if !cv.newMsgPillBox.Visible() {
		t.Fatal("precondition: view should be pinned with the pill up")
	}

	msgs := cv.messages[cv.currentChatJID]
	own := &Message{ID: "mine", Sender: "You", Text: "sending anyway", IsOwn: true,
		Timestamp: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)}
	cv.messages[cv.currentChatJID] = append(msgs, own)
	cv.appendMessageBubble(own)

	if !cv.followTail {
		t.Fatal("sending a message should snap back to the newest content")
	}
	if cv.newMsgPillBox.Visible() {
		t.Fatal("pill should clear once the user is back at the newest message")
	}
}

func TestScrollingBackToTheNewestClearsThePill(t *testing.T) {
	cv, w := newFollowTestView(t, 60)
	defer w.Close()

	cv.messageList.ScrollToOffset(120)
	arrive(cv, "missed this one")
	if !cv.newMsgPillBox.Visible() {
		t.Fatal("precondition: pill should be up")
	}

	cv.messageList.ScrollToBottom() // user scrolls back down on their own

	if cv.newMsgPillBox.Visible() {
		t.Fatal("pill should clear as soon as the newest row is on screen")
	}
	if !cv.followTail {
		t.Fatal("reaching the newest row should resume following")
	}
	if cv.pendingNew != 0 {
		t.Fatalf("pendingNew = %d, want 0 once the user caught up", cv.pendingNew)
	}

	before := cv.messageList.GetScrollOffset()
	arrive(cv, "and now follow again")
	if got := cv.messageList.GetScrollOffset(); got <= before {
		t.Fatalf("following should have resumed: offset %v -> %v", before, got)
	}
}

// The render window is a tail slice of the message slice, so a pinned view
// has to widen it per arrival or the content under the user's eyes slides
// up one bubble at a time.
func TestPinnedArrivalsKeepTheRenderWindowStill(t *testing.T) {
	cv, w := newFollowTestView(t, initialRenderLimit+40)
	defer w.Close()

	cv.messageList.ScrollToOffset(120)
	want := cv.messageWindowStart(cv.messages[cv.currentChatJID])

	for i := 0; i < 6; i++ {
		arrive(cv, fmt.Sprintf("burst %d", i))
		got := cv.messageWindowStart(cv.messages[cv.currentChatJID])
		if got != want {
			t.Fatalf("after %d arrivals window start = %d, want %d", i+1, got, want)
		}
	}
	if cv.pendingNew != 6 {
		t.Fatalf("pendingNew = %d, want 6", cv.pendingNew)
	}
}

// Following must not let the window creep: the row borrowed to anchor a
// pinned view is handed straight back when we follow instead.
func TestFollowingLeavesTheRenderWindowBounded(t *testing.T) {
	cv, w := newFollowTestView(t, initialRenderLimit+40)
	defer w.Close()

	limit := cv.renderLimit
	for i := 0; i < 6; i++ {
		arrive(cv, fmt.Sprintf("live %d", i))
	}
	if cv.renderLimit != limit {
		t.Fatalf("renderLimit grew from %d to %d while following", limit, cv.renderLimit)
	}
}
