package builtin

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8s-inspect/internal/cluster"
)

// GPUInspect checks GPU nodes (label deeproute.cn/instance-type=gpu) across one or all clusters.
// A node is anomalous when its nvidia.com/gpu capacity != 8.
// Unhealthy nodes (NotReady or unschedulable) are skipped.
type GPUInspect struct {
	Manager *cluster.Manager
}

func (t *GPUInspect) Name() string { return "gpu_inspect" }

func (t *GPUInspect) Description() string {
	return "Inspect GPU nodes (label deeproute.cn/instance-type=gpu) and report any node whose nvidia.com/gpu capacity is not 8. Skips nodes that are NotReady or unschedulable. Optionally target a specific cluster."
}

func (t *GPUInspect) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cluster": map[string]any{
				"type":        "string",
				"description": "Cluster name to inspect. If omitted, inspects the current cluster.",
			},
		},
	}
}

func (t *GPUInspect) Execute(ctx context.Context, input map[string]any) (string, error) {
	clusterName, _ := input["cluster"].(string)

	type target struct {
		name string
		c    *cluster.Cluster
	}

	var targets []target
	if clusterName != "" {
		c, err := t.Manager.Get(clusterName)
		if err != nil {
			return "", fmt.Errorf("cluster %q not found: %w", clusterName, err)
		}
		targets = append(targets, target{clusterName, c})
	} else {
		c, err := t.Manager.Current()
		if err != nil {
			return "", err
		}
		targets = append(targets, target{t.Manager.CurrentName(), c})
	}

	var sb strings.Builder
	for _, tgt := range targets {
		sb.WriteString(fmt.Sprintf("集群: %s\n", tgt.name))

		nodeList, err := tgt.c.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "deeproute.cn/instance-type=gpu",
		})
		if err != nil {
			sb.WriteString(fmt.Sprintf("  ❌ 获取节点失败: %v\n", err))
			continue
		}

		// Build unhealthy set
		unhealthy := make(map[string]bool)
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			if node.Spec.Unschedulable {
				unhealthy[node.Name] = true
				continue
			}
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
					unhealthy[node.Name] = true
				}
			}
		}

		type anomaly struct {
			name    string
			ip      string
			gpuType string
			cap     int64
		}
		var anomalies []anomaly
		total := 0
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			if unhealthy[node.Name] || skipGPUInspect(node, tgt.name) {
				continue
			}
			total++
			q := node.Status.Capacity["nvidia.com/gpu"]
			cap := q.Value()
			if cap == 8 {
				continue
			}
			var ip string
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					ip = addr.Address
					break
				}
			}
			gpuType := node.Labels["deeproute.cn/gpu-type"]
			if gpuType == "" {
				if idx := strings.LastIndex(node.Name, "."); idx >= 0 {
					gpuType = node.Name[idx+1:]
				}
			}
			anomalies = append(anomalies, anomaly{node.Name, ip, gpuType, cap})
		}

		if len(anomalies) == 0 {
			sb.WriteString(fmt.Sprintf("  ✅ 全部正常，共 %d 个 GPU 节点（每节点 8 张卡）\n", total))
		} else {
			sb.WriteString(fmt.Sprintf("  ⚠️ 发现 %d 个异常节点（共巡检 %d 个）:\n", len(anomalies), total))
			for _, a := range anomalies {
				sb.WriteString(fmt.Sprintf("  • %s (%s) [%s] — 当前 %d 张，期望 8 张\n",
					a.ip, a.name, a.gpuType, a.cap))
			}
		}
	}

	return sb.String(), nil
}

// isEmbeddedGPUNode returns true if any label value contains "orin", "thor", or "jetson"
// (case-insensitive), indicating an embedded platform that should be skipped.
func isEmbeddedGPUNode(node *corev1.Node) bool {
	for _, v := range node.Labels {
		lv := strings.ToLower(v)
		if strings.Contains(lv, "orin") || strings.Contains(lv, "thor") || strings.Contains(lv, "jetson") {
			return true
		}
	}
	if ut := strings.ToLower(node.Labels["deeproute.cn/user-type"]); strings.Contains(ut, "desay") {
		return true
	}
	return false
}

func skipGPUInspect(node *corev1.Node, clusterName string) bool {
	if isEmbeddedGPUNode(node) {
		return true
	}
	if clusterName == "volc-vke" {
		return true
	}
	if clusterName == "cicd" {
		it := strings.ToLower(node.Labels["instance-type"])
		if strings.Contains(it, "2060") || strings.Contains(it, "3060") || strings.Contains(it, "a100") {
			return true
		}
	}
	if clusterName == "jobss" {
		it := strings.ToLower(node.Labels["instance-type"])
		if strings.Contains(it, "a30") {
			return true
		}
	}
	return false
}
