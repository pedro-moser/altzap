package ui

import "testing"

// parkedAtLiveEdge only shortcuts the probe; a false answer sends the
// caller to the list itself, so it must never claim the user is at the
// bottom when they might not be.
func TestParkedAtLiveEdge(t *testing.T) {
	cases := []struct {
		name      string
		following bool
		cur, last float32
		want      bool
	}{
		{"untouched viewport skips the probe", true, 900, 900, true},
		{"nudge up within slack skips the probe", true, 880, 900, true},
		{"nudge down within slack skips the probe", true, 920, 900, true},
		{"exactly at slack skips the probe", true, 900 - tailSlackPx, 900, true},
		{"scrolled away needs the probe", true, 400, 900, false},
		{"a pixel past slack needs the probe", true, 900 - tailSlackPx - 1, 900, false},
		{"pinned view always needs the probe", false, 400, 400, false},
		{"pinned view needs the probe even at the same offset", false, 900, 900, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parkedAtLiveEdge(tc.following, tc.cur, tc.last)
			if got != tc.want {
				t.Fatalf("parkedAtLiveEdge(%v, %v, %v) = %v, want %v",
					tc.following, tc.cur, tc.last, got, tc.want)
			}
		})
	}
}

// The window start is len(msgs)-renderLimit, so the limit must grow in
// lockstep with arrivals to keep that start — and everything the user is
// reading — pinned in place.
func TestAnchoredRenderLimitHoldsWindowStart(t *testing.T) {
	cases := []struct {
		name         string
		limit, total int
		want         int
	}{
		{"zero limit means the default", 0, 10, initialRenderLimit},
		{"window not full yet stays put", 100, 40, 100},
		{"window exactly full stays put", 100, 100, 100},
		{"first arrival past the window widens by one", 100, 101, 101},
		{"default limit widens once overflowed", 0, initialRenderLimit + 1, initialRenderLimit + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anchoredRenderLimit(tc.limit, tc.total); got != tc.want {
				t.Fatalf("anchoredRenderLimit(%d, %d) = %d, want %d",
					tc.limit, tc.total, got, tc.want)
			}
		})
	}
}

// A frozen view taking a burst of arrivals must keep messageWindowStart
// frozen too, otherwise the list slides up a bubble per message.
func TestAnchoredRenderLimitKeepsStartAcrossBurst(t *testing.T) {
	total := 200
	limit := 0
	start := func(l, n int) int {
		if l <= 0 {
			l = initialRenderLimit
		}
		if n > l {
			return n - l
		}
		return 0
	}
	want := start(limit, total)
	for i := 0; i < 25; i++ {
		total++
		limit = anchoredRenderLimit(limit, total)
		if got := start(limit, total); got != want {
			t.Fatalf("after %d arrivals window start = %d, want %d (limit %d)",
				i+1, got, want, limit)
		}
	}
}

func TestNewMessagesPillLabel(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "↓ 1 new message"},
		{2, "↓ 2 new messages"},
		{17, "↓ 17 new messages"},
	}
	for _, tc := range cases {
		if got := newMessagesPillLabel(tc.n); got != tc.want {
			t.Fatalf("newMessagesPillLabel(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
