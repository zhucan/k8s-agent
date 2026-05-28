package lark

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CardBuilder 飞书卡片消息构建器
type CardBuilder struct {
	header   *CardHeader
	elements []CardElement
	config   *CardConfig
}

// CardConfig 卡片配置
type CardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

// CardHeader 卡片头部
type CardHeader struct {
	Title    *CardText `json:"title"`
	Template string    `json:"template,omitempty"` // blue, wathet, turquoise, green, yellow, orange, red, carmine, violet, purple, indigo, grey
}

// CardText 文本元素
type CardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// CardElement 卡片元素接口
type CardElement interface {
	GetTag() string
}

// CardDiv 文本块元素
type CardDiv struct {
	Tag  string                 `json:"tag"`
	Text *CardText              `json:"text,omitempty"`
	Fields []*CardField         `json:"fields,omitempty"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
}

func (d *CardDiv) GetTag() string { return d.Tag }

// CardField 字段元素
type CardField struct {
	IsShort bool      `json:"is_short"`
	Text    *CardText `json:"text"`
}

// CardHr 分割线元素
type CardHr struct {
	Tag string `json:"tag"`
}

func (h *CardHr) GetTag() string { return h.Tag }

// CardNote 备注元素
type CardNote struct {
	Tag      string      `json:"tag"`
	Elements []*CardText `json:"elements"`
}

func (n *CardNote) GetTag() string { return n.Tag }

// NewCardBuilder 创建卡片构建器
func NewCardBuilder() *CardBuilder {
	return &CardBuilder{
		config: &CardConfig{
			WideScreenMode: true,
		},
		elements: []CardElement{},
	}
}

// SetHeader 设置卡片头部
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

// AddMarkdown 添加 Markdown 文本块
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

// AddPlainText 添加纯文本块
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

// AddFields 添加字段列表（两列布局）
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

// AddDivider 添加分割线
func (b *CardBuilder) AddDivider() *CardBuilder {
	b.elements = append(b.elements, &CardHr{
		Tag: "hr",
	})
	return b
}

// AddNote 添加备注（灰色小字）
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

// Build 构建卡片 JSON
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

// FormatNodeStatus 格式化节点状态为卡片消息
func FormatNodeStatus(nodeName, ip, status string, ready, schedulable bool) (string, error) {
	builder := NewCardBuilder()

	// 根据状态选择颜色主题
	template := "green"
	if !ready {
		template = "red"
	} else if !schedulable {
		template = "orange"
	}

	builder.SetHeader(fmt.Sprintf("节点状态: %s", nodeName), template)

	// 基本信息
	statusIcon := "🔴"
	if ready {
		statusIcon = "🟢"
	}
	scheduleIcon := "🔴"
	if schedulable {
		scheduleIcon = "🟢"
	}

	builder.AddMarkdown(fmt.Sprintf(
		"**IP:** %s\n**状态:** %s %s\n**调度:** %s %s",
		ip,
		statusIcon, boolToText(ready, "Ready", "NotReady"),
		scheduleIcon, boolToText(schedulable, "Schedulable", "Unschedulable"),
	))

	return builder.Build()
}

// FormatNodeList 格式化节点列表为卡片消息
func FormatNodeList(clusterName string, nodes []NodeInfo) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(fmt.Sprintf("集群节点列表: %s", clusterName), "blue")

	builder.AddMarkdown(fmt.Sprintf("**节点总数:** %d", len(nodes)))
	builder.AddDivider()

	// 按状态分组
	readyNodes := []NodeInfo{}
	notReadyNodes := []NodeInfo{}

	for _, node := range nodes {
		if node.Ready {
			readyNodes = append(readyNodes, node)
		} else {
			notReadyNodes = append(notReadyNodes, node)
		}
	}

	// Ready 节点
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

	// NotReady 节点
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

// FormatClusterList 格式化集群列表为卡片消息
func FormatClusterList(clusters []ClusterInfo, currentCluster string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader("集群列表", "blue")

	builder.AddMarkdown(fmt.Sprintf("**集群总数:** %d", len(clusters)))
	builder.AddDivider()

	var sb strings.Builder
	for _, cluster := range clusters {
		icon := "○"
		if cluster.Name == currentCluster {
			icon = "●"
		}
		sb.WriteString(fmt.Sprintf("%s **%s**\n", icon, cluster.Name))
		sb.WriteString(fmt.Sprintf("  节点数: %d\n", cluster.NodeCount))
		if cluster.Name == currentCluster {
			sb.WriteString("  (当前集群)\n")
		}
		sb.WriteString("\n")
	}

	builder.AddMarkdown(sb.String())
	return builder.Build()
}

// FormatError 格式化错误消息为卡片
func FormatError(title, message string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(title, "red")
	builder.AddMarkdown(message)
	return builder.Build()
}

// FormatSuccess 格式化成功消息为卡片
func FormatSuccess(title, message string) (string, error) {
	builder := NewCardBuilder()
	builder.SetHeader(title, "green")
	builder.AddMarkdown(message)
	return builder.Build()
}

// NodeInfo 节点信息
type NodeInfo struct {
	Name        string
	IP          string
	Ready       bool
	Schedulable bool
}

// ClusterInfo 集群信息
type ClusterInfo struct {
	Name      string
	NodeCount int
}

// 辅助函数
func boolToText(value bool, trueText, falseText string) string {
	if value {
		return trueText
	}
	return falseText
}
