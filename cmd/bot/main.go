package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/k8s-inspect/internal/alert"
	"github.com/k8s-inspect/internal/bot"
)

var (
	larkClient      *lark.Client
	components      *bot.Components
	processedEvents = make(map[string]bool) // 用于去重
	eventMutex      sync.Mutex
)

func main() {
	bot.LoadDotEnv(".env")

	var (
		kubeconfigFlag string
		multiCluster   bool
		clusterConfig  string
		noLLM          bool
	)
	flag.StringVar(&kubeconfigFlag, "kubeconfig", "", "Path to kubeconfig")
	flag.BoolVar(&multiCluster, "multi-cluster", false, "Enable multi-cluster mode")
	flag.StringVar(&clusterConfig, "cluster-config", "", "Path to cluster config file")
	flag.BoolVar(&noLLM, "no-llm", false, "Disable LLM mode, use direct tool invocation (parse commands from user messages)")
	flag.Parse()

	// 飞书应用凭证
	appID := bot.MustEnv("LARK_APP_ID")
	appSecret := bot.MustEnv("LARK_APP_SECRET")

	// 初始化 K8s 组件
	ctx := context.Background()
	components = bot.Setup(ctx, bot.Options{
		Kubeconfig:    kubeconfigFlag,
		MultiCluster:  multiCluster,
		ClusterConfig: clusterConfig,
		NoLLM:         noLLM,
	})
	defer components.Stop()

	if noLLM {
		log.Printf("k8s-bot initialized in Direct Tool Mode (nodes=%d)", len(components.Nodes.List()))
		log.Println("📝 Bot will parse tool commands from user messages")
		log.Println("   Format: <tool_name> [json_input]")
		log.Println("   Example: list_nodes {\"filter\":\"unhealthy\"}")
	} else {
		log.Printf("k8s-bot initialized in LLM Mode (nodes=%d)", len(components.Nodes.List()))
	}

	// 创建飞书客户端
	larkClient = lark.NewClient(appID, appSecret)

	// 启动定时巡检告警
	if components.ClusterManager != nil {
		alertChatID := os.Getenv("LARK_ALERT_CHAT_ID")
		var mentionIDs []string
		if raw := os.Getenv("LARK_ALERT_MENTION_OPENIDS"); raw != "" {
			for _, id := range strings.Split(raw, ",") {
				if id = strings.TrimSpace(id); id != "" {
					mentionIDs = append(mentionIDs, id)
				}
			}
		}
		var mentionEmails []string
		if raw := os.Getenv("LARK_ALERT_MENTION_EMAILS"); raw != "" {
			for _, email := range strings.Split(raw, ",") {
				if email = strings.TrimSpace(email); email != "" {
					mentionEmails = append(mentionEmails, email)
				}
			}
		}
		cronExpr := bot.EnvOr("LARK_ALERT_CRON", "0 10 * * *")

		alert.Start(ctx, alert.Config{
			ChatID:        alertChatID,
			MentionIDs:    mentionIDs,
			MentionEmails: mentionEmails,
			CronExpr:      cronExpr,
			LarkClient:    larkClient,
			ClusterMgr:    components.ClusterManager,
		})
	}

	// 创建事件处理器（参数必须为空字符串）
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(handleMessageEvent).
		OnP2ChatMemberBotAddedV1(handleBotAddedEvent)

	// 创建 WebSocket 长连接客户端
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelWarn))

	// 启动 WebSocket 长连接（阻塞主线程）
	log.Println("🚀 Starting WebSocket long connection to Feishu...")
	log.Println("✅ No public URL needed - using WebSocket long connection mode")
	log.Println("📝 Waiting for messages from Feishu...")

	if err := wsClient.Start(context.Background()); err != nil {
		log.Fatalf("❌ WebSocket connection failed: %v", err)
	}
}

func handleBotAddedEvent(ctx context.Context, event *larkim.P2ChatMemberBotAddedV1) error {
	log.Printf("Bot added to chat: %v", event)
	return nil
}

// handleMessageEvent 处理接收到的消息事件
func handleMessageEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	sender := event.Event.Sender

	// 获取事件 ID（用于去重）
	eventID := ""
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		eventID = event.EventV2Base.Header.EventID
	}

	log.Printf("[lark] ===== New Message =====")
	log.Printf("[lark] EventID: %s", eventID)
	log.Printf("[lark] From: %s", *sender.SenderId.OpenId)
	log.Printf("[lark] SenderType: %s", *sender.SenderType)
	log.Printf("[lark] Chat: %s", *msg.ChatId)
	log.Printf("[lark] MessageID: %s", *msg.MessageId)
	log.Printf("[lark] Type: %s", *msg.MessageType)
	log.Printf("[lark] CreateTime: %s", *msg.CreateTime)

	// 事件去重：检查是否已处理过此事件
	if eventID != "" {
		eventMutex.Lock()
		if processedEvents[eventID] {
			eventMutex.Unlock()
			log.Printf("[lark] Event already processed, ignoring (EventID: %s)", eventID)
			return nil
		}
		processedEvents[eventID] = true
		eventMutex.Unlock()
	}

	// 忽略机器人自己发送的消息
	if *sender.SenderType == "app" {
		log.Printf("[lark] Message from bot itself, ignoring")
		return nil
	}

	// 只处理文本消息
	if *msg.MessageType != "text" {
		log.Printf("[lark] Non-text message, ignoring")
		return nil
	}

	// 提取文本内容
	text := extractText(*msg.Content, msg.Mentions)
	if text == "" {
		log.Printf("[lark] Empty text, ignoring")
		return nil
	}

	log.Printf("[lark] Text: %q", text)
	log.Printf("[lark] =======================")

	trimmedText := strings.TrimSpace(text)

	// 处理查询
	log.Printf("[lark] Processing query: %q", text)
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	start := time.Now()
	var reply string
	var llmErr error
	if strings.EqualFold(trimmedText, "help") {
		reply = showAllTools()
	} else if strings.HasPrefix(strings.ToLower(trimmedText), "help ") {
		specificTool := strings.TrimSpace(trimmedText[5:])
		reply = showToolHelp(specificTool)
	} else if isToolCommand(trimmedText) {
		// 输入匹配工具名，直接执行
		reply, llmErr = processDirectToolCommand(runCtx, text)
	} else if components.LLM != nil {
		// 自然语言，走 LLM
		reply, llmErr = components.LLM.Run(runCtx, text)
	} else {
		// 没有 LLM，提示用户用命令格式
		reply = "❌ 无法识别命令\n\n请使用工具命令格式: <tool_name> [json_input]\n输入 'help' 查看所有可用工具"
	}

	elapsed := time.Since(start)

	if llmErr != nil {
		log.Printf("[lark] LLM error after %v: %v", elapsed, llmErr)
		reply = "❌ " + llmErr.Error()
	} else {
		log.Printf("[lark] LLM success after %v, reply length: %d", elapsed, len(reply))
	}

	// 回复结果
	sendReply(ctx, msg, reply)
	return nil
}

// sendReply 发送回复消息（自动选择文本或卡片格式）
func sendReply(ctx context.Context, msg *larkim.EventMessage, reply string) {
	log.Printf("[lark] Sending reply to message %s", *msg.MessageId)

	// 尝试使用卡片消息格式
	msgType := larkim.MsgTypeText
	content := fmt.Sprintf(`{"text":"%s"}`, escapeJSON(reply))

	// 如果回复内容看起来像是结构化数据，尝试转换为卡片消息
	if shouldUseCard(reply) {
		if cardContent, err := formatAsCard(reply); err == nil {
			msgType = larkim.MsgTypeInteractive
			content = cardContent
			log.Printf("[lark] Using card message format")
		} else {
			log.Printf("[lark] Failed to format as card, using text: %v", err)
		}
	}

	_, replyErr := larkClient.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(*msg.MessageId).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(content).
			Build()).
		Build())

	if replyErr != nil {
		log.Printf("[lark] Reply error: %v", replyErr)
	} else {
		log.Println("[lark] Reply sent successfully")
	}
}

// extractText 从消息内容中提取文本，并去除 @mention
func extractText(contentJSON string, mentions []*larkim.MentionEvent) string {
	log.Printf("[extractText] raw contentJSON: %s", contentJSON)
	log.Printf("[extractText] mentions count: %d", len(mentions))
	for i, m := range mentions {
		if m.Key != nil {
			log.Printf("[extractText] mention[%d] key=%s", i, *m.Key)
		}
	}
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentJSON), &content); err != nil {
		return ""
	}

	text := content.Text
	// 去除 @mention 占位符
	for _, mention := range mentions {
		if mention.Key != nil {
			text = strings.ReplaceAll(text, *mention.Key, "")
		}
	}

	// 额外清理：去除所有 @_user_X 格式的文本
	// 使用正则表达式或简单的字符串处理
	words := strings.Fields(text)
	var cleaned []string
	for _, word := range words {
		// 跳过 @_user_X 格式的占位符
		if strings.HasPrefix(word, "@_user_") {
			continue
		}
		cleaned = append(cleaned, word)
	}

	return strings.TrimSpace(strings.Join(cleaned, " "))
}

// escapeJSON 转义 JSON 字符串中的特殊字符
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// shouldUseCard 判断是否应该使用卡片消息格式
func shouldUseCard(reply string) bool {
	// 如果包含特定的标记，使用卡片消息
	indicators := []string{
		"📋", "📊", "🌐", "📦", "📁", // 列表类标记
		"✅", "❌", "⚠️", // 状态标记
		"节点状态", "集群列表", "Pod", "Namespace",
	}

	for _, indicator := range indicators {
		if strings.Contains(reply, indicator) {
			return true
		}
	}

	// 如果内容较长且包含多行，使用卡片消息
	lines := strings.Split(reply, "\n")
	return len(lines) > 5
}

// formatAsCard 将文本格式化为卡片消息
func formatAsCard(reply string) (string, error) {
	// 简单的卡片格式化
	card := map[string]interface{}{
		"config": map[string]interface{}{
			"wide_screen_mode": true,
		},
		"elements": []map[string]interface{}{
			{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": convertToMarkdown(reply),
				},
			},
		},
	}

	// 根据内容添加头部
	if header, template := detectHeader(reply); header != "" {
		card["header"] = map[string]interface{}{
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": header,
			},
			"template": template,
		}
	}

	data, err := json.Marshal(card)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// detectHeader 检测并提取标题
func detectHeader(reply string) (string, string) {
	lines := strings.Split(reply, "\n")
	if len(lines) == 0 {
		return "", ""
	}

	firstLine := strings.TrimSpace(lines[0])

	// 检测标题和对应的颜色主题
	headers := map[string]string{
		"📋": "blue",
		"📊": "blue",
		"🌐": "blue",
		"📦": "blue",
		"📁": "blue",
		"✅": "green",
		"❌": "red",
		"⚠️": "orange",
	}

	for emoji, template := range headers {
		if strings.HasPrefix(firstLine, emoji) {
			title := strings.TrimSpace(strings.TrimPrefix(firstLine, emoji))
			return title, template
		}
	}

	return "", "blue"
}

// convertToMarkdown 转换文本为 Markdown 格式
func convertToMarkdown(text string) string {
	// 移除第一行（如果是标题）
	lines := strings.Split(text, "\n")
	if len(lines) > 0 {
		firstLine := strings.TrimSpace(lines[0])
		for emoji := range map[string]bool{"📋": true, "📊": true, "🌐": true, "📦": true, "📁": true, "✅": true, "❌": true, "⚠️": true} {
			if strings.HasPrefix(firstLine, emoji) {
				lines = lines[1:]
				break
			}
		}
	}

	// 重新组合
	result := strings.Join(lines, "\n")

	// 转换 emoji 为文本标记（可选）
	// result = strings.ReplaceAll(result, "✅", "**[OK]**")
	// result = strings.ReplaceAll(result, "❌", "**[ERROR]**")
	// result = strings.ReplaceAll(result, "⚠️", "**[WARNING]**")

	return strings.TrimSpace(result)
}

// isToolCommand 判断输入的第一个 token 是否匹配已注册的工具名
func isToolCommand(text string) bool {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 0 {
		return false
	}
	toolName := strings.TrimSpace(parts[0])
	_, found := components.Tools.Get(toolName)
	return found
}

// processDirectToolCommand 在非 LLM 模式下解析并执行工具命令
// 格式: <tool_name> [json_input]
// 示例: list_nodes {"filter":"unhealthy"}
// 特殊命令: help [tool_name]
func processDirectToolCommand(ctx context.Context, text string) (string, error) {
	// 清理输入文本
	text = strings.TrimSpace(text)
	if text == "" {
		return "❌ 命令为空\n\n格式: <tool_name> [json_input]\n示例: list_nodes {\"filter\":\"unhealthy\"}\n\n输入 'help' 查看所有可用工具", nil
	}

	// 解析命令: <tool_name> [json_input]
	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 0 {
		return "❌ 命令格式错误\n\n格式: <tool_name> [json_input]\n示例: list_nodes {\"filter\":\"unhealthy\"}\n\n输入 'help' 查看所有可用工具", nil
	}

	toolName := strings.TrimSpace(parts[0])

	// 验证工具名称不为空且不包含特殊字符
	if toolName == "" || strings.HasPrefix(toolName, "@") {
		return "❌ 无效的工具名称\n\n格式: <tool_name> [json_input]\n示例: list_nodes {\"filter\":\"unhealthy\"}\n\n输入 'help' 查看所有可用工具", nil
	}

	// 处理 help 命令
	if toolName == "help" {
		if len(parts) > 1 {
			// help <tool_name> - 显示特定工具的详细信息
			specificTool := strings.TrimSpace(parts[1])
			return showToolHelp(specificTool), nil
		}
		// help - 显示所有工具列表
		return showAllTools(), nil
	}

	inputJSON := "{}"
	if len(parts) > 1 {
		inputJSON = strings.TrimSpace(parts[1])
	}

	// 查找工具
	var foundTool interface {
		Name() string
		Execute(context.Context, map[string]any) (string, error)
	}

	for _, t := range components.Tools.List() {
		if t.Name() == toolName {
			foundTool = t
			break
		}
	}

	if foundTool == nil {
		// 工具未找到，列出所有可用工具
		var toolList strings.Builder
		toolList.WriteString(fmt.Sprintf("❌ 工具未找到: %s\n\n", toolName))
		toolList.WriteString("可用工具:\n\n")

		// 按类别分组
		toolList.WriteString("集群管理:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "cluster") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\n节点管理:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "node") || strings.Contains(name, "cordon") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\n资源查询:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "pod") || strings.Contains(name, "namespace") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\n硬件信息:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "k8s_") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		return toolList.String(), nil
	}

	// 解析输入
	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return fmt.Sprintf("❌ JSON 格式错误: %v\n\n输入的 JSON: %s", err, inputJSON), nil
	}

	// 执行工具
	result, err := foundTool.Execute(ctx, input)
	if err != nil {
		return fmt.Sprintf("❌ 工具执行失败: %v", err), nil
	}

	return result, nil
}

// showAllTools 显示所有可用工具的列表
func showAllTools() string {
	var result strings.Builder
	result.WriteString("📚 可用工具列表\n\n")
	result.WriteString("使用方法: <tool_name> [json_input]\n")
	result.WriteString("查看工具详情: help <tool_name>\n\n")

	// 按类别分组显示
	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("🌐 集群管理\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "cluster") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("🖥️  节点管理\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "node") || strings.Contains(name, "cordon") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("📦 资源查询\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "pod") || strings.Contains(name, "namespace") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("🔧 硬件信息\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "k8s_") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n💡 提示: 输入 'help <tool_name>' 查看工具的详细参数说明\n")
	result.WriteString("   示例: help list_nodes\n")

	return result.String()
}

// showToolHelp 显示特定工具的详细帮助信息
func showToolHelp(toolName string) string {
	// 查找工具
	var foundTool interface {
		Name() string
		Description() string
		InputSchema() map[string]any
	}

	for _, t := range components.Tools.List() {
		if t.Name() == toolName {
			foundTool = t
			break
		}
	}

	if foundTool == nil {
		return fmt.Sprintf("❌ 工具未找到: %s\n\n输入 'help' 查看所有可用工具", toolName)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📖 工具详情: %s\n\n", toolName))

	// 解析并显示参数 schema
	schema := foundTool.InputSchema()
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		result.WriteString("参数:\n")
		for paramName, paramSchema := range props {
			if ps, ok := paramSchema.(map[string]any); ok {
				result.WriteString(fmt.Sprintf("  • %s", paramName))

				// 参数类型
				if paramType, ok := ps["type"].(string); ok {
					result.WriteString(fmt.Sprintf(" (%s)", paramType))
				}

				// 是否必需
				if required, ok := schema["required"].([]any); ok {
					isRequired := false
					for _, r := range required {
						if r.(string) == paramName {
							isRequired = true
							break
						}
					}
					if isRequired {
						result.WriteString(" [必需]")
					} else {
						result.WriteString(" [可选]")
					}
				} else {
					result.WriteString(" [可选]")
				}

				result.WriteString("\n")

				// 参数描述
				if desc, ok := ps["description"].(string); ok {
					result.WriteString(fmt.Sprintf("    %s\n", desc))
				}

				// 枚举值
				if enum, ok := ps["enum"].([]any); ok {
					result.WriteString("    可选值: ")
					enumStrs := make([]string, len(enum))
					for i, e := range enum {
						enumStrs[i] = fmt.Sprintf("%v", e)
					}
					result.WriteString(strings.Join(enumStrs, ", "))
					result.WriteString("\n")
				}

				result.WriteString("\n")
			}
		}
	} else {
		result.WriteString("参数: 无需参数或使用默认值\n\n")
	}

	// 使用示例
	result.WriteString("使用示例:\n")
	switch toolName {
	case "list_nodes":
		result.WriteString("  # 列出所有节点\n")
		result.WriteString("  list_nodes\n\n")
		result.WriteString("  # 只列出不健康的节点\n")
		result.WriteString("  list_nodes {\"filter\":\"unhealthy\"}\n\n")
		result.WriteString("  # 只列出健康的节点\n")
		result.WriteString("  list_nodes {\"filter\":\"healthy\"}\n")
	case "switch_cluster":
		result.WriteString("  switch_cluster {\"cluster\":\"prod\"}\n")
	case "node_status":
		result.WriteString("  node_status {\"node\":\"master-01\"}\n")
		result.WriteString("  node_status {\"node\":\"10.1.1.83\"}\n")
	case "list_pods":
		result.WriteString("  # 列出所有命名空间的 Pod\n")
		result.WriteString("  list_pods {\"namespace\":\"all\"}\n\n")
		result.WriteString("  # 列出特定命名空间的 Pod\n")
		result.WriteString("  list_pods {\"namespace\":\"default\"}\n\n")
		result.WriteString("  # 列出特定节点上的 Pod\n")
		result.WriteString("  list_pods {\"namespace\":\"all\",\"field_selector\":\"spec.nodeName=master-01\"}\n")
	case "cordon_node":
		result.WriteString("  cordon_node {\"name\":\"master-01\"}\n")
	case "uncordon_node":
		result.WriteString("  uncordon_node {\"name\":\"master-01\"}\n")
	case "diagnose_node":
		result.WriteString("  diagnose_node {\"node\":\"master-01\"}\n")
		result.WriteString("  diagnose_node {\"node\":\"10.1.1.83\"}\n")
	case "k8s_hardware_info", "k8s_cpu_info", "k8s_memory_info", "k8s_network_info":
		result.WriteString(fmt.Sprintf("  %s {\"node\":\"master-01\"}\n", toolName))
		result.WriteString(fmt.Sprintf("  %s {\"node\":\"10.1.1.83\"}\n", toolName))
	default:
		result.WriteString(fmt.Sprintf("  %s\n", toolName))
	}

	return result.String()
}

