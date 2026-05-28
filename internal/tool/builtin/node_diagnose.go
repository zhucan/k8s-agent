package builtin

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/nodes"
)

// DiagnoseNode 诊断节点问题 - 收集 containerd、kubelet、系统日志等信息
type DiagnoseNode struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *DiagnoseNode) Name() string { return "diagnose_node" }

func (t *DiagnoseNode) Description() string {
	return "Diagnose node issues by collecting containerd status, kubelet status, and system logs. Use this when a node is NotReady or has problems."
}

func (t *DiagnoseNode) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname)",
			},
		},
		"required": []string{"node"},
	}
}

func (t *DiagnoseNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	nodeID, ok := input["node"].(string)
	if !ok || nodeID == "" {
		return "", fmt.Errorf("node parameter is required")
	}

	node, err := t.Nodes.Resolve(nodeID)
	if err != nil {
		return "", fmt.Errorf("node %q not found in whitelist: %w", nodeID, err)
	}

	executor := &NodeShellExecutor{
		CS:         t.CS,
		RestConfig: t.RestConfig,
		Nodes:      t.Nodes,
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("=== Node Diagnostics: %s ===\n\n", node.Name))

	// 1. 检查 containerd 状态
	result.WriteString("## Containerd Status\n")
	containerdStatus, err := executor.execOnNodeViaPod(ctx, node.Name,
		"systemctl status containerd --no-pager -l || echo 'containerd service check failed'")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(containerdStatus)
	}
	result.WriteString("\n\n")

	// 2. 检查 kubelet 状态
	result.WriteString("## Kubelet Status\n")
	kubeletStatus, err := executor.execOnNodeViaPod(ctx, node.Name,
		"systemctl status kubelet --no-pager -l || echo 'kubelet service check failed'")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(kubeletStatus)
	}
	result.WriteString("\n\n")

	// 3. 获取最近的 containerd 日志
	result.WriteString("## Recent Containerd Logs (last 50 lines)\n")
	containerdLogs, err := executor.execOnNodeViaPod(ctx, node.Name,
		"journalctl -u containerd -n 50 --no-pager || echo 'containerd logs unavailable'")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(containerdLogs)
	}
	result.WriteString("\n\n")

	// 4. 获取最近的 kubelet 日志
	result.WriteString("## Recent Kubelet Logs (last 50 lines)\n")
	kubeletLogs, err := executor.execOnNodeViaPod(ctx, node.Name,
		"journalctl -u kubelet -n 50 --no-pager || echo 'kubelet logs unavailable'")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(kubeletLogs)
	}
	result.WriteString("\n\n")

	// 5. 检查系统关键错误日志
	result.WriteString("## System Error Logs (last 30 lines with ERROR/FATAL/panic)\n")
	systemErrors, err := executor.execOnNodeViaPod(ctx, node.Name,
		"journalctl -p err -n 30 --no-pager || echo 'system error logs unavailable'")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(systemErrors)
	}
	result.WriteString("\n\n")

	// 6. 检查磁盘空间
	result.WriteString("## Disk Space\n")
	diskSpace, err := executor.execOnNodeViaPod(ctx, node.Name,
		"df -h / /var/lib/containerd /var/lib/kubelet 2>/dev/null || df -h /")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(diskSpace)
	}
	result.WriteString("\n\n")

	// 7. 检查内存使用
	result.WriteString("## Memory Usage\n")
	memUsage, err := executor.execOnNodeViaPod(ctx, node.Name,
		"free -h")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(memUsage)
	}
	result.WriteString("\n\n")

	// 8. 检查 inode 使用情况
	result.WriteString("## Inode Usage\n")
	inodeUsage, err := executor.execOnNodeViaPod(ctx, node.Name,
		"df -i / /var/lib/containerd /var/lib/kubelet 2>/dev/null || df -i /")
	if err != nil {
		result.WriteString(fmt.Sprintf("Error: %v\n", err))
	} else {
		result.WriteString(inodeUsage)
	}

	return result.String(), nil
}
