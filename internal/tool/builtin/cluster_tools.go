package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/k8s-inspect/internal/cluster"
)

// ListClusters lists all available clusters.
type ListClusters struct {
	Manager *cluster.Manager
}

func (t *ListClusters) Name() string { return "list_clusters" }

func (t *ListClusters) Description() string {
	return "List all available Kubernetes clusters."
}

func (t *ListClusters) InputSchema() map[string]any {
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

func (t *ListClusters) Execute(ctx context.Context, _ map[string]any) (string, error) {
	clusters := t.Manager.List()

	b, err := json.MarshalIndent(clusters, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d clusters:\n%s", len(clusters), string(b)), nil
}

// SwitchCluster switches to the specified cluster.
type SwitchCluster struct {
	Manager *cluster.Manager
}

func (t *SwitchCluster) Name() string { return "switch_cluster" }

func (t *SwitchCluster) Description() string {
	return "Switch to a different Kubernetes cluster. Supports cluster name, context name, alias, or fuzzy matching (e.g., 'prod', 'production', 'production-cluster')."
}

func (t *SwitchCluster) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cluster": map[string]any{
				"type":        "string",
				"description": "The cluster name (context name) to switch to",
			},
		},
		"required": []string{"cluster"},
	}
}

func (t *SwitchCluster) Execute(ctx context.Context, input map[string]any) (string, error) {
	clusterName, _ := input["cluster"].(string)
	if clusterName == "" {
		return "", fmt.Errorf("cluster name is required")
	}

	if err := t.Manager.SwitchCluster(clusterName); err != nil {
		return "", err
	}

	cluster, _ := t.Manager.Current()
	return fmt.Sprintf("✅ Switched to cluster %q (%d nodes)", clusterName, len(cluster.Nodes.List())), nil
}

// AddClusterToConfig adds a cluster to the config file and hot-reloads it.
type AddClusterToConfig struct {
	ConfigFile string
	Manager    *cluster.Manager
}

func (t *AddClusterToConfig) Name() string { return "add_cluster_to_config" }

func (t *AddClusterToConfig) Description() string {
	return "Add a new Kubernetes cluster to the configuration file and load it immediately."
}

func (t *AddClusterToConfig) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cluster_name": map[string]any{
				"type":        "string",
				"description": "The display name for the cluster (e.g., 'production', 'dev-cluster')",
			},
			"kubeconfig_content": map[string]any{
				"type":        "string",
				"description": "The content of the kubeconfig file (optional if kubeconfig_path is provided)",
			},
			"kubeconfig_path": map[string]any{
				"type":        "string",
				"description": "Path to the kubeconfig file (optional if kubeconfig_content is provided)",
			},
		},
		"required": []string{"cluster_name"},
	}
}

func (t *AddClusterToConfig) Execute(ctx context.Context, input map[string]any) (string, error) {
	clusterName, _ := input["cluster_name"].(string)
	kubeconfigContent, _ := input["kubeconfig_content"].(string)
	kubeconfigPath, _ := input["kubeconfig_path"].(string)

	if clusterName == "" {
		return "", fmt.Errorf("cluster_name is required")
	}

	// Resolve kubeconfig content
	var content []byte
	var err error

	if kubeconfigContent != "" {
		// Option 1: content provided directly
		content = []byte(kubeconfigContent)
	} else if kubeconfigPath != "" {
		// Option 2: read from file path
		content, err = os.ReadFile(kubeconfigPath)
		if err != nil {
			return "", fmt.Errorf("read kubeconfig file: %w", err)
		}
	} else {
		return "", fmt.Errorf("either kubeconfig_content or kubeconfig_path is required")
	}

	// Step 1: save kubeconfig and add to config file
	savedPath, contextName, err := cluster.AddClusterFromKubeconfig(t.ConfigFile, clusterName, content)
	if err != nil {
		return "", fmt.Errorf("add cluster: %w", err)
	}

	// Step 2: hot-reload — add immediately to Manager
	if t.Manager != nil {
		// Use the saved kubeconfig path, cluster name, and resolved context
		if err := t.Manager.AddCluster(ctx, clusterName, savedPath, contextName); err != nil {
			return "", fmt.Errorf("load cluster: %w", err)
		}

		// Get cluster info
		cluster, _ := t.Manager.Get(clusterName)
		nodeCount := 0
		if cluster != nil {
			nodeCount = len(cluster.Nodes.List())
		}

		return fmt.Sprintf("✅ Cluster '%s' added and loaded successfully\n📊 Nodes: %d\n\n💡 Use switch_cluster to switch to the new cluster", clusterName, nodeCount), nil
	}

	return fmt.Sprintf("✅ Cluster '%s' added to config file\n\n💡 Restart the bot to load the new cluster", clusterName), nil
}

// FindNodeInClusters searches for a node across all clusters.
type FindNodeInClusters struct {
	Manager *cluster.Manager
}

func (t *FindNodeInClusters) Name() string { return "find_node_in_clusters" }

func (t *FindNodeInClusters) Description() string {
	return "Search for a node across all available clusters. Use this when a node is not found in the current cluster. Returns which cluster(s) contain the node."
}

func (t *FindNodeInClusters) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node name, IP, or hostname to search for",
			},
		},
		"required": []string{"node"},
	}
}

func (t *FindNodeInClusters) Execute(ctx context.Context, input map[string]any) (string, error) {
	nodeIdentifier, _ := input["node"].(string)
	if nodeIdentifier == "" {
		return "", fmt.Errorf("node identifier is required")
	}

	clusters := t.Manager.List()
	var foundClusters []string

	for _, clusterInfo := range clusters {
		cluster, err := t.Manager.Get(clusterInfo.Name)
		if err != nil {
			continue
		}

		// Search for the node in this cluster's node list
		for _, node := range cluster.Nodes.List() {
			if node.Name == nodeIdentifier ||
				node.InternalIP == nodeIdentifier ||
				node.Hostname == nodeIdentifier {
				foundClusters = append(foundClusters, clusterInfo.Name)
				break
			}
		}
	}

	if len(foundClusters) == 0 {
		return fmt.Sprintf("❌ Node '%s' not found in any cluster", nodeIdentifier), nil
	}

	// Return the cluster name where the node was found
	if len(foundClusters) == 1 {
		return fmt.Sprintf("FOUND_IN_CLUSTER:%s", foundClusters[0]), nil
	}

	// Node found in multiple clusters — return the first one
	return fmt.Sprintf("FOUND_IN_CLUSTER:%s", foundClusters[0]), nil
}
