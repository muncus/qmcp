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

package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/tmc/a2ui"
)

const (
	MCPAppsExtensionID = "io.modelcontextprotocol/ui"
	MCPAppMIMEType     = "text/html;profile=mcp-app"
	MCPAppResourceURI  = "ui://qmcp/board"
)

//go:embed ui/index.html
var boardAppHTML string

func mcpAppToolMeta() mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"resourceUri": MCPAppResourceURI,
		},
	}
}

func registerMCPAppResource(srv *mcp.Server) {
	srv.AddResource(&mcp.Resource{
		URI:         MCPAppResourceURI,
		Name:        "qmcp-board",
		Title:       "QMCP Queue Board (A2UI)",
		Description: "Interactive MCP App and A2UI renderer for QMCP queues",
		MIMEType:    MCPAppMIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: MCPAppMIMEType,
					Text:     boardAppHTML,
				},
			},
		}, nil
	})
}

type apiToolRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type apiToolResponse struct {
	StructuredContent any                 `json:"structuredContent,omitempty"`
	A2UI              []a2ui.AgentMessage `json:"a2ui,omitempty"`
	Error             string              `json:"error,omitempty"`
}

// NewHTTPHandler returns an http.Handler that serves:
//   - GET / and GET /ui: Direct browser view rendering A2UI queue surfaces
//   - GET /api/events: Server-Sent Events (SSE) stream pushing live store updates
//   - POST /api/tool: JSON endpoint used by the direct browser view to invoke tools and receive A2UI messages
//   - /mcp: Streamable HTTP MCP endpoint
func NewHTTPHandler(srv *mcp.Server, store queue.Store) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return srv
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveBrowserUI)
	mux.HandleFunc("GET /ui", serveBrowserUI)
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		handleSSEEvents(w, r, store)
	})
	mux.HandleFunc("POST /api/tool", func(w http.ResponseWriter, r *http.Request) {
		handleAPITool(w, r, store)
	})
	mux.Handle("/mcp", mcpHandler)

	return mux
}

func serveBrowserUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(boardAppHTML))
}

func handleSSEEvents(w http.ResponseWriter, r *http.Request, store queue.Store) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	notifier, ok := store.(queue.ChangeNotifier)
	if !ok {
		http.Error(w, "change notifications unsupported", http.StatusNotImplemented)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := notifier.Subscribe(r.Context())
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"type\":\"change\"}\n\n")
			flusher.Flush()
		}
	}
}

func handleAPITool(w http.ResponseWriter, r *http.Request, store queue.Store) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	var req apiToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid request JSON: %w", err))
		return
	}

	ctx := r.Context()
	switch req.Name {
	case "list_queues":
		var in ListQueuesInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		queues, err := store.ListQueues(ctx, in.Identity)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		itemsByQueue, err := loadItemsByQueue(ctx, store, in.Identity, in.AllOwners, queues)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		out := ListQueuesOutput{Queues: queues}
		msgs := BuildQueuesSummarySurface(in.Identity, queues, itemsByQueue)
		_ = json.NewEncoder(w).Encode(apiToolResponse{
			StructuredContent: out,
			A2UI:              msgs,
		})

	case "get_queue":
		var in GetQueueInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		items, err := store.GetQueue(ctx, queue.GetQueueOptions{
			Identity:  in.Identity,
			Queue:     in.Queue,
			Owner:     in.Owner,
			AllOwners: in.AllOwners,
			Status:    in.Status,
			Limit:     in.Limit,
			Offset:    in.Offset,
		})
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		out := GetQueueOutput{Queue: in.Queue, Items: items}
		msgs := BuildQueueSurface(in.Identity, in.Queue, items)
		_ = json.NewEncoder(w).Encode(apiToolResponse{
			StructuredContent: out,
			A2UI:              msgs,
		})

	case "upsert_item":
		var in UpsertItemInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		item, err := store.UpsertItem(ctx, in.Identity, queue.Item{
			ID:       in.ID,
			Title:    in.Title,
			Queue:    in.Queue,
			Status:   in.Status,
			Position: in.Position,
			Text:     in.Text,
			Comments: in.Comments,
			Data:     in.Data,
			Links:    in.Links,
			Owner:    in.Owner,
		})
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		out := UpsertItemOutput{Item: item}
		msgs := BuildItemSurface(in.Identity, item)
		_ = json.NewEncoder(w).Encode(apiToolResponse{
			StructuredContent: out,
			A2UI:              msgs,
		})

	case "add_comment":
		var in AddCommentInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		item, err := store.AddComment(ctx, in.Identity, in.ID, in.Text)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		out := AddCommentOutput{Item: item}
		msgs := BuildItemSurface(in.Identity, item)
		_ = json.NewEncoder(w).Encode(apiToolResponse{
			StructuredContent: out,
			A2UI:              msgs,
		})

	default:
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("unknown tool %q", req.Name))
	}
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiToolResponse{Error: err.Error()})
}

func loadItemsByQueue(ctx context.Context, store queue.Store, identity string, allOwners bool, queues []queue.QueueSummary) (map[queue.QueueName][]queue.Item, error) {
	out := make(map[queue.QueueName][]queue.Item, len(queues))
	for _, q := range queues {
		if q.Name == queue.QueueArchive {
			continue
		}
		items, err := store.GetQueue(ctx, queue.GetQueueOptions{
			Identity:  identity,
			Queue:     q.Name,
			AllOwners: allOwners,
		})
		if err != nil {
			return nil, err
		}
		out[q.Name] = items
	}
	return out, nil
}
