package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/k8s-inspect/internal/nodes"
)


// ListNodes lists the whitelist nodes (= current cluster nodes). No SSH required.
type ListNodes struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *ListNodes) Name() string { return "list_nodes" }

func (t *ListNodes) Description() string {
	return "List all nodes available for inspection (cluster node IPs / names / roles / status). Can filter to show only unhealthy nodes (NotReady or Unschedulable)."
}

func (t *ListNodes) InputSchema() map[string]any {
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

func (t *ListNodes) Execute(ctx context.Context, input map[string]any) (string, error) {
	// Get filter parameter
	filter := "all"
	if f, ok := input["filter"].(string); ok && f != "" {
		filter = f
	}

	// Fetch all nodes from the K8s API in one call
	nodeList, err := t.CS.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	type nodeInfo struct {
		Name        string   `json:"name"`
		InternalIP  string   `json:"internal_ip"`
		Hostname    string   `json:"hostname,omitempty"`
		Roles       []string `json:"roles,omitempty"`
		Ready       bool     `json:"ready"`
		Cordoned    bool     `json:"cordoned"`  // true only when explicitly cordoned via kubectl cordon
		Schedulable bool     `json:"schedulable"`
		Healthy     bool     `json:"healthy"`
	}

	result := make([]nodeInfo, 0, len(nodeList.Items))
	readyCount := 0
	schedulableCount := 0
	healthyCount := 0
	totalCount := len(nodeList.Items)

	for i := range nodeList.Items {
		node := &nodeList.Items[i]

		// Check Ready condition
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				ready = (cond.Status == corev1.ConditionTrue)
				break
			}
		}

		schedulable := !node.Spec.Unschedulable
		cordoned := node.Spec.Unschedulable // explicitly set by kubectl cordon
		healthy := ready && schedulable

		if ready {
			readyCount++
		}
		if schedulable {
			schedulableCount++
		}
		if healthy {
			healthyCount++
		}

		// Skip nodes that don't match the filter
		if filter == "unhealthy" && healthy {
			continue
		}
		if filter == "healthy" && !healthy {
			continue
		}

		// Extract IP and hostname
		var internalIP, hostname string
		for _, addr := range node.Status.Addresses {
			switch addr.Type {
			case corev1.NodeInternalIP:
				internalIP = addr.Address
			case corev1.NodeHostName:
				hostname = addr.Address
			}
		}

		// Extract roles from labels
		var roles []string
		for label := range node.Labels {
			if strings.HasPrefix(label, "node-role.kubernetes.io/") {
				role := strings.TrimPrefix(label, "node-role.kubernetes.io/")
				if role != "" {
					roles = append(roles, role)
				}
			}
		}

		result = append(result, nodeInfo{
			Name:        node.Name,
			InternalIP:  internalIP,
			Hostname:    hostname,
			Roles:       roles,
			Ready:       ready,
			Cordoned:    cordoned,
			Schedulable: schedulable,
			Healthy:     healthy,
		})
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	// Add summary stats
	unhealthyCount := totalCount - healthyCount

	summary := fmt.Sprintf("Total: %d nodes | Healthy: %d | Unhealthy: %d (NotReady: %d, Unschedulable: %d)\n",
		totalCount, healthyCount, unhealthyCount, totalCount-readyCount, totalCount-schedulableCount)

	if filter == "unhealthy" {
		summary += fmt.Sprintf("Showing %d unhealthy nodes:\n\n", len(result))
	} else if filter == "healthy" {
		summary += fmt.Sprintf("Showing %d healthy nodes:\n\n", len(result))
	} else {
		summary += fmt.Sprintf("\nShowing all %d nodes:\n\n", len(result))
	}

	return summary + string(b), nil
}

// NodeStatus gets a single node's conditions, capacity, and allocatable from the K8s API. No SSH required.
type NodeStatus struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *NodeStatus) Name() string { return "node_status" }

func (t *NodeStatus) Description() string {
	return "Get a node's status from the Kubernetes API: conditions (Ready, MemoryPressure, DiskPressure, PIDPressure), capacity and allocatable resources."
}

func (t *NodeStatus) InputSchema() map[string]any { return nodeArgSchema() }

func (t *NodeStatus) Execute(ctx context.Context, input map[string]any) (string, error) {
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
		Name        string                              `json:"name"`
		InternalIP  string                              `json:"internal_ip"`
		Cordoned    bool                                `json:"cordoned"`
		Conditions  map[corev1.NodeConditionType]string `json:"conditions"`
		Capacity    map[corev1.ResourceName]string      `json:"capacity"`
		Allocatable map[corev1.ResourceName]string      `json:"allocatable"`
	}{
		Name:        node.Name,
		InternalIP:  n.InternalIP,
		Cordoned:    node.Spec.Unschedulable,
		Conditions:  map[corev1.NodeConditionType]string{},
		Capacity:    map[corev1.ResourceName]string{},
		Allocatable: map[corev1.ResourceName]string{},
	}
	for _, c := range node.Status.Conditions {
		out.Conditions[c.Type] = string(c.Status)
	}
	for k, v := range node.Status.Capacity {
		out.Capacity[k] = v.String()
	}
	for k, v := range node.Status.Allocatable {
		out.Allocatable[k] = v.String()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// CordonNode marks a node as unschedulable (kubectl cordon).
type CordonNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *CordonNode) Name() string { return "cordon_node" }

func (t *CordonNode) Description() string {
	return "Mark a node as unschedulable (cordon). New pods will not be scheduled on this node, but existing pods remain running."
}

func (t *CordonNode) InputSchema() map[string]any { return nodeArgSchema() }

func (t *CordonNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if node.Spec.Unschedulable {
		return fmt.Sprintf("Node %s is already unschedulable", n.Name), nil
	}

	node.Spec.Unschedulable = true
	_, err = t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("cordon node %s: %w", n.Name, err)
	}

	return fmt.Sprintf("✅ Node %s marked as unschedulable (cordoned)", n.Name), nil
}

// UncordonNode marks a node as schedulable (kubectl uncordon).
type UncordonNode struct {
	CS    *kubernetes.Clientset
	Nodes *nodes.Registry
}

func (t *UncordonNode) Name() string { return "uncordon_node" }

func (t *UncordonNode) Description() string {
	return "Mark a node as schedulable (uncordon). New pods can be scheduled on this node again."
}

func (t *UncordonNode) InputSchema() map[string]any { return nodeArgSchema() }

func (t *UncordonNode) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	node, err := t.CS.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", n.Name, err)
	}

	if !node.Spec.Unschedulable {
		return fmt.Sprintf("Node %s is already schedulable", n.Name), nil
	}

	node.Spec.Unschedulable = false
	_, err = t.CS.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("uncordon node %s: %w", n.Name, err)
	}

	return fmt.Sprintf("✅ Node %s marked as schedulable (uncordoned)", n.Name), nil
}

// ListPods lists pods in a namespace (or all namespaces).
type ListPods struct {
	CS *kubernetes.Clientset
}

func (t *ListPods) Name() string { return "list_pods" }

func (t *ListPods) Description() string {
	return "List pods in a specific namespace or all namespaces. Supports filtering by node (field_selector) and labels (label_selector)."
}

func (t *ListPods) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"namespace": map[string]any{
				"type":        "string",
				"description": "Namespace name, or 'all' for all namespaces",
			},
			"label_selector": map[string]any{
				"type":        "string",
				"description": "Label selector to filter pods (e.g., 'app=nginx', 'env=prod,tier=frontend'). Optional.",
			},
			"field_selector": map[string]any{
				"type":        "string",
				"description": "Field selector to filter pods. Format: 'spec.nodeName=<node-name>' to filter by node, 'status.phase=Running' to filter by phase. Optional.",
			},
		},
		"required": []string{"namespace"},
	}
}

func (t *ListPods) Execute(ctx context.Context, input map[string]any) (string, error) {
	namespace, _ := input["namespace"].(string)
	labelSelector, _ := input["label_selector"].(string)
	fieldSelector, _ := input["field_selector"].(string)

	if namespace == "" {
		namespace = "default"
	}

	// When namespace is "all", query all namespaces
	listNS := namespace
	if namespace == "all" {
		listNS = ""
	}

	// Build ListOptions
	listOpts := metav1.ListOptions{}
	if labelSelector != "" {
		listOpts.LabelSelector = labelSelector
	}
	if fieldSelector != "" {
		listOpts.FieldSelector = fieldSelector
	}

	pods, err := t.CS.CoreV1().Pods(listNS).List(ctx, listOpts)
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}

	// Pod details
	type podDetail struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Status    string `json:"status"`
		Restarts  int32  `json:"restarts"`
		Node      string `json:"node,omitempty"`
		IP        string `json:"ip,omitempty"`
	}

	// Group pods by status
	podsByStatus := make(map[string][]podDetail)
	statusCount := make(map[string]int)

	for _, pod := range pods.Items {
		status := string(pod.Status.Phase)

		// Count restarts across all containers
		var restarts int32
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}

		detail := podDetail{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    status,
			Restarts:  restarts,
			Node:      pod.Spec.NodeName,
			IP:        pod.Status.PodIP,
		}

		podsByStatus[status] = append(podsByStatus[status], detail)
		statusCount[status]++
	}

	out := struct {
		Namespace     string                  `json:"namespace"`
		TotalPods     int                     `json:"total_pods"`
		StatusCount   map[string]int          `json:"status_count"`
		PodsByStatus  map[string][]podDetail  `json:"pods_by_status"`
		LabelSelector string                  `json:"label_selector,omitempty"`
		FieldSelector string                  `json:"field_selector,omitempty"`
	}{
		Namespace:     namespace,
		TotalPods:     len(pods.Items),
		StatusCount:   statusCount,
		PodsByStatus:  podsByStatus,
		LabelSelector: labelSelector,
		FieldSelector: fieldSelector,
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}


// ListNamespaces lists all namespaces in the cluster.
type ListNamespaces struct {
	CS *kubernetes.Clientset
}

func (t *ListNamespaces) Name() string { return "list_namespaces" }

func (t *ListNamespaces) Description() string {
	return "List all namespaces in the cluster with their status and age."
}

func (t *ListNamespaces) InputSchema() map[string]any {
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

func (t *ListNamespaces) Execute(ctx context.Context, _ map[string]any) (string, error) {
	namespaces, err := t.CS.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list namespaces: %w", err)
	}

	type nsInfo struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Age    string `json:"age"`
	}

	result := make([]nsInfo, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		age := ""
		if !ns.CreationTimestamp.IsZero() {
			age = fmt.Sprintf("%v", metav1.Now().Sub(ns.CreationTimestamp.Time).Round(24*3600*1000000000))
		}

		result = append(result, nsInfo{
			Name:   ns.Name,
			Status: string(ns.Status.Phase),
			Age:    age,
		})
	}

	out := struct {
		Total      int      `json:"total"`
		Namespaces []nsInfo `json:"namespaces"`
	}{
		Total:      len(result),
		Namespaces: result,
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// AnalyzePodLogs fetches the last 1000 log lines from a pod for error analysis.
type AnalyzePodLogs struct {
	CS *kubernetes.Clientset
}

func (t *AnalyzePodLogs) Name() string { return "analyze_pod_logs" }

func (t *AnalyzePodLogs) Description() string {
	return "Fetch the last 1000 lines of logs from a pod for error analysis. Returns raw log text."
}

func (t *AnalyzePodLogs) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pod": map[string]any{
				"type":        "string",
				"description": "Pod name (required)",
			},
			"namespace": map[string]any{
				"type":        "string",
				"description": "Namespace (default: default)",
			},
			"container": map[string]any{
				"type":        "string",
				"description": "Container name (optional, for multi-container pods)",
			},
		},
		"required": []string{"pod"},
	}
}

func (t *AnalyzePodLogs) Execute(ctx context.Context, input map[string]any) (string, error) {
	podName, _ := input["pod"].(string)
	if podName == "" {
		return "", fmt.Errorf("pod name is required")
	}

	ns := "default"
	if v, ok := input["namespace"].(string); ok && v != "" {
		ns = v
	}

	tailLines := int64(1000)
	opts := &corev1.PodLogOptions{
		TailLines: &tailLines,
	}
	if container, ok := input["container"].(string); ok && container != "" {
		opts.Container = container
	}

	stream, err := t.CS.CoreV1().Pods(ns).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("get pod logs: %w", err)
	}
	defer stream.Close()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, stream)
	if err != nil {
		return "", fmt.Errorf("read pod logs: %w", err)
	}

	logs := buf.String()
	if logs == "" {
		return "No logs found for pod " + podName, nil
	}

	return fmt.Sprintf("Pod: %s/%s (last 1000 lines):\n\n%s", ns, podName, logs), nil
}

