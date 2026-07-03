package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8s-inspect/internal/cluster"
)

// ClusterResources aggregates CPU, memory, and GPU totals across a cluster.
// Master / control-plane and monitor nodes are excluded from every total.
//
// Node categories:
//   - CPU node: neither GPU-typed nor embedded → CPU/memory roll into totals.
//   - Regular GPU node (deeproute.cn/instance-type=gpu, not embedded): only
//     nvidia.com/gpu rolls into total.gpu (broken down by model). CPU/memory
//     are ignored.
//   - Embedded GPU node (orin/thor/jetson/desay): counts as exactly 1 GPU card
//     into total.embedded_gpu (broken down by model). CPU/memory DO roll into
//     total.cpu/total.memory (aggregated from .status.capacity).
type ClusterResources struct {
	Manager *cluster.Manager
}

func (t *ClusterResources) Name() string { return "cluster_resources" }

func (t *ClusterResources) Description() string {
	return "Sum CPU, memory, and GPU capacity for a cluster (broken down by GPU model). Skips master/control-plane and monitor nodes. Regular GPU nodes (deeproute.cn/instance-type=gpu) contribute only nvidia.com/gpu to total.gpu_by_type. Embedded GPU nodes (orin/thor/jetson/desay) are counted separately as 1 card each into total.embedded_gpu_by_type, but their CPU/memory DO roll into the CPU/memory totals."
}

func (t *ClusterResources) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cluster": map[string]any{
				"type":        "string",
				"description": "Cluster name to inspect. If omitted, uses the current cluster.",
			},
		},
	}
}

func (t *ClusterResources) Execute(ctx context.Context, input map[string]any) (string, error) {
	clusterName, _ := input["cluster"].(string)

	var (
		c   *cluster.Cluster
		err error
	)
	if clusterName != "" {
		c, err = t.Manager.Get(clusterName)
		if err != nil {
			return "", fmt.Errorf("cluster %q not found: %w", clusterName, err)
		}
	} else {
		c, err = t.Manager.Current()
		if err != nil {
			return "", err
		}
		clusterName = t.Manager.CurrentName()
	}

	nodeList, err := c.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	totalCPU := resource.NewQuantity(0, resource.DecimalSI)
	totalMem := resource.NewQuantity(0, resource.BinarySI)
	totalGPU := resource.NewQuantity(0, resource.DecimalSI)
	totalEmbeddedGPU := int64(0)

	gpuByType := map[string]int64{}
	embeddedByType := map[string]int64{}

	var cpuNodeCount, gpuNodeCount, embeddedNodeCount, skipMaster, skipMonitor int

	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if isMasterNode(node) {
			skipMaster++
			continue
		}
		if isMonitorNode(node) {
			skipMonitor++
			continue
		}
		cap := node.Status.Capacity
		switch {
		case isEmbeddedGPUNode(node):
			model := detectEmbeddedGPUModel(node)
			embeddedByType[model]++
			totalEmbeddedGPU++
			if cpu := cap.Cpu(); cpu != nil {
				totalCPU.Add(*cpu)
			}
			if mem := cap.Memory(); mem != nil {
				totalMem.Add(*mem)
			}
			embeddedNodeCount++
		case isGPUNode(node) || hasGPUCapacity(cap):
			gpu := cap["nvidia.com/gpu"]
			totalGPU.Add(gpu)
			model := detectGPUModel(node)
			gpuByType[model] += gpu.Value()
			gpuNodeCount++
		default:
			if cpu := cap.Cpu(); cpu != nil {
				totalCPU.Add(*cpu)
			}
			if mem := cap.Memory(); mem != nil {
				totalMem.Add(*mem)
			}
			cpuNodeCount++
		}
	}

	out := struct {
		Cluster           string `json:"cluster"`
		CPUNodeCount      int    `json:"cpu_node_count"`
		GPUNodeCount      int    `json:"gpu_node_count"`
		EmbeddedNodeCount int    `json:"embedded_gpu_node_count"`
		SkippedMaster     int    `json:"skipped_master_nodes"`
		SkippedMonitor    int    `json:"skipped_monitor_nodes"`
		Total             struct {
			CPU              string           `json:"cpu"`
			Memory           string           `json:"memory"`
			GPU              string           `json:"gpu"`
			GPUByType        map[string]int64 `json:"gpu_by_type"`
			EmbeddedGPU      int64            `json:"embedded_gpu"`
			EmbeddedGPUByTyp map[string]int64 `json:"embedded_gpu_by_type"`
		} `json:"total"`
		Note string `json:"note"`
	}{
		Cluster:           clusterName,
		CPUNodeCount:      cpuNodeCount,
		GPUNodeCount:      gpuNodeCount,
		EmbeddedNodeCount: embeddedNodeCount,
		SkippedMaster:     skipMaster,
		SkippedMonitor:    skipMonitor,
		Note:              "total.cpu/total.memory = sum of CPU nodes + embedded GPU nodes (orin/thor/jetson). total.gpu = sum of nvidia.com/gpu across regular GPU nodes, broken down in gpu_by_type. total.embedded_gpu = count of embedded GPU nodes (fixed 1 card each), broken down in embedded_gpu_by_type.",
	}
	out.Total.CPU = totalCPU.String()
	out.Total.Memory = totalMem.String()
	out.Total.GPU = totalGPU.String()
	out.Total.GPUByType = gpuByType
	out.Total.EmbeddedGPU = totalEmbeddedGPU
	out.Total.EmbeddedGPUByTyp = embeddedByType

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// isMonitorNode reports whether the node is a monitoring node. Matches either
// the standard node-role convention (node-role.kubernetes.io/monitor) or the
// deeproute user-type label containing "monitor".
func isMonitorNode(node *corev1.Node) bool {
	if _, ok := node.Labels["node-role.kubernetes.io/monitor"]; ok {
		return true
	}
	if ut := strings.ToLower(node.Labels["deeproute.cn/user-type"]); strings.Contains(ut, "monitor") {
		return true
	}
	return false
}

// isGPUNode reports whether the node is labelled as a GPU node.
func isGPUNode(node *corev1.Node) bool {
	return strings.ToLower(node.Labels["deeproute.cn/instance-type"]) == "gpu"
}

// hasGPUCapacity reports whether the node advertises any nvidia.com/gpu
// capacity. Used as a fallback when the deeproute label isn't set (e.g. on
// Volcengine VKE clusters), so GPU nodes still get classified as GPU rather
// than falling into the CPU bucket.
func hasGPUCapacity(cap corev1.ResourceList) bool {
	q, ok := cap["nvidia.com/gpu"]
	if !ok {
		return false
	}
	return q.Value() > 0
}

// detectGPUModel returns the GPU model for a regular GPU node. Prefers the
// deeproute.cn/gpu-type label; falls back to the last dotted segment of the
// node name (matches gpu_inspect's convention). Returns "unknown" if neither
// yields a value.
func detectGPUModel(node *corev1.Node) string {
	if v := strings.TrimSpace(node.Labels["deeproute.cn/gpu-type"]); v != "" {
		return strings.ToLower(v)
	}
	if idx := strings.LastIndex(node.Name, "."); idx >= 0 && idx+1 < len(node.Name) {
		return strings.ToLower(node.Name[idx+1:])
	}
	return "unknown"
}

// detectEmbeddedGPUModel returns the embedded GPU family (orin, thor, jetson,
// desay) for an embedded node, by scanning label values. Falls back to
// "embedded" when the family can't be identified.
func detectEmbeddedGPUModel(node *corev1.Node) string {
	if ut := strings.ToLower(node.Labels["deeproute.cn/user-type"]); strings.Contains(ut, "desay") {
		return "desay"
	}
	for _, v := range node.Labels {
		lv := strings.ToLower(v)
		switch {
		case strings.Contains(lv, "orinx"):
			return "orinx"
		case strings.Contains(lv, "orin"):
			return "orin"
		case strings.Contains(lv, "thor"):
			return "thor"
		case strings.Contains(lv, "jetson"):
			return "jetson"
		}
	}
	return "embedded"
}
