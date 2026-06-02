// Package llm wraps the Anthropic SDK into a single Run entry point: receives user text,
// runs an internal tool-use loop, and returns the final text reply when the model signals end_turn.
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
	maxIter      = 20
	maxOutTokens = 4096
)

type Client struct {
	api              anthropic.Client
	tools            *tool.Registry
	system           string
	systemGenerator  func() string // callback for dynamically generating the system prompt
	model            anthropic.Model
	enableCache      bool
}

// New constructs a Client.
// Reads from environment variables automatically:
//
//	ANTHROPIC_API_KEY       - required (default SDK behaviour)
//	ANTHROPIC_BASE_URL      - optional; routes requests to a proxy / Anthropic-compatible endpoint
//	ANTHROPIC_MODEL         - optional; model to use (default: claude-opus-4-7)
//	                          Supported: claude-opus-4-7, claude-sonnet-4-6, claude-sonnet-3-5-20241022, claude-haiku-4-5-20251001
//	ANTHROPIC_ENABLE_CACHE  - optional; enable prompt caching (default: true; set to "false" to disable)
//
// systemPrompt should be >= 4096 tokens to benefit from prompt caching.
// If systemPrompt is empty, SetSystemGenerator must be called before Run.
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

	// Read model from environment variable
	model := getModelFromEnv()
	log.Printf("Using model: %s", model)

	// Read cache config from environment variable
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

// SetSystemGenerator sets the dynamic system prompt generator (used in multi-cluster mode).
func (c *Client) SetSystemGenerator(generator func() string) {
	c.systemGenerator = generator
}

// getModelFromEnv reads the model from environment variable; defaults to claude-opus-4-7.
func getModelFromEnv() anthropic.Model {
	modelStr := os.Getenv("ANTHROPIC_MODEL")
	if modelStr == "" {
		return anthropic.ModelClaudeOpus4_7
	}

	// Supported model mapping
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

	// If not in the predefined list, use the string directly (supports custom models like dj-claude-opus-4-7)
	log.Printf("Using custom model: %s", modelStr)
	return anthropic.Model(modelStr)
}

// Run executes one conversation turn and returns the final text reply.
func (c *Client) Run(ctx context.Context, userText string) (string, error) {
	tools := buildToolParams(c.tools)

	// Dynamically generate the system prompt (used in multi-cluster mode)
	systemPrompt := c.system
	if c.systemGenerator != nil {
		systemPrompt = c.systemGenerator()
	}

	// Check if the system+tools workaround is needed (some API proxies don't support the combination)
	useSystemWorkaround := os.Getenv("ANTHROPIC_SYSTEM_WORKAROUND") == "true"

	var messages []anthropic.MessageParam
	if useSystemWorkaround {
		// Workaround: inject the system prompt as the first user message
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

		// Only add the system prompt when not using the workaround
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

		// Append assistant reply to message history
		messages = append(messages, resp.ToParam())

		var textOut strings.Builder
		var toolResults []anthropic.ContentBlockParamUnion
		var finalResult string // formatted output from a final-result tool
		for _, block := range resp.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				textOut.WriteString(v.Text)
			case anthropic.ToolUseBlock:
				result, isErr, isFinal := c.execTool(ctx, v.Name, v.JSON.Input.Raw())
				if isFinal && !isErr {
					// Final-result tool: return formatted result directly without sending back to model
					finalResult = result
				} else {
					toolResults = append(toolResults,
						anthropic.NewToolResultBlock(v.ID, result, isErr))
				}
			}
		}

		// Return immediately if a final-result tool produced output
		if finalResult != "" {
			return finalResult, nil
		}

		// No tool_use, or model signaled end_turn — this turn is complete
		if resp.StopReason != anthropic.StopReasonToolUse {
			return strings.TrimSpace(textOut.String()), nil
		}

		// Send tool_result back as the next user message
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

	// Check whether the tool implements FinalResultFormatter
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

		// Build InputSchema
		// Note: Type defaults to "object" and does not need to be set explicitly
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

// schemaProperties extracts the "properties" field from the simplified schema used by tool.Tool.
// Accepts schemas of the form {type:"object", properties:{...}}; falls back to an empty map.
func schemaProperties(s map[string]any) map[string]any {
	if p, ok := s["properties"].(map[string]any); ok {
		return p
	}
	return map[string]any{}
}
