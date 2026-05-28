# 多集群管理指南

K8s-Inspect 支持管理和切换多个 Kubernetes 集群，提供灵活的多集群运维能力。

## 📋 功能概述

- ✅ **CLI 模式** - 通过 `--context` 参数指定集群
- ✅ **Bot 模式** - 运行时动态切换集群
- ✅ **智能匹配** - 支持集群名称、context 名称、模糊匹配
- ✅ **自动同步** - 启动时扫描 kubeconfigs 目录，自动更新 clusters.json
- ✅ **热加载** - 运行中新增/删除/修改 kubeconfig 文件，自动生效无需重启
- ✅ **跨集群查找** - 自动在多个集群中查找节点

---

## 🚀 快速开始

### 方式 1：CLI 模式 - Context 切换

适用于命令行工具和脚本自动化。

#### 查看可用的 context

```bash
# 使用 kubectl
kubectl config get-contexts

# 或使用 k8s-inspect CLI
./bin/cli --list-contexts
```

输出示例：
```
Available contexts:
* test-cluster (cluster: test-k8s)
  cdn-cluster (cluster: cdn-k8s)
  jobss-cluster (cluster: jobss-k8s)
```

#### 使用指定 context

```bash
# 方式 1: 通过 --context 参数
./bin/cli --context test-cluster -once "列出节点"

# 方式 2: 切换 kubectl 默认 context
kubectl config use-context cdn-cluster
./bin/cli -once "列出节点"
```

#### 使用场景

**开发调试：**
```bash
# 快速切换不同环境
./bin/cli --context dev-cluster -once "列出节点"
./bin/cli --context prod-cluster -once "查看所有 namespace"
```

**脚本自动化：**
```bash
#!/bin/bash
for ctx in test-cluster cdn-cluster jobss-cluster; do
    echo "=== Checking $ctx ==="
    ./bin/cli --context $ctx -once "列出节点"
    echo ""
done
```

---

### 方式 2：Bot 模式 - 运行时动态切换

适用于飞书机器人，支持运行时动态切换集群。

#### 启动多集群模式

```bash
# 自动加载 kubeconfig 中的所有 context
./bin/bot --multi-cluster

# 或使用自定义配置文件
./bin/bot --multi-cluster --cluster-config clusters.json
```

启动日志示例：
```
multi-cluster mode: loaded 3 clusters, current=test
k8s-bot initialized (nodes=8)
🚀 Starting WebSocket long connection to Feishu...
```

#### 在飞书中使用

**1. 查看所有集群**

```
列出所有集群
```

输出：
```
🌐 当前共 3 个集群：

✅ test (当前) - 8 个节点
• cdn - 12 个节点
• jobss - 6 个节点
```

**2. 切换集群**

```
切换到 cdn 集群
```

输出：
```
✅ 已切换到集群: cdn (12 nodes)
```

**3. 自动跨集群查找节点**

当节点不在当前集群时，自动查找并切换：

```
用户: 查看 node-xyz 节点状态

Bot: 节点在 jobss 集群，已自动切换
     
     📊 节点状态：
     • 名称: node-xyz
     • IP: 10.1.1.100
     • 状态: ✅ Ready
```

---

## ⚙️ 配置文件

### clusters.json 格式

```json
{
  "clusters": [
    {
      "name": "test",
      "context": "context-cluster1",
      "kubeconfig": "kubeconfigs/test.yaml"
    },
    {
      "name": "cdn",
      "context": "kubernetes-admin@kubernetes",
      "kubeconfig": "kubeconfigs/cdn.yaml"
    },
    {
      "name": "jobss",
      "context": "kubernetes-admin@cluster.local",
      "kubeconfig": "kubeconfigs/jobss.yaml"
    }
  ]
}
```

### 字段说明

| 字段 | 必需 | 说明 | 示例 |
|------|------|------|------|
| `name` | ✅ | 集群显示名称 | `"test"`, `"生产环境"` |
| `context` | ✅ | kubeconfig 中的 context 名称 | `"kubernetes-admin@kubernetes"` |
| `kubeconfig` | ✅ | kubeconfig 文件路径（相对或绝对） | `"kubeconfigs/test.yaml"` |
| `aliases` | ❌ | 集群别名（用于模糊匹配） | `["prod", "生产"]` |

### 目录结构

```
.
├── clusters.json              # 集群配置文件
├── kubeconfigs/              # kubeconfig 文件目录
│   ├── test.yaml
│   ├── cdn.yaml
│   └── jobss.yaml
└── bin/
    ├── bot
    └── cli
```

---

## 🔧 添加集群

### 方式 1：放入 kubeconfig 文件（推荐）

只需将 kubeconfig 文件放入 `kubeconfigs/` 目录，Bot 会自动识别：

```bash
# 复制 kubeconfig 到目录（文件名即为集群名）
cp /path/to/new-kubeconfig kubeconfigs/new-cluster.yaml
```

- **启动时**：自动扫描目录，与 `clusters.json` 同步
- **运行时**：文件变化自动热加载，无需重启

### 方式 2：飞书中添加

在飞书对话中发送 kubeconfig 文件给 Bot，Bot 会自动保存并加载。

### 方式 3：手动编辑配置

1. 复制 kubeconfig 文件到 `kubeconfigs/` 目录
2. `clusters.json` 会在下次启动时自动更新

### 删除集群

直接删除 `kubeconfigs/` 目录中对应的文件即可：

```bash
rm kubeconfigs/old-cluster.yaml
```

Bot 运行中会自动检测并卸载该集群。

---

## 🎯 使用场景

### 场景 1：跨环境问题排查

```
用户: 生产环境的 API 服务响应慢

Bot: [在当前集群检查]
     ⚠️ 发现 api-server 节点 CPU 使用率 85%

用户: 测试环境有这个问题吗？

Bot: [自动切换到测试环境]
     ✅ 测试环境 CPU 使用率正常，在 30% 左右
```

### 场景 2：多集群巡检

```bash
#!/bin/bash
# 巡检所有集群的节点状态
for cluster in test cdn jobss; do
    echo "=== $cluster 集群 ==="
    ./bin/cli --multi-cluster --cluster-config clusters.json \
        -once "切换到 $cluster 然后列出所有节点"
done
```

### 场景 3：自动节点查找

```
用户: 查看 10.1.1.100 节点状态

Bot: [在当前集群未找到]
     [自动搜索其他集群]
     节点在 jobss 集群，已自动切换
     
     📊 节点状态：Ready
```

---

## 🔍 技术实现

### 单集群模式

```
CLI → bot.Setup(context="xxx") → K8s Client → Tools
```

- 使用 `k8s.NewClient()` 或 `k8s.NewClientForContext()` 创建单个客户端
- 所有工具直接操作该客户端
- 适用于 CLI 模式和单集群 Bot

### 多集群模式

```
Bot → bot.Setup(MultiCluster=true) → ClusterManager
                                      ├─ Cluster 1 (test)
                                      │   ├─ K8s Client
                                      │   ├─ Nodes Registry
                                      │   └─ Auto Refresh
                                      ├─ Cluster 2 (cdn)
                                      └─ Cluster 3 (jobss)

Tools → Dynamic Wrapper → ClusterManager.Current() → 当前集群
```

- 使用 `cluster.Manager` 管理多个集群实例
- 每个集群有独立的 K8s clientset 和节点注册表
- 所有工具通过动态包装器从 Manager 获取当前集群
- 支持运行时切换，无需重启

### 动态工具包装器

```go
type dynamicListNodes struct {
    mgr *cluster.Manager
}

func (t *dynamicListNodes) Execute(ctx context.Context, input map[string]any) (string, error) {
    c, err := t.mgr.Current()  // 获取当前集群
    if err != nil {
        return "", err
    }
    tool := &builtin.ListNodes{CS: c.CS, Nodes: c.Nodes}
    return tool.Execute(ctx, input)
}
```

---

## 📊 可用工具

### 多集群专用工具

1. **list_clusters** - 列出所有集群
   - 显示集群名称、节点数、当前状态
   
2. **switch_cluster** - 切换到指定集群
   - 支持精确匹配、模糊匹配
   - 支持集群名称和 context 名称
   
3. **find_node_in_clusters** - 跨集群查找节点
   - 在所有集群中搜索指定节点
   - 返回节点所在集群

### 通用工具（所有模式）

所有基础工具在多集群模式下都可用，操作当前选中的集群：
- list_nodes, node_status, cordon_node, uncordon_node
- list_pods, list_namespaces
- diagnose_node, k8s_hardware_info, k8s_cpu_info, etc.

---

## ⚠️ 注意事项

### CLI 模式

**优点：**
- ✅ 每次启动只连接一个集群，安全性高
- ✅ 不会修改 kubeconfig 文件
- ✅ 适合脚本自动化和一次性查询

**限制：**
- ❌ 不支持运行时切换集群
- ❌ 每次切换需要重新启动

### Bot 模式

**优点：**
- ✅ 支持运行时动态切换
- ✅ 自动跨集群查找节点
- ✅ 适合交互式运维

**注意：**
- ⚠️ 所有用户共享同一个 bot 实例
- ⚠️ 切换集群会影响所有用户的后续操作
- ⚠️ 建议团队内协调使用，或为不同集群部署独立 bot

### 资源消耗

- 每个集群都会启动后台节点刷新任务（每 5 分钟）
- 集群数量过多时注意内存占用
- 建议根据实际需求加载必要的集群

---

## 🛠️ 故障排查

### 问题 1：context 不存在

**错误：**
```
FATAL: k8s client: context "xxx" does not exist
```

**解决：**
```bash
# 查看可用的 context
kubectl config get-contexts
./bin/cli --list-contexts

# 使用正确的 context 名称
./bin/cli --context correct-context-name -once "列出节点"
```

### 问题 2：切换集群失败

**错误：**
```
❌ cluster "xxx" not found
```

**解决：**
```
# 先查看可用集群
列出所有集群

# 使用精确的集群名称
切换到 test
```

### 问题 3：节点列表为空

**错误：** 集群添加成功但节点数为 0

**原因：** kubeconfig 权限不足或集群不可达

**解决：**
```bash
# 测试 kubeconfig 是否有效
kubectl --kubeconfig=kubeconfigs/test.yaml get nodes

# 检查 RBAC 权限
kubectl --kubeconfig=kubeconfigs/test.yaml auth can-i list nodes
```

### 问题 4：权限不足

**错误：**
```
WARN: skip context "xxx": nodes is forbidden
```

**原因：** kubeconfig 中的用户没有 list nodes 权限

**解决：**
1. 为用户授予 nodes 资源的 get/list 权限
2. 或使用具有足够权限的 ServiceAccount

---

## 💡 最佳实践

### 1. 命名规范

使用清晰的集群名称：
```json
{
  "name": "生产环境-北京",
  "name": "测试环境-上海",
  "name": "开发环境-本地"
}
```

### 2. 配置备份

定期备份配置文件：
```bash
# 备份集群配置
cp clusters.json clusters.json.bak
cp -r kubeconfigs kubeconfigs.bak
```

### 3. 权限最小化

为 bot 创建只读的 ServiceAccount：
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: k8s-inspect-readonly
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8s-inspect-readonly
rules:
- apiGroups: [""]
  resources: ["nodes", "pods", "namespaces"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8s-inspect-readonly
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: k8s-inspect-readonly
subjects:
- kind: ServiceAccount
  name: k8s-inspect-readonly
  namespace: default
```

### 4. 使用别名简化命令

```bash
# 添加到 ~/.bashrc 或 ~/.zshrc
alias k8s-test='./bin/cli --context test-cluster'
alias k8s-cdn='./bin/cli --context cdn-cluster'
alias k8s-jobss='./bin/cli --context jobss-cluster'

# 使用
k8s-test -once "列出节点"
k8s-cdn -once "查看所有 namespace"
```

---

## 📚 相关文档

- [部署指南](DEPLOY.md) - 部署配置
- [功能特性](FEATURES.md) - 详细功能列表
- [添加集群](docs/ADD_CLUSTER.md) - 命令行添加集群
- [自然语言指南](docs/NATURAL_LANGUAGE.md) - 自然语言交互
