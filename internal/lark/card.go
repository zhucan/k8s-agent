package lark

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CardBuilder builds a Feishu (Lark) interactive card message.
type CardBuilder struct {
	header   *CardHeader
	elements []CardElement
	config   *CardConfig
}

// CardConfig holds card-level configuration.
type CardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

// CardHeader is the card title area.
type CardHeader struct {
	Title    *CardText `json:"title"`
	Template string    `json:"template,omitempty"` // blue, wathet, turquoise, green, yellow, orange, red, carmine, violet, purple, indigo, grey
}

// CardText is a text element.
type CardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// CardElement is the interface for card content blocks.
type CardElement interface {
	GetTag() string
}

// CardDiv is a text/div block element.
type CardDiv struct {
	Tag  string                 `json:"tag"`
	Text *CardText              `json:"text,omitempty"`
	Fields []*CardField         `json:"fields,omitempty"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
}

func (d *CardDiv) GetTag() string { return d.Tag }

// CardField is a field element within a div (used for two-column layout).
type CardField struct {
	IsShort bool      `json:"is_short"`
	Text    *CardText `json:"text"`
}

// CardHr is a horizontal divider element.
type CardHr struct {
	Tag string `json:"tag"`
}

func (h *CardHr) GetTag() string { return h.Tag }

// CardNote is a note/footer element (rendered as small grey text).
type CardNote struct {
	Tag      string      `json:"tag"`
	Elements []*CardText `json:"elements"`
}

func (n *CardNote) GetTag() string { return n.Tag }

// NewCardBuilder creates a new CardBuilder with wide-screen mode enabled.
func NewCardBuilder() *CardBuilder {
	return &CardBuilder{
		config: &CardConfig{
			WideScreenMode: true,
		},
		elements: []CardElement{},
	}
}

// SetHeader sets the card title and color template.
func (b *CardBuilder) SetHeader(title string, template string) *CardBuilder {
	b.header = &CardHeader{
		Title: &CardText{
			Tag:     "plain_text",
			Content: title,
		},
		Template: template,
	}
	return b
}

// AddMarkdown adds a Markdown text block.
func (b *CardBuilder) AddMarkdown(content string) *CardBuilder {
	b.elements = append(b.elements, &CardDiv{
		Tag: "div",
		Text: &CardText{
			Tag:     "lark_md",
			Content: content,
		},
	})
	return b
}

// AddPlainText adds a plain text block.
func (b *CardBuilder) AddPlainText(content string) *CardBuilder {
	b.elements = append(b.elements, &CardDiv{
		Tag: "div",
		Text: &CardText{
			Tag:     "plain_text",
			Content: content,
		},
	})
	return b
}

// AddFields adds a two-column field list.
func (b *CardBuilder) AddFields(fields map[string]string) *CardBuilder {
	cardFields := make([]*CardField, 0, len(fields))
	for key, value := range fields {
		cardFields = append(cardFields, &CardField{
			IsShort: true,
			Text: &CardText{
				Tag:     "lark_md",
				Content: fmt.Sprintf("**%s**\n%s", key, value),
			},
		})
	}

	b.elements = append(b.elements, &CardDiv{
		Tag:    "div",
		Fields: cardFields,
	})
	return b
}

// AddDivider adds a horizontal divider line.
func (b *CardBuilder) AddDivider() *CardBuilder {
	b.elements = append(b.elements, &CardHr{
		Tag: "hr",
	})
	return b
}

// AddNote adds a note (small grey text) at the bottom.
func (b *CardBuilder) AddNote(text string) *CardBuilder {
	b.elements = append(b.elements, &CardNote{
		Tag: "note",
		Elements: []*CardText{
			{
				Tag:     "plain_text",
				Content: text,
			},
		},
	})
	return b
}

// Build serializes the card to a JSON string.
func (b *CardBuilder) Build() (string, error) {
	card := map[string]interface{}{
		"config":   b.config,
		"elements": b.elements,
	}

	if b.header != nil {
		card["header"] = b.header
	}

	data, err := json.Marshal(card)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// FormatNodeStatus formats a node's status as a card message.
func FormatNodeStatus(nodeName, ip, status string, ready, schedulable bool) (string, error) {
	builder := NewCardBuilder()

	template := "green"
	if !ready {
		template = "red"
	} else if !schedulable {
		template = "orange"
	}

	builder.SetHeader(fmt.Sprintf("Node Status: %s", nodeName), template)

	statusIcon := "🔴"
	if ready {
		statusIcon = "🟢"
	}
	scheduleIcon := "🔴"
	if schedulable {
		scheduleIcon = "🟢"
	}

	builder.AddMarkdown(fmt.Sprintf(
		"**IP:** %s\n**Status:** %s %s\n**Scheduling:** %s %s",
		ip,
		statusIcon, boolToText(ready, "Ready", "NotReady"),
		scheduleIcon, boolToText(schedulable, "Schedulable", "Unschedulable"),
	))

	return builder.Build()
}

// FormatNodeList formats a list of nodes as a card message.
func FormatNodeList(clusterName string, nodes []NodeInfo) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(fmt.Sprintf("Cluster Node List: %s", clusterName), "blue")

	builder.AddMarkdown(fmt.Sprintf("**Total Nodes:** %d", len(nodes)))
	builder.AddDivider()

	readyNodes := []NodeInfo{}
	notReadyNodes := []NodeInfo{}

	for _, node := range nodes {
		if node.Ready {
			readyNodes = append(readyNodes, node)
		} else {
			notReadyNodes = append(notReadyNodes, node)
		}
	}

	if len(readyNodes) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**🟢 Ready (%d)**\n", len(readyNodes)))
		for _, node := range readyNodes {
			scheduleIcon := "✓"
			if !node.Schedulable {
				scheduleIcon = "⚠"
			}
			sb.WriteString(fmt.Sprintf("• %s (%s) %s\n", node.Name, node.IP, scheduleIcon))
		}
		builder.AddMarkdown(sb.String())
	}

	if len(notReadyNodes) > 0 {
		builder.AddDivider()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**🔴 NotReady (%d)**\n", len(notReadyNodes)))
		for _, node := range notReadyNodes {
			sb.WriteString(fmt.Sprintf("• %s (%s)\n", node.Name, node.IP))
		}
		builder.AddMarkdown(sb.String())
	}

	return builder.Build()
}

// FormatClusterList formats a list of clusters as a card message.
func FormatClusterList(clusters []ClusterInfo, currentCluster string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader("Cluster List", "blue")

	builder.AddMarkdown(fmt.Sprintf("**Total Clusters:** %d", len(clusters)))
	builder.AddDivider()

	var sb strings.Builder
	for _, cluster := range clusters {
		icon := "○"
		if cluster.Name == currentCluster {
			icon = "●"
		}
		sb.WriteString(fmt.Sprintf("%s **%s**\n", icon, cluster.Name))
		sb.WriteString(fmt.Sprintf("  Nodes: %d\n", cluster.NodeCount))
		if cluster.Name == currentCluster {
			sb.WriteString("  (current cluster)\n")
		}
		sb.WriteString("\n")
	}

	builder.AddMarkdown(sb.String())
	return builder.Build()
}

// FormatError formats an error message as a card.
func FormatError(title, message string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(title, "red")
	builder.AddMarkdown(message)
	return builder.Build()
}

// FormatSuccess formats a success message as a card.
func FormatSuccess(title, message string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(title, "green")
	builder.AddMarkdown(message)
	return builder.Build()
}

// NodeInfo holds basic node information for card rendering.
type NodeInfo struct {
	Name        string
	IP          string
	Ready       bool
	Schedulable bool
}

// ClusterInfo holds basic cluster information for card rendering.
type ClusterInfo struct {
	Name      string
	NodeCount int
}

func boolToText(value bool, trueText, falseText string) string {
	if value {
		return trueText
	}
	return falseText
}
