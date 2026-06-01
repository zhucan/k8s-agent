package builtin

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/nodes"
)

// CollectLogs collects system, kubelet, or containerd logs from a specified node.
type CollectLogs struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *CollectLogs) Name() string { return "collect_logs" }

func (t *CollectLogs) Description() string {
	return "Collect system logs, kubelet logs, or containerd logs from a specified node. Supports choosing log type and number of lines."
}

func (t *CollectLogs) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname)",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "Log type: system (system journal), kubelet, containerd, all (all types)",
				"enum":        []string{"system", "kubelet", "containerd", "all"},
			},
			"lines": map[string]any{
				"type":        "number",
				"description": "Number of log lines to collect (default: 200)",
			},
		},
		"required": []string{"node"},
	}
}

func (t *CollectLogs) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	logType, _ := input["type"].(string)
	linesFloat, _ := input["lines"].(float64)

	if logType == "" {
		logType = "all"
	}
	lines := 200
	if linesFloat > 0 {
		lines = int(linesFloat)
	}
	if lines > 2000 {
		lines = 2000
	}

	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	executor := &NodeShellExecutor{CS: t.CS, RestConfig: t.RestConfig, Nodes: t.Nodes}
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("📋 Node %s (%s) — Log Collection\n", n.Name, n.InternalIP))
	sb.WriteString(fmt.Sprintf("Type: %s | Lines: %d\n", logType, lines))
	sb.WriteString("═══════════════════════════════\n\n")

	type logTask struct {
		name string
		cmd  string
	}

	var tasks []logTask

	if logType == "system" || logType == "all" {
		tasks = append(tasks,
			logTask{
				name: "System Journal (journalctl)",
				cmd:  fmt.Sprintf("journalctl --no-pager -n %d --priority=err..emerg 2>/dev/null || echo '(journalctl unavailable)'", lines),
			},
			logTask{
				name: "Kernel Log (dmesg)",
				cmd:  fmt.Sprintf("dmesg --time-format iso 2>/dev/null | tail -n %d || dmesg | tail -n %d", lines, lines),
			},
		)
	}

	if logType == "kubelet" || logType == "all" {
		tasks = append(tasks, logTask{
			name: "Kubelet Logs",
			cmd:  fmt.Sprintf("journalctl --no-pager -n %d -u kubelet 2>/dev/null || echo '(kubelet logs unavailable)'", lines),
		})
	}

	if logType == "containerd" || logType == "all" {
		tasks = append(tasks, logTask{
			name: "Containerd Logs",
			cmd:  fmt.Sprintf("journalctl --no-pager -n %d -u containerd 2>/dev/null || echo '(containerd logs unavailable)'", lines),
		})
	}

	for _, task := range tasks {
		sb.WriteString(fmt.Sprintf("=== %s ===\n", task.name))
		output, err := executor.execOnNodeViaPod(ctx, n.Name, task.cmd)
		if err != nil {
			sb.WriteString(fmt.Sprintf("❌ Collection failed: %v\n", err))
		} else if strings.TrimSpace(output) == "" {
			sb.WriteString("(no output)\n")
		} else {
			sb.WriteString(output)
			if !strings.HasSuffix(output, "\n") {
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
