package ui

import (
	"testing"
	"time"

	"altzap/client"
)

func TestCanEditMessage(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Minute)
	stale := now.Add(-client.EditWindow - time.Minute)

	base := func() *Message {
		return &Message{ID: "M1", IsOwn: true, Text: "oi", Timestamp: fresh}
	}

	cases := []struct {
		name   string
		mutate func(*Message)
		want   bool
	}{
		{"own fresh text is editable", func(m *Message) {}, true},
		{"not own", func(m *Message) { m.IsOwn = false }, false},
		{"deleted", func(m *Message) { m.Deleted = true }, false},
		{"media message", func(m *Message) { m.MediaType = "image" }, false},
		{"empty text", func(m *Message) { m.Text = "" }, false},
		{"pending (no ACK yet)", func(m *Message) { m.Status = "pending" }, false},
		{"past the edit window", func(m *Message) { m.Timestamp = stale }, false},
		{"delivered status still editable", func(m *Message) { m.Status = "delivered" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			if got := canEditMessage(m, now); got != tc.want {
				t.Errorf("canEditMessage(%+v) = %v, want %v", m, got, tc.want)
			}
		})
	}

	if canEditMessage(nil, now) {
		t.Error("nil message must not be editable")
	}
}
