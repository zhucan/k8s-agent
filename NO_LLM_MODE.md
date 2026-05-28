# 非 LLM 模式使用指南

k8s-inspect 支持两种工作模式：

## 📋 模式 1: LLM 自然语言交互模式（默认）

使用 AI 理解自然语言查询，自动调用相应工具。

**优点：**
- 自然语言交互，无需记忆命令
- 智能理解用户意图
- 适合人工交互和探索性查询

**缺点：**
- 需要 ANTHROPIC_API_KEY
- 响应时间较长（需要 LLM 推理）
- 有 API 调用成本

## 🔧 模式 2: 直接工具调用模式（--no-llm）

不使用 LLM，直接调用工具，返回结构化数据。

**优点：**
- 不需要 ANTHROPIC_API_KEY
- 响应速度快
- 返回结构化 JSON 数据
- 适合脚本集成、API 调用、自动化场景
- 无 API 调用成本

**缺点：**
- 需要记忆工具名称和参数格式
- 需要手动构造 JSON 参数

---

## CLI 使用方法

### 查看帮助

```bash
./bin/cli --help
```

### LLM 模式（默认）

```bash
# 交互模式
./bin/cli --multi-cluster --cluster-config clusters.json

# 单次查询
./bin/cli --once "列出所有不健康的节点"
```

### 非 LLM 模式

#### 1. 单次工具调用

```bash
# 查看所有可用工具
./bin/cli --no-llm --kubeconfig config.yaml --tool help

# 查看特定工具的详细帮助
./bin/cli --no-llm --kubeconfig config.yaml --tool help --input 'list_nodes'
./bin/cli --no-llm --kubeconfig config.yaml --tool help --input 'list_pods'

# 列出所有集群
./bin/cli --no-llm --multi-cluster --cluster-config clusters.json --tool list_clusters

# 列出所有节点
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool list_nodes

# 列出不健康的节点
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool list_nodes --input '{"filter":"unhealthy"}'

# 列出健康的节点
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool list_nodes --input '{"filter":"healthy"}'

# 切换集群
./bin/cli --no-llm --multi-cluster --cluster-config clusters.json --tool switch_cluster --input '{"cluster":"prod"}'

# 查看节点状态
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool node_status --input '{"node":"10-3-10-101.nvidia.gpu.4080s"}'

# 标记节点为不可调度（cordon）
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool cordon_node --input '{"name":"node-name"}'

# 标记节点为可调度（uncordon）
./bin/cli --no-llm --kubeconfig kubeconfigs/job.yaml --tool uncordon_node --input '{"name":"node-name"}'
```

#### 2. 交互模式

```bash
./bin/cli --no-llm --multi-cluster --cluster-config clusters.json
```

然后输入工具命令：

```
tool> help
tool> help list_nodes
tool> list_clusters
tool> switch_cluster {"cluster":"job"}
tool> list_nodes {"filter":"unhealthy"}
tool> node_status {"node":"10-3-10-101.nvidia.gpu.4080s"}
tool> exit
```

---

## Bot（飞书机器人）使用方法

### LLM 模式（默认）

```bash
./bin/bot --multi-cluster --cluster-config clusters.json
```

用户在飞书中发送自然语言消息：
- "列出所有节点"
- "job 集群有哪些不健康的节点"
- "查看 10-3-10-101 节点的状态"

### 非 LLM 模式

```bash
./bin/bot --no-llm --multi-cluster --cluster-config clusters.json
```

用户在飞书中发送工具命令：

```
help
help list_nodes
list_clusters
switch_cluster {"cluster":"job"}
list_nodes {"filter":"unhealthy"}
node_status {"node":"10-3-10-101.nvidia.gpu.4080s"}
```

**注意事项：**

1. **help 命令：** 
   - 输入 `help` 查看所有可用工具
   - 输入 `help <tool_name>` 查看特定工具的详细参数说明
   - 示例：`help list_nodes`、`help list_pods`

2. **命令格式：** 非 LLM 模式下，用户需要按照工具命令格式发送消息：
   ```
   <tool_name> [json_input]
   ```

3. **@提及处理：** 机器人会自动过滤掉消息中的 @提及，所以以下两种方式都可以：
   ```
   @机器人 list_nodes
   list_nodes
   ```

4. **错误提示：** 如果工具名称错误，机器人会返回所有可用工具的列表，按类别分组显示。

5. **JSON 参数：** 如果工具需要参数，必须使用有效的 JSON 格式：
   ```
   list_nodes {"filter":"unhealthy"}
   switch_cluster {"cluster":"prod"}
   ```

---

## 可用工具列表

### 集群管理
- `list_clusters` - 列出所有集群
- `switch_cluster` - 切换当前集群
  - 参数: `{"cluster":"集群名称"}`
- `add_cluster_to_config` - 添加新集群

### 节点管理
- `list_nodes` - 列出节点
  - 参数: `{"filter":"all|healthy|unhealthy"}` (可选，默认 all)
- `node_status` - 查看节点详细状态
  - 参数: `{"node":"节点名称或IP"}`
- `cordon_node` - 标记节点为不可调度
  - 参数: `{"name":"节点名称"}`
- `uncordon_node` - 标记节点为可调度
  - 参数: `{"name":"节点名称"}`
- `diagnose_node` - 诊断节点问题
  - 参数: `{"node":"节点名称或IP"}`
- `find_node_in_clusters` - 在所有集群中查找节点
  - 参数: `{"node":"节点名称或IP"}`

### 资源查询
- `list_pods` - 列出 Pod
  - 参数: `{"namespace":"命名空间","field_selector":"spec.nodeName=节点名"}` (可选)
- `list_namespaces` - 列出命名空间

### 硬件信息
- `k8s_hardware_info` - 查看节点硬件信息
  - 参数: `{"node":"节点名称或IP"}`
- `k8s_cpu_info` - 查看 CPU 信息
  - 参数: `{"node":"节点名称或IP"}`
- `k8s_memory_info` - 查看内存信息
  - 参数: `{"node":"节点名称或IP"}`
- `k8s_network_info` - 查看网络信息
  - 参数: `{"node":"节点名称或IP"}`

---

## 使用场景建议

### 适合 LLM 模式的场景：
- 人工交互查询
- 探索性问题
- 复杂的多步骤操作
- 不确定具体使用哪个工具

### 适合非 LLM 模式的场景：
- 脚本自动化
- CI/CD 集成
- 监控告警集成
- API 调用
- 需要快速响应
- 不想使用 API key
- 需要精确控制工具调用

---

## 示例：监控脚本集成

```bash
#!/bin/bash

# 检查不健康节点
UNHEALTHY=$(./bin/cli --no-llm --kubeconfig config.yaml --tool list_nodes --input '{"filter":"unhealthy"}' 2>/dev/null)

# 解析 JSON 结果
UNHEALTHY_COUNT=$(echo "$UNHEALTHY" | grep "Unhealthy:" | awk '{print $4}')

if [ "$UNHEALTHY_COUNT" -gt 0 ]; then
    echo "⚠️ 发现 $UNHEALTHY_COUNT 个不健康节点"
    echo "$UNHEALTHY"
    # 发送告警...
fi
```

---

## 环境变量

- `ANTHROPIC_API_KEY` - Anthropic API 密钥（仅 LLM 模式需要）
- `KUBECONFIG` - 默认 kubeconfig 路径
- `LARK_APP_ID` - 飞书应用 ID（仅 bot 需要）
- `LARK_APP_SECRET` - 飞书应用密钥（仅 bot 需要）
