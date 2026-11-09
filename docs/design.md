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
- **Writes (`upsert_item`, `add_comment`):** Default `owner` / `author` to the caller's `identity` if not explicitly specified.

## Data Model

### Queue
- `name` (`string`): Queue identifier (`Inbox`, `Outbox`, `Needs Attention`, `Archive`).
- `items` (`[]Item`): Ordered list of items currently in the queue.

### Item
- `id` (`string`): Unique server-generated identifier.
- `title` (`string`): Short summary of the item (required).
- `status` (`string`): Lifecycle state: `open`, `in_progress`, `blocked`, `done`, `cancelled` (defaults to `open` when omitted).
- `queue` (`string`): Current queue name (defaults to `Inbox` when omitted).
  - **Auto-archiving:** Setting `status` to `done` or `cancelled` automatically moves `queue` to `Archive`.
- `position` (`int`): Explicit 1-indexed position within the item's current queue (`1` = first item).
  - Omitting `position` (`0`) on insert or queue change appends the item to the end of the target queue.
  - Omitting `position` (`0`) when updating an item in the same queue preserves its current position.
  - Specifying `position >= 1` inserts/moves the item to that 1-based position and shifts subsequent items in that queue.
- `text` (`string`, optional): Detailed description or body text (server-rendered Markdown).
- `comments` (`[]Comment`, optional): Ordered thread of comments (`author`, `text`, `created_at`) on the item.
- `data` (`map[string]any`, optional): Arbitrary structured JSON key-value metadata.
- `links` (`[]string`, optional): List of URLs, URIs, or item references.
- `owner` (`string`): Identity string of the item's owner (defaults to caller's `identity` when omitted).
- `updated_at` (`time.Time` / RFC3339 `string`): Timestamp automatically updated on insert and any modification.

## MCP Tools

All tools require an `identity` (`string`) parameter identifying the caller.

### 1. `list_queues`
Returns all available queues along with item counts (`total_count` and `caller_count` where `owner == identity`).
- **Inputs:**
  - `identity` (`string`, required)
  - `all_owners` (`bool`, optional)

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
Creates a new item if `id` is omitted, or replaces an existing item if `id` is provided.
- **Inputs:**
  - `identity` (`string`, required)
  - `id` (`string`, optional): Item ID to update. If omitted, inserts a new item.
  - `title` (`string`, required)
  - `queue` (`string`, optional): Defaults to `Inbox` when omitted. Overridden to `Archive` if `status` is `done` or `cancelled`.
  - `status` (`string`, optional): Defaults to `open` when omitted.
  - `position` (`int`, optional): Target 1-indexed position in the queue (`0` means default/append or keep position).
  - `text` (`string`, optional)
  - `comments` (`[]Comment`, optional)
  - `data` (`map[string]any`, optional)
  - `links` (`[]string`, optional)
  - `owner` (`string`, optional): Defaults to `identity` when omitted.
- **Returns:** The created or updated `Item`.

### 4. `add_comment`
Appends a structured comment (`author`, `text`, `created_at`) to an existing item's comment thread.
- **Inputs:**
  - `identity` (`string`, required)
  - `id` (`string`, required)
  - `text` (`string`, required)
- **Returns:** The updated `Item`.

## Architecture & Persistence

- **Language & SDK:** Go using the official MCP SDK (`github.com/modelcontextprotocol/go-sdk`) and A2UI v1.0 (`github.com/tmc/a2ui`).
- **Transport:** `stdio` by default, with an optional `-http` flag for Streamable HTTP (`/mcp`) and live browser Kanban UI (`/`).
- **Storage Interface:** A clean `Store` interface supporting:
  - **JSONL Backend (default):** Configured via `-store-file` / `QMCP_STORE_FILE`.
  - **Google Cloud Firestore Backend:** Configured via `-firestore-project` / `QMCP_FIRESTORE_PROJECT`.
