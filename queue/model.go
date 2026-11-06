package queue

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// QueueName identifies a named queue.
type QueueName string

const (
	QueueInbox          QueueName = "Inbox"
	QueueOutbox         QueueName = "Outbox"
	QueueNeedsAttention QueueName = "Needs Attention"
	QueueArchive        QueueName = "Archive"
)

// StandardQueues defines the fixed set of queues in canonical order.
var StandardQueues = []QueueName{
	QueueInbox,
	QueueOutbox,
	QueueNeedsAttention,
	QueueArchive,
}

// ValidateQueue checks whether q is one of the supported standard queues.
func ValidateQueue(q QueueName) error {
	if slices.Contains(StandardQueues, q) {
		return nil
	}
	return fmt.Errorf("invalid queue %q (valid queues: Inbox, Outbox, Needs Attention, Archive)", q)
}

// Status represents the lifecycle state of an Item.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

// ValidStatuses lists all supported item statuses.
var ValidStatuses = []Status{
	StatusOpen,
	StatusInProgress,
	StatusBlocked,
	StatusDone,
	StatusCancelled,
}

// ValidateStatus checks whether s is a valid item status.
func ValidateStatus(s Status) error {
	if slices.Contains(ValidStatuses, s) {
		return nil
	}
	return fmt.Errorf("invalid status %q (valid statuses: open, in_progress, blocked, done, cancelled)", s)
}

// IsTerminalStatus returns true if the status should automatically move the item to Archive.
func IsTerminalStatus(s Status) bool {
	return s == StatusDone || s == StatusCancelled
}

// Item represents a single task/message in a queue.
type Item struct {
	ID        string         `json:"id" jsonschema:"Unique identifier of the item"`
	Title     string         `json:"title" jsonschema:"Short summary title of the item"`
	Status    Status         `json:"status" jsonschema:"Lifecycle status (open, in_progress, blocked, done, cancelled)"`
	Queue     QueueName      `json:"queue" jsonschema:"Queue name (Inbox, Outbox, Needs Attention, Archive)"`
	Position  int            `json:"position" jsonschema:"1-indexed position within the queue"`
	Text      string         `json:"text,omitempty" jsonschema:"Detailed description or body text"`
	Data      map[string]any `json:"data,omitempty" jsonschema:"Arbitrary structured key-value metadata"`
	Links     []string       `json:"links,omitempty" jsonschema:"List of URLs, URIs, or item references"`
	Owner     string         `json:"owner" jsonschema:"Identity string of the item owner"`
	UpdatedAt time.Time      `json:"updated_at" jsonschema:"RFC3339 timestamp of last modification"`
}

// Clone returns a deep copy of the Item so callers cannot mutate store state.
func (it Item) Clone() Item {
	out := it
	out.Data = maps.Clone(it.Data)
	out.Links = slices.Clone(it.Links)
	return out
}

// QueueSummary summarizes a queue for list_queues.
type QueueSummary struct {
	Name        QueueName `json:"name" jsonschema:"Name of the queue"`
	TotalCount  int       `json:"total_count" jsonschema:"Total number of items in the queue across all owners"`
	CallerCount int       `json:"caller_count" jsonschema:"Number of items in the queue owned by the caller identity"`
}

// GetQueueOptions specifies filtering and pagination parameters for GetQueue.
type GetQueueOptions struct {
	Identity  string
	Queue     QueueName
	Owner     string
	AllOwners bool
	Status    Status
	Limit     int
	Offset    int
}
