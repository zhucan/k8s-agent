# K8s-Inspect

基于 Claude AI 的 Kubernetes 集群智能运维助手，支持通过飞书机器人或命令行进行自然语言交互。

**🆕 现在支持非 LLM 模式！** 可以不使用 AI，直接调用工具，适合脚本集成和自动化场景。

## ✨ 核心特性

- 🤖 **飞书集成** - 通过飞书 WebSocket 长连接，无需公网 IP
- 🗣️ **自然语言** - 用自然语言管理集群，无需记忆命令（LLM 模式）
- 🔧 **直接工具调用** - 不使用 AI，直接调用工具，返回结构化数据（非 LLM 模式）
- 🔄 **多集群管理** - 支持管理和动态切换多个 Kubernetes 集群
- 📊 **节点监控** - 查看节点状态、资源使用、硬件信息
- 🔍 **智能诊断** - 自动分析节点故障，提供修复建议
- 🚀 **无需 SSH** - 通过 K8s 特权 Pod 执行诊断，更安全
- ⏰ **定时巡检** - 自动检测不健康节点并发送飞书告警
- 🔥 **热加载** - kubeconfigs 目录文件变化时自动加载/卸载集群

## 🎯 两种工作模式

### 📋 模式 1: LLM 自然语言交互模式（默认）
- 使用 AI 理解自然语言查询
- 适合人工交互和探索性查询
- 需要 ANTHROPIC_API_KEY

### 🔧 模式 2: 直接工具调用模式（--no-llm）
- 不使用 LLM，直接调用工具
- 返回结构化 JSON 数据
- 适合脚本集成、API 调用、自动化场景
- **不需要 ANTHROPIC_API_KEY**
- 响应速度快，无 API 调用成本

详细使用方法请查看 [NO_LLM_MODE.md](./NO_LLM_MODE.md)

## 🚀 快速开始

### 1. 环境准备

创建 `.env` 文件：

```bash
# Anthropic API 配置（仅 LLM 模式需要）
ANTHROPIC_API_KEY=sk-ant-xxx

# 飞书应用配置（仅 bot 模式需要）
LARK_APP_ID=cli_xxx
LARK_APP_SECRET=xxx

# 定时巡检告警（可选）
LARK_ALERT_CHAT_ID=oc_xxx                    # 告警群 Chat ID
LARK_ALERT_MENTION_EMAILS=user@company.com   # 要 @的人的邮箱
LARK_ALERT_CRON=0 10 * * *                   # 巡检时间
```

**注意：** 如果使用 `--no-llm` 模式，不需要配置 `ANTHROPIC_API_KEY`

### 2. 配置集群

**单集群模式：**
```bash
# 使用默认 kubeconfig
export KUBECONFIG=~/.kube/config
```

**多集群模式：**
```bash
# 准备 kubeconfig 文件（文件名即为集群名）
mkdir -p kubeconfigs
cp /path/to/test-kubeconfig kubeconfigs/test.yaml
cp /path/to/cdn-kubeconfig kubeconfigs/cdn.yaml

# clusters.json 会在启动时自动生成/同步
```

### 3. 编译和运行

```bash
# 编译
go build -o bin/bot ./cmd/bot
go build -o bin/cli ./cmd/cli

# 运行飞书 Bot（LLM 模式，多集群）
./bin/bot --multi-cluster --cluster-config clusters.json

# 运行飞书 Bot（非 LLM 模式，多集群）
./bin/bot --no-llm --multi-cluster --cluster-config clusters.json

# 运行 CLI（LLM 模式，单集群）
./bin/cli --kubeconfig ~/.kube/config

# 运行 CLI（非 LLM 模式，单集群）
./bin/cli --no-llm --kubeconfig ~/.kube/config
```

## 💬 使用示例

### LLM 模式（自然语言交互）

#### 飞书机器人

在飞书群聊中 @机器人：

```
# 多集群管理
列出所有集群
切换到 cdn 集群

# 节点查询
列出所有节点
列出所有不健康的节点
查看 master-01 节点状态

# Pod 管理
列出 default 命名空间的 pod
master-03 节点上有哪些 pod

# 故障诊断
诊断 master-01 节点
分析 10.1.1.83 节点为什么 NotReady

# 硬件信息
查看 master-01 的 CPU 信息
master-02 的内存使用情况
```

#### 命令行工具

```bash
# 交互模式
./bin/cli --multi-cluster --cluster-config clusters.json

# 单次查询
./bin/cli --once "列出所有节点"
./bin/cli --once "列出所有不健康的节点"
./bin/cli --once "查看 master-01 节点状态"
```

### 非 LLM 模式（直接工具调用）

#### 飞书机器人

在飞书群聊中 @机器人，发送工具命令：

```
# 列出所有集群
list_clusters

# 切换集群
switch_cluster {"cluster":"cdn"}

# 列出所有节点
list_nodes

# 列出不健康的节点
list_nodes {"filter":"unhealthy"}

# 查看节点状态
node_status {"node":"master-01"}

# 诊断节点
diagnose_node {"node":"10.1.1.83"}
```

#### 命令行工具

```bash
# 查看帮助
./bin/cli --help

# 单次工具调用
./bin/cli --no-llm --multi-cluster --tool list_clusters
./bin/cli --no-llm --kubeconfig config.yaml --tool list_nodes --input '{"filter":"unhealthy"}'
./bin/cli --no-llm --kubeconfig config.yaml --tool node_status --input '{"node":"master-01"}'

# 交互模式
./bin/cli --no-llm --multi-cluster --cluster-config clusters.json
tool> list_nodes {"filter":"unhealthy"}
tool> switch_cluster {"cluster":"prod"}
tool> node_status {"node":"master-01"}
```

#### 脚本集成示例

```bash
#!/bin/bash
# 监控不健康节点的脚本

UNHEALTHY=$(./bin/cli --no-llm --kubeconfig config.yaml \
  --tool list_nodes --input '{"filter":"unhealthy"}' 2>/dev/null)

UNHEALTHY_COUNT=$(echo "$UNHEALTHY" | grep "Unhealthy:" | awk '{print $4}')

if [ "$UNHEALTHY_COUNT" -gt 0 ]; then
    echo "⚠️ 发现 $UNHEALTHY_COUNT 个不健康节点"
    echo "$UNHEALTHY"
    # 发送告警...
fi
```

## 📚 文档

- [非 LLM 模式指南](NO_LLM_MODE.md) - 直接工具调用模式详细说明
- [部署指南](DEPLOY.md) - Docker、Kubernetes 部署方案
- [功能特性](FEATURES.md) - 详细功能列表
- [多集群管理](MULTI_CLUSTER.md) - 多集群配置和使用
- [自然语言指南](docs/NATURAL_LANGUAGE.md) - 自然语言交互示例
- [添加集群](docs/ADD_CLUSTER.md) - 集群管理（自动同步与热加载）

## 🛠️ 可用功能

### 基础功能
- ✅ 列出节点（含状态）
- ✅ 查看节点详细信息
- ✅ Cordon/Uncordon 节点
- ✅ 列出 Pod（支持按节点、命名空间筛选）
- ✅ 列出 Namespace

### 多集群功能
- ✅ 列出所有集群
- ✅ 动态切换集群
- ✅ 自动同步 kubeconfigs 目录
- ✅ 运行时热加载（新增/删除/修改集群无需重启）

### 告警功能
- ✅ 定时巡检不健康节点
- ✅ 飞书卡片告警（支持 @相关人员）
- ✅ 支持邮箱配置 @ 人（自动解析为 Open ID）

### 诊断功能
- ✅ 节点故障诊断（通过 K8s Pod）
- ✅ 查看硬件信息（CPU、内存、网络、磁盘）
- ✅ 分析 containerd、kubelet 状态
- ✅ 查看系统日志

## 🏗️ 项目结构

```
.
├── cmd/
│   ├── bot/              # 飞书机器人服务
│   └── cli/              # 命令行工具
├── internal/
│   ├── alert/            # 定时巡检告警
│   ├── bot/              # Bot 核心逻辑
│   ├── cluster/          # 集群管理（含自动同步和热加载）
│   ├── llm/              # LLM 集成（Claude）
│   ├── k8s/              # Kubernetes 客户端
│   ├── lark/             # 飞书消息卡片
│   ├── nodes/            # 节点注册表
│   └── tool/             # 工具函数
│       └── builtin/      # 内置工具
├── kubeconfigs/          # 集群 kubeconfig 文件（放入即自动加载）
└── clusters.json         # 集群列表配置（自动维护）
```

## 🔒 安全特性

- **无需 SSH** - 通过 K8s 原生 API 和特权 Pod 执行诊断
- **节点白名单** - 只能访问集群内已注册的节点
- **命令硬编码** - LLM 不能执行任意命令，只能调用预定义工具
- **RBAC 控制** - 支持 Kubernetes RBAC 权限管理

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

MIT License
