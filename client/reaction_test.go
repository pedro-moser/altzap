package client

import (
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestMergeReaction(t *testing.T) {
	alice := SavedReaction{Emoji: "👍", SenderJID: "111@s.whatsapp.net", Timestamp: 10}
	bob := SavedReaction{Emoji: "❤️", SenderJID: "222@s.whatsapp.net", Timestamp: 20}

	cases := []struct {
		name     string
		existing []SavedReaction
		incoming SavedReaction
		want     []SavedReaction
	}{
		{
			name:     "add to empty",
			existing: nil,
			incoming: alice,
			want:     []SavedReaction{alice},
		},
		{
			name:     "add second sender keeps first",
			existing: []SavedReaction{alice},
			incoming: bob,
			want:     []SavedReaction{alice, bob},
		},
		{
			name:     "same sender replaces previous emoji",
			existing: []SavedReaction{alice, bob},
			incoming: SavedReaction{Emoji: "😂", SenderJID: alice.SenderJID, Timestamp: 30},
			want: []SavedReaction{
				bob,
				{Emoji: "😂", SenderJID: alice.SenderJID, Timestamp: 30},
			},
		},
		{
			name:     "empty emoji removes sender's reaction",
			existing: []SavedReaction{alice, bob},
			incoming: SavedReaction{Emoji: "", SenderJID: alice.SenderJID, Timestamp: 30},
			want:     []SavedReaction{bob},
		},
		{
			name:     "removal when sender never reacted is a no-op",
			existing: []SavedReaction{bob},
			incoming: SavedReaction{Emoji: "", SenderJID: "333@s.whatsapp.net"},
			want:     []SavedReaction{bob},
		},
		{
			name:     "removal on empty list stays empty",
			existing: nil,
			incoming: SavedReaction{Emoji: "", SenderJID: alice.SenderJID},
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeReaction(tc.existing, tc.incoming)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergeReaction() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDedupeReactions(t *testing.T) {
	// Post-canonicalization, a user's legacy LID- and PN-flavoured entries
	// collapse to the same SenderJID; the most recent (last) must win.
	canon := "5511999999999@s.whatsapp.net"
	other := SavedReaction{Emoji: "❤️", SenderJID: "222@s.whatsapp.net", Timestamp: 15}

	in := []SavedReaction{
		{Emoji: "👍", SenderJID: canon, Timestamp: 10}, // old LID twin, now canonical
		other,
		{Emoji: "😂", SenderJID: canon, Timestamp: 20}, // newer entry from the same user
	}
	want := []SavedReaction{
		other,
		{Emoji: "😂", SenderJID: canon, Timestamp: 20},
	}

	if got := dedupeReactions(in); !reflect.DeepEqual(got, want) {
		t.Errorf("dedupeReactions() = %+v, want %+v", got, want)
	}

	// No twins → unchanged.
	clean := []SavedReaction{other, {Emoji: "👍", SenderJID: canon, Timestamp: 9}}
	if got := dedupeReactions(append([]SavedReaction(nil), clean...)); !reflect.DeepEqual(got, clean) {
		t.Errorf("dedupeReactions(clean) = %+v, want unchanged %+v", got, clean)
	}

	if got := dedupeReactions(nil); len(got) != 0 {
		t.Errorf("dedupeReactions(nil) = %+v, want empty", got)
	}
}

func TestDropNegativeLIDEntries(t *testing.T) {
	w := newTestClient()
	pn, _ := types.ParseJID("5511999999999@s.whatsapp.net")
	w.lidPNCache["resolved@lid"] = pn
	w.lidPNCache["unknown1@lid"] = types.JID{}
	w.lidPNCache["unknown2@lid"] = types.JID{}

	w.dropNegativeLIDEntries()

	if _, ok := w.lidPNCache["resolved@lid"]; !ok {
		t.Error("positive entry must survive")
	}
	if _, ok := w.lidPNCache["unknown1@lid"]; ok {
		t.Error("negative entry must be dropped")
	}
	if len(w.lidPNCache) != 1 {
		t.Errorf("cache has %d entries, want 1", len(w.lidPNCache))
	}
}

func TestEditMutator(t *testing.T) {
	rec := SavedMessage{Text: "original"}

	if !editMutator("novo", 100)(&rec) {
		t.Fatal("first edit must report a change")
	}
	if rec.Text != "novo" || !rec.Edited || rec.EditedAt != 100 {
		t.Fatalf("edit not applied: %+v", rec)
	}

	if editMutator("novo", 200)(&rec) {
		t.Fatal("re-applying the identical edit must be a no-op")
	}
	if rec.EditedAt != 100 {
		t.Fatalf("no-op edit must not touch EditedAt, got %d", rec.EditedAt)
	}

	if !editMutator("outro", 300)(&rec) {
		t.Fatal("a different edit must apply")
	}
	if rec.Text != "outro" || rec.EditedAt != 300 {
		t.Fatalf("second edit not applied: %+v", rec)
	}
}

func TestRevokeMutator(t *testing.T) {
	rec := SavedMessage{Text: "conteúdo"}

	if !revokeMutator(100)(&rec) {
		t.Fatal("first revoke must report a change")
	}
	if !rec.Deleted || rec.DeletedAt != 100 {
		t.Fatalf("revoke not applied: %+v", rec)
	}
	if rec.Text != "conteúdo" {
		t.Fatal("revoke must keep the original text in the record")
	}

	if revokeMutator(200)(&rec) {
		t.Fatal("re-revoking must be a no-op")
	}
	if rec.DeletedAt != 100 {
		t.Fatalf("no-op revoke must not touch DeletedAt, got %d", rec.DeletedAt)
	}
}
