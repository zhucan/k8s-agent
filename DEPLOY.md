# K8s-Inspect 部署指南

## 概述

K8s-Inspect 支持两种运行模式：
1. **飞书机器人** - 通过飞书 WebSocket 长连接接收和回复消息（推荐）
2. **CLI 工具** - 命令行交互式工具

---

## 快速开始

### 1. 环境准备

#### 1.1 配置环境变量

复制示例配置文件并修改：

```bash
cp .env.example .env
```

编辑 `.env` 文件：

```bash
# Anthropic API 配置（必填）
ANTHROPIC_API_KEY=sk-ant-xxx

# 可选：使用代理或兼容服务
# ANTHROPIC_BASE_URL=https://your-proxy.com

# 飞书应用配置（仅 bot 模式需要）
LARK_APP_ID=cli_xxx
LARK_APP_SECRET=xxx
```

#### 1.2 配置 Kubernetes 集群

**单集群模式：**

使用默认的 `~/.kube/config` 或通过 `--kubeconfig` 参数指定。

**多集群模式（推荐）：**

创建 `clusters.json` 配置文件：

```bash
cp clusters.example.json clusters.json
```

编辑 `clusters.json`：

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

准备 kubeconfig 文件：

```bash
mkdir -p kubeconfigs
cp /path/to/test-kubeconfig kubeconfigs/test.yaml
cp /path/to/cdn-kubeconfig kubeconfigs/cdn.yaml
cp /path/to/jobss-kubeconfig kubeconfigs/jobss.yaml
```

### 2. 编译项目

```bash
# 编译 bot
go build -o bin/bot ./cmd/bot

# 编译 CLI
go build -o bin/cli ./cmd/cli
```

---

## 飞书机器人部署

### 1. 创建飞书应用

1. 登录 [飞书开放平台](https://open.feishu.cn/)
2. 创建企业自建应用
3. 获取凭证：
   - **App ID** (应用 ID)
   - **App Secret** (应用密钥)
4. 配置应用权限：
   - `im:message` - 接收和发送消息
   - `im:message:send_as_bot` - 以机器人身份发送消息
5. **启用 WebSocket 长连接模式**（重要）：
   - 进入"事件订阅"页面
   - 选择"长连接模式"
   - ✅ 优点：无需公网 IP，无需配置回调地址，部署简单

### 2. 本地运行

```bash
# 单集群模式
./bin/bot --kubeconfig ~/.kube/config

# 多集群模式（推荐）
./bin/bot --multi-cluster --cluster-config clusters.json
```

启动成功后会看到：

```
🚀 Starting WebSocket long connection to Feishu...
✅ No public URL needed - using WebSocket long connection mode
📝 Waiting for messages from Feishu...
```

### 3. 使用机器人

1. 在飞书中找到你的机器人应用
2. 添加机器人到群聊或私聊
3. @机器人 发送消息，例如：
   - "列出所有节点"
   - "切换到 cdn 集群"
   - "查看 master-01 节点状态"

---

## Docker 部署

### 1. 创建 Dockerfile

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /bot ./cmd/bot

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app

COPY --from=builder /bot /app/bot
COPY .env /app/.env
COPY clusters.json /app/clusters.json
COPY kubeconfigs /app/kubeconfigs

CMD ["/app/bot", "--multi-cluster", "--cluster-config", "clusters.json"]
```

### 2. 构建和运行

```bash
# 构建镜像
docker build -t k8s-inspect-bot:latest .

# 运行容器
docker run -d \
  --name k8s-inspect-bot \
  -v $(pwd)/.env:/app/.env \
  -v $(pwd)/clusters.json:/app/clusters.json \
  -v $(pwd)/kubeconfigs:/app/kubeconfigs \
  k8s-inspect-bot:latest

# 查看日志
docker logs -f k8s-inspect-bot
```

---

## Kubernetes 部署

### 1. 准备配置文件

创建 `k8s-bot-deployment.yaml`：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: k8s-bot-config
  namespace: default
data:
  clusters.json: |
    {
      "clusters": [
        {
          "name": "test",
          "context": "context-cluster1",
          "kubeconfig": "/kubeconfigs/test.yaml"
        },
        {
          "name": "cdn",
          "context": "kubernetes-admin@kubernetes",
          "kubeconfig": "/kubeconfigs/cdn.yaml"
        }
      ]
    }
---
apiVersion: v1
kind: Secret
metadata:
  name: k8s-bot-secrets
  namespace: default
type: Opaque
stringData:
  .env: |
    ANTHROPIC_API_KEY=sk-ant-xxx
    ANTHROPIC_BASE_URL=https://api.anthropic.com
    LARK_APP_ID=cli_xxx
    LARK_APP_SECRET=xxx
---
apiVersion: v1
kind: Secret
metadata:
  name: k8s-bot-kubeconfigs
  namespace: default
type: Opaque
data:
  # 使用 base64 编码的 kubeconfig 文件
  test.yaml: <base64-encoded-kubeconfig>
  cdn.yaml: <base64-encoded-kubeconfig>
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8s-bot
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: k8s-bot
  template:
    metadata:
      labels:
        app: k8s-bot
    spec:
      serviceAccountName: k8s-bot
      containers:
      - name: bot
        image: k8s-inspect-bot:latest
        imagePullPolicy: IfNotPresent
        volumeMounts:
        - name: config
          mountPath: /app/clusters.json
          subPath: clusters.json
        - name: secrets
          mountPath: /app/.env
          subPath: .env
        - name: kubeconfigs
          mountPath: /app/kubeconfigs
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "1Gi"
            cpu: "500m"
      volumes:
      - name: config
        configMap:
          name: k8s-bot-config
      - name: secrets
        secret:
          secretName: k8s-bot-secrets
      - name: kubeconfigs
        secret:
          secretName: k8s-bot-kubeconfigs
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: k8s-bot
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8s-bot
rules:
- apiGroups: [""]
  resources: ["nodes", "pods", "namespaces", "services", "events"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["patch"]  # 用于 cordon/uncordon
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["create", "delete"]  # 用于创建诊断 Pod
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]  # 用于在 Pod 中执行命令
- apiGroups: ["apps"]
  resources: ["deployments", "daemonsets", "statefulsets"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8s-bot
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: k8s-bot
subjects:
- kind: ServiceAccount
  name: k8s-bot
  namespace: default
```

### 2. 创建 kubeconfig Secret

```bash
# 方法 1：从文件创建
kubectl create secret generic k8s-bot-kubeconfigs \
  --from-file=test.yaml=kubeconfigs/test.yaml \
  --from-file=cdn.yaml=kubeconfigs/cdn.yaml \
  -n default

# 方法 2：手动 base64 编码后填入 YAML
cat kubeconfigs/test.yaml | base64
```

### 3. 部署

```bash
# 部署
kubectl apply -f k8s-bot-deployment.yaml

# 检查状态
kubectl get pods -l app=k8s-bot -n default

# 查看日志
kubectl logs -f deployment/k8s-bot -n default
```

---

## CLI 工具使用

### 1. 交互模式

```bash
# 单集群模式
./bin/cli --kubeconfig ~/.kube/config

# 多集群模式
./bin/cli --multi-cluster --cluster-config clusters.json
```

### 2. 单次查询模式

```bash
# 执行单次查询后退出
./bin/cli --multi-cluster --cluster-config clusters.json -once "列出所有节点"
```

### 3. 示例查询

```bash
# 基础查询
./bin/cli -once "列出所有节点"
./bin/cli -once "查看 master-01 节点状态"
./bin/cli -once "列出 default 命名空间的 pod"

# 多集群操作
./bin/cli -once "列出所有集群"
./bin/cli -once "切换到 cdn 集群"
./bin/cli -once "cdn 集群有多少节点"

# 故障诊断
./bin/cli -once "诊断 master-01 节点"
./bin/cli -once "分析 10.1.1.83 节点为什么 NotReady"
```

---

## 支持的功能

### 基础查询
- 列出所有节点
- 查看节点详细信息
- 列出 Pod（支持按命名空间、节点筛选）
- 查看 Pod 详情和日志

### 多集群管理
- 列出所有集群
- 切换集群
- 跨集群查询

### 节点操作
- Cordon（标记不可调度）
- Uncordon（恢复调度）
- Drain（驱逐 Pod）

### 故障诊断
- 节点状态分析
- Pod 异常诊断
- 事件查询
- 资源使用分析

### 硬件信息查询
- CPU 信息
- 内存使用情况
- 磁盘状态
- 网络配置

---

## 故障排查

### Bot 无响应

1. 检查日志：
```bash
# Docker
docker logs -f k8s-inspect-bot

# Kubernetes
kubectl logs -f deployment/k8s-bot -n default
```

2. 验证飞书配置：
   - 确认 `LARK_APP_ID` 和 `LARK_APP_SECRET` 正确
   - 检查飞书应用是否启用了 WebSocket 长连接模式
   - 确认应用权限配置正确

3. 检查网络连接：
   - Bot 需要能访问 `open.feishu.cn`
   - 检查防火墙和代理设置

### API 调用失败

1. 验证 Anthropic API：
```bash
curl -H "x-api-key: $ANTHROPIC_API_KEY" \
     -H "anthropic-version: 2023-06-01" \
     https://api.anthropic.com/v1/messages
```

2. 检查配置：
   - `ANTHROPIC_API_KEY` 是否正确
   - `ANTHROPIC_BASE_URL` 是否可访问（如果使用代理）
   - 检查 API 配额和限流

### Kubernetes 权限错误

1. 检查 ServiceAccount：
```bash
kubectl get sa k8s-bot -n default
kubectl describe clusterrolebinding k8s-bot
```

2. 验证 RBAC 权限：
```bash
kubectl auth can-i list nodes --as=system:serviceaccount:default:k8s-bot
kubectl auth can-i get pods --as=system:serviceaccount:default:k8s-bot
```

3. 查看具体错误：
```bash
kubectl logs -f deployment/k8s-bot -n default | grep -i "forbidden\|unauthorized"
```

### 多集群配置问题

1. 验证 kubeconfig 文件：
```bash
# 测试单个 kubeconfig
kubectl --kubeconfig=kubeconfigs/test.yaml get nodes

# 检查 context
kubectl --kubeconfig=kubeconfigs/test.yaml config get-contexts
```

2. 检查 clusters.json 配置：
   - `context` 名称必须与 kubeconfig 中的 context 匹配
   - `kubeconfig` 路径必须正确（相对于程序运行目录）

---

## 性能优化

### 1. 启用 Prompt Caching

在 `.env` 中配置：
```bash
ANTHROPIC_ENABLE_CACHE=true
```

### 2. 调整资源限制

根据集群规模调整 Kubernetes Deployment 的资源配置：

```yaml
resources:
  requests:
    memory: "512Mi"  # 大集群建议增加
    cpu: "200m"
  limits:
    memory: "2Gi"
    cpu: "1000m"
```

### 3. 使用更快的模型

在 `.env` 中配置：
```bash
# 使用 Sonnet（更快，成本更低）
ANTHROPIC_MODEL=claude-sonnet-4-6

# 使用 Opus（更强，响应更慢）
ANTHROPIC_MODEL=claude-opus-4-7
```

---

## 安全建议

1. **保护敏感信息**
   - 不要将 `.env` 文件提交到 Git
   - 使用 Kubernetes Secret 存储敏感配置
   - 定期轮换 API Key

2. **最小权限原则**
   - 仅授予必要的 RBAC 权限
   - 使用独立的 ServiceAccount
   - 限制跨命名空间访问

3. **网络安全**
   - 使用 HTTPS/TLS
   - 限制 Bot 的网络访问范围
   - 启用飞书消息加密（生产环境）

4. **审计日志**
   - 记录所有操作日志
   - 监控异常行为
   - 定期审查访问记录

---

## 更多文档

- [功能特性](FEATURES.md)
- [多集群管理](MULTI_CLUSTER.md)
- [快速开始](MULTI_CLUSTER_QUICKSTART.md)
- [项目说明](README.md)
