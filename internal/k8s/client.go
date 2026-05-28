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

// loadingRules 让 BuildConfigFromKubeconfigGetter / overrides 走标准的 kubeconfig 解析,支持
// KUBECONFIG 中的多文件(冒号/分号分隔)
func loadingRules(kubeconfig string) *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	return rules
}

// NewClient 用 kubeconfig 默认 context 构造 client(in-cluster fallback)
func NewClient(kubeconfig string) (*kubernetes.Clientset, error) {
	return NewClientForContext(kubeconfig, "")
}

// NewClientForContext 用指定 context 构造 client。kubeconfig 为空 + context 为空时退化到 in-cluster。
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

// BuildConfigForContext 构造 rest.Config（用于需要 RestConfig 的场景，如 remotecommand）
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

// ContextInfo: kubeconfig 中一个 context 的概览
type ContextInfo struct {
	Name    string
	Cluster string
	Current bool
}

// ListContexts 列出 kubeconfig 中的所有 context;current 标志标识默认 context。
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
