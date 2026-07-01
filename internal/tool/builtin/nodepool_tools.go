package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// nodePoolLabelKey is the label the drscaler controller writes onto each node
// to mark its *effective* pool assignment. This label is the source of truth
// for "which pool is this node in" — the fixedNodes list on the NodePool CR
// is the *write* path, but the label is what the controller has actually
// reconciled onto the node. Read tools and preflight checks should compare
// against this label, not scan fixedNodes.
const nodePoolLabelKey = "drscaler.deeproute.ai/nodepool"

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

// fetchNodeAndCheck loads the K8s Node object and runs the shared mutation
// permission checks (allowlist + master protection). Callers reuse the
// returned Node to also read the drscaler.deeproute.ai/nodepool label without
// a second API call.
func fetchNodeAndCheck(ctx context.Context, cs *kubernetes.Clientset, nodeName string) (*corev1.Node, error) {
	node, err := cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", nodeName, err)
	}
	if err := checkNodeMutationPermission(ctx, node, "modify NodePool membership"); err != nil {
		return nil, err
	}
	return node, nil
}

// resolveEffectivePool returns the pool the node effectively belongs to, based
// on the drscaler.deeproute.ai/nodepool label — but only when that label names
// a NodePool CR that actually exists. A stale label (pointing at a deleted or
// never-existed pool) is treated as "no assignment", which lets the tool
// proceed with add/move without a spurious "already in X" error.
//
// knownPools are pool names the caller has already fetched; if the label
// matches one of those it is trusted without an extra API call.
func resolveEffectivePool(ctx context.Context, dc dynamic.Interface, node *corev1.Node, knownPools ...string) (string, error) {
	labelValue := node.Labels[nodePoolLabelKey]
	if labelValue == "" {
		return "", nil
	}
	for _, known := range knownPools {
		if labelValue == known {
			return labelValue, nil
		}
	}
	if _, err := dc.Resource(nodePoolGVR).Get(ctx, labelValue, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("verify current pool %q from node label: %w", labelValue, err)
	}
	return labelValue, nil
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

// ListPoolMembers is the label-authoritative "which nodes are in which pool"
// view. It first lists all NodePool CRs, then for each pool queries the K8s
// API with a labelSelector on drscaler.deeproute.ai/nodepool=<pool> to find
// the nodes actually carrying that label. It also surfaces nodes whose label
// points at a pool that no longer exists (stale labels) and nodes that carry
// no drscaler.deeproute.ai/nodepool label at all.
//
// This is the tool the LLM should reach for whenever the user asks "which
// nodes are in pool X" / "who's in mlp-4090" / "list the members of ..." —
// it never consults NodePool.spec.configuration.fixedNodes, so it cannot
// drift or produce a "fixedNodes vs label" comparison.
type ListPoolMembers struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
}

func (t *ListPoolMembers) Name() string { return "list_pool_members" }

func (t *ListPoolMembers) Description() string {
	return "List actual node members of each NodePool, grouped by pool. Membership is determined by the drscaler.deeproute.ai/nodepool label on each node (the only source of truth) — this tool does NOT read NodePool.spec.configuration.fixedNodes. Also reports nodes with stale labels (pointing at a pool that no longer exists) and unassigned nodes (no drscaler.deeproute.ai/nodepool label). Use this whenever the user asks who is in a pool, which pool a node belongs to, or wants a pool→nodes mapping. Set pool=\"unassigned\" (or \"none\") to return only nodes with no drscaler.deeproute.ai/nodepool label."
}

func (t *ListPoolMembers) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pool": map[string]any{
				"type":        "string",
				"description": "Optional. NodePool CR name to restrict results to (e.g. 'mlp-4090'). Special values: \"unassigned\" or \"none\" returns only nodes with no drscaler.deeproute.ai/nodepool label. Omit to get every pool plus stale/unassigned nodes.",
			},
		},
	}
}

func (t *ListPoolMembers) Execute(ctx context.Context, input map[string]any) (string, error) {
	poolFilter, _ := input["pool"].(string)
	poolFilter = strings.TrimSpace(poolFilter)

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	poolList, err := dc.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodepools: %w", err)
	}

	existingPools := make(map[string]bool, len(poolList.Items))
	poolNames := make([]string, 0, len(poolList.Items))
	unassignedRequested := false
	switch strings.ToLower(poolFilter) {
	case "unassigned", "none", "no-pool", "no_pool":
		unassignedRequested = true
	}
	for i := range poolList.Items {
		name := poolList.Items[i].GetName()
		existingPools[name] = true
		if !unassignedRequested && (poolFilter == "" || poolFilter == name) {
			poolNames = append(poolNames, name)
		}
	}

	if poolFilter != "" && !unassignedRequested && !existingPools[poolFilter] {
		return "", fmt.Errorf("nodepool %q not found in cluster %s (use pool=\"unassigned\" to list nodes with no drscaler.deeproute.ai/nodepool label)", poolFilter, authz.ClusterNameFrom(ctx))
	}

	type poolMembers struct {
		Name      string   `json:"pool"`
		NodeCount int      `json:"node_count"`
		Nodes     []string `json:"nodes"`
	}
	pools := make([]poolMembers, 0, len(poolNames))
	for _, name := range poolNames {
		sel := fmt.Sprintf("%s=%s", nodePoolLabelKey, name)
		nl, err := t.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err != nil {
			return "", fmt.Errorf("list nodes with %s: %w", sel, err)
		}
		names := make([]string, 0, len(nl.Items))
		for i := range nl.Items {
			names = append(names, nl.Items[i].Name)
		}
		pools = append(pools, poolMembers{
			Name:      name,
			NodeCount: len(names),
			Nodes:     names,
		})
	}

	// Surface nodes with no drscaler.deeproute.ai/nodepool label and nodes whose
	// label points at a non-existent pool. We compute these when the caller
	// asks for "all pools" (poolFilter == "") or specifically requests the
	// unassigned view.
	var stale []map[string]string
	var unassigned []string
	if poolFilter == "" || unassignedRequested {
		allNodes, err := t.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("list all nodes: %w", err)
		}
		for i := range allNodes.Items {
			n := &allNodes.Items[i]
			labelValue := n.Labels[nodePoolLabelKey]
			if labelValue == "" {
				unassigned = append(unassigned, n.Name)
				continue
			}
			if !existingPools[labelValue] {
				stale = append(stale, map[string]string{
					"node":            n.Name,
					"label_points_at": labelValue,
				})
			}
		}
	}

	out := struct {
		Cluster       string              `json:"cluster,omitempty"`
		LabelKey      string              `json:"membership_source_label"`
		Pools         []poolMembers       `json:"pools"`
		StaleLabels   []map[string]string `json:"stale_labels,omitempty"`
		Unassigned    []string            `json:"unassigned_nodes,omitempty"`
		UnassignedNum int                 `json:"unassigned_count,omitempty"`
	}{
		Cluster:       authz.ClusterNameFrom(ctx),
		LabelKey:      nodePoolLabelKey,
		Pools:         pools,
		StaleLabels:   stale,
		Unassigned:    unassigned,
		UnassignedNum: len(unassigned),
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
	return "Move a node from one NodePool to another in a single operation. Equivalent to remove_node_from_pool(from) then add_node_to_pool(to), but does both in one tool call. This is the canonical way to change a node's team/pool assignment — the drscaler controller will then reconcile labels and taints on the node to match the new pool (no need to modify labels or taints directly). The from_pool argument is validated against the node's drscaler.deeproute.ai/nodepool label (source of truth for current membership) before any writes. Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
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
	node, err := fetchNodeAndCheck(ctx, t.CS, nodeName)
	if err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	// Fetch both pools up front — this both validates their existence and
	// gives us the fixedNodes lists we'll mutate below.
	fromNP, err := dc.Resource(nodePoolGVR).Get(ctx, from, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get from_pool %s: %w", from, err)
	}
	fromFixed, err := getFixedNodes(fromNP)
	if err != nil {
		return "", err
	}
	toNP, err := dc.Resource(nodePoolGVR).Get(ctx, to, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get to_pool %s: %w", to, err)
	}
	toFixed, err := getFixedNodes(toNP)
	if err != nil {
		return "", err
	}

	// Effective pool assignment is determined by the drscaler.deeproute.ai/nodepool
	// label, but only when it points at a NodePool that actually exists. A stale
	// label (pointing at a deleted or renamed pool) is treated as "no assignment"
	// so we don't block a legitimate move. We do NOT consult fixedNodes for this
	// check — the label is authoritative.
	effectivePool, err := resolveEffectivePool(ctx, dc, node, from, to)
	if err != nil {
		return "", err
	}
	if effectivePool == to {
		return fmt.Sprintf("[cluster=%s] node %s is already in NodePool %s (per %s label) — nothing to move", clusterTag(ctx), nodeName, to, nodePoolLabelKey), nil
	}
	if effectivePool != "" && effectivePool != from {
		return "", fmt.Errorf("[cluster=%s] node %s is currently in NodePool %q (per %s label), not %s — check the actual source with list_node_labels before retrying", clusterTag(ctx), nodeName, effectivePool, nodePoolLabelKey, from)
	}

	// Build the new fixedNodes lists and write both pools unconditionally.
	// Whether or not the node currently appears in either list is NOT a decision
	// input — the label already told us the node belongs in `from` and not in
	// `to`, so we strip it from `from` (a no-op if absent) and append it to `to`
	// (a no-op if the pool already contained it after dedup).
	fromKept := make([]string, 0, len(fromFixed))
	for _, n := range fromFixed {
		if n == nodeName {
			continue
		}
		fromKept = append(fromKept, n)
	}
	if err := unstructured.SetNestedStringSlice(fromNP.Object, fromKept, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set from_pool fixedNodes: %w", err)
	}
	if _, err := dc.Resource(nodePoolGVR).Update(ctx, fromNP, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("update from_pool %s: %w", from, err)
	}

	toNewFixed := make([]string, 0, len(toFixed)+1)
	for _, n := range toFixed {
		if n == nodeName {
			continue
		}
		toNewFixed = append(toNewFixed, n)
	}
	toNewFixed = append(toNewFixed, nodeName)
	if err := unstructured.SetNestedStringSlice(toNP.Object, toNewFixed, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("[cluster=%s] set to_pool fixedNodes: %w — node %s has been removed from %s but NOT added to %s. Recover with: add_node_to_pool(pool=%s, node=%s)", clusterTag(ctx), err, nodeName, from, to, to, nodeName)
	}
	if _, err := dc.Resource(nodePoolGVR).Update(ctx, toNP, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("[cluster=%s] update to_pool %s: %w — node %s has been removed from %s but NOT added to %s. Recover with: add_node_to_pool(pool=%s, node=%s)", clusterTag(ctx), to, err, nodeName, from, to, to, nodeName)
	}

	return fmt.Sprintf("✅ [cluster=%s] moved node %s from NodePool %s to %s. Controller will reconcile labels/taints in a few seconds — verify with list_node_labels + list_node_taints.", clusterTag(ctx), nodeName, from, to), nil
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
	return "Add a node to a NodePool's spec.configuration.fixedNodes list. The drscaler controller will then reconcile the node's labels and taints to match the pool. Current pool membership is determined by the node's drscaler.deeproute.ai/nodepool label — the tool refuses if that label points at a different pool (use move_node_between_pools instead, or first remove_node_from_pool). Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
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

	node, err := fetchNodeAndCheck(ctx, t.CS, nodeName)
	if err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	// Fetch the target pool first — this verifies it exists before we do
	// anything else, and gives us the current fixedNodes list to mutate.
	np, err := dc.Resource(nodePoolGVR).Get(ctx, pool, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get nodepool %s: %w", pool, err)
	}

	// Effective pool assignment is determined by the drscaler.deeproute.ai/nodepool
	// label, but only when it points at a NodePool that actually exists. A stale
	// label (pointing at a deleted or renamed pool) is treated as "no assignment"
	// so we don't block a legitimate add. fixedNodes is NEVER consulted for the
	// membership decision — the label is the sole source of truth.
	effectivePool, err := resolveEffectivePool(ctx, dc, node, pool)
	if err != nil {
		return "", err
	}
	if effectivePool == pool {
		return fmt.Sprintf("[cluster=%s] node %s already in NodePool %s (per %s label) — no change", clusterTag(ctx), nodeName, pool, nodePoolLabelKey), nil
	}
	if effectivePool != "" {
		return "", fmt.Errorf("[cluster=%s] node %s is currently in NodePool %q (per %s label) — remove it from that pool first (or use move_node_between_pools)", clusterTag(ctx), nodeName, effectivePool, nodePoolLabelKey)
	}

	fixed, err := getFixedNodes(np)
	if err != nil {
		return "", err
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

	if _, err := fetchNodeAndCheck(ctx, t.CS, nodeName); err != nil {
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
