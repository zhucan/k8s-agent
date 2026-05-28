package llm

import (
	"context"
	"testing"

	"github.com/k8s-inspect/internal/tool"
)

// mockTool 普通工具
type mockTool struct{}

func (t *mockTool) Name() string                                              { return "mock_tool" }
func (t *mockTool) Description() string                                       { return "mock" }
func (t *mockTool) InputSchema() map[string]any                               { return map[string]any{} }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return "raw output", nil
}

// mockFinalTool 终结性工具
type mockFinalTool struct{}

func (t *mockFinalTool) Name() string                                              { return "mock_final_tool" }
func (t *mockFinalTool) Description() string                                       { return "mock final" }
func (t *mockFinalTool) InputSchema() map[string]any                               { return map[string]any{} }
func (t *mockFinalTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return "raw output", nil
}
func (t *mockFinalTool) FormatFinalResult(_ string) string {
	return "✅ formatted final result"
}

func TestExecTool_FinalResultFormatter(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockTool{})
	reg.Register(&mockFinalTool{})

	c := &Client{tools: reg}

	// 测试普通工具
	result, isErr, isFinal := c.execTool(context.Background(), "mock_tool", "{}")
	if isErr {
		t.Errorf("expected no error for mock_tool")
	}
	if isFinal {
		t.Errorf("expected mock_tool to not be final")
	}
	if result != "raw output" {
		t.Errorf("expected 'raw output', got %q", result)
	}

	// 测试终结性工具
	result, isErr, isFinal = c.execTool(context.Background(), "mock_final_tool", "{}")
	if isErr {
		t.Errorf("expected no error for mock_final_tool")
	}
	if !isFinal {
		t.Errorf("expected mock_final_tool to be final")
	}
	if result != "✅ formatted final result" {
		t.Errorf("expected formatted result, got %q", result)
	}
}
