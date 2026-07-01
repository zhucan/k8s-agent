// Package bot provides shared startup wiring for the bot (K8s client, node whitelist,
// tool registry, LLM client, and system prompt). Used by both cmd/bot (Feishu) and cmd/cli (terminal).
package bot

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k8s-inspect/internal/cluster"
	"github.com/k8s-inspect/internal/k8s"
	"github.com/k8s-inspect/internal/llm"
	"github.com/k8s-inspect/internal/nodes"
	"github.com/k8s-inspect/internal/tool"
	"github.com/k8s-inspect/internal/tool/builtin"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Options holds startup options parsed from flags/env.
type Options struct {
	Kubeconfig    string
	Context       string // K8s context name (optional; defaults to kubeconfig's current context)
	MultiCluster  bool   // enable multi-cluster mode (used by the Feishu bot)
	ClusterConfig string // path to cluster config file (optional; for custom cluster names and aliases)
	NoLLM         bool   // disable LLM mode and use direct tool invocation
}

// Components holds the assembled runtime components.
type Components struct {
	Nodes          *nodes.Registry
	Tools          *tool.Registry
	LLM            *llm.Client
	Stop           context.CancelFunc // cancels background node refresh
	ClusterManager *cluster.Manager   // multi-cluster manager (available when MultiCluster=true)
}

// Setup runs full initialization. Calls log.Fatalf on any required failure.
// ctx is used for background node refresh; the caller owns the cancel function.
func Setup(parent context.Context, opts Options) *Components {
	// Only require API key when LLM mode is enabled
	if !opts.NoLLM && os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatalf("ANTHROPIC_API_KEY is required (or use --no-llm to disable LLM mode)")
	}

	// Multi-cluster mode
	if opts.MultiCluster {
		return setupMultiCluster(parent, opts)
	}

	// Single-cluster mode (default CLI mode)
	return setupSingleCluster(parent, opts)
}

// setupSingleCluster initializes single-cluster mode (default CLI mode).
func setupSingleCluster(parent context.Context, opts Options) *Components {
	// K8s
	kcfg := k8s.ResolveKubeconfig(opts.Kubeconfig)
	if kcfg == "" {
		log.Fatalf("could not resolve kubeconfig (pass --kubeconfig or set KUBECONFIG)")
	}

	var cs *kubernetes.Clientset
	var restConfig *rest.Config
	var err error
	if opts.Context != "" {
		cs, err = k8s.NewClientForContext(kcfg, opts.Context)
		if err != nil {
			log.Fatalf("k8s client: %v", err)
		}
		restConfig, err = k8s.BuildConfigForContext(kcfg, opts.Context)
		if err != nil {
			log.Fatalf("k8s rest config: %v", err)
		}
		log.Printf("using kubeconfig context: %s", opts.Context)
	} else {
		cs, err = k8s.NewClient(kcfg)
		if err != nil {
			log.Fatalf("k8s client: %v", err)
		}
		restConfig, err = k8s.BuildConfigForContext(kcfg, "")
		if err != nil {
			log.Fatalf("k8s rest config: %v", err)
		}
	}

	// Node whitelist
	ctx, cancel := context.WithCancel(parent)
	nodeReg := nodes.New(cs)
	if err := nodeReg.Refresh(ctx); err != nil {
		cancel()
		log.Fatalf("initial node list: %v", err)
	}
	nodeReg.StartAutoRefresh(ctx, 5*time.Minute)
	log.Printf("loaded %d nodes from cluster", len(nodeReg.List()))

	// Tool registry
	tr := tool.NewRegistry()
	tr.Register(&builtin.ListNodes{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.NodeStatus{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.CordonNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.UncordonNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.ListNodeTaints{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.TaintNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.UntaintNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.ListNodeLabels{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.LabelNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.UnlabelNode{CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.ListPods{CS: cs})
	tr.Register(&builtin.ListNamespaces{CS: cs})
	// K8s-based hardware tools (no SSH required)
	tr.Register(&builtin.K8sHardwareInfo{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.K8sCPUInfo{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.K8sNetworkInfo{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.K8sMemoryInfo{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	// Node diagnostics
	tr.Register(&builtin.DiagnoseNode{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.CollectLogs{CS: cs, RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.AnalyzePodLogs{CS: cs})
	// NodePool CRD
	tr.Register(&builtin.ListNodePools{RestConfig: restConfig})
	tr.Register(&builtin.GetNodePool{RestConfig: restConfig, Nodes: nodeReg})
	tr.Register(&builtin.AddNodeToPool{RestConfig: restConfig, CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.RemoveNodeFromPool{RestConfig: restConfig, CS: cs, Nodes: nodeReg})
	tr.Register(&builtin.MoveNodeBetweenPools{RestConfig: restConfig, CS: cs, Nodes: nodeReg})

	// LLM (only initialized when LLM mode is enabled)
	var llmClient *llm.Client
	if !opts.NoLLM {
		llmClient = llm.New("", buildSystemPrompt(nodeReg, tr, nil), tr)
	}

	return &Components{
		Nodes: nodeReg,
		Tools: tr,
		LLM:   llmClient,
		Stop:  cancel,
	}
}

// setupMultiCluster initializes multi-cluster mode (used by the Feishu bot).
func setupMultiCluster(parent context.Context, opts Options) *Components {
	var clusterConfig *cluster.Config
	var kubeconfigsDir string

	// Prefer the cluster-config file if provided
	if opts.ClusterConfig != "" {
		// Sync kubeconfigs directory with clusters.json
		kubeconfigsDir = filepath.Join(filepath.Dir(opts.ClusterConfig), "kubeconfigs")
		if info, err := os.Stat(kubeconfigsDir); err == nil && info.IsDir() {
			synced, _, err := cluster.SyncConfigWithDir(opts.ClusterConfig, kubeconfigsDir)
			if err != nil {
				log.Printf("warn: sync kubeconfigs dir failed: %v", err)
			}
			if synced != nil {
				clusterConfig = synced
			}
		}

		// If sync returned no valid config, load normally
		if clusterConfig == nil {
			var err error
			clusterConfig, err = cluster.LoadConfig(opts.ClusterConfig)
			if err != nil {
				log.Fatalf("failed to load cluster config from %s: %v", opts.ClusterConfig, err)
			}
		}
		log.Printf("loaded cluster config from %s", opts.ClusterConfig)
	} else {
		// No cluster-config: read context list from the default kubeconfig
		kcfg := k8s.ResolveKubeconfig(opts.Kubeconfig)
		if kcfg == "" {
			log.Fatalf("could not resolve kubeconfig (pass --kubeconfig, set KUBECONFIG, or use --cluster-config)")
		}

		contexts, err := k8s.ListContexts(kcfg)
		if err != nil {
			log.Fatalf("list contexts: %v", err)
		}
		if len(contexts) == 0 {
			log.Fatalf("no contexts found in kubeconfig")
		}

		contextNames := make([]string, len(contexts))
		for i, ctx := range contexts {
			contextNames[i] = ctx.Name
		}
		clusterConfig = cluster.DefaultConfig(contextNames)
		log.Printf("using auto-generated cluster config from kubeconfig")
	}

	// Create cluster manager
	ctx, cancel := context.WithCancel(parent)
	mgr := cluster.NewManagerWithConfig(clusterConfig)

	// Load all clusters from config
	loadedCount := 0
	for _, clusterCfg := range clusterConfig.Clusters {
		kubeconfigPath := clusterCfg.Kubeconfig
		if kubeconfigPath == "" {
			log.Printf("warn: skip cluster %q: no kubeconfig path specified", clusterCfg.Name)
			continue
		}

		if err := mgr.AddCluster(ctx, clusterCfg.Name, kubeconfigPath, clusterCfg.Context); err != nil {
			log.Printf("warn: skip cluster %q (context: %s): %v", clusterCfg.Name, clusterCfg.Context, err)
			continue
		}
		loadedCount++
	}

	if loadedCount == 0 {
		cancel()
		log.Fatalf("no valid clusters loaded")
	}

	// Get current cluster
	currentCluster, err := mgr.Current()
	if err != nil {
		cancel()
		log.Fatalf("get current cluster: %v", err)
	}

	log.Printf("multi-cluster mode: loaded %d clusters, current=%s", loadedCount, mgr.CurrentName())

	// Start kubeconfigs directory watcher for hot-reload
	if kubeconfigsDir != "" {
		w := cluster.NewWatcher(ctx, kubeconfigsDir, opts.ClusterConfig, mgr)
		if err := w.Start(); err != nil {
			log.Printf("warn: failed to start kubeconfigs watcher: %v", err)
		}
	}

	// Tool registry — use dynamic tool wrappers
	tr := tool.NewRegistry()
	tr.Register(&builtin.ListClusters{Manager: mgr})
	tr.Register(&builtin.SwitchCluster{Manager: mgr})
	tr.Register(&builtin.FindNodeInClusters{Manager: mgr})
	tr.Register(&builtin.AddClusterToConfig{
		ConfigFile: opts.ClusterConfig,
		Manager:    mgr,
	})
	tr.Register(newDynamicListNodes(mgr))
	tr.Register(newDynamicNodeStatus(mgr))
	tr.Register(newDynamicCordonNode(mgr))
	tr.Register(newDynamicUncordonNode(mgr))
	tr.Register(newDynamicListNodeTaints(mgr))
	tr.Register(newDynamicTaintNode(mgr))
	tr.Register(newDynamicUntaintNode(mgr))
	tr.Register(newDynamicListNodeLabels(mgr))
	tr.Register(newDynamicLabelNode(mgr))
	tr.Register(newDynamicUnlabelNode(mgr))
	tr.Register(newDynamicListPods(mgr))
	tr.Register(newDynamicListNamespaces(mgr))
	// K8s-based hardware tools (no SSH required)
	tr.Register(newDynamicK8sHardwareInfo(mgr))
	tr.Register(newDynamicK8sCPUInfo(mgr))
	tr.Register(newDynamicK8sNetworkInfo(mgr))
	tr.Register(newDynamicK8sMemoryInfo(mgr))
	// Node diagnostics
	tr.Register(newDynamicDiagnoseNode(mgr))
	tr.Register(newDynamicCollectLogs(mgr))
	// Pod log analysis
	tr.Register(newDynamicAnalyzePodLogs(mgr))
	// GPU inspection
	tr.Register(&builtin.GPUInspect{Manager: mgr})
	// NodePool CRD
	tr.Register(newDynamicListNodePools(mgr))
	tr.Register(newDynamicGetNodePool(mgr))
	tr.Register(newDynamicAddNodeToPool(mgr))
	tr.Register(newDynamicRemoveNodeFromPool(mgr))
	tr.Register(newDynamicMoveNodeBetweenPools(mgr))

	// LLM (only initialized when LLM mode is enabled)
	var llmClient *llm.Client
	if !opts.NoLLM {
		llmClient = llm.New("", "", tr)
		// Set dynamic system prompt generator
		llmClient.SetSystemGenerator(func() string {
			currentCluster, err := mgr.Current()
			if err != nil {
				log.Printf("warn: failed to get current cluster for system prompt: %v", err)
				return ""
			}
			return buildSystemPrompt(currentCluster.Nodes, tr, mgr)
		})
	}

	return &Components{
		Nodes:          currentCluster.Nodes,
		Tools:          tr,
		LLM:            llmClient,
		Stop:           cancel,
		ClusterManager: mgr,
	}
}

// LoadDotEnv is a minimal .env loader; missing file is silently ignored. Call before flag.Parse.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warn: open %s: %v", path, err)
		}
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		_ = os.Setenv(key, val)
	}
	log.Printf("loaded env from %s", path)
}

// EnvOr returns the environment variable value, or def if unset.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// MustEnv returns the environment variable value, or calls log.Fatalf if unset.
func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

// buildSystemPrompt generates the system prompt for Claude.
func buildSystemPrompt(reg *nodes.Registry, tr *tool.Registry, mgr *cluster.Manager) string {
	var b strings.Builder
	b.WriteString(`你是一个 Kubernetes 集群运维助手机器人。

🚨 **关键规则 - 必须遵守**：
当用户要求查看**特定节点**上的 Pod 时（包含节点名称的查询，如"列出 master-03 节点上的 pod"、"master-01 的 pod"、"node-1 上有哪些 pod"），
你**必须**在调用 list_pods 工具时传入 field_selector 参数：
  field_selector="spec.nodeName=<节点名>"
例如：list_pods(namespace="all", field_selector="spec.nodeName=master-03")
**禁止**查询所有 Pod 后再手动筛选。

## 你的能力
你可以通过一组只读 tool 查看集群节点状态。所有 tool 调用都受白名单限制 —
只能访问下面列出的集群节点,且每个 tool 的命令是预定义的(用户不能让你执行任意命令)。

`)

	// 多集群模式提示
	if mgr != nil {
		clusters := mgr.List()
		b.WriteString("## 多集群模式\n")
		b.WriteString(fmt.Sprintf("当前管理 %d 个集群:\n", len(clusters)))
		for _, c := range clusters {
			displayName := c.DisplayName
			if displayName == "" {
				displayName = c.Name
			}
			b.WriteString(fmt.Sprintf("- %s (context: %s)\n", displayName, c.Name))
		}
		b.WriteString(fmt.Sprintf("\n当前集群: **%s**\n\n", mgr.CurrentName()))
		b.WriteString("🚨 **重要规则 - 节点查找与集群切换**：\n")
		b.WriteString("1. **默认在当前集群操作**：除非用户明确要求切换集群，否则始终在当前集群操作\n")
		b.WriteString("2. **节点不存在时的自动切换流程**：\n")
		b.WriteString("   - 如果在当前集群找不到节点，使用 find_node_in_clusters 工具搜索其他集群\n")
		b.WriteString("   - 如果返回 'FOUND_IN_CLUSTER:<集群名>'，立即使用 switch_cluster 切换到该集群\n")
		b.WriteString("   - 切换后告诉用户：'节点在 <集群显示名称> 集群，已自动切换'\n")
		b.WriteString("   - 然后继续执行用户的原始请求\n")
		b.WriteString("3. **集群名称显示**：向用户显示集群名称时，使用上面列表中的集群显示名称（不是 context 名称）\n")
		b.WriteString("4. **不要询问用户**：找到节点后直接切换，不需要询问用户是否切换\n")
		b.WriteString("5. **查询所有集群的不健康/健康节点**：当用户要求查看「所有集群」的节点状态时，必须按以下流程操作：\n")
		b.WriteString("   - 先调用 list_clusters 获取所有集群名称\n")
		b.WriteString("   - 对每个集群依次执行：switch_cluster(cluster=<名称>) → list_nodes(filter=\"unhealthy\" 或 \"healthy\")\n")
		b.WriteString("   - 汇总所有集群的结果后一次性回复用户，格式为：每个集群单独一段，标注集群名称\n")
		b.WriteString("   - 查询完毕后，切回原来的集群（如果用户没有要求保持切换）\n\n")
		b.WriteString("6. **巡检指定集群**：当用户说「巡检 XX 集群」、「检查 XX 集群」、「XX 集群的情况」等时，必须按以下流程：\n")
		b.WriteString("   - switch_cluster 切换到目标集群\n")
		b.WriteString("   - list_nodes(filter=\"unhealthy\") 检查不健康节点\n")
		b.WriteString("   - gpu_inspect 检查 GPU 卡数是否异常\n")
		b.WriteString("   - 汇总两项结果一起回复\n")
		b.WriteString("   - 例外：用户明确说「只看 GPU」或「只巡检节点」时，只执行对应的单项检查\n\n")
	}

	b.WriteString(`## 工作原则
1. 用户给的节点标识可能是 IP、hostname 或 K8s node name,都先调用对应 tool 让它解析。
2. 如果用户问的内容涉及多项检查(比如"看下磁盘和内存"),按顺序多次调用对应 tool。
3. 拿到 tool 结果后用简洁中文总结给用户,**不要**原样复述命令的全部输出,只挑关键信息(使用率、异常项)。
4. 节点不在白名单时直接告诉用户,不要尝试别的方法。
5. 如果用户没指定节点,先调 list_nodes 让用户选。
6. **硬件信息查询**: 使用 k8s_hardware_info、k8s_cpu_info、k8s_network_info、k8s_memory_info 等工具查询节点硬件信息(通过 K8s 特权 Pod 执行)。
7. **节点故障诊断**: 当节点状态为 NotReady 或有问题时,使用 diagnose_node 工具收集 containerd、kubelet 状态和系统日志,然后分析根本原因并给出修复建议。
8. **不健康节点**: 当用户询问"不健康的节点"、"有问题的节点"、"异常节点"时，使用 list_nodes(filter="unhealthy") 查询。若用户要求查询**所有集群**，参见多集群规则第 5 条。
9. **查看集群节点概况时的标准流程**：当用户要求"查看节点信息"、"节点状态"、"集群状态"等综合查询时，必须按以下流程：
   - 先调用 list_nodes(filter="unhealthy") 获取不健康节点
   - 再调用 list_nodes(filter="healthy") 获取健康节点数量
   - 禁止用 filter="all" 后自行从全量数据里筛选，工具返回什么就汇报什么，不得自行判断哪些节点健康/不健康

## 🚨 重要：节点状态展示规则
节点有两个独立状态：ready（是否正常运行）和 cordoned（是否被人为禁止调度）。

展示节点状态时，**必须**按以下规则：
- ready=true, cordoned=false → ✅ 正常
- ready=true, cordoned=true  → ⚠️ 正常运行中，已 cordon（人为禁止调度，节点本身无故障）
- ready=false, cordoned=false → ❌ NotReady（节点故障）
- ready=false, cordoned=true  → ❌ NotReady 且已 cordon

**禁止**把 cordoned=true 的节点描述为"故障"、"异常"、"有问题"，cordon 是人为操作，节点本身可能完全正常。
**禁止**根据 ready 字段推断 cordoned 状态，两者完全独立。

- 当用户询问"不健康的节点"、"有问题的节点"、"异常节点"时，调用 list_nodes(filter="unhealthy")
- 当用户询问"健康的节点"、"正常的节点"时，调用 list_nodes(filter="healthy")
- 默认情况下使用 list_nodes(filter="all") 或不传 filter 参数
`)
	b.WriteString("- 节点是否被 cordon，**只能**根据 list_nodes 返回的 `cordoned` 字段判断（true = 被 cordon）\n")
	b.WriteString("- `ready: false` 表示节点 NotReady（节点故障），**不等于** 被 cordon\n")
	b.WriteString("- cordon 是人为操作，**不代表节点故障**\n\n")
	b.WriteString(`## 🚨 重要：节点污点（Taint）管理
用户涉及"污点 / taint / 打 taint / 加污点 / 去掉污点 / 移除 taint / 查看污点"等说法时，使用以下三个工具，**不要**和 cordon / uncordon / label 混淆：

- 查看节点污点：list_node_taints(node="<节点>")
- 添加/更新污点：taint_node(node="<节点>", key="<key>", value="<value>", effect="<NoSchedule|PreferNoSchedule|NoExecute>")
  - value 可选（缺省表示空值）；同 key+effect 已存在时会覆盖 value
- 移除污点：untaint_node(node="<节点>", key="<key>", effect="<可选>")
  - 不传 effect 表示删除该 key 下所有 effect 的污点

🚨 **权限与安全边界（必须遵守）**：
- **禁止**对 master / control-plane 节点执行 taint_node / untaint_node / label_node / unlabel_node（例如 node name 含 "master"、"control-plane"，或 list_nodes 里 roles 含 master/control-plane 的节点）。用户要求时直接拒绝并说明「master 节点受保护」，**不要**调用工具。list_node_taints / list_node_labels 查看则允许。
- 只有获授权的飞书人员才能改污点和 label。工具会自行检查用户身份，无权限时会返回 "permission denied"，请把该错误原样告诉用户，让其联系管理员加白名单，**不要**再重试。
- 每次改污点/label 前后建议调对应的 list_ 工具展示当前状态，便于用户核对。
- **务必确认集群上下文**：所有 taint / label 相关工具均只在「当前集群」执行。用户如果提到"XX 集群的 YY 节点"，必须先 switch_cluster 再操作；如果只给节点 IP 而未指定集群，可用 find_node_in_clusters 定位。工具返回体里带 cluster=<名称> 字段（或 JSON 里的 cluster 字段），请把它一并展示给用户，避免张冠李戴。

🚨 **taint 与 label 的联动 — 用 NodePool 工具，别自己改 label/taint**：
本集群跑着 drscaler 的 NodePool 控制器。它按 NodePool CR 的 spec.configuration.fixedNodes 列表把节点的 label（deeproute.cn/user-type、drscaler.deeproute.ai/nodepool 等）和 taint（cloud.deeproute.cn/team 等）**反查回目标值**。也就是说：
- **只改 label/taint 是白改**：控制器几秒内就会用它认定的目标值覆盖回来。taint_node / label_node 工具已经在响应里做了验证，遇到这种情况会返回 "appeared to succeed but ... is NOT present ... likely reverted it"，请原样告诉用户，**不要**假装成功。
- **改归属的正确姿势是改 NodePool**：用户说"把 XX 节点从 simulation 池挪到 mlp 池"、"改成 XX team"、"改归属"、"迁池子"、"从 A 池挪到 B 池"等，一律用 NodePool 工具，不要直接调 label_node / taint_node：
  1. 先 list_nodepools 看当前有哪些池 → 或者 get_nodepool(name=<池>) 确认节点是不是在里面
  2. 用 move_node_between_pools(node=<节点>, from_pool=<源池>, to_pool=<目标池>) 一步完成
  3. 完成后建议再调 list_node_labels + list_node_taints 复核控制器有没有把状态调过来（一般几秒内）
- 只想加/去掉某一个 pool 归属时，用 add_node_to_pool / remove_node_from_pool。
- **单独调 taint_node / label_node 只在少数场景下有意义**：临时打业务标签、加与 pool 无关的自定义 label/taint。改 pool 相关的键（user-type、nodepool、team 等）请一律走 NodePool。

参数识别与追问规则：
1. 节点标识（name/IP/hostname）识别方式与其它工具一致，交给工具解析。
2. **effect 是必填**（taint_node）。用户没说 effect 时，先追问一次，默认建议 NoSchedule；不要擅自假设。
3. 用户一次列出多组 key/value/effect 时，逐条调用 taint_node，每次只处理一组。
4. 用户可能只给「key=value:effect」这种 kubectl 风格字符串，请解析为对应参数。
5. 操作前后如需确认，先/后调用 list_node_taints 展示节点当前污点。
6. 涉及去污点但没指定 effect 时，可以调 list_node_taints 让用户/自己确认后再操作。

示例：
- "给 node-a 打 nvidia.com/gpu=4090:NoSchedule 污点" → taint_node(node="node-a", key="nvidia.com/gpu", value="4090", effect="NoSchedule")
- "在 node-a 加 team=simulation 污点，不允许调度" → taint_node(node="node-a", key="cloud.deeproute.cn/team", value="simulation", effect="NoSchedule")（若 key 不全，先追问）
- "去掉 node-a 的 nvidia.com/gpu 污点" → untaint_node(node="node-a", key="nvidia.com/gpu")
- "看下 node-a 有哪些污点" → list_node_taints(node="node-a")

## 🚨 重要：查询特定节点上的 Pod
当用户要求查看**特定节点**上的 Pod 时（例如："列出 master-03 节点上的 pod"、"master-01 的 pod"、"node-1 上有哪些 pod"），
你**必须**使用 list_pods 工具的 field_selector 参数来过滤，**不要**查询所有 Pod 后再手动筛选。

正确用法：
- "列出 master-03 上的 pod" → list_pods(namespace="all", field_selector="spec.nodeName=master-03")
- "查看 node-1 的 pod" → list_pods(namespace="all", field_selector="spec.nodeName=node-1")
- "master-02 节点的 pod" → list_pods(namespace="all", field_selector="spec.nodeName=master-02")

注意：field_selector 使用**精确匹配**，master-03 只会匹配 master-03，不会匹配 master-02 或 master-031。

## 可用节点(白名单)
`)
	for _, n := range reg.List() {
		fmt.Fprintf(&b, "- %s (IP: %s", n.Name, n.InternalIP)
		if n.Hostname != "" {
			fmt.Fprintf(&b, ", hostname: %s", n.Hostname)
		}
		if len(n.Roles) > 0 {
			fmt.Fprintf(&b, ", roles: %s", strings.Join(n.Roles, ","))
		}
		b.WriteString(")\n")
	}

	b.WriteString("\n## 可用 tool\n")
	for _, t := range tr.List() {
		fmt.Fprintf(&b, "- `%s`: %s\n", t.Name(), t.Description())
	}

	b.WriteString("\n## 回复格式\n")
	b.WriteString("- 用中文,简洁、聚焦关键数字和异常\n")
	b.WriteString("- 适当用 emoji 标记状态:✅ 正常 ⚠️ 警告 ❌ 异常\n")
	b.WriteString("- 不要长篇大论,3-5 行能说清楚就别更多\n")
	b.WriteString("- **禁止使用 markdown 格式**（如 **粗体**、`代码`、## 标题等），飞书文本消息不支持 markdown，会原样显示\n")
	b.WriteString("- 使用纯文本 + emoji + 空行分段来组织内容，保持清晰易读\n")
	b.WriteString("- **节点展示格式**：展示节点时，优先显示 internal_ip，K8s node name（name 字段）仅在与 IP 明显不同时作为补充说明，格式为「IP (node: node名)」，不要把 node name 当主标识放在前面\n")

	return b.String()
}

// ListContexts lists all contexts in the kubeconfig (exported for CLI use).
func ListContexts(kubeconfig string) ([]k8s.ContextInfo, error) {
	return k8s.ListContexts(kubeconfig)
}
