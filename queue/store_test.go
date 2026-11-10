// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package queue

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONLStore_InsertAndDefaults(t *testing.T) {
	ctx := context.Background()
	storeFile := filepath.Join(t.TempDir(), "items.jsonl")
	s, err := NewJSONLStore(storeFile)
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	fixedTime := time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixedTime }

	it1, err := s.UpsertItem(ctx, "alice", Item{
		Title: "First task",
		Text:  "Details for task 1",
		Data:  map[string]any{"priority": "high"},
		Links: []string{"https://example.com/1"},
	})
	if err != nil {
		t.Fatalf("UpsertItem insert failed: %v", err)
	}

	if it1.ID == "" {
		t.Errorf("expected generated ID, got empty")
	}
	if it1.Queue != QueueInbox {
		t.Errorf("expected default queue %q, got %q", QueueInbox, it1.Queue)
	}
	if it1.Status != StatusOpen {
		t.Errorf("expected default status %q, got %q", StatusOpen, it1.Status)
	}
	if it1.Owner != "alice" {
		t.Errorf("expected default owner 'alice', got %q", it1.Owner)
	}
	if it1.Position != 1 {
		t.Errorf("expected position 1, got %d", it1.Position)
	}
	if !it1.UpdatedAt.Equal(fixedTime) {
		t.Errorf("expected UpdatedAt %v, got %v", fixedTime, it1.UpdatedAt)
	}

	// Insert second item without position -> should append at position 2.
	it2, err := s.UpsertItem(ctx, "alice", Item{
		Title: "Second task",
	})
	if err != nil {
		t.Fatalf("UpsertItem 2 failed: %v", err)
	}
	if it2.Position != 2 {
		t.Errorf("expected position 2, got %d", it2.Position)
	}

	// Insert third item at position 1 -> should shift it1 and it2.
	it3, err := s.UpsertItem(ctx, "alice", Item{
		Title:    "Urgent task",
		Position: 1,
	})
	if err != nil {
		t.Fatalf("UpsertItem 3 failed: %v", err)
	}
	if it3.Position != 1 {
		t.Errorf("expected position 1, got %d", it3.Position)
	}

	items, err := s.GetQueue(ctx, GetQueueOptions{
		Identity: "alice",
		Queue:    QueueInbox,
	})
	if err != nil {
		t.Fatalf("GetQueue failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	wantTitles := []string{"Urgent task", "First task", "Second task"}
	for i, want := range wantTitles {
		wantPos := i + 1
		if items[i].Title != want {
			t.Errorf("index %d: want title %q, got %q", i, want, items[i].Title)
		}
		if items[i].Position != wantPos {
			t.Errorf("index %d: want position %d, got %d", i, wantPos, items[i].Position)
		}
	}

	// Reload from disk and verify state persisted identically.
	s2, err := NewJSONLStore(storeFile)
	if err != nil {
		t.Fatalf("reload NewJSONLStore failed: %v", err)
	}
	reloaded, err := s2.GetQueue(ctx, GetQueueOptions{
		Identity: "alice",
		Queue:    QueueInbox,
	})
	if err != nil {
		t.Fatalf("GetQueue on reloaded store failed: %v", err)
	}
	if len(reloaded) != 3 {
		t.Fatalf("expected 3 items after reload, got %d", len(reloaded))
	}
	for i, want := range wantTitles {
		wantPos := i + 1
		if reloaded[i].Title != want || reloaded[i].Position != wantPos {
			t.Errorf("reloaded[%d]: got (%q, pos %d), want (%q, pos %d)", i, reloaded[i].Title, reloaded[i].Position, want, wantPos)
		}
	}
}

func TestJSONLStore_ReplaceUpdateReorderAndAutoArchive(t *testing.T) {
	ctx := context.Background()
	s, err := NewJSONLStore(filepath.Join(t.TempDir(), "items.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	a, _ := s.UpsertItem(ctx, "alice", Item{
		Title: "Task A",
		Text:  "Initial text",
		Data:  map[string]any{"k": "v"},
		Links: []string{"link-a"},
	})
	b, _ := s.UpsertItem(ctx, "alice", Item{
		Title: "Task B",
	})
	c, _ := s.UpsertItem(ctx, "alice", Item{
		Title: "Task C",
	})

	// Move Task C to position 2 within Inbox.
	updatedC, err := s.UpsertItem(ctx, "alice", Item{
		ID:       c.ID,
		Title:    "Task C",
		Position: 2,
		Status:   StatusInProgress,
	})
	if err != nil {
		t.Fatalf("update C failed: %v", err)
	}
	if updatedC.Title != "Task C" || updatedC.Position != 2 || updatedC.Status != StatusInProgress {
		t.Errorf("unexpected updatedC: %+v", updatedC)
	}

	inbox, _ := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox})
	if len(inbox) != 3 || inbox[0].ID != a.ID || inbox[1].ID != c.ID || inbox[2].ID != b.ID {
		t.Fatalf("unexpected order after reorder: %+v", inbox)
	}

	// Mark Task A as done -> should automatically move to Archive and renumber Inbox.
	a.Status = StatusDone
	a.Position = 0
	archivedA, err := s.UpsertItem(ctx, "alice", a)
	if err != nil {
		t.Fatalf("archive A failed: %v", err)
	}
	if archivedA.Queue != QueueArchive {
		t.Errorf("expected QueueArchive on done status, got %q", archivedA.Queue)
	}
	if archivedA.Position != 1 {
		t.Errorf("expected position 1 in Archive, got %d", archivedA.Position)
	}

	inbox, _ = s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox})
	if len(inbox) != 2 || inbox[0].ID != c.ID || inbox[0].Position != 1 || inbox[1].ID != b.ID || inbox[1].Position != 2 {
		t.Fatalf("unexpected Inbox after auto-archive: %+v", inbox)
	}

	archive, _ := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueArchive})
	if len(archive) != 1 || archive[0].ID != a.ID || archive[0].Position != 1 {
		t.Fatalf("unexpected Archive contents: %+v", archive)
	}
}

func TestJSONLStore_MultiUserFilteringAndPagination(t *testing.T) {
	ctx := context.Background()
	s, err := NewJSONLStore("")
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	// Alice inserts 2 items in Inbox; Bob inserts 1 item in Inbox and 1 in Outbox.
	_, _ = s.UpsertItem(ctx, "alice", Item{Title: "Alice 1"})
	_, _ = s.UpsertItem(ctx, "bob", Item{Title: "Bob 1"})
	_, _ = s.UpsertItem(ctx, "alice", Item{Title: "Alice 2", Status: StatusBlocked})
	_, _ = s.UpsertItem(ctx, "bob", Item{Title: "Bob Outbox", Queue: QueueOutbox})

	summaries, err := s.ListQueues(ctx, "alice")
	if err != nil {
		t.Fatalf("ListQueues failed: %v", err)
	}
	if len(summaries) != 4 {
		t.Fatalf("expected 4 queues, got %d", len(summaries))
	}
	if summaries[0].Name != QueueInbox || summaries[0].TotalCount != 3 || summaries[0].CallerCount != 2 {
		t.Errorf("unexpected Inbox summary for alice: %+v", summaries[0])
	}
	if summaries[1].Name != QueueOutbox || summaries[1].TotalCount != 1 || summaries[1].CallerCount != 0 {
		t.Errorf("unexpected Outbox summary for alice: %+v", summaries[1])
	}

	// Default GetQueue for alice returns only alice's 2 items.
	aliceInbox, err := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox})
	if err != nil {
		t.Fatalf("GetQueue alice failed: %v", err)
	}
	if len(aliceInbox) != 2 {
		t.Fatalf("expected 2 items for alice, got %d", len(aliceInbox))
	}
	// Global 1-indexed positions are preserved (1 and 3).
	if aliceInbox[0].Position != 1 || aliceInbox[1].Position != 3 {
		t.Errorf("expected global positions 1 and 3, got %d and %d", aliceInbox[0].Position, aliceInbox[1].Position)
	}

	// GetQueue with AllOwners returns all 3 items.
	allInbox, err := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox, AllOwners: true})
	if err != nil {
		t.Fatalf("GetQueue all owners failed: %v", err)
	}
	if len(allInbox) != 3 {
		t.Fatalf("expected 3 items across all owners, got %d", len(allInbox))
	}

	// Filter by status=blocked.
	blockedInbox, err := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox, Status: StatusBlocked})
	if err != nil {
		t.Fatalf("GetQueue status filter failed: %v", err)
	}
	if len(blockedInbox) != 1 || blockedInbox[0].Title != "Alice 2" {
		t.Errorf("unexpected blocked items: %+v", blockedInbox)
	}

	// Pagination (Offset=1, Limit=1 on allInbox).
	page, err := s.GetQueue(ctx, GetQueueOptions{Identity: "alice", Queue: QueueInbox, Owner: "*", Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("GetQueue pagination failed: %v", err)
	}
	if len(page) != 1 || page[0].Title != "Bob 1" {
		t.Errorf("unexpected paginated result: %+v", page)
	}
}

func TestJSONLStore_OwnerAndAssigneeSplit(t *testing.T) {
	ctx := context.Background()
	s, err := NewJSONLStore("")
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	// Alice creates a task in Inbox.
	created, err := s.UpsertItem(ctx, "alice", Item{
		Title: "Build feature X",
	})
	if err != nil {
		t.Fatalf("UpsertItem create failed: %v", err)
	}
	if created.Owner != "alice" || created.Assignee != "" {
		t.Fatalf("expected Owner='alice' and Assignee='', got Owner=%q Assignee=%q", created.Owner, created.Assignee)
	}

	// Agent claims the task by setting status=in_progress without specifying Owner or Assignee.
	// Owner should remain 'alice' and Assignee should auto-default to 'agent-1'.
	claimed, err := s.UpsertItem(ctx, "agent-1", Item{
		ID:     created.ID,
		Title:  created.Title,
		Status: StatusInProgress,
	})
	if err != nil {
		t.Fatalf("UpsertItem claim failed: %v", err)
	}
	if claimed.Owner != "alice" {
		t.Errorf("expected Owner 'alice' to be preserved, got %q", claimed.Owner)
	}
	if claimed.Assignee != "agent-1" {
		t.Errorf("expected Assignee to auto-default to 'agent-1' on in_progress, got %q", claimed.Assignee)
	}

	// Both 'alice' (owner) and 'agent-1' (assignee) should see CallerCount=1 and see the item in default GetQueue.
	for _, who := range []string{"alice", "agent-1"} {
		summaries, err := s.ListQueues(ctx, who)
		if err != nil {
			t.Fatalf("ListQueues(%q) failed: %v", who, err)
		}
		if summaries[0].CallerCount != 1 {
			t.Errorf("expected CallerCount=1 for %q, got %d", who, summaries[0].CallerCount)
		}
		qItems, err := s.GetQueue(ctx, GetQueueOptions{Identity: who, Queue: QueueInbox})
		if err != nil {
			t.Fatalf("GetQueue(%q) failed: %v", who, err)
		}
		if len(qItems) != 1 || qItems[0].ID != created.ID {
			t.Errorf("expected %q to see item %q in default GetQueue, got %+v", who, created.ID, qItems)
		}
	}

	// Explicit filter by Assignee.
	byAssignee, err := s.GetQueue(ctx, GetQueueOptions{Identity: "bob", Queue: QueueInbox, Assignee: "agent-1"})
	if err != nil {
		t.Fatalf("GetQueue(Assignee='agent-1') failed: %v", err)
	}
	if len(byAssignee) != 1 || byAssignee[0].ID != created.ID {
		t.Errorf("expected 1 item when filtering by Assignee='agent-1', got %+v", byAssignee)
	}
}
