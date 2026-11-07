package queue

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFirestoreStore_DocRoundTripAndHelpers(t *testing.T) {
	now := time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	orig := Item{
		ID:        "abc12345",
		Title:     "Firestore item",
		Status:    StatusInProgress,
		Queue:     QueueNeedsAttention,
		Position:  2,
		Text:      "Body text",
		Data:      map[string]any{"env": "prod"},
		Links:     []string{"https://firebase.google.com/docs/firestore"},
		Owner:     "muncus",
		UpdatedAt: now,
	}

	doc := toFirestoreDoc(orig)
	got := doc.toItem()
	if got.ID != orig.ID || got.Title != orig.Title || got.Status != orig.Status || got.Queue != orig.Queue || got.Position != orig.Position || got.Owner != orig.Owner {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, orig)
	}

	// Verify insertAndRenumberItems 1-indexed positioning.
	var list []Item
	list, placed1 := insertAndRenumberItems(list, Item{ID: "1", Title: "One"}, 0)
	if placed1.Position != 1 || len(list) != 1 {
		t.Fatalf("unexpected first insert: %+v", placed1)
	}
	list, placed2 := insertAndRenumberItems(list, Item{ID: "2", Title: "Two"}, 1)
	if placed2.Position != 1 || list[0].ID != "2" || list[1].ID != "1" || list[1].Position != 2 {
		t.Fatalf("unexpected prepend insert: %+v", list)
	}
}

func TestFirestoreStore_Validation(t *testing.T) {
	ctx := context.Background()
	if _, err := NewFirestoreStore(ctx, "", ""); err == nil {
		t.Errorf("expected error for empty projectID")
	}

	s := NewFirestoreStoreWithClient(nil, "")
	if s.collection != DefaultFirestoreCollection {
		t.Errorf("expected default collection %q, got %q", DefaultFirestoreCollection, s.collection)
	}
	if _, err := s.ListQueues(ctx, ""); err == nil {
		t.Errorf("expected error for empty identity in ListQueues")
	}
	if _, err := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: "Invalid"}); err == nil {
		t.Errorf("expected error for invalid queue in GetQueue")
	}
	if _, err := s.UpsertItem(ctx, "alice", Item{}); err == nil {
		t.Errorf("expected error for empty title in UpsertItem")
	}
}

func TestFirestoreStore_Emulator(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping live Firestore emulator test")
	}
	ctx := context.Background()
	s, err := NewFirestoreStore(ctx, "qmcp-test-project", "qmcp_test_items_"+t.Name())
	if err != nil {
		t.Fatalf("NewFirestoreStore failed: %v", err)
	}
	defer s.Close()

	it, err := s.UpsertItem(ctx, "alice", Item{Title: "Emulator task"})
	if err != nil {
		t.Fatalf("UpsertItem failed: %v", err)
	}
	if it.Position != 1 || it.Queue != QueueInbox {
		t.Fatalf("unexpected inserted item: %+v", it)
	}
}
