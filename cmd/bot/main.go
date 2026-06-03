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
	processedEvents = make(map[string]bool) // deduplication map
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

	// Feishu app credentials
	appID := bot.MustEnv("LARK_APP_ID")
	appSecret := bot.MustEnv("LARK_APP_SECRET")

	// Initialize K8s components
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

	// Create Feishu client
	larkClient = lark.NewClient(appID, appSecret)

	// Start scheduled health check alerts
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

	// Create event dispatcher (parameters must be empty strings)
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(handleMessageEvent).
		OnP2ChatMemberBotAddedV1(handleBotAddedEvent)

	// Create WebSocket long-connection client
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithOnReconnecting(func() {
			log.Println("[lark] WebSocket disconnected, reconnecting...")
		}),
		larkws.WithOnReconnected(func() {
			log.Println("[lark] WebSocket reconnected successfully")
		}))

	// Start WebSocket long connection (blocks main goroutine)
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

// handleMessageEvent handles incoming message events.
func handleMessageEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	sender := event.Event.Sender

	// Get event ID for deduplication
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

	// Deduplicate: skip events already processed
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

	// Ignore messages sent by the bot itself
	if *sender.SenderType == "app" {
		log.Printf("[lark] Message from bot itself, ignoring")
		return nil
	}

	// Only handle text messages
	if *msg.MessageType != "text" {
		log.Printf("[lark] Non-text message, ignoring")
		return nil
	}

	// Extract text content
	text := extractText(*msg.Content, msg.Mentions)
	if text == "" {
		log.Printf("[lark] Empty text, ignoring")
		return nil
	}

	log.Printf("[lark] Text: %q", text)
	log.Printf("[lark] =======================")

	trimmedText := strings.TrimSpace(text)

	// Handle the incoming query
	log.Printf("[lark] Processing query: %q", text)
	runCtx, cancel := context.WithTimeout(ctx, 3 * time.Minute)
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
		// Input matches a tool name — execute directly
		reply, llmErr = processDirectToolCommand(runCtx, text)
	} else if components.LLM != nil {
		// Natural language — route through LLM
		reply, llmErr = components.LLM.Run(runCtx, text)
	} else {
		// No LLM available — prompt user to use command format
		reply = "❌ Unrecognized command\n\nUse tool command format: <tool_name> [json_input]\nType 'help' to see all available tools"
	}

	elapsed := time.Since(start)

	if llmErr != nil {
		log.Printf("[lark] LLM error after %v: %v", elapsed, llmErr)
		reply = "❌ " + llmErr.Error()
	} else {
		log.Printf("[lark] LLM success after %v, reply length: %d", elapsed, len(reply))
	}

	// Send reply
	sendReply(ctx, msg, reply)
	return nil
}

// sendReply sends a reply message (automatically selects text or card format).
func sendReply(ctx context.Context, msg *larkim.EventMessage, reply string) {
	log.Printf("[lark] Sending reply to message %s", *msg.MessageId)

	msgType := larkim.MsgTypeText
	content := fmt.Sprintf(`{"text":"%s"}`, escapeJSON(reply))

	// If the reply looks like structured data, try formatting it as a card
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

// extractText extracts text from message content and strips @mentions.
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
	// Strip @mention placeholders
	for _, mention := range mentions {
		if mention.Key != nil {
			text = strings.ReplaceAll(text, *mention.Key, "")
		}
	}

	// Additional cleanup: remove all @_user_X format tokens
	words := strings.Fields(text)
	var cleaned []string
	for _, word := range words {
		// Skip @_user_X placeholder tokens
		if strings.HasPrefix(word, "@_user_") {
			continue
		}
		cleaned = append(cleaned, word)
	}

	return strings.TrimSpace(strings.Join(cleaned, " "))
}

// escapeJSON escapes special characters in a JSON string value.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// shouldUseCard reports whether the reply should be sent as an interactive card.
func shouldUseCard(reply string) bool {
	// Use card format when the reply contains certain indicators
	indicators := []string{
		"📋", "📊", "🌐", "📦", "📁",
		"✅", "❌", "⚠️",
		"Node Status", "Cluster List", "Pod", "Namespace",
	}

	for _, indicator := range indicators {
		if strings.Contains(reply, indicator) {
			return true
		}
	}

	// Also use card format for long multi-line responses
	lines := strings.Split(reply, "\n")
	return len(lines) > 5
}

// formatAsCard formats a text reply as an interactive card message.
func formatAsCard(reply string) (string, error) {
	// Simple card formatting
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

	// Add header based on content type
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

// detectHeader extracts a card header title and color template from the first line.
func detectHeader(reply string) (string, string) {
	lines := strings.Split(reply, "\n")
	if len(lines) == 0 {
		return "", ""
	}

	firstLine := strings.TrimSpace(lines[0])

	// Map leading emoji to card color theme
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

// convertToMarkdown converts plain text into Lark Markdown format.
func convertToMarkdown(text string) string {
	// Remove the first line if it was used as a card header
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

	// Rejoin remaining lines
	result := strings.Join(lines, "\n")

	// Optional: convert emojis to text markers
	// result = strings.ReplaceAll(result, "✅", "**[OK]**")
	// result = strings.ReplaceAll(result, "❌", "**[ERROR]**")
	// result = strings.ReplaceAll(result, "⚠️", "**[WARNING]**")

	return strings.TrimSpace(result)
}

// isToolCommand reports whether the first token of text matches a registered tool name.
func isToolCommand(text string) bool {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 0 {
		return false
	}
	toolName := strings.TrimSpace(parts[0])
	_, found := components.Tools.Get(toolName)
	return found
}

// processDirectToolCommand parses and executes a tool command from user message text.
// Format: <tool_name> [json_input]
// Example: list_nodes {"filter":"unhealthy"}
// Special: help [tool_name]
func processDirectToolCommand(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "❌ Empty command\n\nFormat: <tool_name> [json_input]\nExample: list_nodes {\"filter\":\"unhealthy\"}\n\nType 'help' to see all available tools", nil
	}

	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 0 {
		return "❌ Invalid command format\n\nFormat: <tool_name> [json_input]\nExample: list_nodes {\"filter\":\"unhealthy\"}\n\nType 'help' to see all available tools", nil
	}

	toolName := strings.TrimSpace(parts[0])

	if toolName == "" || strings.HasPrefix(toolName, "@") {
		return "❌ Invalid tool name\n\nFormat: <tool_name> [json_input]\nExample: list_nodes {\"filter\":\"unhealthy\"}\n\nType 'help' to see all available tools", nil
	}

	// Handle help command
	if toolName == "help" {
		if len(parts) > 1 {
			// help <tool_name> — show detailed info
			specificTool := strings.TrimSpace(parts[1])
			return showToolHelp(specificTool), nil
		}
		// help — show all tools list
		return showAllTools(), nil
	}

	inputJSON := "{}"
	if len(parts) > 1 {
		inputJSON = strings.TrimSpace(parts[1])
	}

	// Find the tool
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
		var toolList strings.Builder
		toolList.WriteString(fmt.Sprintf("❌ Tool not found: %s\n\n", toolName))
		toolList.WriteString("Available tools:\n\n")

		toolList.WriteString("Cluster Management:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "cluster") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\nNode Management:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "node") || strings.Contains(name, "cordon") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\nResource Queries:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "pod") || strings.Contains(name, "namespace") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		toolList.WriteString("\nHardware Info:\n")
		for _, t := range components.Tools.List() {
			name := t.Name()
			if strings.Contains(name, "k8s_") {
				toolList.WriteString(fmt.Sprintf("  • %s\n", name))
			}
		}

		return toolList.String(), nil
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return fmt.Sprintf("❌ Invalid JSON input: %v\n\nInput: %s", err, inputJSON), nil
	}

	result, err := foundTool.Execute(ctx, input)
	if err != nil {
		return fmt.Sprintf("❌ Tool execution failed: %v", err), nil
	}

	return result, nil
}

// showAllTools returns a formatted list of all available tools grouped by category.
func showAllTools() string {
	var result strings.Builder
	result.WriteString("📚 Available Tools\n\n")
	result.WriteString("Usage: <tool_name> [json_input]\n")
	result.WriteString("Tool details: help <tool_name>\n\n")

	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("🌐 Cluster Management\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "cluster") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("🖥️  Node Management\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "node") || strings.Contains(name, "cordon") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("📦 Resource Queries\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "pod") || strings.Contains(name, "namespace") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n═══════════════════════════════════\n")
	result.WriteString("🔧 Hardware Info\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range components.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "k8s_") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("\n💡 Tip: type 'help <tool_name>' for detailed parameter info\n")
	result.WriteString("   Example: help list_nodes\n")

	return result.String()
}

// showToolHelp returns detailed help for a specific tool.
func showToolHelp(toolName string) string {
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
		return fmt.Sprintf("❌ Tool not found: %s\n\nType 'help' to see all available tools", toolName)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📖 Tool details: %s\n\n", toolName))
	result.WriteString(fmt.Sprintf("Description:\n  %s\n\n", foundTool.Description()))

	schema := foundTool.InputSchema()
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		result.WriteString("Parameters:\n")
		for paramName, paramSchema := range props {
			if ps, ok := paramSchema.(map[string]any); ok {
				result.WriteString(fmt.Sprintf("  • %s", paramName))

				if paramType, ok := ps["type"].(string); ok {
					result.WriteString(fmt.Sprintf(" (%s)", paramType))
				}

				if required, ok := schema["required"].([]any); ok {
					isRequired := false
					for _, r := range required {
						if r.(string) == paramName {
							isRequired = true
							break
						}
					}
					if isRequired {
						result.WriteString(" [required]")
					} else {
						result.WriteString(" [optional]")
					}
				} else {
					result.WriteString(" [optional]")
				}

				result.WriteString("\n")

				if desc, ok := ps["description"].(string); ok {
					result.WriteString(fmt.Sprintf("    %s\n", desc))
				}

				if enum, ok := ps["enum"].([]any); ok {
					result.WriteString("    Values: ")
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
		result.WriteString("Parameters: none required\n\n")
	}

	result.WriteString("Examples:\n")
	switch toolName {
	case "list_nodes":
		result.WriteString("  # List all nodes\n")
		result.WriteString("  list_nodes\n\n")
		result.WriteString("  # List unhealthy nodes only\n")
		result.WriteString("  list_nodes {\"filter\":\"unhealthy\"}\n\n")
		result.WriteString("  # List healthy nodes only\n")
		result.WriteString("  list_nodes {\"filter\":\"healthy\"}\n")
	case "switch_cluster":
		result.WriteString("  switch_cluster {\"cluster\":\"prod\"}\n")
	case "node_status":
		result.WriteString("  node_status {\"node\":\"master-01\"}\n")
		result.WriteString("  node_status {\"node\":\"10.1.1.83\"}\n")
	case "list_pods":
		result.WriteString("  # List pods in all namespaces\n")
		result.WriteString("  list_pods {\"namespace\":\"all\"}\n\n")
		result.WriteString("  # List pods in a specific namespace\n")
		result.WriteString("  list_pods {\"namespace\":\"default\"}\n\n")
		result.WriteString("  # List pods on a specific node\n")
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

