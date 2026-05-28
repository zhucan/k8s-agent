package nodes

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k8s-inspect/internal/k8s"
	"k8s.io/client-go/kubernetes"
)

// Node 是给上层 tool 用的简化视图
type Node struct {
	Name       string   `json:"name"`
	InternalIP string   `json:"internal_ip"`
	Hostname   string   `json:"hostname,omitempty"`
	Roles      []string `json:"roles,omitempty"`
}

// Registry 维护一份从 K8s API 抓来的节点白名单,
// 用户从飞书消息里给的"节点标识"(IP/hostname/node name) 必须能在这里查到才允许 SSH。
type Registry struct {
	cs *kubernetes.Clientset

	mu       sync.RWMutex
	nodes    []Node
	byKey    map[string]Node // key 都是小写,可能是 IP / hostname / name
	loadedAt time.Time
}

func New(cs *kubernetes.Clientset) *Registry {
	return &Registry{cs: cs}
}

// Refresh 同步刷新一次白名单。启动时和后台定时各调一次。
func (r *Registry) Refresh(ctx context.Context) error {
	infos, err := k8s.ListNodes(ctx, r.cs)
	if err != nil {
		return err
	}
	nodes := make([]Node, 0, len(infos))
	idx := make(map[string]Node, len(infos)*3)
	for _, ni := range infos {
		n := Node{
			Name:       ni.Name,
			InternalIP: ni.InternalIP,
			Hostname:   ni.Hostname,
			Roles:      ni.Roles,
		}
		nodes = append(nodes, n)
		if n.InternalIP != "" {
			idx[strings.ToLower(n.InternalIP)] = n
		}
		if n.Name != "" {
			idx[strings.ToLower(n.Name)] = n
		}
		if n.Hostname != "" {
			idx[strings.ToLower(n.Hostname)] = n
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	r.mu.Lock()
	r.nodes = nodes
	r.byKey = idx
	r.loadedAt = time.Now()
	r.mu.Unlock()
	return nil
}

// StartAutoRefresh 后台每 interval 刷新一次。返回的 stop 调用后停止。
func (r *Registry) StartAutoRefresh(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := r.Refresh(ctx); err != nil {
					log.Printf("nodes refresh: %v", err)
				}
			}
		}
	}()
}

// Resolve 把用户给的节点标识(IP / hostname / k8s node name)解析为白名单内的 Node。
// 不在白名单返回 ErrUnknownNode。
func (r *Registry) Resolve(input string) (Node, error) {
	key := strings.TrimSpace(strings.ToLower(input))
	if key == "" {
		return Node{}, fmt.Errorf("node identifier is empty")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n, ok := r.byKey[key]; ok {
		return n, nil
	}
	// 用户可能传了"10.0.0.5:9100"这种带端口形式
	if ip, _, err := net.SplitHostPort(key); err == nil {
		if n, ok := r.byKey[strings.ToLower(ip)]; ok {
			return n, nil
		}
	}
	return Node{}, fmt.Errorf("node %q not in cluster whitelist", input)
}

func (r *Registry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, len(r.nodes))
	copy(out, r.nodes)
	return out
}

func (r *Registry) LoadedAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadedAt
}
