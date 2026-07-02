package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/authz"
	"github.com/k8s-inspect/internal/nodes"
)

var nodePoolGVR = schema.GroupVersionResource{
	Group:    "infrastructure.deeproute.ai",
	Version:  "v1alpha1",
	Resource: "nodepools",
}

// nodePoolFixedNodesPath is the *write* path — the drscaler controller reads
// this list to decide which nodes should belong to the pool.
var nodePoolFixedNodesPath = []string{"spec", "configuration", "fixedNodes"}

// nodePoolOwnedNodesPath is the source of truth for current pool membership —
// the drscaler controller populates status.ownedNodes with the nodes that
// actually belong to this pool right now. All membership checks read this,
// never fixedNodes and never the node's drscaler.deeproute.ai/nodepool label.
var nodePoolOwnedNodesPath = []string{"status", "ownedNodes"}

func newDynamicClient(rc *rest.Config) (dynamic.Interface, error) {
	if rc == nil {
		return nil, fmt.Errorf("no rest config available for dynamic client")
	}
	return dynamic.NewForConfig(rc)
}

// getFixedNodes extracts spec.configuration.fixedNodes from an unstructured
// NodePool. Only used on the write path.
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

// getOwnedNodes extracts status.ownedNodes from an unstructured NodePool.
// This is the authoritative "who belongs to this pool right now" list.
func getOwnedNodes(np *unstructured.Unstructured) ([]string, error) {
	raw, found, err := unstructured.NestedStringSlice(np.Object, nodePoolOwnedNodesPath...)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", nodePoolOwnedNodesPath, err)
	}
	if !found {
		return nil, nil
	}
	return raw, nil
}

// fetchNodeAndCheck loads the K8s Node object and runs the shared mutation
// permission checks (allowlist + master protection).
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

// resolveEffectivePool scans every NodePool's status.ownedNodes list and
// returns the pool that currently owns nodeName. Returns "" if no pool owns
// it. Uses a single List call.
func resolveEffectivePool(ctx context.Context, dc dynamic.Interface, nodeName string) (string, error) {
	list, err := dc.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodepools: %w", err)
	}
	for i := range list.Items {
		np := &list.Items[i]
		owned, err := getOwnedNodes(np)
		if err != nil {
			return "", fmt.Errorf("pool %s: %w", np.GetName(), err)
		}
		for _, n := range owned {
			if n == nodeName {
				return np.GetName(), nil
			}
		}
	}
	return "", nil
}

// ListNodePools lists all NodePool CRs in the current cluster with a summary
// of each pool's status.ownedNodes count and a short preview.
type ListNodePools struct {
	RestConfig *rest.Config
}

func (t *ListNodePools) Name() string { return "list_nodepools" }

func (t *ListNodePools) Description() string {
	return "List all NodePool CRs (infrastructure.deeproute.ai/v1alpha1). Shows each pool's name, current member count (from status.ownedNodes — the drscaler controller's authoritative view of who's in the pool), and a short preview. Read-only."
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
		owned, err := getOwnedNodes(np)
		if err != nil {
			return "", fmt.Errorf("pool %s: %w", np.GetName(), err)
		}
		preview := owned
		if len(preview) > 5 {
			preview = append([]string(nil), owned[:5]...)
			preview = append(preview, fmt.Sprintf("... (+%d more)", len(owned)-5))
		}
		pools = append(pools, poolSummary{
			Name:      np.GetName(),
			NodeCount: len(owned),
			Preview:   preview,
		})
	}

	out := struct {
		Cluster          string        `json:"cluster,omitempty"`
		MembershipSource string        `json:"membership_source"`
		Total            int           `json:"total"`
		Pools            []poolSummary `json:"pools"`
	}{
		Cluster:          authz.ClusterNameFrom(ctx),
		MembershipSource: "NodePool.status.ownedNodes",
		Total:            len(pools),
		Pools:            pools,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// ListPoolMembers returns pool→node mappings sourced from each NodePool's
// status.ownedNodes. It does not read fixedNodes and does not consult node
// labels; those are byproducts of reconciliation and are not the truth.
type ListPoolMembers struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
}

func (t *ListPoolMembers) Name() string { return "list_pool_members" }

func (t *ListPoolMembers) Description() string {
	return "List the current node members of each NodePool, grouped by pool. Membership is read from NodePool.status.ownedNodes (the drscaler controller's authoritative view). Also reports nodes that no pool owns. Use this whenever the user asks who is in a pool, which pool a node belongs to, or wants a pool→nodes mapping. Set pool=\"unassigned\" (or \"none\") to return only nodes that no pool owns."
}

func (t *ListPoolMembers) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pool": map[string]any{
				"type":        "string",
				"description": "Optional. NodePool CR name to restrict results to (e.g. 'mlp-4090'). Special values: \"unassigned\" or \"none\" returns only nodes that no NodePool currently owns. Omit to get every pool plus unassigned nodes.",
			},
		},
	}
}

func (t *ListPoolMembers) Execute(ctx context.Context, input map[string]any) (string, error) {
	poolFilter, _ := input["pool"].(string)
	poolFilter = strings.TrimSpace(poolFilter)

	unassignedRequested := false
	switch strings.ToLower(poolFilter) {
	case "unassigned", "none", "no-pool", "no_pool":
		unassignedRequested = true
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	poolList, err := dc.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodepools: %w", err)
	}

	type poolMembers struct {
		Name      string   `json:"pool"`
		NodeCount int      `json:"node_count"`
		Nodes     []string `json:"nodes"`
	}
	pools := make([]poolMembers, 0, len(poolList.Items))
	ownedByAny := make(map[string]bool)
	poolExists := false
	for i := range poolList.Items {
		np := &poolList.Items[i]
		name := np.GetName()
		if name == poolFilter {
			poolExists = true
		}
		owned, err := getOwnedNodes(np)
		if err != nil {
			return "", fmt.Errorf("pool %s: %w", name, err)
		}
		for _, n := range owned {
			ownedByAny[n] = true
		}
		if !unassignedRequested && (poolFilter == "" || poolFilter == name) {
			pools = append(pools, poolMembers{
				Name:      name,
				NodeCount: len(owned),
				Nodes:     owned,
			})
		}
	}

	if poolFilter != "" && !unassignedRequested && !poolExists {
		return "", fmt.Errorf("nodepool %q not found in cluster %s (use pool=\"unassigned\" to list nodes that no pool owns)", poolFilter, authz.ClusterNameFrom(ctx))
	}

	var unassigned []string
	if poolFilter == "" || unassignedRequested {
		allNodes, err := t.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("list all nodes: %w", err)
		}
		for i := range allNodes.Items {
			n := &allNodes.Items[i]
			if !ownedByAny[n.Name] {
				unassigned = append(unassigned, n.Name)
			}
		}
	}

	out := struct {
		Cluster          string        `json:"cluster,omitempty"`
		MembershipSource string        `json:"membership_source"`
		Pools            []poolMembers `json:"pools"`
		Unassigned       []string      `json:"unassigned_nodes,omitempty"`
		UnassignedNum    int           `json:"unassigned_count,omitempty"`
	}{
		Cluster:          authz.ClusterNameFrom(ctx),
		MembershipSource: "NodePool.status.ownedNodes",
		Pools:            pools,
		Unassigned:       unassigned,
		UnassignedNum:    len(unassigned),
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// MoveNodeBetweenPools moves a node from one NodePool to another by rewriting
// both pools' spec.configuration.fixedNodes. Membership decisions come from
// scanning every pool's status.ownedNodes — never fixedNodes, never node labels.
type MoveNodeBetweenPools struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *MoveNodeBetweenPools) Name() string { return "move_node_between_pools" }

func (t *MoveNodeBetweenPools) Description() string {
	return "Move a node from one NodePool to another in a single operation. Equivalent to remove_node_from_pool(from) then add_node_to_pool(to), but does both in one tool call. This is the canonical way to change a node's team/pool assignment — the drscaler controller will then reconcile labels and taints on the node to match the new pool. The from_pool argument is validated against NodePool.status.ownedNodes (source of truth for current membership) before any writes. Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
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
				"description": "Source NodePool CR name (e.g. 'simulation-4090'). Node must currently be in this pool (per status.ownedNodes).",
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
	if _, err := fetchNodeAndCheck(ctx, t.CS, nodeName); err != nil {
		return "", err
	}

	dc, err := newDynamicClient(t.RestConfig)
	if err != nil {
		return "", err
	}

	// Fetch both pools up front to validate existence and grab fixedNodes.
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

	// Sole membership check: scan every pool's status.ownedNodes.
	effectivePool, err := resolveEffectivePool(ctx, dc, nodeName)
	if err != nil {
		return "", err
	}
	if effectivePool == to {
		return fmt.Sprintf("[cluster=%s] node %s is already in NodePool %s (per status.ownedNodes) — nothing to move", clusterTag(ctx), nodeName, to), nil
	}
	if effectivePool != "" && effectivePool != from {
		return "", fmt.Errorf("[cluster=%s] node %s is currently in NodePool %q (per status.ownedNodes), not %s — check list_pool_members before retrying", clusterTag(ctx), nodeName, effectivePool, from)
	}

	// fixedNodes rewrites — ownedNodes already told us which direction to go.
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

	return fmt.Sprintf("✅ [cluster=%s] moved node %s from NodePool %s to %s. Controller will reconcile labels/taints and status.ownedNodes in a few seconds — verify with list_pool_members.", clusterTag(ctx), nodeName, from, to), nil
}

// AddNodeToPool appends a node to a NodePool's spec.configuration.fixedNodes.
// Refuses if some other pool currently owns the node (per status.ownedNodes).
type AddNodeToPool struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *AddNodeToPool) Name() string { return "add_node_to_pool" }

func (t *AddNodeToPool) Description() string {
	return "Add a node to a NodePool's spec.configuration.fixedNodes list. The drscaler controller will then reconcile the node's labels and taints to match the pool. Current pool membership is determined by scanning NodePool.status.ownedNodes across all pools — the tool refuses if another pool currently owns the node (use move_node_between_pools instead, or first remove_node_from_pool). Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
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

	// Sole membership check: scan every pool's status.ownedNodes.
	effectivePool, err := resolveEffectivePool(ctx, dc, nodeName)
	if err != nil {
		return "", err
	}
	if effectivePool == pool {
		return fmt.Sprintf("[cluster=%s] node %s already in NodePool %s (per status.ownedNodes) — no change", clusterTag(ctx), nodeName, pool), nil
	}
	if effectivePool != "" {
		return "", fmt.Errorf("[cluster=%s] node %s is currently in NodePool %q (per status.ownedNodes) — remove it from that pool first (or use move_node_between_pools)", clusterTag(ctx), nodeName, effectivePool)
	}

	fixed, err := getFixedNodes(np)
	if err != nil {
		return "", err
	}
	newFixed := append([]string(nil), fixed...)
	alreadyInFixed := false
	for _, n := range newFixed {
		if n == nodeName {
			alreadyInFixed = true
			break
		}
	}
	if !alreadyInFixed {
		newFixed = append(newFixed, nodeName)
	}
	if err := unstructured.SetNestedStringSlice(np.Object, newFixed, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set fixedNodes: %w", err)
	}
	if _, err := dc.Resource(nodePoolGVR).Update(ctx, np, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("update nodepool %s: %w", pool, err)
	}

	return fmt.Sprintf("✅ [cluster=%s] added node %s to NodePool %s fixedNodes. Controller will reconcile labels/taints and status.ownedNodes in a few seconds — verify with list_pool_members.", clusterTag(ctx), nodeName, pool), nil
}

// RemoveNodeFromPool removes a node from a NodePool's spec.configuration.fixedNodes.
type RemoveNodeFromPool struct {
	RestConfig *rest.Config
	CS         *kubernetes.Clientset
	Nodes      *nodes.Registry
}

func (t *RemoveNodeFromPool) Name() string { return "remove_node_from_pool" }

func (t *RemoveNodeFromPool) Description() string {
	return "Remove a node from a NodePool's spec.configuration.fixedNodes list. After removal, the drscaler controller will stop reconciling that pool's labels/taints onto the node and drop it from status.ownedNodes. Blocked on master/control-plane nodes; requires the caller to be on the taint/label mutation allowlist."
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
		return fmt.Sprintf("[cluster=%s] node %s not in NodePool %s fixedNodes — nothing removed", clusterTag(ctx), nodeName, pool), nil
	}
	if err := unstructured.SetNestedStringSlice(np.Object, kept, nodePoolFixedNodesPath...); err != nil {
		return "", fmt.Errorf("set fixedNodes: %w", err)
	}
	if _, err := dc.Resource(nodePoolGVR).Update(ctx, np, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("update nodepool %s: %w", pool, err)
	}

	return fmt.Sprintf("✅ [cluster=%s] removed node %s from NodePool %s fixedNodes. Controller will drop it from status.ownedNodes in a few seconds.", clusterTag(ctx), nodeName, pool), nil
}

// resolveNodeName turns a user-supplied identifier (IP / hostname / node name)
// into the canonical K8s node name used inside NodePool.spec.configuration.fixedNodes.
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

// GetNodePool returns a NodePool's fixedNodes (write path) and
// status.ownedNodes (current members).
type GetNodePool struct {
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *GetNodePool) Name() string { return "get_nodepool" }

func (t *GetNodePool) Description() string {
	return "Get a NodePool's status.ownedNodes (authoritative current members) and spec.configuration.fixedNodes (controller input). Read-only. When answering 'which nodes are in this pool', use ownedNodes — fixedNodes is what the controller has been instructed to reconcile, not necessarily current membership."
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
	fixed, err := getFixedNodes(np)
	if err != nil {
		return "", err
	}
	owned, err := getOwnedNodes(np)
	if err != nil {
		return "", err
	}
	out := struct {
		Cluster    string   `json:"cluster,omitempty"`
		Name       string   `json:"name"`
		OwnedCount int      `json:"owned_count"`
		OwnedNodes []string `json:"owned_nodes"`
		FixedNodes []string `json:"fixed_nodes,omitempty"`
	}{
		Cluster:    authz.ClusterNameFrom(ctx),
		Name:       np.GetName(),
		OwnedCount: len(owned),
		OwnedNodes: owned,
		FixedNodes: fixed,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
