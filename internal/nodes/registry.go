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

// Node is a simplified view of a cluster node exposed to tools.
type Node struct {
	Name       string   `json:"name"`
	InternalIP string   `json:"internal_ip"`
	Hostname   string   `json:"hostname,omitempty"`
	Roles      []string `json:"roles,omitempty"`
}

// Registry maintains a node whitelist fetched from the K8s API.
// Node identifiers supplied by the user (IP / hostname / node name) must resolve here before access is granted.
type Registry struct {
	cs *kubernetes.Clientset

	mu       sync.RWMutex
	nodes    []Node
	byKey    map[string]Node // keys are lowercase; may be IP, hostname, or name
	loadedAt time.Time
}

func New(cs *kubernetes.Clientset) *Registry {
	return &Registry{cs: cs}
}

// Refresh performs a synchronous refresh of the whitelist. Called once at startup and periodically in the background.
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

// StartAutoRefresh starts a background goroutine that refreshes the registry every interval.
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

// Resolve maps a user-supplied node identifier (IP / hostname / K8s node name) to a whitelisted Node.
// Returns an error if the identifier is not in the whitelist.
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
	// Handle "host:port" form (e.g. "10.0.0.5:9100")
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
