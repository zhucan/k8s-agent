// Package llm 把 Anthropic SDK 包装成一个 Run 入口:接收用户文本,在内部跑 tool use 循环,
// 直到模型给出 end_turn 的文本回复。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/k8s-inspect/internal/tool"
)

const (
	maxIter      = 8
	maxOutTokens = 4096
)

type Client struct {
	api              anthropic.Client
	tools            *tool.Registry
	system           string
	systemGenerator  func() string // 动态生成 system prompt 的回调函数
	model            anthropic.Model
	enableCache      bool
}

// New 构造 Client。
// 从环境变量自动读取:
//   ANTHROPIC_API_KEY  - 必填 (SDK 默认行为)
//   ANTHROPIC_BASE_URL - 可选,设置后请求会发到指定 base URL (用于代理网关 / Anthropic-compatible 服务)
//   ANTHROPIC_MODEL    - 可选,指定使用的模型 (默认: claude-opus-4-7)
//                        支持: claude-opus-4-7, claude-sonnet-4-6, claude-sonnet-3-5-20241022, claude-haiku-4-5-20251001
//   ANTHROPIC_ENABLE_CACHE - 可选,是否启用 prompt caching (默认: true, 设为 false 禁用)
//
// systemPrompt 长度建议 >= 4096 tokens 以触发 prompt cache。
// 如果 systemPrompt 为空字符串,则必须通过 SetSystemGenerator 设置动态生成函数。
func New(apiKey, systemPrompt string, reg *tool.Registry) *Client {
	var opts []option.RequestOption
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
		log.Printf("Anthropic client using base URL: %s", baseURL)
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	api := anthropic.NewClient(opts...)

	// 从环境变量读取模型配置
	model := getModelFromEnv()
	log.Printf("Using model: %s", model)

	// 从环境变量读取 cache 配置
	enableCache := os.Getenv("ANTHROPIC_ENABLE_CACHE") != "false"
	if enableCache {
		log.Printf("Prompt caching enabled")
	} else {
		log.Printf("Prompt caching disabled")
	}

	return &Client{
		api:         api,
		tools:       reg,
		system:      systemPrompt,
		model:       model,
		enableCache: enableCache,
	}
}

// SetSystemGenerator 设置动态 system prompt 生成函数(用于多集群模式)
func (c *Client) SetSystemGenerator(generator func() string) {
	c.systemGenerator = generator
}

// getModelFromEnv 从环境变量读取模型配置,默认使用 claude-opus-4-7
func getModelFromEnv() anthropic.Model {
	modelStr := os.Getenv("ANTHROPIC_MODEL")
	if modelStr == "" {
		return anthropic.ModelClaudeOpus4_7
	}

	// 支持的模型映射
	models := map[string]anthropic.Model{
		"claude-opus-4-7":            anthropic.ModelClaudeOpus4_7,
		"claude-sonnet-4-6":          anthropic.ModelClaudeSonnet4_6,
		"claude-haiku-4-5":           anthropic.ModelClaudeHaiku4_5,
		"claude-haiku-4-5-20251001":  anthropic.ModelClaudeHaiku4_5_20251001,
		"claude-opus-4-6":            anthropic.ModelClaudeOpus4_6,
		"claude-3-haiku-20240307":    anthropic.ModelClaude_3_Haiku_20240307,
	}

	if model, ok := models[modelStr]; ok {
		return model
	}

	// 如果不在预定义列表中，直接使用字符串（支持自定义模型如 dj-claude-opus-4-7）
	log.Printf("Using custom model: %s", modelStr)
	return anthropic.Model(modelStr)
}

// Run 执行一次对话回合,返回最终文本回复。
func (c *Client) Run(ctx context.Context, userText string) (string, error) {
	tools := buildToolParams(c.tools)

	// 动态生成 system prompt (用于多集群模式)
	systemPrompt := c.system
	if c.systemGenerator != nil {
		systemPrompt = c.systemGenerator()
	}

	// 检查是否需要 workaround：某些 API 代理不支持 system + tools 组合
	useSystemWorkaround := os.Getenv("ANTHROPIC_SYSTEM_WORKAROUND") == "true"

	var messages []anthropic.MessageParam
	if useSystemWorkaround {
		// Workaround: 将 system prompt 作为第一条 user message
		messages = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(
				"<system>\n" + systemPrompt + "\n</system>\n\n" + userText,
			)),
		}
		log.Printf("[LLM] Using system prompt workaround (system in user message)")
	} else {
		messages = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userText)),
		}
	}

	for i := 0; i < maxIter; i++ {
		log.Printf("[LLM] Sending request to model %s (iteration %d/%d)", c.model, i+1, maxIter)

		params := anthropic.MessageNewParams{
			Model:     c.model,
			MaxTokens: maxOutTokens,
			Tools:     tools,
			Messages:  messages,
		}

		// 只在不使用 workaround 时添加 system prompt
		if !useSystemWorkaround {
			var systemBlocks []anthropic.TextBlockParam
			if c.enableCache {
				systemBlocks = []anthropic.TextBlockParam{{
					Text:         systemPrompt,
					CacheControl: anthropic.NewCacheControlEphemeralParam(),
				}}
			} else {
				systemBlocks = []anthropic.TextBlockParam{{
					Text: systemPrompt,
				}}
			}
			params.System = systemBlocks
		}

		resp, err := c.api.Messages.New(ctx, params)
		if err != nil {
			log.Printf("[LLM] API error: %v", err)
			return "", fmt.Errorf("claude messages: %w", err)
		}
		log.Printf("[LLM] Received response, stop_reason=%s", resp.StopReason)

		// 把 assistant 回复追加到历史
		messages = append(messages, resp.ToParam())

		var textOut strings.Builder
		var toolResults []anthropic.ContentBlockParamUnion
		var finalResult string // 终结性工具的格式化输出
		for _, block := range resp.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				textOut.WriteString(v.Text)
			case anthropic.ToolUseBlock:
				result, isErr, isFinal := c.execTool(ctx, v.Name, v.JSON.Input.Raw())
				if isFinal && !isErr {
					// 终结性工具:直接返回格式化结果,不再送回模型
					finalResult = result
				} else {
					toolResults = append(toolResults,
						anthropic.NewToolResultBlock(v.ID, result, isErr))
				}
			}
		}

		// 如果有终结性工具结果,直接返回
		if finalResult != "" {
			return finalResult, nil
		}

		// 没有 tool_use,或模型已 end_turn,本回合结束
		if resp.StopReason != anthropic.StopReasonToolUse {
			return strings.TrimSpace(textOut.String()), nil
		}

		// 把 tool_result 作为新一轮 user message 发回
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}

	return "", fmt.Errorf("tool use loop exceeded %d iterations", maxIter)
}

func (c *Client) execTool(ctx context.Context, name, rawJSON string) (string, bool, bool) {
	var input map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &input); err != nil {
		return fmt.Sprintf("invalid tool input: %v", err), true, false
	}
	log.Printf("[tool] %s input=%s", name, rawJSON)

	t, ok := c.tools.Get(name)
	if !ok {
		return fmt.Sprintf("unknown tool: %s", name), true, false
	}

	out, err := t.Execute(ctx, input)
	if err != nil {
		log.Printf("[tool] %s error: %v", name, err)
		return err.Error(), true, false
	}

	// 检查是否是终结性工具
	if formatter, ok := t.(tool.FinalResultFormatter); ok {
		formatted := formatter.FormatFinalResult(out)
		log.Printf("[tool] %s is final, returning formatted result directly", name)
		return formatted, false, true
	}

	if len(out) > 8000 {
		out = out[:8000] + "\n...[truncated]"
	}
	return out, false, false
}

func buildToolParams(reg *tool.Registry) []anthropic.ToolUnionParam {
	list := reg.List()
	out := make([]anthropic.ToolUnionParam, 0, len(list))
	for _, t := range list {
		schema := t.InputSchema()
		props := schemaProperties(schema)

		// 构造 InputSchema
		// 注意：Type 字段默认为 "object"，不需要显式设置
		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: props,
		}

		params := anthropic.ToolParam{
			Name:        t.Name(),
			Description: anthropic.String(t.Description()),
			InputSchema: inputSchema,
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &params})
	}
	return out
}

// schemaProperties 从我们 tool.Tool 用的简化 schema 里抽出 properties 字段。
// 这里允许 schema 本身就是 {type:"object", properties:{...}}, 兜底返回空 map。
func schemaProperties(s map[string]any) map[string]any {
	if p, ok := s["properties"].(map[string]any); ok {
		return p
	}
	return map[string]any{}
}
