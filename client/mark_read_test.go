package client

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestGroupMarkTargetsGroupsBySenderPreservingOrder(t *testing.T) {
	targets := []MarkTarget{
		{ID: "m1", SenderJID: "a@s.whatsapp.net"},
		{ID: "m2", SenderJID: "b@s.whatsapp.net"},
		{ID: "m3", SenderJID: "a@s.whatsapp.net"},
		{ID: "", SenderJID: "c@s.whatsapp.net"}, // no ID → dropped
	}

	order, bySender := groupMarkTargets(targets)

	if len(order) != 2 || order[0] != "a@s.whatsapp.net" || order[1] != "b@s.whatsapp.net" {
		t.Fatalf("order = %v, want [a b] em ordem de chegada", order)
	}
	wantA := []types.MessageID{"m1", "m3"}
	gotA := bySender["a@s.whatsapp.net"]
	if len(gotA) != 2 || gotA[0] != wantA[0] || gotA[1] != wantA[1] {
		t.Fatalf("msgs de a = %v, want %v", gotA, wantA)
	}
	if gotB := bySender["b@s.whatsapp.net"]; len(gotB) != 1 || gotB[0] != "m2" {
		t.Fatalf("msgs de b = %v, want [m2]", gotB)
	}
	if _, ok := bySender["c@s.whatsapp.net"]; ok {
		t.Fatal("target sem ID deveria ser descartado")
	}
}

func TestGroupMarkTargetsEmpty(t *testing.T) {
	order, bySender := groupMarkTargets(nil)
	if len(order) != 0 || len(bySender) != 0 {
		t.Fatalf("esperava vazio, got order=%v map=%v", order, bySender)
	}
}
