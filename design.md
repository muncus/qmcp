# Q MCP (Queue MCP)

An MCP server written in Go (`github.com/modelcontextprotocol/go-sdk`) that models a shared collection of task queues for multi-agent and multi-user workflows.

## Queues

For now, the server provides a fixed set of global queues (designed to allow custom queues in the future):
- `Inbox`
- `Outbox`
- `Needs Attention`
- `Archive` (for finished or cancelled tasks)

Queues are global across the server. Every MCP tool call accepts a caller `identity` string:
- **Reads (`list_queues`, `get_queue`):** Filter items where `owner == identity` by default, with an option to view items across all owners (or filter by a specific `owner`).
- **Writes (`upsert_item`):** Default `owner` to the caller's `identity` when inserting a new item if `owner` is not explicitly specified.

## Data Model

### Queue
- `name` (`string`): Queue identifier (`Inbox`, `Outbox`, `Needs Attention`, `Archive`).
- `items` (`[]Item`): Ordered list of items currently in the queue.

### Item
- `id` (`string`): Unique server-generated identifier (e.g., UUID/short ID).
- `title` (`string`): Short summary of the item (required on insert).
- `status` (`string`): Lifecycle state: `open`, `in_progress`, `blocked`, `done`, `cancelled` (defaults to `open` on insert).
- `queue` (`string`): Current queue name (defaults to `Inbox` on insert).
  - **Auto-archiving:** Setting `status` to `done` or `cancelled` automatically moves `queue` to `Archive`.
- `position` (`int`): Explicit 0-indexed position within the item's current queue.
  - Omitting `position` on insert or queue change appends the item to the end of the target queue.
  - Specifying `position` inserts/moves the item to that index and shifts subsequent items in that queue.
- `text` (`string`, optional): Detailed description or body text.
- `data` (`map[string]any`, optional): Arbitrary structured JSON key-value metadata.
- `links` (`[]string`, optional): List of URLs, URIs, or item references.
- `owner` (`string`): Identity string of the item's owner (defaults to caller's `identity` on insert).
- `updated_at` (`time.Time` / RFC3339 `string`): Timestamp automatically updated on insert and any modification.

## MCP Tools

All tools require an `identity` (`string`) parameter identifying the caller.

### 1. `list_queues`
Returns all available queues along with item counts (`total_count` and `caller_count` where `owner == identity`).
- **Inputs:**
  - `identity` (`string`, required)

### 2. `get_queue`
Returns items in a specified queue ordered by `position` ascending.
- **Inputs:**
  - `identity` (`string`, required)
  - `queue` (`string`, required): Name of the queue.
  - `owner` (`string`, optional): Filter by owner. Defaults to `identity`. Pass `"*"` (or `all_owners: true`) to return items for all owners.
  - `status` (`string`, optional): Filter by item status.
  - `limit` (`int`, optional): Maximum number of items to return.
  - `offset` (`int`, optional): Number of items to skip for pagination.

### 3. `upsert_item`
Creates a new item if `id` is omitted, or partially updates (patches) an existing item if `id` is provided.
- **Inputs:**
  - `identity` (`string`, required)
  - `id` (`string`, optional): Item ID to update. If omitted, inserts a new item.
  - `title` (`string`, optional on update, required on insert)
  - `queue` (`string`, optional): Defaults to `Inbox` on insert. Ignored/overridden to `Archive` if `status` is set to `done` or `cancelled`.
  - `status` (`string`, optional): Defaults to `open` on insert.
  - `position` (`int`, optional): Target 0-indexed position in the queue.
  - `text` (`string`, optional)
  - `data` (`map[string]any`, optional)
  - `links` (`[]string`, optional)
  - `owner` (`string`, optional): Defaults to `identity` on insert.
- **Returns:** The created or updated `Item`.

## Architecture & Persistence

- **Language & SDK:** Go using the official MCP SDK (`github.com/modelcontextprotocol/go-sdk`).
- **Transport:** `stdio` by default, with an optional CLI flag for HTTP/SSE transport.
- **Storage Interface:** A clean `Store` interface decoupling tool handlers from persistence so the backend can easily be swapped to SQLite in the future.
- **JSONL Backend (Initial):**
  - Configured via CLI flag / environment variable (e.g., `-store-file` / `QMCP_STORE_FILE`).
  - Loads all items into memory at startup, protects state with a mutex (`sync.RWMutex`), and flushes the current items (one JSON object per line) to the JSONL file on every write.
