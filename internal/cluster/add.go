package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
)

// AddClusterFromKubeconfig 从 kubeconfig 文件内容添加集群到配置
// 返回保存的 kubeconfig 文件路径和 context 名称
func AddClusterFromKubeconfig(configFile, name string, kubeconfigContent []byte) (string, string, error) {
	// 解析 kubeconfig 获取 current-context
	config, err := clientcmd.Load(kubeconfigContent)
	if err != nil {
		return "", "", fmt.Errorf("parse kubeconfig: %w", err)
	}

	// 获取 current-context
	contextName := config.CurrentContext
	if contextName == "" {
		// 如果没有 current-context，使用第一个可用的 context
		if len(config.Contexts) == 0 {
			return "", "", fmt.Errorf("no contexts found in kubeconfig")
		}
		for name := range config.Contexts {
			contextName = name
			break
		}
	}

	// 创建持久化的 kubeconfig 文件目录
	configDir := filepath.Dir(configFile)
	kubeconfigDir := filepath.Join(configDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		return "", "", fmt.Errorf("create kubeconfig dir: %w", err)
	}

	// 保存 kubeconfig 文件
	kubeconfigPath := filepath.Join(kubeconfigDir, fmt.Sprintf("%s.yaml", name))
	if err := os.WriteFile(kubeconfigPath, kubeconfigContent, 0600); err != nil {
		return "", "", fmt.Errorf("write kubeconfig: %w", err)
	}

	// 加载现有配置（如果存在）
	var cfg *Config
	if _, err := os.Stat(configFile); err == nil {
		cfg, err = LoadConfig(configFile)
		if err != nil {
			cfg = &Config{Clusters: []ClusterConfig{}}
		}
	} else {
		cfg = &Config{Clusters: []ClusterConfig{}}
	}

	// 检查是否已存在相同名称
	for i, c := range cfg.Clusters {
		if c.Name == name {
			// 更新现有集群
			cfg.Clusters[i] = ClusterConfig{
				Name:       name,
				Context:    contextName,
				Kubeconfig: kubeconfigPath,
			}
			return kubeconfigPath, contextName, cfg.SaveConfig(configFile)
		}
	}

	// 添加新集群
	cfg.Clusters = append(cfg.Clusters, ClusterConfig{
		Name:       name,
		Context:    contextName,
		Kubeconfig: kubeconfigPath,
	})

	return kubeconfigPath, contextName, cfg.SaveConfig(configFile)
}
