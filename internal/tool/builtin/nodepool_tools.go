package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/authz"
	"github.com/k8s-inspect/internal/nodes"
)

// nodePoolGVR is the group/version/resource for the drscaler NodePool CRD.
// Path to the node list inside the CR is spec.configuration.fixedNodes.
var nodePoolGVR = schema.GroupVersionResource{
	Group:    "infrastructure.deeproute.ai",
	Version:  "v1alpha1",
	Resource: "nodepools",
}

// nodePoolFixedNodesPath is the JSON path of the fixed-node list inside a
// NodePool CR. It is centralized here so the read and write tools stay
// consistent.
var nodePoolFixedNodesPath = []string{"spec", "configuration", "fixedNodes"}

func newDynamicClient(rc *rest.Config) (dynamic.Interface, error) {
	if rc == nil {
		return nil, fmt.Errorf("no rest config available for dynamic client")
	}
	return dynamic.NewForConfig(rc)
}

// getFixedNodes extracts spec.configuration.fixedNodes from an unstructured
// NodePool, returning a plain []string. Missing / empty is not an error.
func getFixedNodes(np *unstructured.Unstructured) ([]string, error) {
	raw, found, err := unstructured.NestedStringSlice(np.Object, nodePoolFixedNodesPath...)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", nodePoolFixedNodesPath, err)
	}
	if !found {
		return nil, nil
	}
	return raw, nil
}

// findPoolContainingNode scans all NodePools and returns the name of the pool
// that lists nodeName in its fixedNodes, or "" if none. Used before adding a
// node to a pool so we can refuse duplicate assignments across pools.
func findPoolContainingNode(ctx context.Context, dc dynamic.Interface, nodeName string) (string, error) {
	list, err := dc.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodepools: %w", err)
	}
	for i := range list.Items {
		np := &list.Items[i]
		fixed, err := getFixedNodes(np)
		if err != nil {
			return "", fmt.Errorf("pool %s: %w", np.GetName(), err)
		}
		for _, n := range fixed {
			if n == nodeName {
				return np.GetName(), nil
			}
		}
	}
	return "", nil
}

// checkNodePoolMutationPermission enforces the same invariants as taint/label
// mutations. NodePool assignment ultimately drives label + taint reconciliation
// via the drscaler controller, so the security surface is identical: caller
// must be allowlisted, and the target must not be a master/control-plane node.
//
// nodeName here is the K8s node object name (as it would appear in `kubectl
// get node`). We look it up so we can check the role labels — the pool's
// fixedNodes list uses the same string, but the label check needs the Node.
func checkNodePoolMutationPermission(ctx context.Context, cs *kubernetes.Clientset, nodeName string) error {
	node, err := cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node %s for permission check: %w", nodeName, err)
	}
	return checkNodeMutationPermission(ctx, node, "modify NodePool membership")
}

// ListNodePools lists all NodePool CRs in the current cluster with a summary
// of each pool's fixed-node count and a short preview.
type ListNodePools struct {
	RestConfig *rest.Config
}

func (t *ListNodePools) Name() string { return "list_nodepools" }

func (t *ListNodePools) Description() string {
	return "List all NodePool CRs (infrastructure.deeproute.ai/v1alpha1). Shows each pool's name, fixed-node count, and a short preview of the first few nodes. NodePools drive node label/taint reconciliation, so moving nodes between pools is the canonical way to change a node's team/pool assignment. Read-only."
}

func (t *ListNodePools) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dummy": map[string]any{
				"type":        "string",
				"description": "Unused parameter (workaround for API compatibility)",
			},
		},
	}
}

func (t *ListNodePools) Execute(ctx context.Context, _ map[string]any) (string, error) {
	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}
	list, err := dc.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodepools: %w", err)
	}

	type poolSummary struct {
		Name      string   `json:"name"`
		NodeCount int      `json:"node_count"`
		Preview   []string `json:"preview,omitempty"`
	}
	pools := make([]poolSummary, 0, len(list.Items))
	for i := range list.Items {
		np := &list.Items[i]
		nodes, err := getFixedNodes(np)
		if err != nil {
			return "", fmt.Errorf("pool %s: %w", np.GetName(), err)
		}
		preview := nodes
		if len(preview) > 5 {
			preview = append([]string(nil), nodes[:5]...)
			preview = append(preview, fmt.Sprintf("... (+%d more)", len(nodes)-5))
		}
		pools = append(pools, poolSummary{
			Name:      np.GetName(),
			NodeCount: len(nodes),
			Preview:   preview,
		})
	}

	out := struct {
		Cluster string        `json:"cluster,omitempty"`
		Total   int           `json:"total"`
		Pools   []poolSummary `json:"pools"`
	}{
		Cluster: authz.ClusterNameFrom(ctx),
		Total:   len(pools),
		Pools:   pools,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// MoveNodeBetweenPools atomically removes a node from one NodePool and adds it
// to another. Both updates happen in the same Execute call so the LLM does not
// have to orchestrate two tool calls (and cannot leave the node in an
// inconsistent "in neither pool" state due to LLM error).
//
// The two Update calls are still separate API operations — if the second one
// fails, the first has already committed. The returned error surfaces this
// explicitly so the caller knows the node currently belongs to no pool.
type MoveNodeBetweenPools struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *MoveNodeBetweenPools) Name() string { return "move_node_between_pools" }

func (t *MoveNodeBetweenPools) Description() string {
	return "Move a node from one NodePool to another in a single operation. Equivalent to remove_node_from_pool(from) then add_node_to_pool(to), but does both in one tool call. This is the canonical way to change a node's team/pool assignment — the drscaler controller will then reconcile labels and taints on the node to match the new pool (no need to modify labels or taints directly). Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
}

func (t *MoveNodeBetweenPools) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (K8s node name, IP, or hostname).",
			},
			"from_pool": map[string]any{
				"type":        "string",
				"description": "Source NodePool CR name (e.g. 'simulation-4090'). Node must currently be in this pool.",
			},
			"to_pool": map[string]any{
				"type":        "string",
				"description": "Destination NodePool CR name (e.g. 'mlp-4090').",
			},
		},
		"required": []string{"node", "from_pool", "to_pool"},
	}
}

func (t *MoveNodeBetweenPools) Execute(ctx context.Context, input map[string]any) (string, error) {
	rawNode, _ := input["node"].(string)
	from, _ := input["from_pool"].(string)
	to, _ := input["to_pool"].(string)
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return "", fmt.Errorf("from_pool and to_pool are required")
	}
	if from == to {
		return "", fmt.Errorf("from_pool and to_pool are the same (%s) — nothing to move", from)
	}

	nodeName, err := resolveNodeName(t.Nodes, rawNode)
	if err != nil {
		return "", err
	}
	if err := checkNodePoolMutationPermission(ctx, t.CS, nodeName); err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	// Preflight: confirm source pool actually has the node and destination
	// doesn't already have it, so we fail before mutating anything.
	fromNP, err := dc.Resource(nodePoolGVR).Get(ctx, from, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get from_pool %s: %w", from, err)
	}
	fromFixed, err := getFixedNodes(fromNP)
	if err != nil {
		return "", err
	}
	inFrom := false
	for _, n := range fromFixed {
		if n == nodeName {
			inFrom = true
			break
		}
	}
	if !inFrom {
		// Not an error if the node is already in to_pool — treat as no-op.
		toNP, err := dc.Resource(nodePoolGVR).Get(ctx, to, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("node %s is not in from_pool %s, and failed to check to_pool %s: %w", nodeName, from, to, err)
		}
		toFixed, err := getFixedNodes(toNP)
		if err != nil {
			return "", err
		}
		for _, n := range toFixed {
			if n == nodeName {
				return fmt.Sprintf("[cluster=%s] node %s is already in to_pool %s (and not in %s) — nothing to move", clusterTag(ctx), nodeName, to, from), nil
			}
		}
		return "", fmt.Errorf("[cluster=%s] node %s is not in from_pool %s (and not in to_pool %s either) — check which pool it belongs to with list_nodepools", clusterTag(ctx), nodeName, from, to)
	}

	toNP, err := dc.Resource(nodePoolGVR).Get(ctx, to, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get to_pool %s: %w", to, err)
	}
	toFixed, err := getFixedNodes(toNP)
	if err != nil {
		return "", err
	}
	for _, n := range toFixed {
		if n == nodeName {
			return "", fmt.Errorf("[cluster=%s] node %s is already in both %s and %s — inconsistent state, resolve with remove_node_from_pool before moving", clusterTag(ctx), nodeName, from, to)
		}
	}

	// Remove from source.
	fromKept := make([]string, 0, len(fromFixed))
	for _, n := range fromFixed {
		if n != nodeName {
			fromKept = append(fromKept, n)
		}
	}
	if err := unstructured.SetNestedStringSlice(fromNP.Object, fromKept, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set from_pool fixedNodes: %w", err)
	}
	fromResult, err := dc.Resource(nodePoolGVR).Update(ctx, fromNP, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update from_pool %s: %w", from, err)
	}
	fromAfter, _ := getFixedNodes(fromResult)
	for _, n := range fromAfter {
		if n == nodeName {
			return "", fmt.Errorf("[cluster=%s] remove from %s appeared to succeed but node %s is still present — controller likely reverted it. Aborting before touching %s", clusterTag(ctx), from, nodeName, to)
		}
	}

	// Add to destination.
	toNewFixed := append([]string(nil), toFixed...)
	toNewFixed = append(toNewFixed, nodeName)
	if err := unstructured.SetNestedStringSlice(toNP.Object, toNewFixed, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("[cluster=%s] set to_pool fixedNodes: %w — node %s has been removed from %s but NOT added to %s. Recover with: add_node_to_pool(pool=%s, node=%s)", clusterTag(ctx), err, nodeName, from, to, to, nodeName)
	}
	toResult, err := dc.Resource(nodePoolGVR).Update(ctx, toNP, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("[cluster=%s] update to_pool %s: %w — node %s has been removed from %s but NOT added to %s. Recover with: add_node_to_pool(pool=%s, node=%s)", clusterTag(ctx), to, err, nodeName, from, to, to, nodeName)
	}
	toAfter, _ := getFixedNodes(toResult)
	present := false
	for _, n := range toAfter {
		if n == nodeName {
			present = true
			break
		}
	}
	if !present {
		afterJSON, _ := json.Marshal(toAfter)
		return "", fmt.Errorf("[cluster=%s] add to %s appeared to succeed but node %s is NOT present — controller likely reverted it. Node has been removed from %s and is currently in NO pool. Actual fixedNodes for %s: %s. Recover with: add_node_to_pool(pool=%s, node=%s)", clusterTag(ctx), to, nodeName, from, to, string(afterJSON), to, nodeName)
	}

	return fmt.Sprintf("✅ [cluster=%s] moved node %s from NodePool %s to %s (verified in API response). Controller will reconcile labels/taints in a few seconds — verify with list_node_labels + list_node_taints.", clusterTag(ctx), nodeName, from, to), nil
}

// AddNodeToPool appends a node to a NodePool's spec.configuration.fixedNodes.
// Refuses if the node is already assigned to any pool (caller should remove
// it first or use a dedicated move operation).
type AddNodeToPool struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *AddNodeToPool) Name() string { return "add_node_to_pool" }

func (t *AddNodeToPool) Description() string {
	return "Add a node to a NodePool's spec.configuration.fixedNodes list. The drscaler controller will then reconcile the node's labels and taints to match the pool. Refuses if the node is already in another pool — remove it from that pool first (or use move_node_between_pools). Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
}

func (t *AddNodeToPool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pool": map[string]any{
				"type":        "string",
				"description": "Target NodePool CR name (e.g. 'mlp-4090').",
			},
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (K8s node name, IP, or hostname). Will be resolved to the canonical node name used in fixedNodes.",
			},
		},
		"required": []string{"pool", "node"},
	}
}

func (t *AddNodeToPool) Execute(ctx context.Context, input map[string]any) (string, error) {
	pool, _ := input["pool"].(string)
	rawNode, _ := input["node"].(string)
	if strings.TrimSpace(pool) == "" {
		return "", fmt.Errorf("pool is required")
	}

	nodeName, err := resolveNodeName(t.Nodes, rawNode)
	if err != nil {
		return "", err
	}

	if err := checkNodePoolMutationPermission(ctx, t.CS, nodeName); err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	existingPool, err := findPoolContainingNode(ctx, dc, nodeName)
	if err != nil {
		return "", err
	}
	if existingPool != "" && existingPool != pool {
		return "", fmt.Errorf("[cluster=%s] node %s is already in NodePool %q — remove it from that pool first (or use move_node_between_pools)", clusterTag(ctx), nodeName, existingPool)
	}

	np, err := dc.Resource(nodePoolGVR).Get(ctx, pool, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get nodepool %s: %w", pool, err)
	}
	fixed, err := getFixedNodes(np)
	if err != nil {
		return "", err
	}
	for _, n := range fixed {
		if n == nodeName {
			return fmt.Sprintf("[cluster=%s] node %s already in NodePool %s — no change", clusterTag(ctx), nodeName, pool), nil
		}
	}
	newFixed := append([]string(nil), fixed...)
	newFixed = append(newFixed, nodeName)
	if err := unstructured.SetNestedStringSlice(np.Object, newFixed, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set fixedNodes: %w", err)
	}

	result, err := dc.Resource(nodePoolGVR).Update(ctx, np, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update nodepool %s: %w", pool, err)
	}

	after, err := getFixedNodes(result)
	if err != nil {
		return "", err
	}
	present := false
	for _, n := range after {
		if n == nodeName {
			present = true
			break
		}
	}
	if !present {
		afterJSON, _ := json.Marshal(after)
		return "", fmt.Errorf("[cluster=%s] update on NodePool %s appeared to succeed but node %s is NOT present in fixedNodes returned by the API server — a mutating admission webhook or a controller likely reverted it. Actual fixedNodes: %s", clusterTag(ctx), pool, nodeName, string(afterJSON))
	}

	return fmt.Sprintf("✅ [cluster=%s] added node %s to NodePool %s (fixedNodes now has %d node(s), verified in API response). Controller will reconcile labels/taints in a few seconds.", clusterTag(ctx), nodeName, pool, len(after)), nil
}

// RemoveNodeFromPool removes a node from a NodePool's spec.configuration.fixedNodes.
type RemoveNodeFromPool struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *RemoveNodeFromPool) Name() string { return "remove_node_from_pool" }

func (t *RemoveNodeFromPool) Description() string {
	return "Remove a node from a NodePool's spec.configuration.fixedNodes list. After removal, the drscaler controller will stop reconciling that pool's labels/taints onto the node (existing labels/taints may or may not be cleared depending on controller behavior — verify with list_node_labels / list_node_taints). Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
}

func (t *RemoveNodeFromPool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pool": map[string]any{
				"type":        "string",
				"description": "NodePool CR name to remove the node from.",
			},
			"node": map[string]any{
				"type":        "string",
				"description": "Node identifier (K8s node name, IP, or hostname).",
			},
		},
		"required": []string{"pool", "node"},
	}
}

func (t *RemoveNodeFromPool) Execute(ctx context.Context, input map[string]any) (string, error) {
	pool, _ := input["pool"].(string)
	rawNode, _ := input["node"].(string)
	if strings.TrimSpace(pool) == "" {
		return "", fmt.Errorf("pool is required")
	}

	nodeName, err := resolveNodeName(t.Nodes, rawNode)
	if err != nil {
		return "", err
	}

	if err := checkNodePoolMutationPermission(ctx, t.CS, nodeName); err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}
	np, err := dc.Resource(nodePoolGVR).Get(ctx, pool, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get nodepool %s: %w", pool, err)
	}
	fixed, err := getFixedNodes(np)
	if err != nil {
		return "", err
	}
	kept := make([]string, 0, len(fixed))
	found := false
	for _, n := range fixed {
		if n == nodeName {
			found = true
			continue
		}
		kept = append(kept, n)
	}
	if !found {
		return fmt.Sprintf("[cluster=%s] node %s not in NodePool %s — nothing removed", clusterTag(ctx), nodeName, pool), nil
	}
	if err := unstructured.SetNestedStringSlice(np.Object, kept, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set fixedNodes: %w", err)
	}

	result, err := dc.Resource(nodePoolGVR).Update(ctx, np, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update nodepool %s: %w", pool, err)
	}

	after, err := getFixedNodes(result)
	if err != nil {
		return "", err
	}
	for _, n := range after {
		if n == nodeName {
			afterJSON, _ := json.Marshal(after)
			return "", fmt.Errorf("[cluster=%s] remove on NodePool %s appeared to succeed but node %s is still present in fixedNodes returned by the API server — a mutating admission webhook or a controller likely reverted it. Actual fixedNodes: %s", clusterTag(ctx), pool, nodeName, string(afterJSON))
		}
	}

	return fmt.Sprintf("✅ [cluster=%s] removed node %s from NodePool %s (fixedNodes now has %d node(s), verified in API response).", clusterTag(ctx), nodeName, pool, len(after)), nil
}

// resolveNodeName turns a user-supplied identifier (IP / hostname / node name)
// into the canonical K8s node name used inside NodePool.spec.configuration.fixedNodes.
// If reg is nil (single-cluster mode with no registry wired in), the raw string
// is returned unchanged.
func resolveNodeName(reg *nodes.Registry, raw string) (string, error) {
	if reg == nil {
		if strings.TrimSpace(raw) == "" {
			return "", fmt.Errorf("node is required")
		}
		return raw, nil
	}
	n, err := reg.Resolve(raw)
	if err != nil {
		return "", err
	}
	return n.Name, nil
}

// GetNodePool returns a single NodePool's fixedNodes list in full.
type GetNodePool struct {
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *GetNodePool) Name() string { return "get_nodepool" }

func (t *GetNodePool) Description() string {
	return "Get a NodePool's full fixedNodes list (infrastructure.deeproute.ai/v1alpha1). Use this to see all nodes currently assigned to a pool. Read-only."
}

func (t *GetNodePool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "NodePool CR name (e.g. 'simulation-4090', 'mlp-4090').",
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetNodePool) Execute(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}
	np, err := dc.Resource(nodePoolGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get nodepool %s: %w", name, err)
	}
	nodes, err := getFixedNodes(np)
	if err != nil {
		return "", err
	}
	out := struct {
		Cluster    string   `json:"cluster,omitempty"`
		Name       string   `json:"name"`
		NodeCount  int      `json:"node_count"`
		FixedNodes []string `json:"fixed_nodes"`
	}{
		Cluster:    authz.ClusterNameFrom(ctx),
		Name:       np.GetName(),
		NodeCount:  len(nodes),
		FixedNodes: nodes,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
