// Package cluster provides multi-cluster management with runtime dynamic switching.
package cluster

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/k8s"
	"github.com/k8s-inspect/internal/nodes"
)

// Cluster represents a runtime instance of a K8s cluster.
type Cluster struct {
	Name       string              // Display name (user-friendly)
	Context    string              // Context name in kubeconfig
	Kubeconfig string              // Path to kubeconfig file
	CS         *kubernetes.Clientset
	RestConfig *rest.Config        // REST config for advanced operations (e.g. remotecommand)
	Nodes      *nodes.Registry
	StopFunc   context.CancelFunc  // Cancels background node refresh
}

// Manager manages multiple clusters and supports dynamic switching.
type Manager struct {
	mu       sync.RWMutex
	clusters map[string]*Cluster  // key: display name
	current  string               // currently selected cluster name
	config   *Config              // optional cluster config (for aliases and metadata)
}

// NewManager creates a new cluster manager.
func NewManager() *Manager {
	return &Manager{
		clusters: make(map[string]*Cluster),
	}
}

// NewManagerWithConfig creates a cluster manager with a pre-loaded config.
func NewManagerWithConfig(cfg *Config) *Manager {
	return &Manager{
		clusters: make(map[string]*Cluster),
		config:   cfg,
	}
}

// SetConfig sets the cluster configuration.
func (m *Manager) SetConfig(cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
}

// AddCluster adds a cluster initialized from a kubeconfig file and context name.
// name is the user-friendly display name; kubeconfig is the file path; contextName is the kubeconfig context.
func (m *Manager) AddCluster(ctx context.Context, name, kubeconfig, contextName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clusters[name]; exists {
		return fmt.Errorf("cluster %q already exists", name)
	}

	cs, err := k8s.NewClientForContext(kubeconfig, contextName)
	if err != nil {
		return fmt.Errorf("create client for context %q: %w", contextName, err)
	}

	restConfig, err := k8s.BuildConfigForContext(kubeconfig, contextName)
	if err != nil {
		return fmt.Errorf("create rest config for context %q: %w", contextName, err)
	}

	clusterCtx, cancel := context.WithCancel(ctx)
	nodeReg := nodes.New(cs)
	if err := nodeReg.Refresh(clusterCtx); err != nil {
		cancel()
		return fmt.Errorf("refresh nodes for context %q: %w", contextName, err)
	}
	nodeReg.StartAutoRefresh(clusterCtx, 5*time.Minute)

	cluster := &Cluster{
		Name:       name,
		Context:    contextName,
		Kubeconfig: kubeconfig,
		CS:         cs,
		RestConfig: restConfig,
		Nodes:      nodeReg,
		StopFunc:   cancel,
	}

	m.clusters[name] = cluster

	// Auto-select the first cluster added
	if m.current == "" {
		m.current = name
	}

	log.Printf("[cluster] added cluster %q (context: %s, %d nodes)", name, contextName, len(nodeReg.List()))
	return nil
}

// RemoveCluster removes a cluster and stops its background tasks.
func (m *Manager) RemoveCluster(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, exists := m.clusters[name]
	if !exists {
		return fmt.Errorf("cluster %q not found", name)
	}

	cluster.StopFunc()
	delete(m.clusters, name)

	// Switch to another cluster if the removed one was current
	if m.current == name {
		m.current = ""
		for clusterName := range m.clusters {
			m.current = clusterName
			break
		}
	}

	log.Printf("[cluster] removed cluster %q", name)
	return nil
}

// SwitchCluster switches the active cluster. Supports exact name, context name, and fuzzy matching.
func (m *Manager) SwitchCluster(input string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	input = strings.TrimSpace(input)

	// 1. Exact match on display name
	if _, exists := m.clusters[input]; exists {
		m.current = input
		log.Printf("[cluster] switched to cluster %q", input)
		return nil
	}

	// 2. Look up via config if available
	if m.config != nil {
		clusterCfg, err := m.config.FindClusterByName(input)
		if err == nil {
			if _, exists := m.clusters[clusterCfg.Name]; exists {
				m.current = clusterCfg.Name
				log.Printf("[cluster] switched to cluster %q", clusterCfg.Name)
				return nil
			}
		}
	}

	// 3. Fuzzy match
	matched := m.fuzzyMatch(input)
	if matched == "" {
		return fmt.Errorf("cluster %q not found", input)
	}

	m.current = matched
	log.Printf("[cluster] switched to cluster %q", matched)
	return nil
}

// fuzzyMatch returns a cluster name matching the input (case-insensitive substring match).
func (m *Manager) fuzzyMatch(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))

	for name := range m.clusters {
		if strings.ToLower(name) == input {
			return name
		}
	}

	for name := range m.clusters {
		if strings.Contains(strings.ToLower(name), input) {
			return name
		}
	}

	return ""
}

// Current returns the currently active cluster.
func (m *Manager) Current() (*Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.current == "" {
		return nil, fmt.Errorf("no cluster selected")
	}

	cluster, exists := m.clusters[m.current]
	if !exists {
		return nil, fmt.Errorf("current cluster %q not found", m.current)
	}

	return cluster, nil
}

// Get returns the cluster with the given name.
func (m *Manager) Get(name string) (*Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, exists := m.clusters[name]
	if !exists {
		return nil, fmt.Errorf("cluster %q not found", name)
	}

	return cluster, nil
}

// List returns summary info for all managed clusters.
func (m *Manager) List() []ClusterInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ClusterInfo, 0, len(m.clusters))
	for name, cluster := range m.clusters {
		info := ClusterInfo{
			Name:        name,
			Context:     cluster.Context,
			DisplayName: name,
			NodeCount:   len(cluster.Nodes.List()),
			Current:     name == m.current,
		}

		result = append(result, info)
	}

	return result
}

// CurrentName returns the name of the currently active cluster.
func (m *Manager) CurrentName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// StopAll stops all background tasks and clears all clusters.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cluster := range m.clusters {
		cluster.StopFunc()
	}
	m.clusters = make(map[string]*Cluster)
	m.current = ""
}

// ClusterInfo is a summary of a managed cluster.
type ClusterInfo struct {
	Name        string // Display name
	Context     string // Kubeconfig context name
	DisplayName string // Display name (from config file)
	NodeCount   int    // Number of nodes
	Current     bool   // Whether this is the active cluster
}
