package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
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
	Owner     string          `json:"owner,omitempty" jsonschema:"Filter by owner identity (defaults to caller identity; pass '*' for all owners)"`
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
	Data     map[string]any  `json:"data,omitempty" jsonschema:"Arbitrary structured key-value metadata"`
	Links    []string        `json:"links,omitempty" jsonschema:"List of URLs, URIs, or item references"`
	Owner    string          `json:"owner,omitempty" jsonschema:"Owner identity string (defaults to caller identity when omitted)"`
}

// UpsertItemOutput defines the structured output for the upsert_item tool.
type UpsertItemOutput struct {
	Item queue.Item `json:"item" jsonschema:"The created or updated item"`
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

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_queues",
		Description: "List all queues along with total item counts and caller-owned item counts, and render a Kanban board A2UI surface.",
		Meta:        mcpAppToolMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListQueuesInput) (*mcp.CallToolResult, ListQueuesOutput, error) {
		queues, err := store.ListQueues(ctx, in.Identity)
		if err != nil {
			return nil, ListQueuesOutput{}, err
		}
		itemsByQueue, err := loadItemsByQueue(ctx, store, in.Identity, in.AllOwners, queues)
		if err != nil {
			return nil, ListQueuesOutput{}, err
		}
		out := ListQueuesOutput{Queues: queues}
		msgs := BuildQueuesSummarySurface(in.Identity, queues, itemsByQueue)
		return toolResultWithA2UI("a2ui://qmcp/queues", out, msgs)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_queue",
		Description: "Get items in a queue ordered by 1-indexed position ascending. Filters by owner == identity by default unless owner or all_owners is specified.",
		Meta:        mcpAppToolMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetQueueInput) (*mcp.CallToolResult, GetQueueOutput, error) {
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
			return nil, GetQueueOutput{}, err
		}
		out := GetQueueOutput{
			Queue: in.Queue,
			Items: items,
		}
		msgs := BuildQueueSurface(in.Identity, in.Queue, items)
		return toolResultWithA2UI("a2ui://qmcp/queue", out, msgs)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "upsert_item",
		Description: "Insert a new queue item (if id is omitted) or replace an existing item (if id is provided). Setting status to done or cancelled automatically moves the item to Archive.",
		Meta:        mcpAppToolMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpsertItemInput) (*mcp.CallToolResult, UpsertItemOutput, error) {
		item, err := store.UpsertItem(ctx, in.Identity, queue.Item{
			ID:       in.ID,
			Title:    in.Title,
			Queue:    in.Queue,
			Status:   in.Status,
			Position: in.Position,
			Text:     in.Text,
			Data:     in.Data,
			Links:    in.Links,
			Owner:    in.Owner,
		})
		if err != nil {
			return nil, UpsertItemOutput{}, err
		}
		out := UpsertItemOutput{Item: item}
		msgs := BuildItemSurface(in.Identity, item)
		return toolResultWithA2UI("a2ui://qmcp/item", out, msgs)
	})

	return srv
}
