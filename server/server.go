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
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/tmc/a2ui"
)

// ListQueuesInput defines the input arguments for the list_queues tool.
type ListQueuesInput struct {
	Identity  string `json:"identity" jsonschema:"Identity string of the caller"`
	AllOwners bool   `json:"all_owners,omitempty" jsonschema:"Set to true to include items across all owners in the A2UI Kanban board"`
}

// ListQueuesOutput defines the structured output for the list_queues tool.
type ListQueuesOutput struct {
	Queues []queue.QueueSummary `json:"queues" jsonschema:"Available queues with total and caller-owned item counts"`
}

// GetQueueInput defines the input arguments for the get_queue tool.
type GetQueueInput struct {
	Identity  string          `json:"identity" jsonschema:"Identity string of the caller"`
	Queue     queue.QueueName `json:"queue" jsonschema:"Queue name (Inbox, Outbox, Needs Attention, Archive)"`
	Owner     string          `json:"owner,omitempty" jsonschema:"Filter by owner identity (pass '*' for all owners)"`
	Assignee  string          `json:"assignee,omitempty" jsonschema:"Optional filter by assignee identity"`
	AllOwners bool            `json:"all_owners,omitempty" jsonschema:"Set to true to return items across all owners"`
	Status    queue.Status    `json:"status,omitempty" jsonschema:"Optional filter by item status (open, in_progress, blocked, done, cancelled)"`
	Limit     int             `json:"limit,omitempty" jsonschema:"Maximum number of items to return (0 means no limit)"`
	Offset    int             `json:"offset,omitempty" jsonschema:"Number of matching items to skip for pagination"`
}

// GetQueueOutput defines the structured output for the get_queue tool.
type GetQueueOutput struct {
	Queue queue.QueueName `json:"queue" jsonschema:"Name of the queue"`
	Items []queue.Item    `json:"items" jsonschema:"Ordered list of matching items in the queue"`
}

// UpsertItemInput defines the input arguments for the upsert_item tool.
type UpsertItemInput struct {
	Identity string          `json:"identity" jsonschema:"Identity string of the caller"`
	ID       string          `json:"id,omitempty" jsonschema:"Item ID to replace. Omit to insert a new item."`
	Title    string          `json:"title" jsonschema:"Short summary of the item"`
	Queue    queue.QueueName `json:"queue,omitempty" jsonschema:"Target queue (Inbox, Outbox, Needs Attention, Archive). Defaults to Inbox when omitted."`
	Status   queue.Status    `json:"status,omitempty" jsonschema:"Lifecycle status (open, in_progress, blocked, done, cancelled). Defaults to open when omitted. Setting done or cancelled automatically moves the item to Archive."`
	Position int             `json:"position,omitempty" jsonschema:"1-indexed position in the target queue. Omit (0) to append on insert/move or keep current position on update."`
	Text     string          `json:"text,omitempty" jsonschema:"Detailed description or body text"`
	Comments []queue.Comment `json:"comments,omitempty" jsonschema:"Optional thread of comments on the item"`
	Data     map[string]any  `json:"data,omitempty" jsonschema:"Arbitrary structured key-value metadata"`
	Links    []string        `json:"links,omitempty" jsonschema:"List of URLs, URIs, or item references"`
	Owner    string          `json:"owner,omitempty" jsonschema:"Owner (requester) identity string (defaults to caller identity on insert; preserved on update when omitted)"`
	Assignee string          `json:"assignee,omitempty" jsonschema:"Assignee (active worker) identity string (defaults to caller identity when status is set to in_progress)"`
}

// UpsertItemOutput defines the structured output for the upsert_item tool.
type UpsertItemOutput struct {
	Item queue.Item `json:"item" jsonschema:"The created or updated item"`
}

// AddCommentInput defines the input arguments for the add_comment tool.
type AddCommentInput struct {
	Identity string `json:"identity" jsonschema:"Identity string of the caller (used as comment author)"`
	ID       string `json:"id" jsonschema:"ID of the item to append a comment to"`
	Text     string `json:"text" jsonschema:"Markdown or plain text comment content"`
}

// AddCommentOutput defines the structured output for the add_comment tool.
type AddCommentOutput struct {
	Item queue.Item `json:"item" jsonschema:"The updated item with the appended comment"`
}

// New creates and configures a new MCP Server backed by store.
func New(store queue.Store) *mcp.Server {
	caps := &mcp.ServerCapabilities{
		Resources: &mcp.ResourceCapabilities{},
		Tools:     &mcp.ToolCapabilities{},
	}
	caps.AddExtension(MCPAppsExtensionID, nil)

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "qmcp",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Capabilities: caps,
	})

	registerMCPAppResource(srv)

	addA2UITool(srv, store, "list_queues",
		"List all queues along with total item counts and caller-owned item counts, and render a Kanban board A2UI surface.",
		"a2ui://qmcp/queues", execListQueues)

	addA2UITool(srv, store, "get_queue",
		"Get items in a queue ordered by 1-indexed position ascending. Filters by owner == identity by default unless owner or all_owners is specified.",
		"a2ui://qmcp/queue", execGetQueue)

	addA2UITool(srv, store, "upsert_item",
		"Insert a new queue item (if id is omitted) or replace an existing item (if id is provided). Setting status to done or cancelled automatically moves the item to Archive.",
		"a2ui://qmcp/item", execUpsertItem)

	addA2UITool(srv, store, "add_comment",
		"Append a comment to an existing queue item's comment thread.",
		"a2ui://qmcp/item", execAddComment)

	return srv
}

type toolFunc[In, Out any] func(ctx context.Context, store queue.Store, in In) (Out, []a2ui.AgentMessage, error)

func addA2UITool[In, Out any](srv *mcp.Server, store queue.Store, name, desc, uri string, fn toolFunc[In, Out]) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        name,
		Description: desc,
		Meta:        mcpAppToolMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, msgs, err := fn(ctx, store, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return toolResultWithA2UI(uri, out, msgs)
	})
}

func callToolJSON[In, Out any](ctx context.Context, store queue.Store, raw json.RawMessage, fn toolFunc[In, Out]) (any, []a2ui.AgentMessage, error) {
	var in In
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	return fn(ctx, store, in)
}

func dispatchTool(ctx context.Context, store queue.Store, name string, raw json.RawMessage) (any, []a2ui.AgentMessage, error) {
	switch name {
	case "list_queues":
		return callToolJSON(ctx, store, raw, execListQueues)
	case "get_queue":
		return callToolJSON(ctx, store, raw, execGetQueue)
	case "upsert_item":
		return callToolJSON(ctx, store, raw, execUpsertItem)
	case "add_comment":
		return callToolJSON(ctx, store, raw, execAddComment)
	default:
		return nil, nil, fmt.Errorf("unknown tool %q", name)
	}
}

func execListQueues(ctx context.Context, store queue.Store, in ListQueuesInput) (ListQueuesOutput, []a2ui.AgentMessage, error) {
	queues, err := store.ListQueues(ctx, in.Identity)
	if err != nil {
		return ListQueuesOutput{}, nil, err
	}
	itemsByQueue, err := loadItemsByQueue(ctx, store, in.Identity, in.AllOwners, queues)
	if err != nil {
		return ListQueuesOutput{}, nil, err
	}
	out := ListQueuesOutput{Queues: queues}
	return out, BuildQueuesSummarySurface(in.Identity, queues, itemsByQueue), nil
}

func execGetQueue(ctx context.Context, store queue.Store, in GetQueueInput) (GetQueueOutput, []a2ui.AgentMessage, error) {
	items, err := store.GetQueue(ctx, queue.GetQueueOptions{
		Identity:  in.Identity,
		Queue:     in.Queue,
		Owner:     in.Owner,
		Assignee:  in.Assignee,
		AllOwners: in.AllOwners,
		Status:    in.Status,
		Limit:     in.Limit,
		Offset:    in.Offset,
	})
	if err != nil {
		return GetQueueOutput{}, nil, err
	}
	out := GetQueueOutput{Queue: in.Queue, Items: items}
	return out, BuildQueueSurface(in.Identity, in.Queue, items), nil
}

func execUpsertItem(ctx context.Context, store queue.Store, in UpsertItemInput) (UpsertItemOutput, []a2ui.AgentMessage, error) {
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
		Assignee: in.Assignee,
	})
	if err != nil {
		return UpsertItemOutput{}, nil, err
	}
	out := UpsertItemOutput{Item: item}
	return out, BuildItemSurface(in.Identity, item), nil
}

func execAddComment(ctx context.Context, store queue.Store, in AddCommentInput) (AddCommentOutput, []a2ui.AgentMessage, error) {
	item, err := store.AddComment(ctx, in.Identity, in.ID, in.Text)
	if err != nil {
		return AddCommentOutput{}, nil, err
	}
	out := AddCommentOutput{Item: item}
	return out, BuildItemSurface(in.Identity, item), nil
}
