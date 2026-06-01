package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
)

// AddClusterFromKubeconfig adds a cluster to the config file from kubeconfig content.
// Returns the saved kubeconfig file path and the context name.
func AddClusterFromKubeconfig(configFile, name string, kubeconfigContent []byte) (string, string, error) {
	// Parse kubeconfig to get current-context
	config, err := clientcmd.Load(kubeconfigContent)
	if err != nil {
		return "", "", fmt.Errorf("parse kubeconfig: %w", err)
	}

	contextName := config.CurrentContext
	if contextName == "" {
		// Fall back to the first available context if current-context is not set
		if len(config.Contexts) == 0 {
			return "", "", fmt.Errorf("no contexts found in kubeconfig")
		}
		for name := range config.Contexts {
			contextName = name
			break
		}
	}

	// Create the kubeconfigs directory alongside the config file
	configDir := filepath.Dir(configFile)
	kubeconfigDir := filepath.Join(configDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		return "", "", fmt.Errorf("create kubeconfig dir: %w", err)
	}

	// Save the kubeconfig file
	kubeconfigPath := filepath.Join(kubeconfigDir, fmt.Sprintf("%s.yaml", name))
	if err := os.WriteFile(kubeconfigPath, kubeconfigContent, 0600); err != nil {
		return "", "", fmt.Errorf("write kubeconfig: %w", err)
	}

	// Load existing config if present
	var cfg *Config
	if _, err := os.Stat(configFile); err == nil {
		cfg, err = LoadConfig(configFile)
		if err != nil {
			cfg = &Config{Clusters: []ClusterConfig{}}
		}
	} else {
		cfg = &Config{Clusters: []ClusterConfig{}}
	}

	// Update existing entry if name already exists
	for i, c := range cfg.Clusters {
		if c.Name == name {
			cfg.Clusters[i] = ClusterConfig{
				Name:       name,
				Context:    contextName,
				Kubeconfig: kubeconfigPath,
			}
			return kubeconfigPath, contextName, cfg.SaveConfig(configFile)
		}
	}

	// Append new cluster
	cfg.Clusters = append(cfg.Clusters, ClusterConfig{
		Name:       name,
		Context:    contextName,
		Kubeconfig: kubeconfigPath,
	})

	return kubeconfigPath, contextName, cfg.SaveConfig(configFile)
}
