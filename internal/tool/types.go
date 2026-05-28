package tool

import "context"

// Tool 是 LLM 可调用的能力单元。
// 命令在 Execute 内部硬编码,LLM 只能选 tool 名 + 提供参数(主要是 node 标识)。
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any // JSON schema(Anthropic tool 输入定义)
	Execute(ctx context.Context, input map[string]any) (string, error)
}

// FinalResultFormatter 可选接口:实现此接口的 tool 会在执行后直接返回格式化结果给用户,
// 不再送回 LLM 进行二次总结。适用于纯查询类工具(list_nodes / node_status 等)。
type FinalResultFormatter interface {
	Tool
	// FormatFinalResult 把 Execute 的原始输出格式化成用户友好的最终文本。
	// 返回的字符串会直接作为对话回复,不再经过 LLM。
	FormatFinalResult(rawOutput string) string
}
