package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/tmc/a2ui"
	"github.com/tmc/a2ui/a2a"
	"github.com/tmc/a2ui/a2uischema"
	"github.com/tmc/a2ui/a2uistate"
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

func newTestValidator(t *testing.T) *a2uischema.Validator {
	t.Helper()
	sm, err := a2uischema.NewSchemaManager([]a2uischema.CatalogConfig{a2uischema.BasicCatalogConfig()}, false)
	if err != nil {
		t.Fatalf("NewSchemaManager failed: %v", err)
	}
	cat, err := sm.SelectedCatalog(nil, nil, nil)
	if err != nil {
		t.Fatalf("SelectedCatalog failed: %v", err)
	}
	return a2uischema.NewValidator(cat)
}

func assertValidA2UIResource(t *testing.T, validator *a2uischema.Validator, res *mcp.CallToolResult, wantSurfaceID string) *a2uistate.Surface {
	t.Helper()
	var embedded *mcp.EmbeddedResource
	for _, c := range res.Content {
		if er, ok := c.(*mcp.EmbeddedResource); ok {
			embedded = er
			break
		}
	}
	if embedded == nil || embedded.Resource == nil {
		t.Fatalf("expected EmbeddedResource in CallToolResult.Content, got %+v", res.Content)
	}
	if embedded.Resource.MIMEType != a2a.A2UIMIMEType {
		t.Errorf("expected MIMEType %q, got %q", a2a.A2UIMIMEType, embedded.Resource.MIMEType)
	}
	rawJSON := []byte(embedded.Resource.Text)
	if err := validator.ValidateJSON(rawJSON); err != nil {
		t.Fatalf("A2UI schema validation failed for surface %q: %v\nJSON: %s", wantSurfaceID, err, embedded.Resource.Text)
	}
	msgs, err := validator.ParseMessages(rawJSON)
	if err != nil {
		t.Fatalf("ParseMessages failed: %v", err)
	}
	stateSurf := a2uistate.NewSurface(wantSurfaceID)
	if err := stateSurf.Apply(msgs...); err != nil {
		t.Fatalf("a2uistate.Surface.Apply failed: %v", err)
	}
	if stateSurf.CatalogID() != a2ui.BasicCatalogID {
		t.Errorf("expected CatalogID %q, got %q", a2ui.BasicCatalogID, stateSurf.CatalogID())
	}
	if _, ok := stateSurf.Root(); !ok {
		t.Errorf("expected root component on surface %q", wantSurfaceID)
	}
	return stateSurf
}

func TestMCPServer_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := queue.NewJSONLStore(filepath.Join(t.TempDir(), "test.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}

	validator := newTestValidator(t)

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

	// 1. Verify tools are registered and carry _meta.ui.resourceUri.
	toolsRes, err := cliSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(toolsRes.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(toolsRes.Tools))
	}
	for _, tool := range toolsRes.Tools {
		uiMeta, ok := tool.Meta["ui"].(map[string]any)
		if !ok || uiMeta["resourceUri"] != MCPAppResourceURI {
			t.Errorf("tool %q missing expected _meta.ui.resourceUri=%q: %+v", tool.Name, MCPAppResourceURI, tool.Meta)
		}
	}

	// 1b. Verify MCP App resource (ui://qmcp/board) is registered and readable.
	resList, err := cliSession.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resList.Resources) != 1 || resList.Resources[0].URI != MCPAppResourceURI || resList.Resources[0].MIMEType != MCPAppMIMEType {
		t.Fatalf("unexpected resources list: %+v", resList.Resources)
	}
	readRes, err := cliSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: MCPAppResourceURI})
	if err != nil {
		t.Fatalf("ReadResource(%q) failed: %v", MCPAppResourceURI, err)
	}
	if len(readRes.Contents) != 1 || !strings.Contains(readRes.Contents[0].Text, "A2UISurfaceState") {
		t.Fatalf("unexpected MCP App HTML contents")
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
	itemSurf := assertValidA2UIResource(t, validator, upsertRes, SurfaceItemID)
	if gotTitle, ok := itemSurf.Data().ResolveString(a2ui.StringBinding("/item/title"), ""); !ok || gotTitle != "Review PR #42" {
		t.Errorf("expected A2UI data binding /item/title == 'Review PR #42', got %q (ok=%v)", gotTitle, ok)
	}

	// 3. Call list_queues and verify counts + A2UI surface.
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
	boardSurf := assertValidA2UIResource(t, validator, listRes, SurfaceQueuesID)
	if boardRow, ok := boardSurf.Component("kanban-board"); !ok || boardRow.Row == nil || len(boardRow.Row.Children.IDs) != 3 {
		t.Errorf("expected kanban-board Row with 3 lanes, got %+v (ok=%v)", boardRow, ok)
	}
	if laneCard, ok := boardSurf.Component("lane-0-card"); !ok || laneCard.Metadata == nil || laneCard.Metadata.Extensions["qmcp_drop_zone"] == nil {
		t.Errorf("expected lane-0-card with qmcp_drop_zone metadata extension, got %+v", laneCard)
	}
	if itemCard, ok := boardSurf.Component("lane-0-item-0-card"); !ok || itemCard.Metadata == nil || itemCard.Metadata.Extensions["qmcp_draggable"] == nil {
		t.Errorf("expected lane-0-item-0-card with qmcp_draggable metadata extension, got %+v", itemCard)
	}

	// 4. Call get_queue for Inbox and verify A2UI surface.
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
	assertValidA2UIResource(t, validator, getRes, SurfaceQueueID)

	// 5. Replace item with status=done -> verify auto-move to Archive + A2UI surface.
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
	assertValidA2UIResource(t, validator, updateRes, SurfaceItemID)

	// 6. Call get_queue on empty Inbox and verify empty-state A2UI surface validates.
	emptyRes, err := cliSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_queue",
		Arguments: map[string]any{
			"identity": "agent-1",
			"queue":    "Inbox",
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_queue (empty) failed: %v", err)
	}
	assertValidA2UIResource(t, validator, emptyRes, SurfaceQueueID)
}

func TestHTTPBrowserAndToolAPI(t *testing.T) {
	store, err := queue.NewJSONLStore("")
	if err != nil {
		t.Fatalf("NewJSONLStore failed: %v", err)
	}
	validator := newTestValidator(t)
	srv := New(store)
	ts := httptest.NewServer(NewHTTPHandler(srv, store))
	defer ts.Close()

	// 1. Direct browser view GET /
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "QMCP") || !strings.Contains(string(body), "A2UISurfaceState") {
		t.Errorf("unexpected HTML served at /")
	}

	// 2. POST /api/tool upsert_item
	reqBody, _ := json.Marshal(map[string]any{
		"name": "upsert_item",
		"arguments": map[string]any{
			"identity": "alice",
			"title":    "Browser created task",
		},
	})
	apiResp, err := http.Post(ts.URL+"/api/tool", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/tool failed: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on POST /api/tool, got %d", apiResp.StatusCode)
	}
	var toolResp apiToolResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&toolResp); err != nil {
		t.Fatalf("decode apiToolResponse failed: %v", err)
	}
	if toolResp.Error != "" {
		t.Fatalf("unexpected API error: %s", toolResp.Error)
	}
	if err := validator.ValidateMessages(toolResp.A2UI); err != nil {
		t.Fatalf("browser API returned invalid A2UI messages: %v", err)
	}

	// 3. Connect to SSE /api/events and verify live notification on item change.
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/events", nil)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /api/events failed: %v", err)
	}
	defer sseResp.Body.Close()

	lineCh := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(sseResp.Body)
		for sc.Scan() {
			text := sc.Text()
			if strings.HasPrefix(text, "data:") {
				lineCh <- text
				return
			}
		}
	}()

	_, err = store.UpsertItem(context.Background(), "alice", queue.Item{
		Title:  "Trigger SSE",
		Status: queue.StatusInProgress,
	})
	if err != nil {
		t.Fatalf("store.UpsertItem failed: %v", err)
	}

	select {
	case line := <-lineCh:
		if !strings.Contains(line, `"change"`) {
			t.Errorf("unexpected SSE data line: %q", line)
		}
	case <-sseCtx.Done():
		t.Fatalf("timed out waiting for SSE change event")
	}
}
