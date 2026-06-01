package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the cluster configuration file structure.
type Config struct {
	Clusters []ClusterConfig `json:"clusters"`
}

// ClusterConfig holds the configuration for a single cluster.
type ClusterConfig struct {
	Name       string `json:"name"`       // Display name (user-friendly)
	Context    string `json:"context"`    // Context name in kubeconfig
	Kubeconfig string `json:"kubeconfig"` // Path to kubeconfig file
}

// LoadConfig loads cluster configuration from a file.
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

// FindClusterByName looks up a cluster config by name, context, or fuzzy match.
func (c *Config) FindClusterByName(input string) (*ClusterConfig, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	// 1. Exact match on name
	for _, cluster := range c.Clusters {
		if strings.ToLower(cluster.Name) == input {
			return &cluster, nil
		}
	}

	// 2. Exact match on context
	for _, cluster := range c.Clusters {
		if strings.ToLower(cluster.Context) == input {
			return &cluster, nil
		}
	}

	// 3. Fuzzy match on name (substring)
	for _, cluster := range c.Clusters {
		if strings.Contains(strings.ToLower(cluster.Name), input) {
			return &cluster, nil
		}
	}

	return nil, fmt.Errorf("cluster not found: %s", input)
}

// GetAllClusters returns all cluster configurations.
func (c *Config) GetAllClusters() []ClusterConfig {
	return c.Clusters
}

// DefaultConfig generates a default configuration from a list of kubeconfig context names.
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

// SaveConfig saves the configuration to a file.
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
