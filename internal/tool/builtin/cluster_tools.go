package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/k8s-inspect/internal/cluster"
)

// ListClusters: 列出所有可用的集群
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

// SwitchCluster: 切换到指定集群
type SwitchCluster struct {
	Manager *cluster.Manager
}

func (t *SwitchCluster) Name() string { return "switch_cluster" }

func (t *SwitchCluster) Description() string {
	return "Switch to a different Kubernetes cluster. Supports cluster name, context name, alias, or fuzzy matching (e.g., 'prod', 'production', '生产集群')."
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

// AddClusterToConfig: 添加集群到配置文件并热加载
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
				"description": "The display name for the cluster (e.g., '生产环境', 'dev-cluster')",
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

	// 获取 kubeconfig 内容
	var content []byte
	var err error

	if kubeconfigContent != "" {
		// 方式 1: 直接提供内容
		content = []byte(kubeconfigContent)
	} else if kubeconfigPath != "" {
		// 方式 2: 从文件路径读取
		content, err = os.ReadFile(kubeconfigPath)
		if err != nil {
			return "", fmt.Errorf("read kubeconfig file: %w", err)
		}
	} else {
		return "", fmt.Errorf("either kubeconfig_content or kubeconfig_path is required")
	}

	// 1. 添加到配置文件并保存 kubeconfig
	savedPath, contextName, err := cluster.AddClusterFromKubeconfig(t.ConfigFile, clusterName, content)
	if err != nil {
		return "", fmt.Errorf("add cluster: %w", err)
	}

	// 2. 热加载：立即添加到 Manager
	if t.Manager != nil {
		// 使用保存的 kubeconfig 路径、集群名称和解析出的 context
		if err := t.Manager.AddCluster(ctx, clusterName, savedPath, contextName); err != nil {
			return "", fmt.Errorf("load cluster: %w", err)
		}

		// 获取集群信息
		cluster, _ := t.Manager.Get(clusterName)
		nodeCount := 0
		if cluster != nil {
			nodeCount = len(cluster.Nodes.List())
		}

		return fmt.Sprintf("✅ 集群 '%s' 已添加并加载成功\n📊 节点数量: %d\n\n💡 提示: 使用 switch_cluster 切换到新集群", clusterName, nodeCount), nil
	}

	return fmt.Sprintf("✅ 集群 '%s' 已添加到配置文件\n\n💡 提示: 需要重启 bot 才能加载新集群", clusterName), nil
}

// FindNodeInClusters: 在所有集群中搜索节点
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

		// 在该集群的节点列表中搜索
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
		return fmt.Sprintf("❌ 节点 '%s' 在所有集群中都未找到", nodeIdentifier), nil
	}

	// 返回找到的集群名称（使用友好名称）
	if len(foundClusters) == 1 {
		return fmt.Sprintf("FOUND_IN_CLUSTER:%s", foundClusters[0]), nil
	}

	// 多个集群都有该节点，返回第一个
	return fmt.Sprintf("FOUND_IN_CLUSTER:%s", foundClusters[0]), nil
}
