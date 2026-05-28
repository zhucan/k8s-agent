package cluster

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

// SyncConfigWithDir scans the kubeconfigs directory and synchronizes clusters.json.
// New yaml files get added; entries whose kubeconfig file no longer exists get removed.
// Returns the updated Config and whether any changes were made.
func SyncConfigWithDir(configFile, kubeconfigsDir string) (*Config, bool, error) {
	// Load existing config (or start fresh)
	var cfg *Config
	if _, err := os.Stat(configFile); err == nil {
		cfg, err = LoadConfig(configFile)
		if err != nil {
			cfg = &Config{Clusters: []ClusterConfig{}}
		}
	} else {
		cfg = &Config{Clusters: []ClusterConfig{}}
	}

	// Scan directory for yaml files
	dirFiles, err := os.ReadDir(kubeconfigsDir)
	if err != nil {
		return cfg, false, fmt.Errorf("read kubeconfigs dir: %w", err)
	}

	filesInDir := make(map[string]string) // name (no ext) -> full path
	for _, f := range dirFiles {
		if f.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ext)
		filesInDir[name] = filepath.Join(kubeconfigsDir, f.Name())
	}

	// Build index of existing config entries by name
	existingByName := make(map[string]int) // name -> index
	for i, c := range cfg.Clusters {
		existingByName[c.Name] = i
	}

	changed := false

	// Add new clusters (in dir but not in config)
	for name, path := range filesInDir {
		if _, exists := existingByName[name]; exists {
			continue
		}
		contextName, err := extractContext(path)
		if err != nil {
			log.Printf("[cluster/sync] skip %s: %v", path, err)
			continue
		}
		cfg.Clusters = append(cfg.Clusters, ClusterConfig{
			Name:       name,
			Context:    contextName,
			Kubeconfig: path,
		})
		changed = true
		log.Printf("[cluster/sync] added cluster %q from %s", name, path)
	}

	// Remove clusters whose kubeconfig file no longer exists
	var kept []ClusterConfig
	for _, c := range cfg.Clusters {
		if c.Kubeconfig == "" {
			kept = append(kept, c)
			continue
		}
		if _, err := os.Stat(c.Kubeconfig); err != nil {
			changed = true
			log.Printf("[cluster/sync] removed cluster %q (file gone: %s)", c.Name, c.Kubeconfig)
			continue
		}
		kept = append(kept, c)
	}
	cfg.Clusters = kept

	// Persist if changed
	if changed {
		if err := cfg.SaveConfig(configFile); err != nil {
			return cfg, changed, fmt.Errorf("save config: %w", err)
		}
		log.Printf("[cluster/sync] clusters.json updated (%d clusters)", len(cfg.Clusters))
	} else {
		log.Printf("[cluster/sync] clusters.json is up-to-date (%d clusters)", len(cfg.Clusters))
	}

	return cfg, changed, nil
}

func extractContext(kubeconfigPath string) (string, error) {
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	config, err := clientcmd.Load(data)
	if err != nil {
		return "", fmt.Errorf("parse kubeconfig: %w", err)
	}
	if config.CurrentContext != "" {
		return config.CurrentContext, nil
	}
	for name := range config.Contexts {
		return name, nil
	}
	return "", fmt.Errorf("no contexts found")
}
