package server

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/tmc/a2ui"
	"github.com/tmc/a2ui/a2a"
	"github.com/tmc/a2ui/a2uibuild"
	"github.com/tmc/a2ui/a2uistate"
)

const (
	SurfaceQueuesID = "qmcp-queues"
	SurfaceQueueID  = "qmcp-queue"
	SurfaceItemID   = "qmcp-item"
)

// BuildQueuesSummarySurface builds an A2UI surface using the basic catalog
// displaying all queues and their item counts.
func BuildQueuesSummarySurface(identity string, queues []queue.QueueSummary) []a2ui.AgentMessage {
	surf := a2uibuild.NewSurface(SurfaceQueuesID, a2ui.BasicCatalogID)

	surf = surf.Add(a2uibuild.Text("header", a2ui.StringBinding("/title")))
	surf = surf.Add(a2uibuild.Divider("header-divider"))

	cardIDs := make([]string, 0, len(queues))
	queueData := make([]any, 0, len(queues))

	for i, q := range queues {
		p := fmt.Sprintf("queue-%d", i)
		nameID := p + "-name"
		countsID := p + "-counts"
		btnLabelID := p + "-open-label"
		btnID := p + "-open-btn"
		colID := p + "-col"
		cardID := p + "-card"

		surf = surf.Add(a2uibuild.Text(nameID, a2ui.StringBinding(fmt.Sprintf("/queues/%d/name", i))))
		surf = surf.Add(a2uibuild.Text(countsID, a2ui.StringLiteral(fmt.Sprintf("Owner (%s): %d | Total: %d", identity, q.CallerCount, q.TotalCount))))
		surf = surf.Add(a2uibuild.Text(btnLabelID, a2ui.StringLiteral("View Queue")))
		surf = surf.Add(a2uibuild.Button(btnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name: "get_queue",
				Context: map[string]a2ui.DynamicValue{
					"identity": a2ui.ValueString(identity),
					"queue":    a2ui.ValueString(string(q.Name)),
				},
			},
		}, btnLabelID))
		surf = surf.Add(a2uibuild.Column(colID, a2uibuild.Children(nameID, countsID, btnID)))
		surf = surf.Add(a2uibuild.Card(cardID, colID))

		cardIDs = append(cardIDs, cardID)
		queueData = append(queueData, map[string]any{
			"name":         string(q.Name),
			"total_count":  q.TotalCount,
			"caller_count": q.CallerCount,
		})
	}

	surf = surf.Add(a2uibuild.Column("queues-list", a2uibuild.Children(cardIDs...)))
	surf = surf.Add(a2uibuild.Column(a2uistate.RootID, a2uibuild.Children("header", "header-divider", "queues-list")))

	surf = surf.WithData(map[string]any{
		"identity": identity,
		"title":    fmt.Sprintf("Queues (%s)", identity),
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
	if len(items) == 0 {
		surf = surf.Add(a2uibuild.Text("empty-state", a2ui.StringLiteral("No items in this queue.")))
		surf = surf.Add(a2uibuild.Column(a2uistate.RootID, a2uibuild.Children("header", "header-divider", "empty-state")))
	} else {
		cardIDs := make([]string, 0, len(items))
		for i, it := range items {
			p := fmt.Sprintf("item-%d", i)
			cardID := p + "-card"
			surf = addItemCardComponents(surf, identity, p, cardID, fmt.Sprintf("/items/%d", i), it)
			cardIDs = append(cardIDs, cardID)
			itemData = append(itemData, itemToDataMap(it))
		}
		surf = surf.Add(a2uibuild.Column("items-list", a2uibuild.Children(cardIDs...)))
		surf = surf.Add(a2uibuild.Column(a2uistate.RootID, a2uibuild.Children("header", "header-divider", "items-list")))
	}

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

func addItemCardComponents(surf a2uibuild.Surface, identity, prefix, cardID, dataPath string, it queue.Item) a2uibuild.Surface {
	titleID := prefix + "-title"
	metaID := prefix + "-meta"
	surf = surf.Add(a2uibuild.Text(titleID, a2ui.StringBinding(dataPath+"/title")))
	surf = surf.Add(a2uibuild.Text(metaID, a2ui.StringLiteral(
		fmt.Sprintf("#%d • %s • status: %s • owner: %s", it.Position, it.Queue, it.Status, it.Owner),
	)))

	colChildren := []string{titleID, metaID}

	if it.Text != "" {
		textID := prefix + "-text"
		surf = surf.Add(a2uibuild.Text(textID, a2ui.StringBinding(dataPath+"/text")))
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

	var actionBtnIDs []string

	viewLblID := prefix + "-view-queue-label"
	viewBtnID := prefix + "-view-queue-btn"
	surf = surf.Add(a2uibuild.Text(viewLblID, a2ui.StringLiteral("View "+string(it.Queue))))
	surf = surf.Add(a2uibuild.Button(viewBtnID, a2ui.Action{
		Event: &a2ui.EventAction{
			Name: "get_queue",
			Context: map[string]a2ui.DynamicValue{
				"identity": a2ui.ValueString(identity),
				"queue":    a2ui.ValueString(string(it.Queue)),
			},
		},
	}, viewLblID))
	actionBtnIDs = append(actionBtnIDs, viewBtnID)

	if !queue.IsTerminalStatus(it.Status) {
		doneLblID := prefix + "-done-label"
		doneBtnID := prefix + "-done-btn"
		surf = surf.Add(a2uibuild.Text(doneLblID, a2ui.StringLiteral("Mark Done")))
		surf = surf.Add(a2uibuild.Button(doneBtnID, a2ui.Action{
			Event: &a2ui.EventAction{
				Name: "upsert_item",
				Context: map[string]a2ui.DynamicValue{
					"identity": a2ui.ValueString(identity),
					"id":       a2ui.ValueString(it.ID),
					"title":    a2ui.ValueString(it.Title),
					"status":   a2ui.ValueString(string(queue.StatusDone)),
				},
			},
		}, doneLblID))
		actionBtnIDs = append(actionBtnIDs, doneBtnID)

		if it.Queue != queue.QueueNeedsAttention {
			attnLblID := prefix + "-attn-label"
			attnBtnID := prefix + "-attn-btn"
			surf = surf.Add(a2uibuild.Text(attnLblID, a2ui.StringLiteral("Needs Attention")))
			surf = surf.Add(a2uibuild.Button(attnBtnID, a2ui.Action{
				Event: &a2ui.EventAction{
					Name: "upsert_item",
					Context: map[string]a2ui.DynamicValue{
						"identity": a2ui.ValueString(identity),
						"id":       a2ui.ValueString(it.ID),
						"title":    a2ui.ValueString(it.Title),
						"queue":    a2ui.ValueString(string(queue.QueueNeedsAttention)),
						"status":   a2ui.ValueString(string(it.Status)),
					},
				},
			}, attnLblID))
			actionBtnIDs = append(actionBtnIDs, attnBtnID)
		}
	}

	actionsRowID := prefix + "-actions"
	surf = surf.Add(a2uibuild.Row(actionsRowID, a2uibuild.Children(actionBtnIDs...)))
	colChildren = append(colChildren, actionsRowID)

	colID := prefix + "-col"
	surf = surf.Add(a2uibuild.Column(colID, a2uibuild.Children(colChildren...)))
	surf = surf.Add(a2uibuild.Card(cardID, colID))
	return surf
}

func itemToDataMap(it queue.Item) map[string]any {
	m := map[string]any{
		"id":         it.ID,
		"title":      it.Title,
		"status":     string(it.Status),
		"queue":      string(it.Queue),
		"position":   it.Position,
		"owner":      it.Owner,
		"updated_at": it.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if it.Text != "" {
		m["text"] = it.Text
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
