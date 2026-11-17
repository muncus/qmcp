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

package queue

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const DefaultFirestoreCollection = "qmcp_items"

type firestoreCommentDoc struct {
	Author    string    `firestore:"author"`
	Text      string    `firestore:"text"`
	CreatedAt time.Time `firestore:"created_at"`
}

type firestoreItemDoc struct {
	ID        string                `firestore:"id"`
	Title     string                `firestore:"title"`
	Status    string                `firestore:"status"`
	Queue     string                `firestore:"queue"`
	Position  int                   `firestore:"position"`
	Text      string                `firestore:"text,omitempty"`
	Comments  []firestoreCommentDoc `firestore:"comments,omitempty"`
	Data      map[string]any        `firestore:"data,omitempty"`
	Links     []string              `firestore:"links,omitempty"`
	Owner     string                `firestore:"owner"`
	Assignee  string                `firestore:"assignee,omitempty"`
	UpdatedAt time.Time             `firestore:"updated_at"`
}

func toFirestoreDoc(it Item) firestoreItemDoc {
	var comments []firestoreCommentDoc
	if len(it.Comments) > 0 {
		comments = make([]firestoreCommentDoc, len(it.Comments))
		for i, c := range it.Comments {
			comments[i] = firestoreCommentDoc{
				Author:    c.Author,
				Text:      c.Text,
				CreatedAt: c.CreatedAt,
			}
		}
	}
	return firestoreItemDoc{
		ID:        it.ID,
		Title:     it.Title,
		Status:    string(it.Status),
		Queue:     string(it.Queue),
		Position:  it.Position,
		Text:      it.Text,
		Comments:  comments,
		Data:      maps.Clone(it.Data),
		Links:     slices.Clone(it.Links),
		Owner:     it.Owner,
		Assignee:  it.Assignee,
		UpdatedAt: it.UpdatedAt,
	}
}

func (d firestoreItemDoc) toItem() Item {
	q := QueueName(d.Queue)
	if q == "" {
		q = QueueInbox
	}
	st := Status(d.Status)
	if st == "" {
		st = StatusOpen
	}
	var comments []Comment
	if len(d.Comments) > 0 {
		comments = make([]Comment, len(d.Comments))
		for i, c := range d.Comments {
			comments[i] = Comment{
				Author:    c.Author,
				Text:      c.Text,
				CreatedAt: c.CreatedAt,
			}
		}
	}
	return Item{
		ID:        d.ID,
		Title:     d.Title,
		Status:    st,
		Queue:     q,
		Position:  d.Position,
		Text:      d.Text,
		Comments:  comments,
		Data:      maps.Clone(d.Data),
		Links:     slices.Clone(d.Links),
		Owner:     d.Owner,
		Assignee:  d.Assignee,
		UpdatedAt: d.UpdatedAt,
	}
}

// FirestoreStore is a Store implementation backed by Google Cloud Firestore.
type FirestoreStore struct {
	client     *firestore.Client
	collection string
	now        func() time.Time
}

// NewFirestoreStore creates a new FirestoreStore connected to projectID.
// If collection is empty, DefaultFirestoreCollection ("qmcp_items") is used.
func NewFirestoreStore(ctx context.Context, projectID, collection string, opts ...option.ClientOption) (*FirestoreStore, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("firestore project ID is required")
	}
	client, err := firestore.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("create firestore client: %w", err)
	}
	return NewFirestoreStoreWithClient(client, collection), nil
}

// NewFirestoreStoreWithClient creates a FirestoreStore using an existing firestore.Client.
func NewFirestoreStoreWithClient(client *firestore.Client, collection string) *FirestoreStore {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		collection = DefaultFirestoreCollection
	}
	return &FirestoreStore{
		client:     client,
		collection: collection,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Close closes the underlying Firestore client.
func (s *FirestoreStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *FirestoreStore) col() *firestore.CollectionRef {
	return s.client.Collection(s.collection)
}

// Subscribe listens to real-time Firestore collection snapshots and emits a
// notification whenever documents in the collection change.
func (s *FirestoreStore) Subscribe(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		defer close(ch)
		it := s.col().Snapshots(ctx)
		defer it.Stop()
		first := true
		for {
			_, err := it.Next()
			if err != nil {
				return
			}
			if first {
				first = false
				continue
			}
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}

// ListQueues returns summaries for all standard queues.
func (s *FirestoreStore) ListQueues(ctx context.Context, identity string) ([]QueueSummary, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, errors.New("identity is required")
	}

	totals := make(map[QueueName]int, len(StandardQueues))
	callers := make(map[QueueName]int, len(StandardQueues))

	iter := s.col().Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate firestore documents: %w", err)
		}
		var d firestoreItemDoc
		if err := doc.DataTo(&d); err != nil {
			return nil, fmt.Errorf("decode document %q: %w", doc.Ref.ID, err)
		}
		it := d.toItem()
		totals[it.Queue]++
		if it.Owner == identity || it.Assignee == identity {
			callers[it.Queue]++
		}
	}

	summaries := make([]QueueSummary, 0, len(StandardQueues))
	for _, q := range StandardQueues {
		summaries = append(summaries, QueueSummary{
			Name:        q,
			TotalCount:  totals[q],
			CallerCount: callers[q],
		})
	}
	return summaries, nil
}

// GetQueue returns items in a queue matching the specified filters, ordered by 1-indexed position ascending.
func (s *FirestoreStore) GetQueue(ctx context.Context, opts GetQueueOptions) ([]Item, error) {
	opts, allOwners, err := validateGetQueueOptions(opts)
	if err != nil {
		return nil, err
	}

	items, err := s.queryQueueItems(ctx, nil, opts.Queue)
	if err != nil {
		return nil, err
	}

	return filterAndPaginateItems(items, opts, allOwners), nil
}

// UpsertItem creates a new item if item.ID is empty, or replaces an existing item if item.ID is set.
func (s *FirestoreStore) UpsertItem(ctx context.Context, identity string, item Item) (Item, error) {
	rawComments := item.Comments
	identity, item, err := validateUpsertInput(identity, item)
	if err != nil {
		return Item{}, err
	}
	item.UpdatedAt = s.now()

	var result Item
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		stored := item.Clone()
		stored.Comments = normalizeComments(stored.Comments, identity, item.UpdatedAt)

		if stored.ID == "" {
			stored = applyInsertDefaults(identity, stored)
			id, err := generateRandomID()
			if err != nil {
				return err
			}
			stored.ID = id

			queueItems, err := s.queryQueueItems(ctx, tx, stored.Queue)
			if err != nil {
				return err
			}
			queueItems, stored = insertAndRenumberItems(queueItems, stored, stored.Position)
			for _, it := range queueItems {
				if err := tx.Set(s.col().Doc(it.ID), toFirestoreDoc(it)); err != nil {
					return err
				}
			}
			result = stored
			return nil
		}

		docSnap, err := tx.Get(s.col().Doc(stored.ID))
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return fmt.Errorf("item %q not found", stored.ID)
			}
			return err
		}
		var existingDoc firestoreItemDoc
		if err := docSnap.DataTo(&existingDoc); err != nil {
			return fmt.Errorf("decode existing item %q: %w", stored.ID, err)
		}
		existing := existingDoc.toItem()
		stored = mergeExistingOnUpdate(identity, stored, existing, rawComments)
		oldQueue := existing.Queue

		oldItems, err := s.queryQueueItems(ctx, tx, oldQueue)
		if err != nil {
			return err
		}
		oldIdx := slices.IndexFunc(oldItems, func(it Item) bool { return it.ID == stored.ID })
		oldPos := existing.Position
		if oldIdx != -1 {
			oldPos = oldIdx + 1
			oldItems = slices.Delete(oldItems, oldIdx, oldIdx+1)
		}

		targetPos := stored.Position
		if stored.Queue == oldQueue && targetPos <= 0 {
			targetPos = oldPos
		}

		if stored.Queue == oldQueue {
			updatedQueue, placed := insertAndRenumberItems(oldItems, stored, targetPos)
			for _, it := range updatedQueue {
				if err := tx.Set(s.col().Doc(it.ID), toFirestoreDoc(it)); err != nil {
					return err
				}
			}
			result = placed
			return nil
		}

		// Moving across queues: renumber both oldQueue and targetQueue.
		for i := range oldItems {
			oldItems[i].Position = i + 1
		}
		newQueueItems, err := s.queryQueueItems(ctx, tx, stored.Queue)
		if err != nil {
			return err
		}
		newQueueItems = slices.DeleteFunc(newQueueItems, func(it Item) bool { return it.ID == stored.ID })
		newQueueItems, stored = insertAndRenumberItems(newQueueItems, stored, targetPos)

		for _, it := range oldItems {
			if err := tx.Set(s.col().Doc(it.ID), toFirestoreDoc(it)); err != nil {
				return err
			}
		}
		for _, it := range newQueueItems {
			if err := tx.Set(s.col().Doc(it.ID), toFirestoreDoc(it)); err != nil {
				return err
			}
		}
		result = stored
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return result.Clone(), nil
}

// AddComment appends a new comment to an existing item's comment thread in Firestore.
func (s *FirestoreStore) AddComment(ctx context.Context, identity, itemID, text string) (Item, error) {
	identity, itemID, text, err := validateAddCommentInput(identity, itemID, text)
	if err != nil {
		return Item{}, err
	}

	var result Item
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		docRef := s.col().Doc(itemID)
		docSnap, err := tx.Get(docRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return fmt.Errorf("item %q not found", itemID)
			}
			return err
		}
		var existingDoc firestoreItemDoc
		if err := docSnap.DataTo(&existingDoc); err != nil {
			return fmt.Errorf("decode existing item %q: %w", itemID, err)
		}
		if existingDoc.ID == "" {
			existingDoc.ID = docRef.ID
		}
		updated := appendCommentToItem(existingDoc.toItem(), identity, text, s.now())
		if err := tx.Set(docRef, toFirestoreDoc(updated)); err != nil {
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return result.Clone(), nil
}

func (s *FirestoreStore) queryQueueItems(ctx context.Context, tx *firestore.Transaction, q QueueName) ([]Item, error) {
	query := s.col().Where("queue", "==", string(q))
	var iter *firestore.DocumentIterator
	if tx != nil {
		iter = tx.Documents(query)
	} else {
		iter = query.Documents(ctx)
	}
	defer iter.Stop()

	var items []Item
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("query queue %q: %w", q, err)
		}
		var d firestoreItemDoc
		if err := doc.DataTo(&d); err != nil {
			return nil, fmt.Errorf("decode document %q: %w", doc.Ref.ID, err)
		}
		if d.ID == "" {
			d.ID = doc.Ref.ID
		}
		items = append(items, d.toItem())
	}

	sortItemsByPosition(items)
	for i := range items {
		items[i].Position = i + 1
	}
	return items, nil
}

func sortItemsByPosition(list []Item) {
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
}

func insertAndRenumberItems(list []Item, item Item, pos int) ([]Item, Item) {
	idx := len(list)
	if pos > 0 && pos-1 < len(list) {
		idx = pos - 1
	}
	list = slices.Insert(list, idx, item)
	for i := range list {
		list[i].Position = i + 1
	}
	return list, list[idx]
}
