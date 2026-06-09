package ui

import "testing"

func TestKeyboardFocusTarget(t *testing.T) {
	search := newEscAwareEntry()
	composer := newPasteAwareEntry(func() (string, error) { return "", nil }, nil)
	cv := &ChatView{
		searchEntry:  search,
		messageInput: composer,
	}

	if got := cv.keyboardFocusTarget(); got != search {
		t.Fatalf("without an open chat, target = %T, want search entry", got)
	}

	cv.setCurrentChat("5511999999999@s.whatsapp.net")
	if got := cv.keyboardFocusTarget(); got != composer {
		t.Fatalf("with an open chat, target = %T, want composer", got)
	}

	cv.muCachedChats.Lock()
	cv.isSearching = true
	cv.muCachedChats.Unlock()
	if got := cv.keyboardFocusTarget(); got != search {
		t.Fatalf("while searching, target = %T, want search entry", got)
	}
}
