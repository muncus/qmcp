package queue

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Store defines the persistence operations for queues and items.
type Store interface {
	ListQueues(ctx context.Context, identity string) ([]QueueSummary, error)
	GetQueue(ctx context.Context, opts GetQueueOptions) ([]Item, error)
	UpsertItem(ctx context.Context, identity string, item Item) (Item, error)
}

// JSONLStore is an in-memory Store implementation that loads items from a JSONL
// file at startup and atomically flushes the state to disk on every write.
type JSONLStore struct {
	mu       sync.RWMutex
	filePath string
	queues   map[QueueName][]Item
	now      func() time.Time
}

// NewJSONLStore creates a new JSONLStore backed by filePath.
// If filePath does not exist yet, it starts empty and creates the file on first write.
// If filePath is empty, the store operates purely in-memory.
func NewJSONLStore(filePath string) (*JSONLStore, error) {
	s := &JSONLStore{
		filePath: filePath,
		queues:   make(map[QueueName][]Item, len(StandardQueues)),
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, q := range StandardQueues {
		s.queues[q] = nil
	}
	if filePath != "" {
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *JSONLStore) load() error {
	f, err := os.Open(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open store file %q: %w", s.filePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow up to 16MB per JSONL line.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item Item
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return fmt.Errorf("parse %q line %d: %w", s.filePath, lineNum, err)
		}
		if item.ID == "" {
			return fmt.Errorf("parse %q line %d: item missing id", s.filePath, lineNum)
		}
		if item.Queue == "" {
			item.Queue = QueueInbox
		}
		if item.Status == "" {
			item.Status = StatusOpen
		}
		if oldQueue, _, ok := s.findByIDLocked(item.ID); ok {
			s.removeFromQueueLocked(oldQueue, item.ID)
		}
		s.queues[item.Queue] = append(s.queues[item.Queue], item.Clone())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan store file %q: %w", s.filePath, err)
	}

	// Sort each queue by Position ascending (tie-breaking by UpdatedAt, then ID) and normalize 1-indexed positions.
	for q, list := range s.queues {
		slices.SortFunc(list, func(a, b Item) int {
			if a.Position != b.Position {
				return a.Position - b.Position
			}
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				if a.UpdatedAt.Before(b.UpdatedAt) {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
		for i := range list {
			list[i].Position = i + 1
		}
		s.queues[q] = list
	}
	return nil
}

func (s *JSONLStore) flushLocked() error {
	if s.filePath == "" {
		return nil
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create store directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".qmcp-*.jsonl.tmp")
	if err != nil {
		return fmt.Errorf("create temp store file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	bw := bufio.NewWriter(tmp)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)

	for _, q := range StandardQueues {
		for _, it := range s.queues[q] {
			if err := enc.Encode(it); err != nil {
				return fmt.Errorf("encode item %q: %w", it.ID, err)
			}
		}
	}

	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush store buffer: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp store file: %w", err)
	}
	if err := os.Rename(tmpName, s.filePath); err != nil {
		return fmt.Errorf("rename temp store file to %q: %w", s.filePath, err)
	}
	cleanup = false
	return nil
}

// ListQueues returns summaries for all standard queues.
func (s *JSONLStore) ListQueues(_ context.Context, identity string) ([]QueueSummary, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, errors.New("identity is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	summaries := make([]QueueSummary, 0, len(StandardQueues))
	for _, q := range StandardQueues {
		list := s.queues[q]
		callerCount := 0
		for _, it := range list {
			if it.Owner == identity {
				callerCount++
			}
		}
		summaries = append(summaries, QueueSummary{
			Name:        q,
			TotalCount:  len(list),
			CallerCount: callerCount,
		})
	}
	return summaries, nil
}

// GetQueue returns items in a queue matching the specified filters, ordered by position ascending.
func (s *JSONLStore) GetQueue(_ context.Context, opts GetQueueOptions) ([]Item, error) {
	opts.Identity = strings.TrimSpace(opts.Identity)
	if opts.Identity == "" {
		return nil, errors.New("identity is required")
	}
	if err := ValidateQueue(opts.Queue); err != nil {
		return nil, err
	}
	if opts.Status != "" {
		if err := ValidateStatus(opts.Status); err != nil {
			return nil, err
		}
	}
	if opts.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if opts.Limit < 0 {
		return nil, errors.New("limit must be non-negative")
	}

	ownerFilter := strings.TrimSpace(opts.Owner)
	allOwners := opts.AllOwners || ownerFilter == "*"
	if !allOwners && ownerFilter == "" {
		ownerFilter = opts.Identity
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	list := s.queues[opts.Queue]
	matched := make([]Item, 0, len(list))
	for _, it := range list {
		if !allOwners && it.Owner != ownerFilter {
			continue
		}
		if opts.Status != "" && it.Status != opts.Status {
			continue
		}
		matched = append(matched, it.Clone())
	}

	if opts.Offset >= len(matched) {
		return []Item{}, nil
	}
	matched = matched[opts.Offset:]
	if opts.Limit > 0 && opts.Limit < len(matched) {
		matched = matched[:opts.Limit]
	}
	return matched, nil
}

// UpsertItem creates a new item if item.ID is empty, or replaces an existing item if item.ID is set.
func (s *JSONLStore) UpsertItem(_ context.Context, identity string, item Item) (Item, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return Item{}, errors.New("identity is required")
	}
	if strings.TrimSpace(item.Title) == "" {
		return Item{}, errors.New("title is required")
	}

	item.ID = strings.TrimSpace(item.ID)
	if item.Status == "" {
		item.Status = StatusOpen
	} else if err := ValidateStatus(item.Status); err != nil {
		return Item{}, err
	}

	if item.Queue == "" {
		item.Queue = QueueInbox
	} else if err := ValidateQueue(item.Queue); err != nil {
		return Item{}, err
	}
	if IsTerminalStatus(item.Status) {
		item.Queue = QueueArchive
	}

	item.Owner = strings.TrimSpace(item.Owner)
	if item.Owner == "" {
		item.Owner = identity
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	item.UpdatedAt = s.now()
	stored := item.Clone()

	if stored.ID == "" {
		id, err := s.generateUniqueIDLocked()
		if err != nil {
			return Item{}, err
		}
		stored.ID = id
		prevQueue := slices.Clone(s.queues[stored.Queue])
		stored = s.insertIntoQueueLocked(stored.Queue, stored, stored.Position)
		if err := s.flushLocked(); err != nil {
			s.queues[stored.Queue] = prevQueue
			return Item{}, err
		}
		return stored.Clone(), nil
	}

	oldQueue, oldIdx, ok := s.findByIDLocked(stored.ID)
	if !ok {
		return Item{}, fmt.Errorf("item %q not found", stored.ID)
	}
	oldPos := oldIdx + 1

	targetPos := stored.Position
	if stored.Queue == oldQueue && targetPos <= 0 {
		targetPos = oldPos
	}

	prevOldQueue := slices.Clone(s.queues[oldQueue])
	prevNewQueue := slices.Clone(s.queues[stored.Queue])

	s.removeFromQueueLocked(oldQueue, stored.ID)
	stored = s.insertIntoQueueLocked(stored.Queue, stored, targetPos)

	if err := s.flushLocked(); err != nil {
		s.queues[oldQueue] = prevOldQueue
		s.queues[stored.Queue] = prevNewQueue
		return Item{}, err
	}
	return stored.Clone(), nil
}

func (s *JSONLStore) findByIDLocked(id string) (QueueName, int, bool) {
	for _, q := range StandardQueues {
		list := s.queues[q]
		idx := slices.IndexFunc(list, func(it Item) bool { return it.ID == id })
		if idx != -1 {
			return q, idx, true
		}
	}
	return "", -1, false
}

// insertIntoQueueLocked inserts item into queue q at 1-indexed pos (<= 0 means append),
// renumbers all items in q to 1..len, and returns the stored Item with its assigned Position.
func (s *JSONLStore) insertIntoQueueLocked(q QueueName, item Item, pos int) Item {
	item.Queue = q
	list := s.queues[q]
	idx := len(list)
	if pos > 0 && pos-1 < len(list) {
		idx = pos - 1
	}
	list = slices.Insert(list, idx, item)
	for i := range list {
		list[i].Position = i + 1
	}
	s.queues[q] = list
	return list[idx]
}

func (s *JSONLStore) removeFromQueueLocked(q QueueName, id string) {
	list := s.queues[q]
	idx := slices.IndexFunc(list, func(it Item) bool { return it.ID == id })
	if idx == -1 {
		return
	}
	list = slices.Delete(list, idx, idx+1)
	for i := range list {
		list[i].Position = i + 1
	}
	s.queues[q] = list
}

func (s *JSONLStore) generateUniqueIDLocked() (string, error) {
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("generate random id: %w", err)
		}
		id := hex.EncodeToString(b[:])
		if _, _, exists := s.findByIDLocked(id); !exists {
			return id, nil
		}
	}
	return "", errors.New("failed to generate unique item id")
}
