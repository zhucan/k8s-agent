package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func ResolveKubeconfig(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}

// loadingRules returns ClientConfigLoadingRules that support colon/semicolon-separated KUBECONFIG paths.
func loadingRules(kubeconfig string) *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	return rules
}

// NewClient builds a client using the default context in the kubeconfig (falls back to in-cluster config).
func NewClient(kubeconfig string) (*kubernetes.Clientset, error) {
	return NewClientForContext(kubeconfig, "")
}

// NewClientForContext builds a client for the specified context. Falls back to in-cluster config when both are empty.
func NewClientForContext(kubeconfig, context string) (*kubernetes.Clientset, error) {
	cfg, err := BuildConfigForContext(kubeconfig, context)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new kube client: %w", err)
	}
	return cs, nil
}

// BuildConfigForContext builds a rest.Config for the specified context (needed for remotecommand etc.).
func BuildConfigForContext(kubeconfig, context string) (*rest.Config, error) {
	var (
		cfg *rest.Config
		err error
	)
	if kubeconfig != "" || context != "" {
		overrides := &clientcmd.ConfigOverrides{}
		if context != "" {
			overrides.CurrentContext = context
		}
		cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules(kubeconfig), overrides)
		cfg, err = cc.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig (context=%q): %w", context, err)
		}
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
	}
	return cfg, nil
}

// ContextInfo is a summary of a single context entry in a kubeconfig file.
type ContextInfo struct {
	Name    string
	Cluster string
	Current bool
}

// ListContexts lists all contexts in the kubeconfig; the Current flag marks the default context.
func ListContexts(kubeconfig string) ([]ContextInfo, error) {
	raw, err := loadingRules(kubeconfig).Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	out := make([]ContextInfo, 0, len(raw.Contexts))
	for name, ctx := range raw.Contexts {
		out = append(out, ContextInfo{
			Name:    name,
			Cluster: ctx.Cluster,
			Current: name == raw.CurrentContext,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
