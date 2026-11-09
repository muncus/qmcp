# QMCP Agent Usage Guide

QMCP provides a shared task queue across four queues: `Inbox`, `Outbox`, `Needs Attention`, and `Archive`.

## Agent Workflow Rules

1. **One item at a time:** Only claim and work on a single `Inbox` item at any given time. Never have multiple items `in_progress` simultaneously.
2. **Claim before working:** Before starting work on an item, call `upsert_item` to:
   - Set `status: "in_progress"`
   - Set `owner` to your agent identity (remember the original `owner` in `data` or a comment if needed so you can reassign on handoff).
3. **Preserve fields on `upsert_item`:** `upsert_item` replaces the item's fields (`title`, `queue`, `status`, `text`, `links`, `data`, `owner`). Always pass the existing `title`, `text`, `links`, and `data` when updating an item. Use `add_comment` to append status updates, questions, or completion notes.
4. **If blocked or clarification is needed:**
   - Call `add_comment` with your question.
   - Move the item to `queue: "Needs Attention"` (and set `status: "blocked"` or reassign `owner` to the user).
   - Do not guess on ambiguous requirements.
5. **On completion:**
   - Call `add_comment` summarizing what was done.
   - Call `upsert_item` to move the item to `queue: "Outbox"` with `status: "open"` (do not set `done` unless archiving; `done` and `cancelled` auto-move items to `Archive`).

## MCP Tools Reference

All tools require `identity` (your agent identifier).

- **`list_queues`** `{ "identity": "<you>", "all_owners": true }`
  Returns queue counts (`Inbox`, `Outbox`, `Needs Attention`, `Archive`).
- **`get_queue`** `{ "identity": "<you>", "queue": "Inbox", "all_owners": true }`
  Returns items in a queue ordered by 1-indexed `position` ascending.
- **`upsert_item`** `{ "identity": "<you>", "id": "<item-id>", "title": "...", "queue": "Inbox", "status": "in_progress", "owner": "<you>", "text": "...", "links": [...], "data": {...} }`
  Inserts a new item (when `id` is omitted) or updates an existing item (when `id` is set). `position` is 1-indexed (`0` = keep position or append).
- **`add_comment`** `{ "identity": "<you>", "id": "<item-id>", "text": "..." }`
  Appends a Markdown comment (`author`, `text`, `created_at`) to the item's comment thread.
