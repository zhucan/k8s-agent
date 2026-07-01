package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/k8s-inspect/internal/authz"
	"github.com/k8s-inspect/internal/nodes"
)

// clusterTag returns the cluster name attached to ctx, or "unknown" when the
// tool is invoked in single-cluster mode where no cluster context is set.
func clusterTag(ctx context.Context) string {
	if name := authz.ClusterNameFrom(ctx); name != "" {
		return name
	}
	return "unknown"
}

// isMasterNode reports whether the node carries a control-plane / master role
// label (matches kubectl's classification). Mutating operations refuse to run
// against these nodes.
func isMasterNode(node *corev1.Node) bool {
	for label := range node.Labels {
		if label == "node-role.kubernetes.io/master" ||
			label == "node-role.kubernetes.io/control-plane" {
			return true
		}
	}
	return false
}

// checkNodeMutationPermission enforces shared invariants for mutating node
// operations (taints, labels):
//   - caller must be on the allowlist (LARK_TAINT_ALLOWED_EMAILS / _OPENIDS)
//   - target node must not be a master / control-plane node
//
// opDesc is used in the error message (e.g. "modify node taints").
func checkNodeMutationPermission(ctx context.Context, node *corev1.Node, opDesc string) error {
	uid := authz.UserIDFrom(ctx)
	if !authz.TaintAllowed(uid) {
		return fmt.Errorf("permission denied: user %q is not allowed to %s (contact admin to be added to LARK_TAINT_ALLOWED_EMAILS)", uid, opDesc)
	}
	if isMasterNode(node) {
		return fmt.Errorf("refused: node %s is a master/control-plane node — %s not permitted", node.Name, opDesc)
	}
	return nil
}

// checkTaintPermission is a thin wrapper preserving the original API used by
// the taint tools.
func checkTaintPermission(ctx context.Context, node *corev1.Node) error {
	return checkNodeMutationPermission(ctx, node, "modify node taints")
}

var validTaintEffects = map[string]corev1.TaintEffect{
	"NoSchedule":       corev1.TaintEffectNoSchedule,
	"PreferNoSchedule": corev1.TaintEffectPreferNoSchedule,
	"NoExecute":        corev1.TaintEffectNoExecute,
}

func normalizeEffect(effect string) (corev1.TaintEffect, error) {
	if e, ok := validTaintEffects[effect]; ok {
		return e, nil
	}
	for k, v := range validTaintEffects {
		if strings.EqualFold(k, effect) {
			return v, nil
		}
	}
	return "", fmt.Errorf("invalid taint effect %q (must be one of: NoSchedule, PreferNoSchedule, NoExecute)", effect)
}

// ListNodeTaints returns the taints currently set on a node.
type ListNodeTaints struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *ListNodeTaints) Name() string { return "list_node_taints" }

func (t *ListNodeTaints) Description() string {
	return "List all taints currently set on a node. Each taint has key, value, and effect (NoSchedule / PreferNoSchedule / NoExecute)."
}

func (t *ListNodeTaints) InputSchema() map[string]any { return nodeArgSchema() }

func (t *ListNodeTaints) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	type taintOut struct {
		Key    string `json:"key"`
		Value  string `json:"value,omitempty"`
		Effect string `json:"effect"`
	}
	out := struct {
		Cluster string     `json:"cluster,omitempty"`
		Node    string     `json:"node"`
		Total   int        `json:"total"`
		Taints  []taintOut `json:"taints"`
	}{
		Cluster: authz.ClusterNameFrom(ctx),
		Node:    node.Name,
		Total:   len(node.Spec.Taints),
		Taints:  make([]taintOut, 0, len(node.Spec.Taints)),
	}
	for _, tt := range node.Spec.Taints {
		out.Taints = append(out.Taints, taintOut{Key: tt.Key, Value: tt.Value, Effect: string(tt.Effect)})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// TaintNode adds or updates a taint on a node.
type TaintNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *TaintNode) Name() string { return "taint_node" }

func (t *TaintNode) Description() string {
	return "Add or update a taint on a specified node. Equivalent to `kubectl taint nodes <node> <key>=<value>:<effect> --overwrite`. Effect must be NoSchedule, PreferNoSchedule, or NoExecute. If a taint with the same key+effect exists, its value is overwritten."
}

func (t *TaintNode) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname). Must be a registered node in the cluster.",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Taint key (e.g., 'nvidia.com/gpu', 'cloud.deeproute.cn/team').",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Taint value (e.g., '4090', 'simulation'). Optional — omit or pass empty for a taint with no value.",
			},
			"effect": map[string]any{
				"type":        "string",
				"description": "Taint effect. One of: NoSchedule, PreferNoSchedule, NoExecute.",
				"enum":        []string{"NoSchedule", "PreferNoSchedule", "NoExecute"},
			},
		},
		"required": []string{"node", "key", "effect"},
	}
}

func (t *TaintNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	key, _ := input["key"].(string)
	value, _ := input["value"].(string)
	effectRaw, _ := input["effect"].(string)

	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("key is required")
	}
	effect, err := normalizeEffect(effectRaw)
	if err != nil {
		return "", err
	}

	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if err := checkTaintPermission(ctx, node); err != nil {
		return "", err
	}

	newTaint := corev1.Taint{Key: key, Value: value, Effect: effect}
	updated := false
	replaced := false
	for i, existing := range node.Spec.Taints {
		if existing.Key == key && existing.Effect == effect {
			if existing.Value == value {
				return fmt.Sprintf("[cluster=%s] Node %s already has taint %s=%s:%s — no change", clusterTag(ctx), n.Name, key, value, effect), nil
			}
			node.Spec.Taints[i] = newTaint
			updated = true
			replaced = true
			break
		}
	}
	if !updated {
		node.Spec.Taints = append(node.Spec.Taints, newTaint)
	}

	result, err := t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update node %s taints: %w", n.Name, err)
	}

	// Verify the desired taint is actually present in the object the API server
	// returned. If not, a mutating admission webhook or a controller that
	// reconciles node taints (common on GPU clusters) reverted the change,
	// and we would otherwise falsely report success.
	found := false
	for _, tt := range result.Spec.Taints {
		if tt.Key == key && tt.Effect == effect && tt.Value == value {
			found = true
			break
		}
	}
	if !found {
		actualJSON, _ := json.Marshal(result.Spec.Taints)
		return "", fmt.Errorf("[cluster=%s] update on node %s appeared to succeed but taint %s=%s:%s is NOT present in the object returned by the API server — a mutating admission webhook or a node-taint reconciler likely reverted it. Actual taints: %s", clusterTag(ctx), n.Name, key, value, effect, string(actualJSON))
	}

	action := "added"
	if replaced {
		action = "updated"
	}
	return fmt.Sprintf("✅ [cluster=%s] %s taint on node %s: %s=%s:%s (verified in API response)", clusterTag(ctx), action, n.Name, key, value, effect), nil
}

// UntaintNode removes a taint from a node.
type UntaintNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *UntaintNode) Name() string { return "untaint_node" }

func (t *UntaintNode) Description() string {
	return "Remove a taint from a node. Equivalent to `kubectl taint nodes <node> <key>[:<effect>]-`. Matches by key; if effect is provided, only the taint with that specific effect is removed, otherwise all taints with that key are removed."
}

func (t *UntaintNode) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (name, IP, or hostname).",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Taint key to remove.",
			},
			"effect": map[string]any{
				"type":        "string",
				"description": "Optional taint effect. If provided, only the taint with this specific effect is removed. Otherwise all taints with the given key are removed.",
				"enum":        []string{"NoSchedule", "PreferNoSchedule", "NoExecute"},
			},
		},
		"required": []string{"node", "key"},
	}
}

func (t *UntaintNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	key, _ := input["key"].(string)
	effectRaw, _ := input["effect"].(string)

	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("key is required")
	}

	var wantEffect corev1.TaintEffect
	if effectRaw != "" {
		e, err := normalizeEffect(effectRaw)
		if err != nil {
			return "", err
		}
		wantEffect = e
	}

	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if err := checkTaintPermission(ctx, node); err != nil {
		return "", err
	}

	kept := make([]corev1.Taint, 0, len(node.Spec.Taints))
	removed := make([]corev1.Taint, 0)
	for _, existing := range node.Spec.Taints {
		if existing.Key == key && (wantEffect == "" || existing.Effect == wantEffect) {
			removed = append(removed, existing)
			continue
		}
		kept = append(kept, existing)
	}

	if len(removed) == 0 {
		if wantEffect != "" {
			return fmt.Sprintf("[cluster=%s] Node %s has no taint with key=%s effect=%s — nothing removed", clusterTag(ctx), n.Name, key, wantEffect), nil
		}
		return fmt.Sprintf("[cluster=%s] Node %s has no taint with key=%s — nothing removed", clusterTag(ctx), n.Name, key), nil
	}

	node.Spec.Taints = kept
	result, err := t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update node %s taints: %w", n.Name, err)
	}

	// Verify the removed taints did not come back. Same rationale as TaintNode:
	// a webhook or reconciler could accept the update but revert the taints.
	stillPresent := make([]string, 0)
	for _, tt := range result.Spec.Taints {
		for _, r := range removed {
			if tt.Key == r.Key && tt.Effect == r.Effect {
				stillPresent = append(stillPresent, fmt.Sprintf("%s=%s:%s", tt.Key, tt.Value, tt.Effect))
			}
		}
	}
	if len(stillPresent) > 0 {
		actualJSON, _ := json.Marshal(result.Spec.Taints)
		return "", fmt.Errorf("[cluster=%s] untaint on node %s appeared to succeed but taints %s are still present in the object returned by the API server — a mutating admission webhook or a node-taint reconciler likely reverted the removal. Actual taints: %s", clusterTag(ctx), n.Name, strings.Join(stillPresent, ", "), string(actualJSON))
	}

	parts := make([]string, 0, len(removed))
	for _, tt := range removed {
		parts = append(parts, fmt.Sprintf("%s=%s:%s", tt.Key, tt.Value, tt.Effect))
	}
	return fmt.Sprintf("✅ [cluster=%s] removed %d taint(s) from node %s: %s (verified in API response)", clusterTag(ctx), len(removed), n.Name, strings.Join(parts, ", ")), nil
}
