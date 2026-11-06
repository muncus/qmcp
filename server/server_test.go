package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
)

func decodeStructured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("CallTool returned tool error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal StructuredContent into %T: %v", out, err)
	}
	return out
}

func TestMCPServer_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := queue.NewJSONLStore(filepath.Join(t.TempDir(), "test.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	srv := New(store)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)

	t1, t2 := mcp.NewInMemoryTransports()
	srvSession, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("srv.Connect failed: %v", err)
	}
	defer srvSession.Close()

	cliSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer cliSession.Close()

	// 1. Verify tools are registered.
	toolsRes, err := cliSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(toolsRes.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(toolsRes.Tools))
	}

	// 2. Insert an item via upsert_item.
	upsertRes, err := cliSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "upsert_item",
		Arguments: map[string]any{
			"identity": "agent-1",
			"title":    "Review PR #42",
			"text":     "Check concurrency edge cases",
			"data":     map[string]any{"pr": 42},
			"links":    []string{"https://github.com/example/repo/pull/42"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool upsert_item (insert) failed: %v", err)
	}
	inserted := decodeStructured[UpsertItemOutput](t, upsertRes).Item
	if inserted.ID == "" || inserted.Queue != queue.QueueInbox || inserted.Status != queue.StatusOpen || inserted.Owner != "agent-1" || inserted.Position != 1 {
		t.Fatalf("unexpected inserted item: %+v", inserted)
	}

	// 3. Call list_queues and verify counts.
	listRes, err := cliSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_queues",
		Arguments: map[string]any{
			"identity": "agent-1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool list_queues failed: %v", err)
	}
	listed := decodeStructured[ListQueuesOutput](t, listRes)
	if len(listed.Queues) != 4 {
		t.Fatalf("expected 4 queues, got %d", len(listed.Queues))
	}
	if listed.Queues[0].Name != queue.QueueInbox || listed.Queues[0].TotalCount != 1 || listed.Queues[0].CallerCount != 1 {
		t.Errorf("unexpected Inbox counts: %+v", listed.Queues[0])
	}

	// 4. Call get_queue for Inbox.
	getRes, err := cliSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_queue",
		Arguments: map[string]any{
			"identity": "agent-1",
			"queue":    "Inbox",
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_queue failed: %v", err)
	}
	gotQueue := decodeStructured[GetQueueOutput](t, getRes)
	if len(gotQueue.Items) != 1 || gotQueue.Items[0].ID != inserted.ID {
		t.Fatalf("unexpected get_queue output: %+v", gotQueue)
	}

	// 5. Replace item with status=done -> verify auto-move to Archive.
	updateRes, err := cliSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "upsert_item",
		Arguments: map[string]any{
			"identity": "agent-1",
			"id":       inserted.ID,
			"title":    inserted.Title,
			"status":   "done",
		},
	})
	if err != nil {
		t.Fatalf("CallTool upsert_item (update) failed: %v", err)
	}
	updated := decodeStructured[UpsertItemOutput](t, updateRes).Item
	if updated.Queue != queue.QueueArchive || updated.Status != queue.StatusDone || updated.Position != 1 {
		t.Fatalf("expected item moved to Archive at position 1 with status done, got %+v", updated)
	}
}
