package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/k8s-inspect/internal/authz"
	"github.com/k8s-inspect/internal/nodes"
)

// ListNodeLabels returns the labels currently set on a node.
type ListNodeLabels struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *ListNodeLabels) Name() string { return "list_node_labels" }

func (t *ListNodeLabels) Description() string {
	return "List all labels currently set on a node. Useful for checking node pool / team assignment before or after modifying taints (many clusters have controllers that reconcile taints from labels)."
}

func (t *ListNodeLabels) InputSchema() map[string]any { return nodeArgSchema() }

func (t *ListNodeLabels) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	out := struct {
		Cluster string            `json:"cluster,omitempty"`
		Node    string            `json:"node"`
		Total   int               `json:"total"`
		Labels  map[string]string `json:"labels"`
	}{
		Cluster: authz.ClusterNameFrom(ctx),
		Node:    node.Name,
		Total:   len(node.Labels),
		Labels:  node.Labels,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// LabelNode sets or updates a label on a node.
type LabelNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *LabelNode) Name() string { return "label_node" }

func (t *LabelNode) Description() string {
	return "Set or update a label on a specified node. Equivalent to `kubectl label nodes <node> <key>=<value> --overwrite`. Existing value is overwritten if the key already exists. Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
}

func (t *LabelNode) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname). Must be a registered node in the cluster.",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Label key (e.g., 'deeproute.cn/user-type', 'drscaler.deeproute.ai/nodepool').",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Label value (e.g., 'simulation', 'mlp-4090'). May be empty string, but not null/missing.",
			},
		},
		"required": []string{"node", "key", "value"},
	}
}

func (t *LabelNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	if err := checkCallerAllowed(ctx, "modify node labels"); err != nil {
		return "", err
	}

	raw, _ := input["node"].(string)
	key, _ := input["key"].(string)
	value, _ := input["value"].(string)

	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("key is required")
	}

	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if err := checkNodeMutationPermission(ctx, node, "modify node labels"); err != nil {
		return "", err
	}

	if node.Labels == nil {
		node.Labels = map[string]string{}
	}

	existing, had := node.Labels[key]
	if had && existing == value {
		return fmt.Sprintf("[cluster=%s] Node %s already has label %s=%s — no change", clusterTag(ctx), n.Name, key, value), nil
	}
	node.Labels[key] = value

	result, err := t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update node %s labels: %w", n.Name, err)
	}

	// Verify: same rationale as TaintNode. Some clusters run label webhooks
	// that reject or rewrite label values.
	if got, ok := result.Labels[key]; !ok || got != value {
		actualJSON, _ := json.Marshal(result.Labels)
		return "", fmt.Errorf("[cluster=%s] update on node %s appeared to succeed but label %s=%s is NOT present in the object returned by the API server (actual value=%q, present=%v) — a mutating admission webhook or a controller likely reverted it. Actual labels: %s", clusterTag(ctx), n.Name, key, value, got, ok, string(actualJSON))
	}

	action := "added"
	if had {
		action = fmt.Sprintf("updated (was %q)", existing)
	}
	return fmt.Sprintf("✅ [cluster=%s] %s label on node %s: %s=%s (verified in API response)", clusterTag(ctx), action, n.Name, key, value), nil
}

// UnlabelNode removes a label from a node.
type UnlabelNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *UnlabelNode) Name() string { return "unlabel_node" }

func (t *UnlabelNode) Description() string {
	return "Remove a label from a node. Equivalent to `kubectl label nodes <node> <key>-`. Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
}

func (t *UnlabelNode) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname).",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Label key to remove.",
			},
		},
		"required": []string{"node", "key"},
	}
}

func (t *UnlabelNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	if err := checkCallerAllowed(ctx, "modify node labels"); err != nil {
		return "", err
	}

	raw, _ := input["node"].(string)
	key, _ := input["key"].(string)

	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("key is required")
	}

	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if err := checkNodeMutationPermission(ctx, node, "modify node labels"); err != nil {
		return "", err
	}

	if _, had := node.Labels[key]; !had {
		return fmt.Sprintf("[cluster=%s] Node %s has no label with key=%s — nothing removed", clusterTag(ctx), n.Name, key), nil
	}
	delete(node.Labels, key)

	result, err := t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update node %s labels: %w", n.Name, err)
	}

	if _, stillThere := result.Labels[key]; stillThere {
		actualJSON, _ := json.Marshal(result.Labels)
		return "", fmt.Errorf("[cluster=%s] unlabel on node %s appeared to succeed but label %s is still present in the object returned by the API server — a mutating admission webhook or a controller likely reverted it. Actual labels: %s", clusterTag(ctx), n.Name, key, string(actualJSON))
	}

	return fmt.Sprintf("✅ [cluster=%s] removed label from node %s: %s (verified in API response)", clusterTag(ctx), n.Name, key), nil
}
