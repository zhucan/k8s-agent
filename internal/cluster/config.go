package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config 集群配置文件结构
type Config struct {
	Clusters []ClusterConfig `json:"clusters"`
}

// ClusterConfig 单个集群的配置
type ClusterConfig struct {
	Name       string `json:"name"`       // 显示名称（用户友好）
	Context    string `json:"context"`    // kubeconfig 中的 context 名称
	Kubeconfig string `json:"kubeconfig"` // kubeconfig 文件路径
}

// LoadConfig 从文件加载集群配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// FindClusterByName 根据名称查找集群配置
func (c *Config) FindClusterByName(input string) (*ClusterConfig, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	// 1. 精确匹配 name
	for _, cluster := range c.Clusters {
		if strings.ToLower(cluster.Name) == input {
			return &cluster, nil
		}
	}

	// 2. 精确匹配 context
	for _, cluster := range c.Clusters {
		if strings.ToLower(cluster.Context) == input {
			return &cluster, nil
		}
	}

	// 3. 模糊匹配 name（包含关系）
	for _, cluster := range c.Clusters {
		if strings.Contains(strings.ToLower(cluster.Name), input) {
			return &cluster, nil
		}
	}

	return nil, fmt.Errorf("cluster not found: %s", input)
}

// GetAllClusters 获取所有集群配置
func (c *Config) GetAllClusters() []ClusterConfig {
	return c.Clusters
}

// DefaultConfig 生成默认配置（从 kubeconfig contexts 自动生成）
func DefaultConfig(contexts []string) *Config {
	cfg := &Config{
		Clusters: make([]ClusterConfig, 0, len(contexts)),
	}

	for _, ctx := range contexts {
		cfg.Clusters = append(cfg.Clusters, ClusterConfig{
			Name:    ctx,
			Context: ctx,
		})
	}

	return cfg
}

// SaveConfig 保存配置到文件
func (c *Config) SaveConfig(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}
