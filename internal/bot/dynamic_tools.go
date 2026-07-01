package bot

import (
	"context"

	"github.com/k8s-inspect/internal/authz"
	"github.com/k8s-inspect/internal/cluster"
	"github.com/k8s-inspect/internal/tool"
	"github.com/k8s-inspect/internal/tool/builtin"
)

// Dynamic tool wrappers — fetch resources from the current cluster in ClusterManager at execution time.

// dynamicListNodes dynamically retrieves the node list from the current cluster.
type dynamicListNodes struct {
	mgr *cluster.Manager
}

func newDynamicListNodes(mgr *cluster.Manager) tool.Tool {
	return &dynamicListNodes{mgr: mgr}
}

func (t *dynamicListNodes) Name() string        { return "list_nodes" }
func (t *dynamicListNodes) Description() string {
	return "List all nodes available for inspection (cluster node IPs / names / roles / status). Can filter to show only unhealthy nodes (NotReady or Unschedulable)."
}
func (t *dynamicListNodes) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{
			"filter": map[string]any{
				"type":        "string",
				"description": "Filter nodes by health status. Options: 'all' (default, show all nodes), 'unhealthy' (show only NotReady or Unschedulable nodes), 'healthy' (show only Ready and Schedulable nodes)",
				"enum":        []string{"all", "unhealthy", "healthy"},
			},
		},
	}
}

func (t *dynamicListNodes) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.ListNodes{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicNodeStatus
type dynamicNodeStatus struct {
	mgr *cluster.Manager
}

func newDynamicNodeStatus(mgr *cluster.Manager) tool.Tool {
	return &dynamicNodeStatus{mgr: mgr}
}

func (t *dynamicNodeStatus) Name() string        { return "node_status" }
func (t *dynamicNodeStatus) Description() string {
	return "Get a node's status from the Kubernetes API: conditions (Ready, MemoryPressure, DiskPressure, PIDPressure), capacity and allocatable resources. Does NOT SSH to the node."
}
func (t *dynamicNodeStatus) InputSchema() map[string]any {
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

func (t *dynamicNodeStatus) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.NodeStatus{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicCordonNode
type dynamicCordonNode struct {
	mgr *cluster.Manager
}

func newDynamicCordonNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicCordonNode{mgr: mgr}
}

func (t *dynamicCordonNode) Name() string        { return "cordon_node" }
func (t *dynamicCordonNode) Description() string {
	return "Mark a node as unschedulable (cordon). New pods will not be scheduled on this node, but existing pods remain running."
}
func (t *dynamicCordonNode) InputSchema() map[string]any {
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

func (t *dynamicCordonNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.CordonNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicUncordonNode
type dynamicUncordonNode struct {
	mgr *cluster.Manager
}

func newDynamicUncordonNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicUncordonNode{mgr: mgr}
}

func (t *dynamicUncordonNode) Name() string        { return "uncordon_node" }
func (t *dynamicUncordonNode) Description() string {
	return "Mark a node as schedulable (uncordon). New pods can be scheduled on this node again."
}
func (t *dynamicUncordonNode) InputSchema() map[string]any {
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

func (t *dynamicUncordonNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.UncordonNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicListNodeTaints
type dynamicListNodeTaints struct {
	mgr *cluster.Manager
}

func newDynamicListNodeTaints(mgr *cluster.Manager) tool.Tool {
	return &dynamicListNodeTaints{mgr: mgr}
}

func (t *dynamicListNodeTaints) Name() string { return "list_node_taints" }
func (t *dynamicListNodeTaints) Description() string {
	return (&builtin.ListNodeTaints{}).Description()
}
func (t *dynamicListNodeTaints) InputSchema() map[string]any {
	return (&builtin.ListNodeTaints{}).InputSchema()
}
func (t *dynamicListNodeTaints) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.ListNodeTaints{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicTaintNode
type dynamicTaintNode struct {
	mgr *cluster.Manager
}

func newDynamicTaintNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicTaintNode{mgr: mgr}
}

func (t *dynamicTaintNode) Name() string { return "taint_node" }
func (t *dynamicTaintNode) Description() string {
	return (&builtin.TaintNode{}).Description()
}
func (t *dynamicTaintNode) InputSchema() map[string]any {
	return (&builtin.TaintNode{}).InputSchema()
}
func (t *dynamicTaintNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.TaintNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicUntaintNode
type dynamicUntaintNode struct {
	mgr *cluster.Manager
}

func newDynamicUntaintNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicUntaintNode{mgr: mgr}
}

func (t *dynamicUntaintNode) Name() string { return "untaint_node" }
func (t *dynamicUntaintNode) Description() string {
	return (&builtin.UntaintNode{}).Description()
}
func (t *dynamicUntaintNode) InputSchema() map[string]any {
	return (&builtin.UntaintNode{}).InputSchema()
}
func (t *dynamicUntaintNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.UntaintNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicListNodeLabels
type dynamicListNodeLabels struct {
	mgr *cluster.Manager
}

func newDynamicListNodeLabels(mgr *cluster.Manager) tool.Tool {
	return &dynamicListNodeLabels{mgr: mgr}
}

func (t *dynamicListNodeLabels) Name() string { return "list_node_labels" }
func (t *dynamicListNodeLabels) Description() string {
	return (&builtin.ListNodeLabels{}).Description()
}
func (t *dynamicListNodeLabels) InputSchema() map[string]any {
	return (&builtin.ListNodeLabels{}).InputSchema()
}
func (t *dynamicListNodeLabels) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.ListNodeLabels{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicLabelNode
type dynamicLabelNode struct {
	mgr *cluster.Manager
}

func newDynamicLabelNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicLabelNode{mgr: mgr}
}

func (t *dynamicLabelNode) Name() string { return "label_node" }
func (t *dynamicLabelNode) Description() string {
	return (&builtin.LabelNode{}).Description()
}
func (t *dynamicLabelNode) InputSchema() map[string]any {
	return (&builtin.LabelNode{}).InputSchema()
}
func (t *dynamicLabelNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.LabelNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicUnlabelNode
type dynamicUnlabelNode struct {
	mgr *cluster.Manager
}

func newDynamicUnlabelNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicUnlabelNode{mgr: mgr}
}

func (t *dynamicUnlabelNode) Name() string { return "unlabel_node" }
func (t *dynamicUnlabelNode) Description() string {
	return (&builtin.UnlabelNode{}).Description()
}
func (t *dynamicUnlabelNode) InputSchema() map[string]any {
	return (&builtin.UnlabelNode{}).InputSchema()
}
func (t *dynamicUnlabelNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.UnlabelNode{CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicListPods
type dynamicListPods struct {
	mgr *cluster.Manager
}

func newDynamicListPods(mgr *cluster.Manager) tool.Tool {
	return &dynamicListPods{mgr: mgr}
}

func (t *dynamicListPods) Name() string        { return "list_pods" }
func (t *dynamicListPods) Description() string {
	return "List pods in a specific namespace or all namespaces. Returns pod count and status summary."
}
func (t *dynamicListPods) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"namespace": map[string]any{
				"type":        "string",
				"description": "Namespace name, or 'all' for all namespaces",
			},
		},
		"required": []string{"namespace"},
	}
}

func (t *dynamicListPods) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.ListPods{CS: c.CS}
	return tool.Execute(ctx, input)
}

// dynamicListNamespaces
type dynamicListNamespaces struct {
	mgr *cluster.Manager
}

func newDynamicListNamespaces(mgr *cluster.Manager) tool.Tool {
	return &dynamicListNamespaces{mgr: mgr}
}

func (t *dynamicListNamespaces) Name() string        { return "list_namespaces" }
func (t *dynamicListNamespaces) Description() string {
	return "List all namespaces in the cluster with their status and age."
}
func (t *dynamicListNamespaces) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{
			"dummy": map[string]any{
				"type":        "string",
				"description": "Unused parameter (workaround for API compatibility)",
			},
		},
	}
}

func (t *dynamicListNamespaces) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.ListNamespaces{CS: c.CS}
	return tool.Execute(ctx, input)
}

// K8s-based hardware tools (no SSH required)

// dynamicK8sHardwareInfo
type dynamicK8sHardwareInfo struct {
	mgr *cluster.Manager
}

func newDynamicK8sHardwareInfo(mgr *cluster.Manager) tool.Tool {
	return &dynamicK8sHardwareInfo{mgr: mgr}
}

func (t *dynamicK8sHardwareInfo) Name() string        { return "k8s_hardware_info" }
func (t *dynamicK8sHardwareInfo) Description() string {
	return "Show comprehensive hardware information on a node. Includes CPU model/cores, total memory, network interfaces, and disk info."
}
func (t *dynamicK8sHardwareInfo) InputSchema() map[string]any {
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

func (t *dynamicK8sHardwareInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.K8sHardwareInfo{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicK8sCPUInfo
type dynamicK8sCPUInfo struct {
	mgr *cluster.Manager
}

func newDynamicK8sCPUInfo(mgr *cluster.Manager) tool.Tool {
	return &dynamicK8sCPUInfo{mgr: mgr}
}

func (t *dynamicK8sCPUInfo) Name() string        { return "k8s_cpu_info" }
func (t *dynamicK8sCPUInfo) Description() string {
	return "Show detailed CPU information on a node. Includes model, architecture, cores, threads, frequency, cache, and virtualization support."
}
func (t *dynamicK8sCPUInfo) InputSchema() map[string]any {
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

func (t *dynamicK8sCPUInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.K8sCPUInfo{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicK8sNetworkInfo
type dynamicK8sNetworkInfo struct {
	mgr *cluster.Manager
}

func newDynamicK8sNetworkInfo(mgr *cluster.Manager) tool.Tool {
	return &dynamicK8sNetworkInfo{mgr: mgr}
}

func (t *dynamicK8sNetworkInfo) Name() string        { return "k8s_network_info" }
func (t *dynamicK8sNetworkInfo) Description() string {
	return "Show detailed network interface information on a node. Includes IP addresses, MAC addresses, link status, speed, and routing table."
}
func (t *dynamicK8sNetworkInfo) InputSchema() map[string]any {
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

func (t *dynamicK8sNetworkInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.K8sNetworkInfo{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicK8sMemoryInfo
type dynamicK8sMemoryInfo struct {
	mgr *cluster.Manager
}

func newDynamicK8sMemoryInfo(mgr *cluster.Manager) tool.Tool {
	return &dynamicK8sMemoryInfo{mgr: mgr}
}

func (t *dynamicK8sMemoryInfo) Name() string        { return "k8s_memory_info" }
func (t *dynamicK8sMemoryInfo) Description() string {
	return "Show detailed memory information on a node. Includes total/available/used memory, swap, cache, buffers, and memory hardware details."
}
func (t *dynamicK8sMemoryInfo) InputSchema() map[string]any {
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

func (t *dynamicK8sMemoryInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.K8sMemoryInfo{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicDiagnoseNode
type dynamicDiagnoseNode struct {
	mgr *cluster.Manager
}

func newDynamicDiagnoseNode(mgr *cluster.Manager) tool.Tool {
	return &dynamicDiagnoseNode{mgr: mgr}
}

func (t *dynamicDiagnoseNode) Name() string        { return "diagnose_node" }
func (t *dynamicDiagnoseNode) Description() string {
	return "Diagnose node issues by collecting containerd status, kubelet status, and system logs. Use this when a node is NotReady or has problems."
}
func (t *dynamicDiagnoseNode) InputSchema() map[string]any {
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

func (t *dynamicDiagnoseNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.DiagnoseNode{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicCollectLogs
type dynamicCollectLogs struct {
	mgr *cluster.Manager
}

func newDynamicCollectLogs(mgr *cluster.Manager) tool.Tool {
	return &dynamicCollectLogs{mgr: mgr}
}

func (t *dynamicCollectLogs) Name() string { return "collect_logs" }
func (t *dynamicCollectLogs) Description() string {
	return "Collect system logs, kubelet logs, or containerd logs from a specified node. Supports choosing log type and number of lines."
}
func (t *dynamicCollectLogs) InputSchema() map[string]any {
	return (&builtin.CollectLogs{}).InputSchema()
}

func (t *dynamicCollectLogs) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.CollectLogs{CS: c.CS, RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicAnalyzePodLogs
type dynamicAnalyzePodLogs struct {
	mgr *cluster.Manager
}

func newDynamicAnalyzePodLogs(mgr *cluster.Manager) tool.Tool {
	return &dynamicAnalyzePodLogs{mgr: mgr}
}

func (t *dynamicAnalyzePodLogs) Name() string { return "analyze_pod_logs" }
func (t *dynamicAnalyzePodLogs) Description() string {
	return "Fetch the last 1000 lines of logs from a pod for error analysis. Returns raw log text."
}
func (t *dynamicAnalyzePodLogs) InputSchema() map[string]any {
	return (&builtin.AnalyzePodLogs{}).InputSchema()
}

func (t *dynamicAnalyzePodLogs) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	tool := &builtin.AnalyzePodLogs{CS: c.CS}
	return tool.Execute(ctx, input)
}

// dynamicListNodePools
type dynamicListNodePools struct {
	mgr *cluster.Manager
}

func newDynamicListNodePools(mgr *cluster.Manager) tool.Tool {
	return &dynamicListNodePools{mgr: mgr}
}

func (t *dynamicListNodePools) Name() string { return "list_nodepools" }
func (t *dynamicListNodePools) Description() string {
	return (&builtin.ListNodePools{}).Description()
}
func (t *dynamicListNodePools) InputSchema() map[string]any {
	return (&builtin.ListNodePools{}).InputSchema()
}
func (t *dynamicListNodePools) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.ListNodePools{RestConfig: c.RestConfig}
	return tool.Execute(ctx, input)
}

// dynamicGetNodePool
type dynamicGetNodePool struct {
	mgr *cluster.Manager
}

func newDynamicGetNodePool(mgr *cluster.Manager) tool.Tool {
	return &dynamicGetNodePool{mgr: mgr}
}

func (t *dynamicGetNodePool) Name() string { return "get_nodepool" }
func (t *dynamicGetNodePool) Description() string {
	return (&builtin.GetNodePool{}).Description()
}
func (t *dynamicGetNodePool) InputSchema() map[string]any {
	return (&builtin.GetNodePool{}).InputSchema()
}
func (t *dynamicGetNodePool) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.GetNodePool{RestConfig: c.RestConfig, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicAddNodeToPool
type dynamicAddNodeToPool struct {
	mgr *cluster.Manager
}

func newDynamicAddNodeToPool(mgr *cluster.Manager) tool.Tool {
	return &dynamicAddNodeToPool{mgr: mgr}
}

func (t *dynamicAddNodeToPool) Name() string { return "add_node_to_pool" }
func (t *dynamicAddNodeToPool) Description() string {
	return (&builtin.AddNodeToPool{}).Description()
}
func (t *dynamicAddNodeToPool) InputSchema() map[string]any {
	return (&builtin.AddNodeToPool{}).InputSchema()
}
func (t *dynamicAddNodeToPool) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.AddNodeToPool{RestConfig: c.RestConfig, CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicRemoveNodeFromPool
type dynamicRemoveNodeFromPool struct {
	mgr *cluster.Manager
}

func newDynamicRemoveNodeFromPool(mgr *cluster.Manager) tool.Tool {
	return &dynamicRemoveNodeFromPool{mgr: mgr}
}

func (t *dynamicRemoveNodeFromPool) Name() string { return "remove_node_from_pool" }
func (t *dynamicRemoveNodeFromPool) Description() string {
	return (&builtin.RemoveNodeFromPool{}).Description()
}
func (t *dynamicRemoveNodeFromPool) InputSchema() map[string]any {
	return (&builtin.RemoveNodeFromPool{}).InputSchema()
}
func (t *dynamicRemoveNodeFromPool) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.RemoveNodeFromPool{RestConfig: c.RestConfig, CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

// dynamicMoveNodeBetweenPools
type dynamicMoveNodeBetweenPools struct {
	mgr *cluster.Manager
}

func newDynamicMoveNodeBetweenPools(mgr *cluster.Manager) tool.Tool {
	return &dynamicMoveNodeBetweenPools{mgr: mgr}
}

func (t *dynamicMoveNodeBetweenPools) Name() string { return "move_node_between_pools" }
func (t *dynamicMoveNodeBetweenPools) Description() string {
	return (&builtin.MoveNodeBetweenPools{}).Description()
}
func (t *dynamicMoveNodeBetweenPools) InputSchema() map[string]any {
	return (&builtin.MoveNodeBetweenPools{}).InputSchema()
}
func (t *dynamicMoveNodeBetweenPools) Execute(ctx context.Context, input map[string]any) (string, error) {
	c, err := t.mgr.Current()
	if err != nil {
		return "", err
	}
	ctx = authz.WithClusterName(ctx, t.mgr.CurrentName())
	tool := &builtin.MoveNodeBetweenPools{RestConfig: c.RestConfig, CS: c.CS, Nodes: c.Nodes}
	return tool.Execute(ctx, input)
}

