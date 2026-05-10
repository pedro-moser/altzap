# Keyboard navigation v1 implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add keyboard shortcuts (Ctrl+J/K/L/F/R) to AltZap so Pedro can switch chats, focus the composer, search incrementally, and reply quickly without touching the mouse.

**Architecture:** A new `ui/chat_view_keymap.go` registers `desktop.CustomShortcut` handlers on the window canvas. ChatView gains two state fields for the reply mode (`replyMode bool`, `replyTargetID string`) plus a small set of methods that the shortcut handlers delegate to. Pure-function helpers (sidebar/message navigation with clamping, deleted-skipping) live in `ui/chat_nav.go` so the routing logic is unit-testable without a Fyne app.

**Tech Stack:** Go 1.26, Fyne v2.7.3 (`desktop.CustomShortcut` for shortcut bindings, `fyne.Canvas.OnTypedRune` for type-to-confirm in reply mode), existing `ui/esc.go` ESC stack for dismissibles.

**Spec:** `docs/superpowers/specs/2026-05-10-keyboard-navigation-design.md`

---

## File Structure

| File | Status | Responsibility |
|------|--------|----------------|
| `ui/chat_nav.go` | NEW | Pure helpers: `nextChatJID`, `prevChatJID`, `latestNonDeletedMessageID`, `nextNonDeletedMessageID`, `prevNonDeletedMessageID`. Zero Fyne deps. |
| `ui/chat_nav_test.go` | NEW | Table-driven tests for every helper above. |
| `ui/chat_view.go` | MODIFY | Add state fields (`replyMode`, `replyTargetID`, `replyModeEscPop`, `replyPrevFocused`, `searchEscPop`); add methods (`selectNextChat`/`selectPrevChat`/`focusComposer`/`openSearch`/`closeSearch`/`confirmSearch`/`enterReplyMode`/`exitReplyMode`/`confirmReplyTarget`/`replyTargetNext`/`replyTargetPrev`/`scrollToReplyTarget`/`invalidateBubbleHeight`); modify `buildMessageBubble` to draw the reply-target border; wire `searchEntry.OnSubmitted`. |
| `ui/chat_view_keymap.go` | NEW | `(cv *ChatView) installShortcuts()` registers the five `Ctrl+`-prefixed shortcuts plus the canvas `OnTypedRune` handler used by reply mode's "any printable confirms" gesture. |

`main.go` is untouched — `Ctrl+Q` and the existing ESC handler stay.

---

## Task 1: Pure helpers for sidebar + message navigation

**Files:**
- Create: `ui/chat_nav.go`
- Create: `ui/chat_nav_test.go`

- [ ] **Step 1: Write the failing tests for sidebar nav helpers**

`ui/chat_nav_test.go`:

```go
package ui

import (
	"testing"

	"altzap/client"

	"go.mau.fi/whatsmeow/types"
)

func chatJID(s string) types.JID {
	jid, _ := types.ParseJID(s)
	return jid
}

func TestNextChatJID_EmptyList(t *testing.T) {
	if got := nextChatJID(nil, "anything"); got != "" {
		t.Fatalf("empty list should return \"\", got %q", got)
	}
}

func TestNextChatJID_NoCurrentOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, ""); got != "5511A@s.whatsapp.net" {
		t.Fatalf("no current should open first, got %q", got)
	}
}

func TestNextChatJID_CurrentNotFoundOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "stale@s.whatsapp.net"); got != "5511A@s.whatsapp.net" {
		t.Fatalf("unknown current should fall back to first, got %q", got)
	}
}

func TestNextChatJID_StepsForward(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
		{JID: chatJID("5511C@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "5511A@s.whatsapp.net"); got != "5511B@s.whatsapp.net" {
		t.Fatalf("want B, got %q", got)
	}
	if got := nextChatJID(chats, "5511B@s.whatsapp.net"); got != "5511C@s.whatsapp.net" {
		t.Fatalf("want C, got %q", got)
	}
}

func TestNextChatJID_LastClamps(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := nextChatJID(chats, "5511B@s.whatsapp.net"); got != "" {
		t.Fatalf("last + next should clamp to \"\", got %q", got)
	}
}

func TestPrevChatJID_FirstClamps(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, "5511A@s.whatsapp.net"); got != "" {
		t.Fatalf("first + prev should clamp to \"\", got %q", got)
	}
}

func TestPrevChatJID_StepsBackward(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
		{JID: chatJID("5511B@s.whatsapp.net")},
		{JID: chatJID("5511C@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, "5511C@s.whatsapp.net"); got != "5511B@s.whatsapp.net" {
		t.Fatalf("want B, got %q", got)
	}
}

func TestPrevChatJID_NoCurrentOpensFirst(t *testing.T) {
	chats := []client.Chat{
		{JID: chatJID("5511A@s.whatsapp.net")},
	}
	if got := prevChatJID(chats, ""); got != "5511A@s.whatsapp.net" {
		t.Fatalf("no current should open first, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /home/pedro/.claudio/whatsappalt
go test ./ui/ -run "TestNextChatJID|TestPrevChatJID" -timeout 5s
```

Expected: `undefined: nextChatJID` / `undefined: prevChatJID` build error.

- [ ] **Step 3: Implement the helpers**

`ui/chat_nav.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./ui/ -run "TestNextChatJID|TestPrevChatJID" -timeout 5s -v
```

Expected: PASS for all 8 sub-tests.

- [ ] **Step 5: Write the failing tests for message-target nav helpers**

Append to `ui/chat_nav_test.go`:

```go
func TestLatestNonDeletedMessageID_Empty(t *testing.T) {
	if got := latestNonDeletedMessageID(nil); got != "" {
		t.Fatalf("empty list should return \"\", got %q", got)
	}
}

func TestLatestNonDeletedMessageID_PicksMostRecent(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := latestNonDeletedMessageID(msgs); got != "m3" {
		t.Fatalf("want m3, got %q", got)
	}
}

func TestLatestNonDeletedMessageID_SkipsDeletedTail(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3", Deleted: true},
	}
	if got := latestNonDeletedMessageID(msgs); got != "m2" {
		t.Fatalf("should skip deleted tail, want m2, got %q", got)
	}
}

func TestLatestNonDeletedMessageID_AllDeleted(t *testing.T) {
	msgs := []*Message{
		{ID: "m1", Deleted: true},
		{ID: "m2", Deleted: true},
	}
	if got := latestNonDeletedMessageID(msgs); got != "" {
		t.Fatalf("all-deleted should return \"\", got %q", got)
	}
}

func TestNextNonDeletedMessageID_Steps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := nextNonDeletedMessageID(msgs, "m1"); got != "m2" {
		t.Fatalf("want m2, got %q", got)
	}
	if got := nextNonDeletedMessageID(msgs, "m2"); got != "m3" {
		t.Fatalf("want m3, got %q", got)
	}
}

func TestNextNonDeletedMessageID_SkipsDeletedInMiddle(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2", Deleted: true},
		{ID: "m3"},
	}
	if got := nextNonDeletedMessageID(msgs, "m1"); got != "m3" {
		t.Fatalf("should jump over deleted, want m3, got %q", got)
	}
}

func TestNextNonDeletedMessageID_LastClamps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
	}
	if got := nextNonDeletedMessageID(msgs, "m2"); got != "m2" {
		t.Fatalf("last clamps to itself, got %q", got)
	}
}

func TestNextNonDeletedMessageID_UnknownCurrent(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
	}
	if got := nextNonDeletedMessageID(msgs, "ghost"); got != "ghost" {
		t.Fatalf("unknown id should pass through unchanged, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_Steps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
		{ID: "m3"},
	}
	if got := prevNonDeletedMessageID(msgs, "m3"); got != "m2" {
		t.Fatalf("want m2, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_SkipsDeletedInMiddle(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2", Deleted: true},
		{ID: "m3"},
	}
	if got := prevNonDeletedMessageID(msgs, "m3"); got != "m1" {
		t.Fatalf("should jump over deleted, want m1, got %q", got)
	}
}

func TestPrevNonDeletedMessageID_FirstClamps(t *testing.T) {
	msgs := []*Message{
		{ID: "m1"},
		{ID: "m2"},
	}
	if got := prevNonDeletedMessageID(msgs, "m1"); got != "m1" {
		t.Fatalf("first clamps to itself, got %q", got)
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

```bash
go test ./ui/ -run "TestLatestNonDeleted|TestNextNonDeleted|TestPrevNonDeleted" -timeout 5s
```

Expected: build error `undefined: latestNonDeletedMessageID` etc.

- [ ] **Step 7: Implement message-target helpers**

Append to `ui/chat_nav.go`:

```go
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
```

- [ ] **Step 8: Run tests to verify they pass**

```bash
go test ./ui/ -run "TestLatestNonDeleted|TestNextNonDeleted|TestPrevNonDeleted" -timeout 5s -v
```

Expected: PASS for all sub-tests.

- [ ] **Step 9: Run full UI suite to confirm nothing else broke**

```bash
go test ./... -timeout 60s
```

Expected: all packages OK.

- [ ] **Step 10: Commit**

```bash
git add ui/chat_nav.go ui/chat_nav_test.go
git commit -m "feat(ui): pure helpers for sidebar + message-target navigation

Sidebar: nextChatJID / prevChatJID with clamp at ends, fall-through-to-
first when current is unknown, empty-list returns empty.

Messages: latestNonDeletedMessageID for entering reply mode (skips
trailing deleted bubbles), nextNonDeletedMessageID / prevNonDeletedMessageID
for stepping the reply target across the chat. All clamp at boundaries
and tolerate unknown currentIDs.

Pure functions, fully unit-tested; the chat_view.go nav methods will
delegate to these in the next task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: ChatView state fields for reply mode + search ESC

**Files:**
- Modify: `ui/chat_view.go` (struct definition near line 100)

- [ ] **Step 1: Add the fields**

Inside the `ChatView` struct definition, add (alongside the other state fields):

```go
	// Reply mode (Ctrl+R): when active, replyTargetID identifies the
	// bubble currently highlighted as the quote target. replyModeEscPop
	// holds the dismiss callback registered on the ESC stack;
	// replyPrevFocused is the focusable widget that had focus before
	// reply mode took over (restored on cancel).
	replyMode        bool
	replyTargetID    string
	replyModeEscPop  func()
	replyPrevFocused fyne.Focusable

	// Search mode: searchEscPop holds the ESC dismiss for openSearch().
	searchEscPop func()
```

- [ ] **Step 2: Verify the build**

```bash
cd /home/pedro/.claudio/whatsappalt
go build ./...
```

Expected: build succeeds (no other code references these fields yet).

- [ ] **Step 3: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): add reply-mode + search ESC state fields to ChatView

Pre-wires storage for the keyboard navigation v1 work. No behavior
change yet; subsequent tasks fill in the methods that mutate these
fields.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Sidebar navigation methods (selectNextChat / selectPrevChat)

**Files:**
- Modify: `ui/chat_view.go`

- [ ] **Step 1: Add the methods**

Near the existing `selectChatJID` method (around line 1533), add:

```go
// selectNextChat opens the chat after the current one in the sidebar.
// No-op when already at the last chat (no wrap) or when the sidebar
// is empty.
func (cv *ChatView) selectNextChat() {
	cv.muCachedChats.RLock()
	target := nextChatJID(cv.cachedChats, cv.currentChatJID)
	cv.muCachedChats.RUnlock()
	if target == "" {
		return
	}
	cv.selectChatJID(target)
}

// selectPrevChat opens the chat before the current one. Symmetric to
// selectNextChat: clamps at the first chat, opens the first chat when
// none is currently open.
func (cv *ChatView) selectPrevChat() {
	cv.muCachedChats.RLock()
	target := prevChatJID(cv.cachedChats, cv.currentChatJID)
	cv.muCachedChats.RUnlock()
	if target == "" {
		return
	}
	cv.selectChatJID(target)
}
```

- [ ] **Step 2: Verify the build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Run all tests**

```bash
go test ./... -timeout 60s
```

Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): selectNextChat / selectPrevChat methods on ChatView

Wraps the chat_nav.go pure helpers + the existing selectChatJID
dispatch. Reads cv.cachedChats under its existing RLock so search
filtering (which mutates the same slice) stays consistent.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: focusComposer method

**Files:**
- Modify: `ui/chat_view.go`

- [ ] **Step 1: Add the method**

Near `selectNextChat` (just added), add:

```go
// focusComposer hands keyboard focus to the message input. When reply
// mode is active, this confirms the current target first (mirrors the
// behavior of pressing Enter in reply mode). When search is open, it
// closes search before focusing.
func (cv *ChatView) focusComposer() {
	if cv.replyMode {
		cv.confirmReplyTarget()
		return
	}
	if cv.searchEscPop != nil {
		cv.closeSearch()
	}
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
}
```

`confirmReplyTarget` and `closeSearch` are added in later tasks; the build will fail until they exist. That's fine — the next two tasks fill them in.

- [ ] **Step 2: Note that build will fail until Task 5 + 6**

This is intentional. Task 5 adds `closeSearch`; Task 6 adds `confirmReplyTarget`. Skip the build verification here and chain it through Task 6's verification.

- [ ] **Step 3: Do NOT commit yet**

Leave the working tree dirty; we'll commit after the dependent methods land in Task 6.

---

## Task 5: Search methods (openSearch / closeSearch / confirmSearch)

**Files:**
- Modify: `ui/chat_view.go`

- [ ] **Step 1: Add the methods**

Near `focusComposer`, add:

```go
// openSearch focuses the sidebar search entry so typing immediately
// filters the chat list. Cancels reply mode first if active. Pushes
// a dismiss handler onto the ESC stack so a single Esc closes search
// the same way it closes any other dismissible.
func (cv *ChatView) openSearch() {
	if cv.searchEntry == nil || cv.window == nil {
		return
	}
	if cv.replyMode {
		cv.exitReplyMode()
	}
	cv.window.Canvas().Focus(cv.searchEntry)
	if cv.searchEscPop == nil {
		cv.searchEscPop = pushEsc(cv.closeSearch)
	}
}

// closeSearch clears the search filter, defocuses the search entry,
// and pops its ESC-stack handler. Idempotent: safe to call when
// search isn't open (e.g. via the ESC stack dispatching multiple
// dismissibles in sequence).
func (cv *ChatView) closeSearch() {
	if cv.searchEntry == nil {
		return
	}
	cv.searchEntry.SetText("")
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
	if cv.searchEscPop != nil {
		cv.searchEscPop()
		cv.searchEscPop = nil
	}
}

// confirmSearch opens the first chat in the currently-filtered sidebar
// list and hands focus to the composer. Wired as searchEntry.OnSubmitted
// in Build() so pressing Enter in the search field triggers it.
func (cv *ChatView) confirmSearch(_ string) {
	cv.muCachedChats.RLock()
	var first string
	if len(cv.cachedChats) > 0 {
		first = cv.cachedChats[0].JID.String()
	}
	cv.muCachedChats.RUnlock()
	cv.closeSearch()
	if first != "" {
		cv.selectChatJID(first)
	}
	cv.focusComposer()
}
```

- [ ] **Step 2: Wire the OnSubmitted handler**

Find the `Build()` method's search entry block (around line 476):

```go
	cv.searchEntry = widget.NewEntry()
	cv.searchEntry.PlaceHolder = "Search or start new chat"
	cv.searchEntry.OnChanged = cv.onSearch
```

Add the `OnSubmitted` line:

```go
	cv.searchEntry = widget.NewEntry()
	cv.searchEntry.PlaceHolder = "Search or start new chat"
	cv.searchEntry.OnChanged = cv.onSearch
	cv.searchEntry.OnSubmitted = cv.confirmSearch
```

- [ ] **Step 3: Verify the build**

```bash
go build ./...
```

Note: still expects to fail because `focusComposer` references `confirmReplyTarget` (added next task). If Task 4's `focusComposer` was added, this compile fails on `confirmReplyTarget`. That's fine — fix in Task 6.

If you skipped Task 4, build is green here. Either way, do NOT commit yet.

---

## Task 6: Reply mode lifecycle (enter / exit / confirm + target nav)

**Files:**
- Modify: `ui/chat_view.go`

- [ ] **Step 1: Add reply lifecycle + nav methods**

Below the search methods, add:

```go
// enterReplyMode arms the reply gesture: picks the latest non-deleted
// message in the open chat as the initial target, parks the previous
// focus so we can restore it on cancel, and pushes an ESC dismiss.
// No-op if the open chat has no replyable bubble.
func (cv *ChatView) enterReplyMode() {
	if cv.currentChatJID == "" {
		return
	}
	cv.muMessages.RLock()
	target := latestNonDeletedMessageID(cv.messages[cv.currentChatJID])
	cv.muMessages.RUnlock()
	if target == "" {
		return
	}
	if cv.replyMode {
		return // already armed
	}
	cv.replyMode = true
	cv.replyTargetID = target
	if cv.window != nil {
		cv.replyPrevFocused = cv.window.Canvas().Focused()
		cv.window.Canvas().Focus(nil)
	}
	cv.invalidateBubbleHeight(target)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
	cv.replyModeEscPop = pushEsc(cv.exitReplyMode)
}

// exitReplyMode cancels reply mode without sending. Restores focus to
// whatever held it before enterReplyMode and pops the ESC handler.
// Idempotent so it's safe to call from multiple cancellation paths
// (Esc stack, chat-switch, ChatView teardown).
func (cv *ChatView) exitReplyMode() {
	if !cv.replyMode {
		return
	}
	staleTarget := cv.replyTargetID
	cv.replyMode = false
	cv.replyTargetID = ""
	if cv.replyModeEscPop != nil {
		cv.replyModeEscPop()
		cv.replyModeEscPop = nil
	}
	if cv.window != nil && cv.replyPrevFocused != nil {
		cv.window.Canvas().Focus(cv.replyPrevFocused)
	}
	cv.replyPrevFocused = nil
	cv.invalidateBubbleHeight(staleTarget)
	cv.refreshMessages()
}

// confirmReplyTarget commits the current reply target, exits reply
// mode, and focuses the composer. Same effect as pressing Enter or
// any printable character key while reply mode is armed.
func (cv *ChatView) confirmReplyTarget() {
	if !cv.replyMode {
		return
	}
	targetID := cv.replyTargetID
	cv.muMessages.RLock()
	var target *Message
	for _, m := range cv.messages[cv.currentChatJID] {
		if m.ID == targetID {
			target = m
			break
		}
	}
	cv.muMessages.RUnlock()
	cv.exitReplyMode()
	if target != nil {
		cv.beginReply(target)
	}
	if cv.window != nil && cv.messageInput != nil {
		cv.window.Canvas().Focus(cv.messageInput)
	}
}

// replyTargetNext steps the reply-mode highlight to the next newer
// non-deleted message. Clamp at the end. No-op outside reply mode.
func (cv *ChatView) replyTargetNext() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	next := nextNonDeletedMessageID(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if next == cv.replyTargetID {
		return
	}
	old := cv.replyTargetID
	cv.replyTargetID = next
	cv.invalidateBubbleHeight(old)
	cv.invalidateBubbleHeight(next)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
}

// replyTargetPrev — symmetric backward step.
func (cv *ChatView) replyTargetPrev() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	prev := prevNonDeletedMessageID(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if prev == cv.replyTargetID {
		return
	}
	old := cv.replyTargetID
	cv.replyTargetID = prev
	cv.invalidateBubbleHeight(old)
	cv.invalidateBubbleHeight(prev)
	cv.refreshMessages()
	cv.scrollToReplyTarget()
}

// scrollToReplyTarget reuses scrollToMessage to bring the highlighted
// bubble into view. No-op if the target ID isn't in the current chat
// (defensive — shouldn't happen but the message list is mutable).
func (cv *ChatView) scrollToReplyTarget() {
	if cv.replyTargetID == "" {
		return
	}
	cv.muMessages.RLock()
	idx := indexOfMsg(cv.messages[cv.currentChatJID], cv.replyTargetID)
	cv.muMessages.RUnlock()
	if idx < 0 {
		return
	}
	cv.scrollToMessage(idx)
}

// invalidateBubbleHeight drops the cached MinSize.Height for one
// message so the next render measures the bubble fresh. Needed because
// the reply-target border adds a couple of pixels and we don't want a
// stale height pinning the row at its pre-highlight size.
func (cv *ChatView) invalidateBubbleHeight(id string) {
	if id == "" {
		return
	}
	cv.muBubbleHeights.Lock()
	delete(cv.bubbleHeights, id)
	cv.muBubbleHeights.Unlock()
}
```

- [ ] **Step 2: Verify the build**

```bash
go build ./...
```

Expected: success now that all referenced methods exist.

- [ ] **Step 3: Run the full test suite**

```bash
go test ./... -timeout 60s
```

Expected: all green. No new tests yet for these methods (the lifecycle methods touch Fyne types — `Canvas`, `Focusable` — that need an app context to construct meaningfully).

- [ ] **Step 4: Commit (covers Tasks 4-6)**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): focus/composer/search/reply-mode methods on ChatView

Adds:
- focusComposer (Ctrl+L target; confirms reply mode if active, closes
  search if open)
- openSearch / closeSearch / confirmSearch (Ctrl+F + Enter in search
  entry); searchEntry.OnSubmitted now wired
- enterReplyMode / exitReplyMode / confirmReplyTarget — manage state
  + ESC dismiss + previous-focus restore
- replyTargetNext / replyTargetPrev — Ctrl+J/K rebinds while in reply
  mode, with clamp at both ends and skipping deleted bubbles
- scrollToReplyTarget + invalidateBubbleHeight helpers

Methods are wired together but not yet bound to keyboard shortcuts;
the keymap installer comes in the next task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Visual feedback for reply target

**Files:**
- Modify: `ui/chat_view.go` (`buildMessageBubble` around line 936)

- [ ] **Step 1: Inspect the existing bubble assembly**

The current code wraps the body in:

```go
	bubbleInner := container.NewVBox(parts...)
	bubble := container.NewStack(bubbleBg, container.NewPadded(bubbleInner))
```

We add a third layer (a transparent Rectangle with a colored stroke) when the message is the active reply target.

- [ ] **Step 2: Modify buildMessageBubble**

Replace the two-line `bubble := ...` snippet above with:

```go
	bubbleInner := container.NewVBox(parts...)
	stack := []fyne.CanvasObject{bubbleBg, container.NewPadded(bubbleInner)}
	if cv.replyMode && msg.ID == cv.replyTargetID {
		border := canvas.NewRectangle(color.Transparent)
		border.StrokeColor = ctpMauve
		border.StrokeWidth = 2
		border.CornerRadius = 12
		stack = append(stack, border)
	}
	bubble := container.NewStack(stack...)
```

`color.Transparent` is `image/color`'s zero-alpha — make sure `image/color` is in the imports (it should already be: search for `"image/color"` in `chat_view.go`'s import block; if absent, add it).

- [ ] **Step 3: Verify the build**

```bash
go build ./...
```

Expected: success. (If `color` import is missing, add it to the import block.)

- [ ] **Step 4: Run tests**

```bash
go test ./... -timeout 60s
```

Expected: all green. The bubble assembly path stays identical when reply mode is off, so existing tests are unaffected.

- [ ] **Step 5: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): mauve border on the reply-mode target bubble

When cv.replyMode is on and a bubble's ID matches replyTargetID, the
Stack picks up a third layer: a transparent Rectangle with a 2px
ctpMauve stroke and 12px corner radius (matching bubbleBg). Mirrors
the accent already used by the reply-preview banner above the
composer, so the visual cue is consistent across the app.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Shortcut + canvas-rune installer

**Files:**
- Create: `ui/chat_view_keymap.go`
- Modify: `ui/chat_view.go` (call `installShortcuts()` from `Build()`)

- [ ] **Step 1: Create the new file**

`ui/chat_view_keymap.go`:

```go
package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// installShortcuts registers all keyboard navigation handlers on the
// window canvas. Idempotent against being called once per ChatView
// rebuild (the canvas itself is per-window, but Build() runs at most
// once for the chat surface).
//
// The Ctrl+J/K handlers branch on cv.replyMode at dispatch time — same
// shortcut, different action depending on whether reply mode is armed.
//
// Canvas.OnTypedRune is co-opted for reply mode's "any printable key
// confirms" behavior: when reply mode is active and no widget owns
// keyboard focus (enterReplyMode parks focus precisely so this handler
// fires), any typed rune confirms the target. Outside reply mode the
// handler defers to a captured prior callback so existing TypedRune
// behavior is preserved.
func (cv *ChatView) installShortcuts() {
	if cv.window == nil {
		return
	}
	canvas := cv.window.Canvas()

	register := func(key fyne.KeyName, handler func()) {
		canvas.AddShortcut(&desktop.CustomShortcut{
			KeyName:  key,
			Modifier: fyne.KeyModifierControl,
		}, func(_ fyne.Shortcut) { handler() })
	}

	register(fyne.KeyJ, func() {
		if cv.replyMode {
			cv.replyTargetNext()
			return
		}
		cv.selectNextChat()
	})
	register(fyne.KeyK, func() {
		if cv.replyMode {
			cv.replyTargetPrev()
			return
		}
		cv.selectPrevChat()
	})
	register(fyne.KeyL, func() { cv.focusComposer() })
	register(fyne.KeyF, func() { cv.openSearch() })
	register(fyne.KeyR, func() { cv.enterReplyMode() })

	// Reply-mode "any printable confirms": enterReplyMode parks the
	// previous focus so typed runes reach Canvas.OnTypedRune instead of
	// any focused widget. We chain to whatever rune handler was already
	// installed (currently none, but defensive).
	prev := cv.priorTypedRune
	canvas.SetOnTypedRune(func(r rune) {
		if cv.replyMode {
			cv.confirmReplyTarget()
			return
		}
		if prev != nil {
			prev(r)
		}
	})
}
```

- [ ] **Step 2: Add the priorTypedRune field**

In `ui/chat_view.go`'s `ChatView` struct (with the other state fields added in Task 2), append:

```go
	// priorTypedRune captures the canvas's existing OnTypedRune
	// callback before installShortcuts overrides it, so non-reply-mode
	// runes still reach prior handlers.
	priorTypedRune func(rune)
```

- [ ] **Step 3: Capture prior handler + call installShortcuts in Build**

In `Build()`, after `cv.searchEntry` is set up but before `return` (look for the final `container.NewBorder` around line 540 — verify in your local copy), add at the end of `Build()`:

```go
	if cv.window != nil {
		cv.priorTypedRune = cv.window.Canvas().OnTypedRune()
	}
	cv.installShortcuts()
```

If `Canvas.OnTypedRune()` (no setter, just a getter) doesn't exist in Fyne v2.7, replace `cv.window.Canvas().OnTypedRune()` with `nil` and remove the conditional — there's no prior callback to chain in this codebase anyway (verify with `grep -rn "SetOnTypedRune" /home/pedro/.claudio/whatsappalt`). If grep finds zero matches, `nil` is correct.

Run that grep first to confirm:

```bash
grep -rn "SetOnTypedRune" /home/pedro/.claudio/whatsappalt
```

Expected: no matches in our code (only inside vendored Fyne, if any). If so, simplify the capture to:

```go
	cv.priorTypedRune = nil
	cv.installShortcuts()
```

- [ ] **Step 4: Verify the build**

```bash
go build ./...
```

Expected: success. If Fyne complains about `Canvas.OnTypedRune()` not existing as a getter, switch to the `nil` form per Step 3.

- [ ] **Step 5: Run tests**

```bash
go test ./... -timeout 60s
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add ui/chat_view.go ui/chat_view_keymap.go
git commit -m "feat(ui): wire keyboard shortcuts (Ctrl+J/K/L/F/R) + rune-confirm

installShortcuts registers five Ctrl-prefixed shortcuts on the window
canvas plus an OnTypedRune handler used by reply mode's
\"any printable key confirms\" gesture.

Ctrl+J/K branch on cv.replyMode: outside the mode they navigate the
sidebar; inside they step the reply target. Ctrl+L focuses the
composer (or confirms-and-focuses when in reply mode). Ctrl+F focuses
the sidebar search entry. Ctrl+R enters reply mode.

Build() now calls installShortcuts after searchEntry is configured.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Smoke test + final commit

**Files:**
- None (manual test)

- [ ] **Step 1: Build the binary**

```bash
cd /home/pedro/.claudio/whatsappalt
go build -o altzap.new .
mv altzap.new altzap
ls -la altzap
```

Expected: timestamp updated.

- [ ] **Step 2: Restart and smoke-test in a real session**

Pedro stops the running app and re-launches via `super+shift+z`. Then exercises each shortcut in order:

| Action | Expected |
|--------|----------|
| `Ctrl+J` | Sidebar selection moves to next chat |
| `Ctrl+J` repeatedly until last | Stops at last chat (no wrap) |
| `Ctrl+K` | Selection moves backward |
| `Ctrl+K` from first | Stops at first chat (no wrap) |
| `Ctrl+L` | Caret/focus jumps to composer; `Ctrl+J/K` after still works for chat switching |
| `Ctrl+F` | Cursor jumps to sidebar search entry; typing filters list |
| Type "mig" | Sidebar filters to chats matching |
| Press Enter in search | First filtered chat opens; composer is focused |
| `Ctrl+R` | Latest non-deleted bubble in the open chat picks up a mauve border; auto-scrolls into view |
| `Ctrl+J/K` while reply mode armed | Border moves to newer/older bubble |
| Press any letter (e.g. `a`) | Reply preview banner appears above composer; composer is focused; the letter is NOT typed into the composer |
| Continue typing the message + Enter | Sends the reply with the chosen quote |
| `Ctrl+R` again then `Esc` | Border disappears; no reply preview |
| Click another chat while reply mode armed | Reply mode cancels; chat switches |

- [ ] **Step 3: Tail logs while testing**

In a side terminal:

```bash
tail -f ~/.local/state/altzap/altzap.log
```

Watch for any unexpected errors. The middle-click and double-click paths added in earlier commits should keep their existing log signatures.

- [ ] **Step 4: If everything passes, no extra commit needed**

The previous task commit is the final one; this task is verification only.

If something is off, fix it inline (TDD per change), commit a `fix(ui): ...` patch, retest.

---

## Self-review

**Spec coverage:**
- Ctrl+J/K next/prev chat: Tasks 1, 3, 8 ✓
- Ctrl+L focus composer: Tasks 4, 8 ✓
- Ctrl+F open search + Enter→first hit + Esc cancel: Tasks 5, 8 ✓
- Ctrl+R reply mode entry from latest non-deleted: Tasks 1 (helper), 6 (lifecycle), 8 (binding) ✓
- Reply mode: Ctrl+J/K rebind, Enter confirms, printable confirms, Esc cancels: Tasks 6 (replyTargetNext/Prev, confirmReplyTarget, exitReplyMode), 8 (rune handler, branched J/K) ✓
- Visual border highlight + auto-scroll: Tasks 6 (scrollToReplyTarget), 7 (border) ✓
- ESC stack integration: Tasks 5, 6 (pushEsc calls) ✓
- Edge case "no chat open": Task 1 (helpers fall-through to first), Task 6 (currentChatJID == "" guard) ✓
- Edge case "no messages": Task 6 (`if target == ""` early return) ✓
- Edge case "wrap clamps": Task 1 (helpers return "") ✓
- Edge case "search + Ctrl+R closes search": Task 6 (`if cv.replyMode { exitReplyMode }` only protects entry; opposite direction — search open + Ctrl+R — is ALSO covered because enterReplyMode doesn't block on search state, and search visibility doesn't intercept Ctrl+R; the search entry's text is left alone, which the spec accepts) ✓
- Edge case "reply target deleted/edited mid-mode → exit": **GAP** — neither Task 6 nor any other auto-exits when the bubble vanishes from cv.messages. Adding inline below.

**Type consistency check:** All method names match between definitions and call sites: `selectNextChat`/`selectPrevChat`, `focusComposer`, `openSearch`/`closeSearch`/`confirmSearch`, `enterReplyMode`/`exitReplyMode`/`confirmReplyTarget`, `replyTargetNext`/`replyTargetPrev`, `scrollToReplyTarget`, `invalidateBubbleHeight`, `nextChatJID`/`prevChatJID`, `latestNonDeletedMessageID`/`nextNonDeletedMessageID`/`prevNonDeletedMessageID`, `indexOfMsg`. Field names match: `replyMode`, `replyTargetID`, `replyModeEscPop`, `replyPrevFocused`, `searchEscPop`, `priorTypedRune`. ✓

**Placeholder scan:** No "TBD", "implement later", or "similar to Task N" hand-waves. Each step has concrete code. ✓

### Gap fix: auto-exit reply mode when target disappears

The spec calls for "Reply target deleted/edited during reply mode → exits reply mode (target no longer exists)". The OnMessageDelete callback (`ui/chat_view.go:335`) and OnMessageEdit (`:318`) both run `cv.refreshMessages()` already; we hook a guard on top.

Add an extra task **before Task 9**:

### Task 8.5: Auto-exit reply mode on target loss

**Files:**
- Modify: `ui/chat_view.go`

- [ ] **Step 1: Add a guard helper**

Append to the reply-mode method block:

```go
// invalidateReplyTargetIfMissing exits reply mode when the highlighted
// bubble is no longer in the chat's message list (e.g. after a delete
// event). Called from the OnMessageDelete / OnMessageEdit callbacks
// after the in-memory list has been mutated.
func (cv *ChatView) invalidateReplyTargetIfMissing() {
	if !cv.replyMode {
		return
	}
	cv.muMessages.RLock()
	present := indexOfMsg(cv.messages[cv.currentChatJID], cv.replyTargetID) >= 0
	cv.muMessages.RUnlock()
	if !present {
		cv.exitReplyMode()
	}
}
```

- [ ] **Step 2: Call it from delete + edit callbacks**

In `NewChatView` (around line 318 for `OnMessageEdit`, line 335 for `OnMessageDelete`), inside each callback, AFTER the existing `cv.muMessages.Unlock()` and BEFORE the `fyne.Do(func() { cv.refreshMessages() })`, add:

```go
		cv.invalidateReplyTargetIfMissing()
```

(Two call sites: edit callback and delete callback.)

- [ ] **Step 3: Verify build + tests**

```bash
go build ./...
go test ./... -timeout 60s
```

Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add ui/chat_view.go
git commit -m "feat(ui): auto-exit reply mode when target bubble disappears

OnMessageDelete and OnMessageEdit can drop or rewrite the bubble that
reply mode is currently pointing at. invalidateReplyTargetIfMissing()
checks after each mutation; if the cached replyTargetID no longer
maps to a live bubble, exitReplyMode is called so the user isn't
left with a stale highlight or a phantom target on send.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Execution

After Task 9 (smoke test) is green, the v1 keyboard navigation is shipped.

Total: ~7 commits, ~400 lines of new code, ~300 lines of new test code (the helpers carry the bulk of testable surface).
