# QMCP Agent Usage Guide

QMCP provides a shared task queue across four queues: `Inbox`, `Outbox`, `Needs Attention`, and `Archive`.

## Agent Workflow Rules

1. **One item at a time:** Only claim and work on a single `Inbox` item at any given time. Never have multiple items `in_progress` simultaneously.
2. **Claim before working:** Before starting work on an item, call `upsert_item` to:
   - Set `status: "in_progress"`
   - Set `assignee` to your agent identity (the server also auto-sets `assignee` to the caller's `identity` when `status` is set to `"in_progress"` and `assignee` is omitted, while preserving the original requester `owner`).
3. **Preserve fields on `upsert_item`:** `upsert_item` replaces the item's fields (`title`, `queue`, `status`, `text`, `links`, `data`, `owner`, `assignee`). Always pass the existing `title`, `text`, `links`, `data`, and `owner` when updating an item. Use `add_comment` to append status updates, questions, or completion notes.
4. **If blocked or clarification is needed:**
   - Call `add_comment` with your question.
   - Move the item to `queue: "Needs Attention"` (and set `status: "blocked"`).
   - Do not guess on ambiguous requirements.
5. **On completion:**
   - Call `add_comment` summarizing what was done.
   - Call `upsert_item` to move the item to `queue: "Outbox"` with `status: "open"` (do not set `done` unless archiving; `done` and `cancelled` auto-move items to `Archive`).

## MCP Tools Reference

All tools require `identity` (your agent identifier, including the basename of your project root — e.g. `agy-qmcp`).

- **`list_queues`** `{ "identity": "<you>", "all_owners": true }`
  Returns queue counts (`Inbox`, `Outbox`, `Needs Attention`, `Archive`), where `caller_count` counts items matching `owner == identity || assignee == identity`.
- **`get_queue`** `{ "identity": "<you>", "queue": "Inbox", "all_owners": true }`
  Returns items in a queue ordered by 1-indexed `position` ascending. Can also filter by `owner`, `assignee`, or `status`.
- **`upsert_item`** `{ "identity": "<you>", "id": "<item-id>", "title": "...", "queue": "Inbox", "status": "in_progress", "owner": "<requester>", "assignee": "<you>", "text": "...", "links": [...], "data": {...} }`
  Inserts a new item (when `id` is omitted) or updates an existing item (when `id` is set). `position` is 1-indexed (`0` = keep position or append).
- **`add_comment`** `{ "identity": "<you>", "id": "<item-id>", "text": "..." }`
  Appends a Markdown comment (`author`, `text`, `created_at`) to the item's comment thread.
