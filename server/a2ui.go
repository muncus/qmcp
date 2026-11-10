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
	"encoding/json"
	"fmt"

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
		countsComp := a2uibuild.Text(countsID, a2ui.StringLiteral(fmt.Sprintf("%d mine • %d total", q.CallerCount, q.TotalCount)))
		countsComp.Text.Variant = a2ui.TextVariantCaption
		surf = surf.Add(countsComp)

		surf = surf.Add(a2uibuild.Text(btnLabelID, a2ui.StringLiteral("Focus")))
		openBtn := a2uibuild.Button(btnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name: "get_queue",
				Context: map[string]a2ui.DynamicValue{
					"identity": a2ui.ValueString(identity),
					"queue":    a2ui.ValueString(string(q.Name)),
				},
			},
		}, btnLabelID)
		openBtn.Button.Variant = a2ui.ButtonVariantBorderless
		surf = surf.Add(openBtn)

		hdrRow := a2uibuild.Row(hdrRowID, a2uibuild.Children(nameID, countsID, btnID))
		hdrRow.Row.Align = a2ui.LayoutAlignCenter
		surf = surf.Add(hdrRow)
		surf = surf.Add(a2uibuild.Divider(divID))

		qItems := itemsByQueue[q.Name]
		itemData := make([]any, 0, len(qItems))
		itemCardIDs := make([]string, 0, max(1, len(qItems)))

		if len(qItems) == 0 {
			emptyID := p + "-empty"
			emptyComp := a2uibuild.Text(emptyID, a2ui.StringLiteral("Empty — drag an item here"))
			emptyComp.Text.Variant = a2ui.TextVariantCaption
			surf = surf.Add(emptyComp)
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

	surf = surf.WithData(map[string]any{
		"identity": identity,
		"title":    fmt.Sprintf("Kanban Overview (%s)", identity),
		"queues":   queueData,
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
		"identity": identity,
		"queue":    string(qName),
		"title":    fmt.Sprintf("%s (%d items)", qName, len(items)),
		"items":    itemData,
	})

	return surf.Messages()
}

// BuildItemSurface builds an A2UI surface using the basic catalog displaying a
// single created or updated item.
func BuildItemSurface(identity string, item queue.Item) []a2ui.AgentMessage {
	surf := a2uibuild.NewSurface(SurfaceItemID, a2ui.BasicCatalogID)
	surf = addItemCardComponents(surf, identity, "item", a2uistate.RootID, "/item", item)
	surf = surf.WithData(map[string]any{
		"identity": identity,
		"item":     itemToDataMap(item),
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
	metaComp := a2uibuild.Text(metaID, a2ui.StringLiteral(metaStr))
	metaComp.Text.Variant = a2ui.TextVariantCaption
	surf = surf.Add(metaComp)

	colChildren := []string{titleID, metaID}

	if summary := cardSummaryText(it); summary != "" {
		textID := prefix + "-text"
		preview := truncateCardText(summary)
		textComp := a2uibuild.Text(textID, a2ui.StringBinding(dataPath+"/text_preview"))
		textComp.Metadata = &a2ui.Metadata{
			Extensions: map[string]any{
				"qmcp_html": renderMarkdownHTML(preview),
			},
		}
		surf = surf.Add(textComp)
		colChildren = append(colChildren, textID)
	}

	if len(it.Links) > 0 {
		linkBtnIDs := make([]string, 0, len(it.Links))
		for li, link := range it.Links {
			lp := fmt.Sprintf("%s-link-%d", prefix, li)
			lblID := lp + "-label"
			btnID := lp + "-btn"
			surf = surf.Add(a2uibuild.Text(lblID, a2ui.StringLiteral(link)))
			surf = surf.Add(a2uibuild.Button(btnID, a2ui.OpenURL(a2ui.StringLiteral(link)), lblID))
			linkBtnIDs = append(linkBtnIDs, btnID)
		}
		linksRowID := prefix + "-links"
		surf = surf.Add(a2uibuild.Row(linksRowID, a2uibuild.Children(linkBtnIDs...)))
		colChildren = append(colChildren, linksRowID)
	}

	// Build single-card detail Modal (trigger = "Details" button, content = full item detail + comment thread Column).
	modalTitleID := prefix + "-modal-title"
	modalMetaID := prefix + "-modal-meta"
	modalDivID := prefix + "-modal-divider"
	surf = surf.Add(a2uibuild.Text(modalTitleID, a2ui.StringBinding(dataPath+"/title")))
	modalMetaStr := fmt.Sprintf("ID: %s • %s • status: %s • owner: %s",
		it.ID, it.Queue, it.Status, it.Owner)
	if it.Assignee != "" {
		modalMetaStr += fmt.Sprintf(" • assignee: %s", it.Assignee)
	}
	modalMetaStr += fmt.Sprintf(" • updated: %s", it.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	modalMetaComp := a2uibuild.Text(modalMetaID, a2ui.StringLiteral(modalMetaStr))
	modalMetaComp.Text.Variant = a2ui.TextVariantCaption
	surf = surf.Add(modalMetaComp)
	surf = surf.Add(a2uibuild.Divider(modalDivID))

	modalChildren := []string{modalTitleID, modalMetaID, modalDivID}
	if it.Text != "" {
		modalTextID := prefix + "-modal-text"
		modalTextComp := a2uibuild.Text(modalTextID, a2ui.StringBinding(dataPath+"/text"))
		modalTextComp.Metadata = &a2ui.Metadata{
			Extensions: map[string]any{
				"qmcp_html": renderMarkdownHTML(it.Text),
			},
		}
		surf = surf.Add(modalTextComp)
		modalChildren = append(modalChildren, modalTextID)
	}

	editTextColID := prefix + "-edit-text-col"
	editTextLblID := prefix + "-edit-text-label"
	editTextBtnID := prefix + "-edit-text-btn"
	surf = surf.Add(a2uibuild.Text(editTextLblID, a2ui.StringLiteral("Edit Text")))
	editTextBtn := a2uibuild.Button(editTextBtnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name: "toggle_edit_text",
			Context: map[string]a2ui.DynamicValue{
				"id":     a2ui.ValueString(it.ID),
				"target": a2ui.ValueString(editTextColID),
			},
		},
	}, editTextLblID)
	surf = surf.Add(editTextBtn)

	editTextInputID := prefix + "-edit-text-input"
	editTextField := a2uibuild.TextField(editTextInputID, a2ui.StringLiteral("Edit item text (Markdown supported)"))
	editTextField.TextField.Value = new(a2ui.StringBinding(dataPath + "/text"))
	editTextField.TextField.Placeholder = new(a2ui.StringLiteral("Item description..."))
	editTextField.TextField.Variant = a2ui.TextFieldVariantLongText
	surf = surf.Add(editTextField)

	saveTextLblID := prefix + "-edit-text-save-label"
	saveTextBtnID := prefix + "-edit-text-save-btn"
	surf = surf.Add(a2uibuild.Text(saveTextLblID, a2ui.StringLiteral("Save Text")))
	editCtx := upsertContextForItem(identity, it, it.Status, it.Queue)
	editCtx["text"] = a2ui.ValueBinding(dataPath + "/text")
	saveTextBtn := a2uibuild.Button(saveTextBtnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name:    "upsert_item",
			Context: editCtx,
		},
	}, saveTextLblID)
	saveTextBtn.Button.Variant = a2ui.ButtonVariantPrimary
	surf = surf.Add(saveTextBtn)

	editTextCol := a2uibuild.Column(editTextColID, a2uibuild.Children(editTextInputID, saveTextBtnID))
	editTextCol.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_collapsible_box": true,
		},
	}
	surf = surf.Add(editTextCol)
	modalChildren = append(modalChildren, editTextBtnID, editTextColID)
	if len(it.Data) > 0 {
		if rawData, err := json.MarshalIndent(it.Data, "", "  "); err == nil {
			modalDataID := prefix + "-modal-data"
			mdBlock := "```json\n" + string(rawData) + "\n```"
			modalDataComp := a2uibuild.Text(modalDataID, a2ui.StringLiteral(string(rawData)))
			modalDataComp.Metadata = &a2ui.Metadata{
				Extensions: map[string]any{
					"qmcp_html": renderMarkdownHTML(mdBlock),
				},
			}
			surf = surf.Add(modalDataComp)
			modalChildren = append(modalChildren, modalDataID)
		}
	}
	if len(it.Links) > 0 {
		modalLinkBtnIDs := make([]string, 0, len(it.Links))
		for li, link := range it.Links {
			lp := fmt.Sprintf("%s-modal-link-%d", prefix, li)
			lblID := lp + "-label"
			btnID := lp + "-btn"
			surf = surf.Add(a2uibuild.Text(lblID, a2ui.StringLiteral(link)))
			surf = surf.Add(a2uibuild.Button(btnID, a2ui.OpenURL(a2ui.StringLiteral(link)), lblID))
			modalLinkBtnIDs = append(modalLinkBtnIDs, btnID)
		}
		modalLinksRowID := prefix + "-modal-links"
		surf = surf.Add(a2uibuild.Row(modalLinksRowID, a2uibuild.Children(modalLinkBtnIDs...)))
		modalChildren = append(modalChildren, modalLinksRowID)
	}

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

		cMetaComp := a2uibuild.Text(cMetaID, a2ui.StringLiteral(
			fmt.Sprintf("%s • %s", c.Author, c.CreatedAt.Format("2006-01-02T15:04:05Z07:00")),
		))
		cMetaComp.Text.Variant = a2ui.TextVariantCaption
		surf = surf.Add(cMetaComp)

		cTextComp := a2uibuild.Text(cTextID, a2ui.StringLiteral(c.Text))
		cTextComp.Metadata = &a2ui.Metadata{
			Extensions: map[string]any{
				"qmcp_html": renderMarkdownHTML(c.Text),
			},
		}
		surf = surf.Add(cTextComp)
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
	surf = surf.Add(a2uibuild.Text(postCommentLblID, a2ui.StringLiteral("Post Comment")))
	postCommentBtn := a2uibuild.Button(postCommentBtnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name: "add_comment",
			Context: map[string]a2ui.DynamicValue{
				"identity": a2ui.ValueString(identity),
				"id":       a2ui.ValueString(it.ID),
				"text":     a2ui.ValueBinding(dataPath + "/draft_comment"),
			},
		},
	}, postCommentLblID)
	postCommentBtn.Button.Variant = a2ui.ButtonVariantPrimary
	surf = surf.Add(postCommentBtn)

	modalChildren = append(modalChildren, commentInputID, postCommentBtnID)

	modalContentID := prefix + "-modal-content"
	surf = surf.Add(a2uibuild.Column(modalContentID, a2uibuild.Children(modalChildren...)))

	detailLblID := prefix + "-detail-label"
	detailBtnID := prefix + "-detail-btn"
	modalID := prefix + "-detail-modal"
	surf = surf.Add(a2uibuild.Text(detailLblID, a2ui.StringLiteral("Details")))
	surf = surf.Add(a2uibuild.Button(detailBtnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name: "open_item_modal",
			Context: map[string]a2ui.DynamicValue{
				"id": a2ui.ValueString(it.ID),
			},
		},
	}, detailLblID))
	modalComp := a2uibuild.Modal(modalID, modalContentID, detailBtnID)
	modalComp.Metadata = &a2ui.Metadata{
		Extensions: map[string]any{
			"qmcp_item_id": it.ID,
		},
	}
	surf = surf.Add(modalComp)

	actionBtnIDs := []string{modalID}

	if !queue.IsTerminalStatus(it.Status) {
		if it.Status != queue.StatusInProgress {
			progLblID := prefix + "-prog-label"
			progBtnID := prefix + "-prog-btn"
			surf = surf.Add(a2uibuild.Text(progLblID, a2ui.StringLiteral("In Progress")))
			surf = surf.Add(a2uibuild.Button(progBtnID, a2ui.Action{
				Event: &a2ui.EventAction{
					Name:    "upsert_item",
					Context: upsertContextForItem(identity, it, queue.StatusInProgress, it.Queue),
				},
			}, progLblID))
			actionBtnIDs = append(actionBtnIDs, progBtnID)
		}

		if it.Status != queue.StatusBlocked {
			blkLblID := prefix + "-blk-label"
			blkBtnID := prefix + "-blk-btn"
			surf = surf.Add(a2uibuild.Text(blkLblID, a2ui.StringLiteral("Block")))
			surf = surf.Add(a2uibuild.Button(blkBtnID, a2ui.Action{
				Event: &a2ui.EventAction{
					Name:    "upsert_item",
					Context: upsertContextForItem(identity, it, queue.StatusBlocked, it.Queue),
				},
			}, blkLblID))
			actionBtnIDs = append(actionBtnIDs, blkBtnID)
		}

		if it.Queue != queue.QueueNeedsAttention {
			attnLblID := prefix + "-attn-label"
			attnBtnID := prefix + "-attn-btn"
			surf = surf.Add(a2uibuild.Text(attnLblID, a2ui.StringLiteral("Needs Attention")))
			surf = surf.Add(a2uibuild.Button(attnBtnID, a2ui.Action{
				Event: &a2ui.EventAction{
					Name:    "upsert_item",
					Context: upsertContextForItem(identity, it, it.Status, queue.QueueNeedsAttention),
				},
			}, attnLblID))
			actionBtnIDs = append(actionBtnIDs, attnBtnID)
		}

		doneLblID := prefix + "-done-label"
		doneBtnID := prefix + "-done-btn"
		surf = surf.Add(a2uibuild.Text(doneLblID, a2ui.StringLiteral("Mark Done")))
		surf = surf.Add(a2uibuild.Button(doneBtnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name:    "upsert_item",
				Context: upsertContextForItem(identity, it, queue.StatusDone, queue.QueueArchive),
			},
		}, doneLblID))
		actionBtnIDs = append(actionBtnIDs, doneBtnID)
	} else {
		reopenLblID := prefix + "-reopen-label"
		reopenBtnID := prefix + "-reopen-btn"
		surf = surf.Add(a2uibuild.Text(reopenLblID, a2ui.StringLiteral("Reopen to Inbox")))
		surf = surf.Add(a2uibuild.Button(reopenBtnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name:    "upsert_item",
				Context: upsertContextForItem(identity, it, queue.StatusOpen, queue.QueueInbox),
			},
		}, reopenLblID))
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
		"queue":         string(it.Queue),
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
