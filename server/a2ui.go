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
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/tmc/a2ui"
	"github.com/tmc/a2ui/a2a"
	"github.com/tmc/a2ui/a2uibuild"
	"github.com/tmc/a2ui/a2uistate"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

func renderMarkdownHTML(md string) string {
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(md), &buf); err != nil {
		return md
	}
	return buf.String()
}

func truncateCardText(s string) string {
	if len(s) >= 200 {
		return s[:200] + "…"
	}
	return s
}

func cardSummaryText(it queue.Item) string {
	if len(it.Comments) > 0 {
		last := it.Comments[len(it.Comments)-1]
		return fmt.Sprintf("**%s:** %s", last.Author, last.Text)
	}
	return it.Text
}

const maxKnownIdentities = 20

func collectIdentities(caller string, items ...queue.Item) []string {
	lastUsed := make(map[string]time.Time)
	record := func(id string, ts time.Time) {
		if id == "" {
			return
		}
		if prev, ok := lastUsed[id]; !ok || ts.After(prev) {
			lastUsed[id] = ts
		}
	}
	if caller != "" {
		var latest time.Time
		for _, it := range items {
			if it.UpdatedAt.After(latest) {
				latest = it.UpdatedAt
			}
		}
		record(caller, latest.Add(time.Nanosecond))
	}
	for _, it := range items {
		record(it.Owner, it.UpdatedAt)
		record(it.Assignee, it.UpdatedAt)
		for _, c := range it.Comments {
			ts := c.CreatedAt
			if ts.IsZero() {
				ts = it.UpdatedAt
			}
			record(c.Author, ts)
		}
	}
	out := make([]string, 0, len(lastUsed))
	for id := range lastUsed {
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b string) int {
		if c := lastUsed[b].Compare(lastUsed[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	if len(out) > maxKnownIdentities {
		out = out[:maxKnownIdentities]
	}
	return out
}

const (
	SurfaceQueuesID = "qmcp-queues"
	SurfaceQueueID  = "qmcp-queue"
	SurfaceItemID   = "qmcp-item"
)

// BuildQueuesSummarySurface builds a multi-column A2UI Kanban board surface
// using the basic catalog, displaying all queues as columns with draggable item cards.
func BuildQueuesSummarySurface(identity string, queues []queue.QueueSummary, itemsByQueue map[queue.QueueName][]queue.Item) []a2ui.AgentMessage {
	surf := a2uibuild.NewSurface(SurfaceQueuesID, a2ui.BasicCatalogID)

	surf = surf.Add(a2uibuild.Text("header", a2ui.StringBinding("/title")))
	surf = surf.Add(a2uibuild.Divider("header-divider"))

	laneIDs := make([]string, 0, len(queues))
	queueData := make([]any, 0, len(queues))

	laneIdx := 0
	for _, q := range queues {
		if q.Name == queue.QueueArchive {
			continue
		}
		i := laneIdx
		laneIdx++
		p := fmt.Sprintf("lane-%d", i)
		nameID := p + "-name"
		countsID := p + "-counts"
		btnLabelID := p + "-open-label"
		btnID := p + "-open-btn"
		hdrRowID := p + "-header"
		divID := p + "-divider"
		itemsColID := p + "-items"
		laneColID := p + "-col"
		laneCardID := p + "-card"

		surf = surf.Add(a2uibuild.Text(nameID, a2ui.StringBinding(fmt.Sprintf("/queues/%d/name", i))))
		surf = addCaptionText(surf, countsID, fmt.Sprintf("%d mine • %d total", q.CallerCount, q.TotalCount))
		surf = addActionBtn(surf, btnID, btnLabelID, "Focus", a2ui.ButtonVariantBorderless, "get_queue", map[string]a2ui.DynamicValue{
			"identity": a2ui.ValueString(identity),
			"queue":    a2ui.ValueString(string(q.Name)),
		})

		hdrRow := a2uibuild.Row(hdrRowID, a2uibuild.Children(nameID, countsID, btnID))
		hdrRow.Row.Align = a2ui.LayoutAlignCenter
		surf = surf.Add(hdrRow)
		surf = surf.Add(a2uibuild.Divider(divID))

		qItems := itemsByQueue[q.Name]
		itemData := make([]any, 0, len(qItems))
		itemCardIDs := make([]string, 0, max(1, len(qItems)))

		if len(qItems) == 0 {
			emptyID := p + "-empty"
			surf = addCaptionText(surf, emptyID, "Empty — drag an item here")
			itemCardIDs = append(itemCardIDs, emptyID)
		} else {
			for j, it := range qItems {
				ip := fmt.Sprintf("%s-item-%d", p, j)
				cardID := ip + "-card"
				dataPath := fmt.Sprintf("/queues/%d/items/%d", i, j)
				surf = addItemCardComponents(surf, identity, ip, cardID, dataPath, it)
				itemCardIDs = append(itemCardIDs, cardID)
				itemData = append(itemData, itemToDataMap(it))
			}
		}

		surf = surf.Add(a2uibuild.Column(itemsColID, a2uibuild.Children(itemCardIDs...)))
		surf = surf.Add(a2uibuild.Column(laneColID, a2uibuild.Children(hdrRowID, divID, itemsColID)))

		laneCard := a2uibuild.Card(laneCardID, laneColID)
		laneCard.Weight = new(1.0)
		laneCard.Metadata = &a2ui.Metadata{
			Extensions: map[string]any{
				"qmcp_drop_zone": map[string]any{
					"action":   "upsert_item",
					"identity": identity,
					"queue":    string(q.Name),
				},
			},
		}
		surf = surf.Add(laneCard)

		laneIDs = append(laneIDs, laneCardID)
		queueData = append(queueData, map[string]any{
			"name":         string(q.Name),
			"total_count":  q.TotalCount,
			"caller_count": q.CallerCount,
			"items":        itemData,
		})
	}

	boardRow := a2uibuild.Row("kanban-board", a2uibuild.Children(laneIDs...))
	boardRow.Row.Align = a2ui.LayoutAlignStart
	surf = surf.Add(boardRow)

	surf = surf.Add(a2uibuild.Column(a2uistate.RootID, a2uibuild.Children("header", "header-divider", "kanban-board")))

	var allItems []queue.Item
	for _, q := range queues {
		allItems = append(allItems, itemsByQueue[q.Name]...)
	}

	surf = surf.WithData(map[string]any{
		"identity":   identity,
		"identities": collectIdentities(identity, allItems...),
		"title":      fmt.Sprintf("Kanban Overview (%s)", identity),
		"queues":     queueData,
	})

	return surf.Messages()
}

// BuildQueueSurface builds an A2UI surface using the basic catalog displaying
// the items in a specific queue.
func BuildQueueSurface(identity string, qName queue.QueueName, items []queue.Item) []a2ui.AgentMessage {
	surf := a2uibuild.NewSurface(SurfaceQueueID, a2ui.BasicCatalogID)

	surf = surf.Add(a2uibuild.Text("header", a2ui.StringBinding("/title")))
	surf = surf.Add(a2uibuild.Divider("header-divider"))

	itemData := make([]any, 0, len(items))
	var listChildID string
	if len(items) == 0 {
		listChildID = "empty-state"
		surf = surf.Add(a2uibuild.Text(listChildID, a2ui.StringLiteral("No items in this queue.")))
	} else {
		cardIDs := make([]string, 0, len(items))
		for i, it := range items {
			p := fmt.Sprintf("item-%d", i)
			cardID := p + "-card"
			surf = addItemCardComponents(surf, identity, p, cardID, fmt.Sprintf("/items/%d", i), it)
			cardIDs = append(cardIDs, cardID)
			itemData = append(itemData, itemToDataMap(it))
		}
		listChildID = "items-list"
		itemsCol := a2uibuild.Column(listChildID, a2uibuild.Children(cardIDs...))
		itemsCol.Metadata = &a2ui.Metadata{
			Extensions: map[string]any{
				"qmcp_drop_zone": map[string]any{
					"action":   "upsert_item",
					"identity": identity,
					"queue":    string(qName),
				},
			},
		}
		surf = surf.Add(itemsCol)
	}

	surf = surf.Add(a2uibuild.Column(a2uistate.RootID, a2uibuild.Children("header", "header-divider", listChildID)))

	surf = surf.WithData(map[string]any{
		"identity":   identity,
		"identities": collectIdentities(identity, items...),
		"queue":      string(qName),
		"title":      fmt.Sprintf("%s (%d items)", qName, len(items)),
		"items":      itemData,
	})

	return surf.Messages()
}

// BuildItemSurface builds an A2UI surface using the basic catalog displaying a
// single created or updated item.
func BuildItemSurface(identity string, item queue.Item) []a2ui.AgentMessage {
	surf := a2uibuild.NewSurface(SurfaceItemID, a2ui.BasicCatalogID)
	surf = addItemCardComponents(surf, identity, "item", a2uistate.RootID, "/item", item)
	surf = surf.WithData(map[string]any{
		"identity":   identity,
		"identities": collectIdentities(identity, item),
		"item":       itemToDataMap(item),
	})
	return surf.Messages()
}

func upsertContextForItem(identity string, it queue.Item, status queue.Status, targetQueue queue.QueueName) map[string]a2ui.DynamicValue {
	assignee := it.Assignee
	if status == queue.StatusInProgress && assignee == "" {
		assignee = identity
	}
	ctx := map[string]a2ui.DynamicValue{
		"identity": a2ui.ValueString(identity),
		"id":       a2ui.ValueString(it.ID),
		"title":    a2ui.ValueString(it.Title),
		"status":   a2ui.ValueString(string(status)),
		"queue":    a2ui.ValueString(string(targetQueue)),
		"owner":    a2ui.ValueString(it.Owner),
		"assignee": a2ui.ValueString(assignee),
		"text":     a2ui.ValueString(it.Text),
	}
	if len(it.Links) > 0 {
		links := make([]any, len(it.Links))
		for i, l := range it.Links {
			links[i] = l
		}
		ctx["links"] = a2ui.ValueArray(links)
	}
	return ctx
}

func addCaptionText(surf a2uibuild.Surface, id, text string) a2uibuild.Surface {
	comp := a2uibuild.Text(id, a2ui.StringLiteral(text))
	comp.Text.Variant = a2ui.TextVariantCaption
	return surf.Add(comp)
}

func addMarkdownText(surf a2uibuild.Surface, id string, text a2ui.DynamicString, rawMarkdown string) a2uibuild.Surface {
	comp := a2uibuild.Text(id, text)
	comp.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_html": renderMarkdownHTML(rawMarkdown),
		},
	}
	return surf.Add(comp)
}

func addActionBtn(surf a2uibuild.Surface, btnID, lblID, label string, variant a2ui.ButtonVariant, eventName string, ctx map[string]a2ui.DynamicValue) a2uibuild.Surface {
	surf = surf.Add(a2uibuild.Text(lblID, a2ui.StringLiteral(label)))
	btn := a2uibuild.Button(btnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name:    eventName,
			Context: ctx,
		},
	}, lblID)
	if variant != "" {
		btn.Button.Variant = variant
	}
	return surf.Add(btn)
}

func addLinksRow(surf a2uibuild.Surface, rowID, prefix string, links []string) a2uibuild.Surface {
	btnIDs := make([]string, 0, len(links))
	for i, link := range links {
		lp := fmt.Sprintf("%s-%d", prefix, i)
		lblID := lp + "-label"
		btnID := lp + "-btn"
		surf = surf.Add(a2uibuild.Text(lblID, a2ui.StringLiteral(link)))
		surf = surf.Add(a2uibuild.Button(btnID, a2ui.OpenURL(a2ui.StringLiteral(link)), lblID))
		btnIDs = append(btnIDs, btnID)
	}
	return surf.Add(a2uibuild.Row(rowID, a2uibuild.Children(btnIDs...)))
}

func addItemCardComponents(surf a2uibuild.Surface, identity, prefix, cardID, dataPath string, it queue.Item) a2uibuild.Surface {
	titleID := prefix + "-title"
	metaID := prefix + "-meta"
	surf = surf.Add(a2uibuild.Text(titleID, a2ui.StringBinding(dataPath+"/title")))

	metaStr := fmt.Sprintf("status: %s", it.Status)
	if it.Assignee != "" {
		metaStr += fmt.Sprintf(" • assignee: %s", it.Assignee)
	}
	if len(it.Comments) > 0 {
		metaStr += fmt.Sprintf(" • 💬 %d", len(it.Comments))
	}
	surf = addCaptionText(surf, metaID, metaStr)

	modalID := prefix + "-detail-modal"
	colChildren := []string{modalID, metaID}

	if summary := cardSummaryText(it); summary != "" {
		textID := prefix + "-text"
		surf = addMarkdownText(surf, textID, a2ui.StringBinding(dataPath+"/text_preview"), truncateCardText(summary))
		colChildren = append(colChildren, textID)
	}

	if len(it.Links) > 0 {
		linksRowID := prefix + "-links"
		surf = addLinksRow(surf, linksRowID, prefix+"-link", it.Links)
		colChildren = append(colChildren, linksRowID)
	}

	// Build single-card detail Modal (trigger = card title; content = title + Tabs(View, ✎ Edit) + comment thread Column).
	modalTitleID := prefix + "-modal-title"
	modalMetaID := prefix + "-modal-meta"
	modalDivID := prefix + "-modal-divider"
	modalViewColID := prefix + "-modal-view-col"
	editTextColID := prefix + "-edit-text-col"
	modalTabsID := prefix + "-modal-tabs"

	surf = surf.Add(a2uibuild.Text(modalTitleID, a2ui.StringBinding(dataPath+"/title")))

	modalMetaStr := fmt.Sprintf("ID: %s • %s • status: %s • owner: %s",
		it.ID, it.Queue, it.Status, it.Owner)
	if it.Assignee != "" {
		modalMetaStr += fmt.Sprintf(" • assignee: %s", it.Assignee)
	}
	modalMetaStr += fmt.Sprintf(" • updated: %s", it.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	surf = addCaptionText(surf, modalMetaID, modalMetaStr)
	surf = surf.Add(a2uibuild.Divider(modalDivID))

	viewChildren := []string{modalMetaID, modalDivID}
	if it.Text != "" {
		modalTextID := prefix + "-modal-text"
		surf = addMarkdownText(surf, modalTextID, a2ui.StringBinding(dataPath+"/text"), it.Text)
		viewChildren = append(viewChildren, modalTextID)
	}
	if len(it.Data) > 0 {
		if rawData, err := json.MarshalIndent(it.Data, "", "  "); err == nil {
			modalDataID := prefix + "-modal-data"
			surf = addMarkdownText(surf, modalDataID, a2ui.StringLiteral(string(rawData)), "```json\n"+string(rawData)+"\n```")
			viewChildren = append(viewChildren, modalDataID)
		}
	}
	if len(it.Links) > 0 {
		modalLinksRowID := prefix + "-modal-links"
		surf = addLinksRow(surf, modalLinksRowID, prefix+"-modal-link", it.Links)
		viewChildren = append(viewChildren, modalLinksRowID)
	}
	surf = surf.Add(a2uibuild.Column(modalViewColID, a2uibuild.Children(viewChildren...)))

	// Edit Item tab inside the Modal (queue, status, assignee, text)
	editQueueID := prefix + "-edit-queue-input"
	queuePicker := a2uibuild.ChoicePicker(editQueueID, []a2ui.ChoiceOption{
		{Label: a2ui.StringLiteral("Inbox"), Value: string(queue.QueueInbox)},
		{Label: a2ui.StringLiteral("Outbox"), Value: string(queue.QueueOutbox)},
		{Label: a2ui.StringLiteral("Needs Attention"), Value: string(queue.QueueNeedsAttention)},
		{Label: a2ui.StringLiteral("Archive"), Value: string(queue.QueueArchive)},
	}, a2ui.StringListBinding(dataPath+"/queue_list"))
	queuePicker.ChoicePicker.Label = new(a2ui.StringLiteral("Queue"))
	queuePicker.ChoicePicker.Variant = a2ui.ChoicePickerVariantMutuallyExclusive
	surf = surf.Add(queuePicker)

	editStatusID := prefix + "-edit-status-input"
	statusPicker := a2uibuild.ChoicePicker(editStatusID, []a2ui.ChoiceOption{
		{Label: a2ui.StringLiteral("open"), Value: string(queue.StatusOpen)},
		{Label: a2ui.StringLiteral("in_progress"), Value: string(queue.StatusInProgress)},
		{Label: a2ui.StringLiteral("blocked"), Value: string(queue.StatusBlocked)},
		{Label: a2ui.StringLiteral("done"), Value: string(queue.StatusDone)},
		{Label: a2ui.StringLiteral("cancelled"), Value: string(queue.StatusCancelled)},
	}, a2ui.StringListBinding(dataPath+"/status_list"))
	statusPicker.ChoicePicker.Label = new(a2ui.StringLiteral("Status"))
	statusPicker.ChoicePicker.Variant = a2ui.ChoicePickerVariantMutuallyExclusive
	surf = surf.Add(statusPicker)

	editAssigneeID := prefix + "-edit-assignee-input"
	assigneeField := a2uibuild.TextField(editAssigneeID, a2ui.StringLiteral("Assignee"))
	assigneeField.TextField.Value = new(a2ui.StringBinding(dataPath + "/assignee"))
	assigneeField.TextField.Placeholder = new(a2ui.StringLiteral("Assignee identity..."))
	assigneeField.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_autocomplete": "identities",
		},
	}
	surf = surf.Add(assigneeField)

	editMetaRowID := prefix + "-edit-meta-row"
	surf = surf.Add(a2uibuild.Row(editMetaRowID, a2uibuild.Children(editQueueID, editStatusID, editAssigneeID)))

	editTextInputID := prefix + "-edit-text-input"
	editTextField := a2uibuild.TextField(editTextInputID, a2ui.StringLiteral("Description (Markdown supported)"))
	editTextField.TextField.Value = new(a2ui.StringBinding(dataPath + "/text"))
	editTextField.TextField.Placeholder = new(a2ui.StringLiteral("Item description..."))
	editTextField.TextField.Variant = a2ui.TextFieldVariantLongText
	surf = surf.Add(editTextField)

	saveTextLblID := prefix + "-edit-text-save-label"
	saveTextBtnID := prefix + "-edit-text-save-btn"
	editCtx := upsertContextForItem(identity, it, it.Status, it.Queue)
	editCtx["queue"] = a2ui.ValueBinding(dataPath + "/queue")
	editCtx["status"] = a2ui.ValueBinding(dataPath + "/status")
	editCtx["assignee"] = a2ui.ValueBinding(dataPath + "/assignee")
	editCtx["text"] = a2ui.ValueBinding(dataPath + "/text")
	surf = addActionBtn(surf, saveTextBtnID, saveTextLblID, "Save Changes", a2ui.ButtonVariantPrimary, "upsert_item", editCtx)

	surf = surf.Add(a2uibuild.Column(editTextColID, a2uibuild.Children(editMetaRowID, editTextInputID, saveTextBtnID)))

	surf = surf.Add(a2uibuild.Tabs(modalTabsID, []a2ui.TabDef{
		{Title: a2ui.StringLiteral("View"), Child: modalViewColID},
		{Title: a2ui.StringLiteral("✎ Edit"), Child: editTextColID},
	}))

	modalChildren := []string{modalTitleID, modalTabsID}

	// Comment thread section inside the Modal
	commentsDivID := prefix + "-comments-divider"
	commentsHdrID := prefix + "-comments-header"
	surf = surf.Add(a2uibuild.Divider(commentsDivID))
	surf = surf.Add(a2uibuild.Text(commentsHdrID, a2ui.StringLiteral(fmt.Sprintf("Comments (%d)", len(it.Comments)))))
	modalChildren = append(modalChildren, commentsDivID, commentsHdrID)

	for ci, c := range it.Comments {
		cp := fmt.Sprintf("%s-comment-%d", prefix, ci)
		cMetaID := cp + "-meta"
		cTextID := cp + "-text"
		cColID := cp + "-col"
		cCardID := cp + "-card"

		surf = addCaptionText(surf, cMetaID, fmt.Sprintf("%s • %s", c.Author, c.CreatedAt.Format("2006-01-02T15:04:05Z07:00")))
		surf = addMarkdownText(surf, cTextID, a2ui.StringLiteral(c.Text), c.Text)
		surf = surf.Add(a2uibuild.Column(cColID, a2uibuild.Children(cMetaID, cTextID)))
		surf = surf.Add(a2uibuild.Card(cCardID, cColID))
		modalChildren = append(modalChildren, cCardID)
	}

	commentInputID := prefix + "-comment-input"
	commentField := a2uibuild.TextField(commentInputID, a2ui.StringLiteral("Add a comment"))
	commentField.TextField.Value = new(a2ui.StringBinding(dataPath + "/draft_comment"))
	commentField.TextField.Placeholder = new(a2ui.StringLiteral("Write a comment (Markdown supported)..."))
	commentField.TextField.Variant = a2ui.TextFieldVariantLongText
	surf = surf.Add(commentField)

	postCommentLblID := prefix + "-comment-post-label"
	postCommentBtnID := prefix + "-comment-post-btn"
	surf = addActionBtn(surf, postCommentBtnID, postCommentLblID, "Post Comment", a2ui.ButtonVariantPrimary, "add_comment", map[string]a2ui.DynamicValue{
		"identity": a2ui.ValueString(identity),
		"id":       a2ui.ValueString(it.ID),
		"text":     a2ui.ValueBinding(dataPath + "/draft_comment"),
	})

	modalChildren = append(modalChildren, commentInputID, postCommentBtnID)

	modalContentID := prefix + "-modal-content"
	surf = surf.Add(a2uibuild.Column(modalContentID, a2uibuild.Children(modalChildren...)))

	modalComp := a2uibuild.Modal(modalID, modalContentID, titleID)
	modalComp.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_item_id": it.ID,
		},
	}
	surf = surf.Add(modalComp)

	var actionBtnIDs []string

	if !queue.IsTerminalStatus(it.Status) {
		if it.Status != queue.StatusInProgress {
			progBtnID := prefix + "-prog-btn"
			surf = addActionBtn(surf, progBtnID, prefix+"-prog-label", "In Progress", "", "upsert_item", upsertContextForItem(identity, it, queue.StatusInProgress, it.Queue))
			actionBtnIDs = append(actionBtnIDs, progBtnID)
		}

		if it.Status != queue.StatusBlocked {
			blkBtnID := prefix + "-blk-btn"
			surf = addActionBtn(surf, blkBtnID, prefix+"-blk-label", "Block", "", "upsert_item", upsertContextForItem(identity, it, queue.StatusBlocked, it.Queue))
			actionBtnIDs = append(actionBtnIDs, blkBtnID)
		}

		if it.Queue != queue.QueueNeedsAttention {
			attnBtnID := prefix + "-attn-btn"
			surf = addActionBtn(surf, attnBtnID, prefix+"-attn-label", "Needs Attention", "", "upsert_item", upsertContextForItem(identity, it, it.Status, queue.QueueNeedsAttention))
			actionBtnIDs = append(actionBtnIDs, attnBtnID)
		}

		doneIconID := prefix + "-done-icon"
		doneLblID := prefix + "-done-label"
		doneRowID := prefix + "-done-row"
		doneBtnID := prefix + "-done-btn"
		surf = surf.Add(a2uibuild.Icon(doneIconID, a2ui.IconNameOrPath{Name: new(a2ui.IconFolder)}))
		surf = surf.Add(a2uibuild.Text(doneLblID, a2ui.StringLiteral("Archive")))
		surf = surf.Add(a2uibuild.Row(doneRowID, a2uibuild.Children(doneIconID, doneLblID)))
		surf = surf.Add(a2uibuild.Button(doneBtnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name:    "upsert_item",
				Context: upsertContextForItem(identity, it, queue.StatusDone, queue.QueueArchive),
			},
		}, doneRowID))
		actionBtnIDs = append(actionBtnIDs, doneBtnID)
	} else {
		reopenBtnID := prefix + "-reopen-btn"
		surf = addActionBtn(surf, reopenBtnID, prefix+"-reopen-label", "Reopen to Inbox", "", "upsert_item", upsertContextForItem(identity, it, queue.StatusOpen, queue.QueueInbox))
		actionBtnIDs = append(actionBtnIDs, reopenBtnID)
	}

	actionsRowID := prefix + "-actions"
	surf = surf.Add(a2uibuild.Row(actionsRowID, a2uibuild.Children(actionBtnIDs...)))
	colChildren = append(colChildren, actionsRowID)

	colID := prefix + "-col"
	surf = surf.Add(a2uibuild.Column(colID, a2uibuild.Children(colChildren...)))

	card := a2uibuild.Card(cardID, colID)
	card.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_draggable": map[string]any{
				"action":   "upsert_item",
				"identity": identity,
				"id":       it.ID,
				"title":    it.Title,
				"status":   string(it.Status),
				"queue":    string(it.Queue),
				"position": it.Position,
				"owner":    it.Owner,
				"assignee": it.Assignee,
				"text":     it.Text,
				"links":    it.Links,
			},
		},
	}
	surf = surf.Add(card)
	return surf
}

func itemToDataMap(it queue.Item) map[string]any {
	m := map[string]any{
		"id":            it.ID,
		"title":         it.Title,
		"status":        string(it.Status),
		"status_list":   []string{string(it.Status)},
		"queue":         string(it.Queue),
		"queue_list":    []string{string(it.Queue)},
		"position":      it.Position,
		"owner":         it.Owner,
		"assignee":      it.Assignee,
		"updated_at":    it.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"text":          it.Text,
		"draft_comment": "",
	}
	if summary := cardSummaryText(it); summary != "" {
		m["text_preview"] = truncateCardText(summary)
	}
	if len(it.Comments) > 0 {
		m["comments"] = it.Comments
	}
	if len(it.Data) > 0 {
		m["data"] = it.Data
	}
	if len(it.Links) > 0 {
		m["links"] = it.Links
	}
	return m
}

func toolResultWithA2UI[Out any](uri string, out Out, msgs []a2ui.AgentMessage) (*mcp.CallToolResult, Out, error) {
	outBytes, err := json.Marshal(out)
	if err != nil {
		return nil, out, fmt.Errorf("marshal tool output: %w", err)
	}
	a2uiBytes, err := json.Marshal(msgs)
	if err != nil {
		return nil, out, fmt.Errorf("marshal a2ui messages: %w", err)
	}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(outBytes)},
			&mcp.EmbeddedResource{
				Resource: &mcp.ResourceContents{
					URI:      uri,
					MIMEType: a2a.A2UIMIMEType,
					Text:     string(a2uiBytes),
				},
			},
		},
	}
	return res, out, nil
}
