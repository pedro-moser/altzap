package ui

import (
	"testing"

	"altzap/client"
)

// Own messages persist Text=caption with an empty SenderName. The old code
// stripped a leading "word: " prefix (meant for legacy "Sender: text" rows),
// which truncated legitimate captions like "Nota: comprar leite".
func TestMessageFromRecord_DoesNotStripOwnCaptionPrefix(t *testing.T) {
	cv := &ChatView{}
	sm := client.SavedMessage{
		ID:         "X1",
		FromMe:     true,
		SenderName: "",
		Text:       "Nota: comprar leite",
		Timestamp:  1700000000,
	}
	if msg := cv.messageFromRecord(sm); msg.Text != "Nota: comprar leite" {
		t.Fatalf("own caption must not be truncated, got %q", msg.Text)
	}
}
