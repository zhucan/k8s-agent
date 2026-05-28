// Package cluster 提供多集群管理能力,支持运行时动态切换集群。
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

// Cluster 代表一个 K8s 集群的运行时实例
type Cluster struct {
	Name       string              // 显示名称（用户友好）
	Context    string              // kubeconfig 中的 context 名称
	Kubeconfig string              // kubeconfig 文件路径
	CS         *kubernetes.Clientset
	RestConfig *rest.Config        // REST config for advanced operations
	Nodes      *nodes.Registry
	StopFunc   context.CancelFunc  // 停止 node refresh
}

// Manager 管理多个集群,支持动态切换
type Manager struct {
	mu       sync.RWMutex
	clusters map[string]*Cluster  // key: 显示名称（name）
	current  string               // 当前选中的集群名称（name）
	config   *Config              // 集群配置（可选，用于别名和元数据）
}

// NewManager 创建集群管理器
func NewManager() *Manager {
	return &Manager{
		clusters: make(map[string]*Cluster),
	}
}

// NewManagerWithConfig 创建带配置的集群管理器
func NewManagerWithConfig(cfg *Config) *Manager {
	return &Manager{
		clusters: make(map[string]*Cluster),
		config:   cfg,
	}
}

// SetConfig 设置集群配置
func (m *Manager) SetConfig(cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
}

// AddCluster 添加一个集群(从 kubeconfig + context 初始化)
// name: 集群的显示名称（用户友好）
// kubeconfig: kubeconfig 文件路径
// contextName: kubeconfig 中的 context 名称
func (m *Manager) AddCluster(ctx context.Context, name, kubeconfig, contextName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clusters[name]; exists {
		return fmt.Errorf("cluster %q already exists", name)
	}

	// 创建 K8s client
	cs, err := k8s.NewClientForContext(kubeconfig, contextName)
	if err != nil {
		return fmt.Errorf("create client for context %q: %w", contextName, err)
	}

	// 创建 REST config
	restConfig, err := k8s.BuildConfigForContext(kubeconfig, contextName)
	if err != nil {
		return fmt.Errorf("create rest config for context %q: %w", contextName, err)
	}

	// 初始化节点注册表
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

	// 如果是第一个集群,自动设为当前集群
	if m.current == "" {
		m.current = name
	}

	log.Printf("[cluster] added cluster %q (context: %s, %d nodes)", name, contextName, len(nodeReg.List()))
	return nil
}

// RemoveCluster 移除一个集群
func (m *Manager) RemoveCluster(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, exists := m.clusters[name]
	if !exists {
		return fmt.Errorf("cluster %q not found", name)
	}

	// 停止后台刷新
	cluster.StopFunc()
	delete(m.clusters, name)

	// 如果删除的是当前集群,切换到第一个可用集群
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

// SwitchCluster 切换当前集群（支持自然语言识别）
func (m *Manager) SwitchCluster(input string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	input = strings.TrimSpace(input)

	// 1. 直接匹配显示名称
	if _, exists := m.clusters[input]; exists {
		m.current = input
		log.Printf("[cluster] switched to cluster %q", input)
		return nil
	}

	// 2. 如果有配置文件，尝试通过配置查找
	if m.config != nil {
		clusterCfg, err := m.config.FindClusterByName(input)
		if err == nil {
			// 找到配置，使用显示名称
			if _, exists := m.clusters[clusterCfg.Name]; exists {
				m.current = clusterCfg.Name
				log.Printf("[cluster] switched to cluster %q", clusterCfg.Name)
				return nil
			}
		}
	}

	// 3. 尝试模糊匹配
	matched := m.fuzzyMatch(input)
	if matched == "" {
		return fmt.Errorf("cluster %q not found", input)
	}

	m.current = matched
	log.Printf("[cluster] switched to cluster %q", matched)
	return nil
}

// fuzzyMatch 模糊匹配集群名称
func (m *Manager) fuzzyMatch(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))

	// 精确匹配
	for name := range m.clusters {
		if strings.ToLower(name) == input {
			return name
		}
	}

	// 包含匹配
	for name := range m.clusters {
		if strings.Contains(strings.ToLower(name), input) {
			return name
		}
	}

	return ""
}

// Current 获取当前集群
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

// Get 获取指定集群
func (m *Manager) Get(name string) (*Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, exists := m.clusters[name]
	if !exists {
		return nil, fmt.Errorf("cluster %q not found", name)
	}

	return cluster, nil
}

// List 列出所有集群
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

// CurrentName 返回当前集群名称
func (m *Manager) CurrentName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// StopAll 停止所有集群的后台任务
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cluster := range m.clusters {
		cluster.StopFunc()
	}
	m.clusters = make(map[string]*Cluster)
	m.current = ""
}

// ClusterInfo 集群概览信息
type ClusterInfo struct {
	Name        string // 显示名称
	Context     string // Context 名称
	DisplayName string // 显示名称（来自配置文件）
	NodeCount   int    // 节点数量
	Current     bool   // 是否为当前集群
}
