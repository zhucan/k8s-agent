package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LabelNodeResources sums CPU, Memory, and GPU capacity across nodes matching a label selector.
type LabelNodeResources struct {
	CS *kubernetes.Clientset
}

func (t *LabelNodeResources) Name() string { return "label_node_resources" }

func (t *LabelNodeResources) Description() string {
	return "Sum up total CPU, memory, and GPU (nvidia.com/gpu) capacity of nodes matching a given label selector in the current cluster. Example: label_selector=\"deeproute.cn/user-type=prediction\"."
}

func (t *LabelNodeResources) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label_selector": map[string]any{
				"type":        "string",
				"description": "Kubernetes label selector to filter nodes, e.g. \"deeproute.cn/user-type=prediction\"",
			},
		},
		"required": []string{"label_selector"},
	}
}

func (t *LabelNodeResources) Execute(ctx context.Context, input map[string]any) (string, error) {
	selector, _ := input["label_selector"].(string)
	if selector == "" {
		return "", fmt.Errorf("label_selector is required")
	}

	nodeList, err := t.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	totalCPU := resource.NewQuantity(0, resource.DecimalSI)
	totalMem := resource.NewQuantity(0, resource.BinarySI)
	totalGPU := resource.NewQuantity(0, resource.DecimalSI)

	type nodeEntry struct {
		Name   string `json:"name"`
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
		GPU    string `json:"gpu,omitempty"`
	}
	entries := make([]nodeEntry, 0, len(nodeList.Items))

	for _, node := range nodeList.Items {
		cap := node.Status.Capacity
		cpu := cap.Cpu()
		mem := cap.Memory()
		gpu := cap["nvidia.com/gpu"]

		totalCPU.Add(*cpu)
		totalMem.Add(*mem)
		totalGPU.Add(gpu)

		entry := nodeEntry{
			Name:   node.Name,
			CPU:    cpu.String(),
			Memory: mem.String(),
		}
		if !gpu.IsZero() {
			entry.GPU = gpu.String()
		}
		entries = append(entries, entry)
	}

	out := struct {
		LabelSelector string      `json:"label_selector"`
		NodeCount     int         `json:"node_count"`
		Nodes         []nodeEntry `json:"nodes"`
		Total         struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
			GPU    string `json:"gpu"`
		} `json:"total"`
	}{
		LabelSelector: selector,
		NodeCount:     len(entries),
		Nodes:         entries,
	}
	out.Total.CPU = totalCPU.String()
	out.Total.Memory = totalMem.String()
	out.Total.GPU = totalGPU.String()

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
