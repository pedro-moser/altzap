package client

import (
	"reflect"
	"testing"
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
