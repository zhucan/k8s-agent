package k8s

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NodeInfo holds node metadata and key conditions for use by inspectors.
type NodeInfo struct {
	Name        string
	InternalIP  string
	Hostname    string
	Roles       []string
	Conditions  map[corev1.NodeConditionType]corev1.ConditionStatus
	Unschedulable bool
}

func ListNodes(ctx context.Context, cs *kubernetes.Clientset) ([]NodeInfo, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeInfo, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		ni := NodeInfo{
			Name:          n.Name,
			Conditions:    map[corev1.NodeConditionType]corev1.ConditionStatus{},
			Unschedulable: n.Spec.Unschedulable,
		}
		for _, addr := range n.Status.Addresses {
			switch addr.Type {
			case corev1.NodeInternalIP:
				ni.InternalIP = addr.Address
			case corev1.NodeHostName:
				ni.Hostname = addr.Address
			}
		}
		for _, c := range n.Status.Conditions {
			ni.Conditions[c.Type] = c.Status
		}
		for k := range n.Labels {
			if strings.HasPrefix(k, "node-role.kubernetes.io/") {
				ni.Roles = append(ni.Roles, strings.TrimPrefix(k, "node-role.kubernetes.io/"))
			}
		}
		out = append(out, ni)
	}
	return out, nil
}
