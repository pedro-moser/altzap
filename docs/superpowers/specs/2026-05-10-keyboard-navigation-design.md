# Keyboard navigation v1 — design

Status: design approved 2026-05-10. Pending implementation plan.

## Goal

Cut the daily mouse cost of using AltZap. Pedro is a Hyprland-heavy
keyboard user; the most frequent friction is reaching for the mouse
to switch chats in the sidebar. v1 covers chat switching, jumping to
the composer, incremental search, and a quick-reply gesture; it
deliberately defers wider in-message navigation, react/forward
shortcuts, and any modal mode.

## Non-goals (deferred)

- Vim-style modal modes (insert/normal/visual). v1 is exclusively
  modifier-based — every shortcut takes Ctrl as a prefix and works the
  same with or without the composer focused.
- Navigating individual messages outside reply mode (no Ctrl+J/K
  scrubbing through bubbles when not replying).
- Reactions / forward / star shortcuts.
- Keyboard tab cycling between sidebar / composer / message list as a
  free gesture.

## Scope of v1

| Shortcut | Action | Active when |
|----------|--------|-------------|
| `Ctrl+J` | Next chat in sidebar | Always (composer focus is irrelevant) |
| `Ctrl+K` | Previous chat in sidebar | Always |
| `Ctrl+L` | Focus composer | Always |
| `Ctrl+F` | Open incremental search (focuses sidebar search entry) | Always |
| `Ctrl+R` | Enter reply mode targeting the last message in the open chat | Only when a chat is open and has at least one message |

While **reply mode** is active, `Ctrl+J` / `Ctrl+K` rebind to move the
target message (J = newer, K = older); `Enter` or any printable key
confirms the target and sends focus to the composer; `Esc` cancels.

While the **search entry** is focused, regular typing filters the
sidebar (existing `cv.onSearch`); `Ctrl+J` / `Ctrl+K` continue to move
across the filtered sidebar; `Enter` opens the first visible result and
focuses the composer; `Esc` closes search and restores the full list.

Everything outside this map keeps its current behavior: double-click
to reply, middle-click to copy, ESC stack for popups, Ctrl+Q to quit,
Ctrl+V image paste, and the standard Entry shortcuts inside the
composer (Ctrl+C/V/A/Z/Y).

## Architecture

### State

Two new fields on `ChatView`:

- `replyMode bool` — true while reply mode is active.
- `replyTargetID string` — message ID of the currently highlighted
  reply target. Empty outside reply mode.

Existing state is reused: `currentChatJID`, `cachedChats` (sidebar
list, already filtered/sorted), `messages[chatJID]`, `searchEntry`
visibility/text, `messageList`.

### Shortcut registration

A new file `ui/chat_view_keymap.go` exposes
`(cv *ChatView) installShortcuts()`. Called once from `NewChatView`
after `cv.window` is wired. Each shortcut is a `desktop.CustomShortcut`
registered on `cv.window.Canvas()`. The handler closures call methods
on `cv`:

- `cv.selectNextChat()` / `cv.selectPrevChat()` — chat-switch (or, in
  reply mode, redirected to `cv.replyTargetNext()` /
  `cv.replyTargetPrev()`).
- `cv.focusComposer()`.
- `cv.openSearch()` — shows + focuses `searchEntry`, registers an ESC
  dismiss.
- `cv.enterReplyMode()` — picks the last message in the open chat,
  sets state, registers an ESC dismiss, scrolls to target, refreshes
  the bubble.

Fyne's canvas-level shortcuts run regardless of widget focus, and the
chosen modifier keys (`Ctrl+J/K/L/F/R`) are not consumed by
`widget.Entry` (Entry only intercepts `Ctrl+C/V/A/Z/Y`). No conflict
with the composer.

### Sidebar navigation

`selectNextChat` / `selectPrevChat` operate on `cv.cachedChats` (the
list already shown in the sidebar, filtered if search is active).
They locate `currentChatJID`'s index, step ±1 with **clamp** (no
wrap — Pedro's chat history is long enough that wrapping back to the
oldest chat is noise; the user reaches for `Ctrl+F` for distant jumps),
and call the existing `cv.selectChatJID(nextJID)`. If no chat is
currently open, both shortcuts open the first chat in the list.

### Reply mode

Entered by `Ctrl+R` only when there is an open chat with at least one
non-deleted message. Initial target is the most recent **non-deleted**
message in the chat (any sender — including from-me; explicit choice
for simplicity). Deleted bubbles are skipped during step navigation
too.

While active:
- `Ctrl+J` / `Ctrl+K` step the target across the chat's non-deleted
  messages (J = newer, K = older). Clamps at both ends.
- `Enter`, or any single printable character key (`a`–`z`, `0`–`9`,
  punctuation, space): confirms — calls the existing `cv.beginReply(msg)`
  (which renders the reply preview banner above the composer), focuses
  the composer, exits reply mode. The typed character is consumed
  (i.e. it does NOT also appear in the composer); the user just starts
  typing again normally.
- `Esc`: cancels — clears state, restores the bubble, no reply.
- Switching chat (`Ctrl+J/K` is *redirected* to target nav, but
  selecting another chat via mouse click or via `Ctrl+F` followed by
  search confirm) cancels reply mode silently.

### Search

`cv.openSearch()` reveals + focuses `searchEntry`. Existing `onSearch`
filters `cachedChats`. New behavior:
- `Enter` while `searchEntry` is focused: opens the first chat in the
  filtered list and focuses the composer.
- `Esc`: hides search entry, clears its text, restores the unfiltered
  sidebar — registered through the existing ESC stack.

### Visual feedback

Reply target gets a 2px stroke around its bubble in the `ctpMauve`
color — the same accent already used by `replyPreviewAccent` in the
composer area, so the visual cue is consistent with "this is the
quoted message" mental model.

`buildMessageBubble` gains an `isReplyTarget bool` parameter. When
true, the existing `container.NewStack(bubbleBg, padded(bubbleInner))`
gets an additional top layer: a `canvas.Rectangle` with `StrokeColor
= ctpMauve`, `StrokeWidth = 2`, transparent fill. The per-message
height cache (`bubbleHeights`) is invalidated for that ID on each
state change so the new stroke is measured correctly.

`refreshMessages` is called by `enterReplyMode`, `replyTargetNext/Prev`,
and `exitReplyMode` so the highlight redraws in sync. The message list
also auto-scrolls to the new target via the existing
`cv.scrollToMessage(idx)` helper (already used for "Load older").

### ESC stack integration

Already exists (`ui/esc.go`): a stack of dismiss callbacks. Reply mode
and open-search each push a dismiss handler when entered, pop it when
exited. ESC behavior layered on top of existing dismissibles (popups,
emoji picker, video, image, reply preview) without changing those.

## Edge cases

| Situation | Behavior |
|-----------|----------|
| No chat open, `Ctrl+J/K` | Opens the first chat in the sidebar |
| No chats at all (fresh login), `Ctrl+J/K` | No-op |
| Last chat selected + `Ctrl+J` | No-op (no wrap) |
| First chat selected + `Ctrl+K` | No-op (no wrap) |
| Empty chat (zero messages) + `Ctrl+R` | No-op silently |
| Reply mode + `Ctrl+R` again | No-op (`Ctrl+J/K` is the way to move target) |
| Composer already focused + `Ctrl+L` | No-op (idempotent) |
| Search open + `Ctrl+L` | Closes search, focuses composer |
| Search open + `Ctrl+F` | Re-focuses the search entry (idempotent) |
| Reply mode + `Ctrl+F` | Cancels reply mode, opens search |
| Search open + `Ctrl+R` | Closes search, enters reply mode in the currently open chat |
| Reply mode + click on another chat | Cancels reply mode, switches chat |
| Reply mode + `Ctrl+L` | Confirms current target as reply, focuses composer (same as Enter) |
| New messages arrive during reply mode | List refreshes, target stays the same (matched by ID) |
| Reply target deleted/edited during reply mode | Exits reply mode (target no longer exists) |

## Testing strategy

**Unit-testable** (no Fyne app needed):

- `selectNextChat` / `selectPrevChat` clamping logic — exercise on a
  fake `cachedChats` slice + mock `currentChatJID`. Verify clamp at
  ends, no-op when slice empty, jump-to-first when no current.
- Reply mode state transitions — `enterReplyMode` picks last message,
  `replyTargetNext/Prev` step with clamp, `exitReplyMode` clears state.
- Reply target survival across `cv.messages` updates — appending a new
  message must not change `replyTargetID` if the old ID still exists.
- Reply target invalidation — when the bubble matching `replyTargetID`
  vanishes (delete event), state must reset.

**Integration / smoke** (Pedro tests in real session):
- Each shortcut fires the right action and doesn't conflict with the
  composer's typing.
- Visual feedback (mauve stroke + auto-scroll) renders correctly.
- ESC out of every state returns to the prior baseline.

## Files touched

- `ui/chat_view.go` — new state fields, methods (`selectNextChat`,
  `selectPrevChat`, `focusComposer`, `openSearch`, `closeSearch`,
  `enterReplyMode`, `exitReplyMode`, `replyTargetNext`,
  `replyTargetPrev`, `confirmReplyTarget`); `buildMessageBubble`
  signature change to accept `isReplyTarget`.
- `ui/chat_view_keymap.go` — new file, `installShortcuts()`.
- `ui/chat_view_keymap_test.go` — new file, unit tests for the
  state-mutating helpers.
- `ui/attachments.go` — `messageInput.OnSubmitted` already triggers
  send; no change. Just verify `Ctrl+L` reaches it.
- `main.go` — no change (existing `Ctrl+Q` shortcut and ESC handler
  stay).

## Open follow-ups (deferred to v2)

- In-message navigation outside reply mode (e.g., `Ctrl+J/K` to scrub
  bubbles for copy without entering reply mode).
- Reactions shortcut (e.g. a `Ctrl+Y` press to apply a thumbs-up to
  the last message — exact key + emoji TBD with Pedro when v2 lands).
- Tab/Shift+Tab cycling between sidebar / message list / composer.
- Vim-style modal mode (if the modifier-only design starts feeling
  cramped). The arch above doesn't preclude this — adding a state
  field and prefixing key matchers covers it later.
