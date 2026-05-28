# 自然语言使用指南

K8s-Inspect 支持通过自然语言管理 Kubernetes 集群，无需记忆复杂的命令格式。

## 🎯 核心理念

- **说人话** - 用日常语言描述你想做什么
- **智能理解** - AI 自动识别意图并选择合适的工具
- **灵活表达** - 支持多种表达方式，中英文混合

---

## 📋 功能分类

### 1. 集群管理

#### 查看集群列表

```
列出所有集群
显示集群列表
有哪些集群
查看集群
show clusters
list all clusters
```

**输出示例：**
```
🌐 当前共 3 个集群：

✅ test (当前) - 8 个节点
• cdn - 12 个节点
• jobss - 6 个节点
```

#### 切换集群

```
切换到 cdn 集群
切到 test
使用 jobss 集群
switch to cdn
use test cluster
change to jobss
```

**输出示例：**
```
✅ 已切换到集群: cdn (12 nodes)
```

---

### 2. 节点管理

#### 查看节点列表

```
列出所有节点
显示节点
有多少节点
查看节点状态
show nodes
list all nodes
```

**输出示例：**
```
📋 当前集群共 8 个节点：

• master-01 (10.16.10.131)
  roles: control-plane,master
  status: ✅ Ready, ✅ Schedulable

• master-02 (10.16.10.132)
  status: ✅ Ready, ✅ Schedulable
```

#### 查看节点详情

```
查看 master-01 节点状态
master-02 的详细信息
10.16.10.133 节点怎么样
show node master-01
get node status master-02
```

**输出示例：**
```
📊 节点状态: master-01

IP: 10.16.10.131
状态: ✅ Ready
调度: ✅ Schedulable

Conditions:
  ✅ Ready: True
  ✅ MemoryPressure: False
  ✅ DiskPressure: False
  ✅ PIDPressure: False

Resources:
  CPU: 56 / 56
  Memory: 125Gi / 120Gi
  Pods: 110 / 110
```

#### 节点操作

```
# Cordon（禁止调度）
cordon master-03
禁止 master-03 调度新 pod
标记 master-03 为不可调度
mark master-03 unschedulable

# Uncordon（恢复调度）
uncordon master-03
恢复 master-03 的调度
master-03 可以调度了
mark master-03 schedulable
```

---

### 3. Pod 管理

#### 查看 Pod 列表

```
# 按命名空间查询
列出 default 命名空间的 pod
查看 kube-system namespace 的 pod 数量
default 有多少 pod
show pods in kube-system

# 按节点查询
master-03 节点上有哪些 pod
列出 master-01 上的 pod
10.16.10.133 的 pod
show pods on master-03

# 全局查询
查看所有 namespace 的 pod 数量
列出所有 pod
show all pods
```

**输出示例：**
```
📦 Namespace: kube-system - 共 36 个 Pod

✅ Running (36):
  • coredns-7d89d9b6b8-abc12 - 节点: master-01
  • kube-proxy-xyz89 - 节点: master-02
  ...
```

---

### 4. Namespace 管理

#### 查看 Namespace 列表

```
列出所有 namespace
显示命名空间
有哪些 namespace
查看 ns
show namespaces
list all namespaces
```

**输出示例：**
```
📁 集群共 8 个 Namespace

✅ default - age: 22896h0m0s
✅ kube-system - age: 22896h0m0s
✅ monitoring - age: 22752h0m0s
```

---

### 5. 故障诊断

#### 节点诊断

```
诊断 master-01 节点
分析 10.16.10.133 节点为什么 NotReady
master-02 有什么问题
检查 master-03 的状态
diagnose node master-01
check node master-02
```

**输出示例：**
```
🔍 诊断结果: master-01

✅ Containerd 运行正常
✅ Kubelet 运行正常
✅ 磁盘空间充足
✅ 内存使用正常

💡 节点状态健康，无异常
```

---

### 6. 硬件信息

#### 综合硬件信息

```
查看 master-01 的硬件信息
master-02 的硬件配置
10.16.10.133 的硬件
show hardware info for master-01
```

#### CPU 信息

```
查看 master-01 的 CPU 信息
master-02 的 CPU 配置
10.16.10.133 有多少核
show cpu info for master-01
```

#### 内存信息

```
查看 master-01 的内存使用情况
master-02 的内存信息
10.16.10.133 内存够不够
show memory info for master-01
```

#### 网络信息

```
查看 master-01 的网络配置
master-02 的网卡信息
10.16.10.133 的 IP 地址
show network info for master-01
```

---

## 💬 对话示例

### 示例 1：多集群切换

```
用户: 有哪些集群？

Bot: 🌐 当前共 3 个集群：
     ✅ test (当前) - 8 个节点
     • cdn - 12 个节点
     • jobss - 6 个节点

用户: 切到 cdn

Bot: ✅ 已切换到集群: cdn (12 nodes)

用户: 列出所有节点

Bot: 📋 当前集群共 12 个节点：
     • cdn-node-01 (10.1.1.1)
       status: ✅ Ready, ✅ Schedulable
     ...
```

### 示例 2：节点维护

```
用户: 我要维护 master-03，先禁止调度

Bot: ✅ master-03 已标记为不可调度
     
     - 新 Pod 不会再调度到该节点
     - 现有 Pod 继续正常运行

用户: master-03 上有哪些 pod

Bot: 📦 master-03 节点共 15 个 Pod
     
     ✅ Running: 15
       • nginx-abc123
       • redis-xyz789
       ...

用户: 维护完了，恢复调度

Bot: ✅ master-03 已恢复调度
```

### 示例 3：故障排查

```
用户: master-01 节点 NotReady，帮我看看

Bot: 🔍 正在诊断 master-01...
     
     ❌ Containerd 服务异常
     - 状态: inactive (dead)
     - 错误: failed to start containerd
     
     ✅ Kubelet 运行正常
     ✅ 磁盘空间充足
     
     💡 建议：
     1. 重启 containerd: systemctl restart containerd
     2. 检查 containerd 配置文件
     3. 查看系统日志: journalctl -u containerd

用户: 重启后好了吗

Bot: [再次诊断]
     ✅ Containerd 运行正常
     ✅ Kubelet 运行正常
     ✅ 节点状态: Ready
     
     💡 问题已解决！
```

### 示例 4：跨集群查找

```
用户: 查看 10.1.1.100 节点状态

Bot: [在当前集群未找到]
     [自动搜索其他集群]
     
     节点在 jobss 集群，已自动切换
     
     📊 节点状态: 10.1.1.100
     IP: 10.1.1.100
     状态: ✅ Ready
     调度: ✅ Schedulable
```

---

## 🎨 表达技巧

### 1. 灵活的动词

查看/列出/显示/show/list/get 都可以：
```
查看节点
列出节点
显示节点
show nodes
list nodes
get nodes
```

### 2. 中英文混合

```
列出所有 nodes
查看 master-01 的 status
show 所有集群
```

### 3. 口语化表达

```
有多少节点
master-01 怎么样
cdn 集群有啥问题
```

### 4. 省略词

```
列出 pod（省略 "所有"）
查看节点（省略 "所有"）
切到 cdn（省略 "集群"）
```

---

## ⚡ 快捷操作

### 节点快速巡检

```
列出所有节点
```
一次性查看所有节点的状态和调度情况。

### Pod 状态总览

```
查看所有 namespace 的 pod 数量
```
快速了解集群中所有 Pod 的状态分布。

### 集群健康检查

```
列出所有集群
列出所有节点
查看所有 namespace 的 pod 数量
```
三条命令快速了解集群整体状况。

---

## 💡 使用建议

### 1. 从简单开始

先用简单的命令熟悉系统：
```
列出所有集群
列出所有节点
列出所有 namespace
```

### 2. 逐步深入

熟悉后再使用复杂查询：
```
master-03 节点上有哪些 pod
诊断 master-01 节点
查看 master-02 的硬件信息
```

### 3. 善用自动切换

不用手动切换集群，直接查询节点：
```
查看 10.1.1.100 节点状态
```
系统会自动找到节点所在集群并切换。

### 4. 组合使用

可以在一句话中完成多个操作：
```
切换到 cdn 然后列出所有节点
```

---

## ⚠️ 注意事项

### 1. 节点标识

支持三种方式指定节点：
- **节点名称**: `master-01`
- **IP 地址**: `10.16.10.131`
- **Hostname**: `master-01.example.com`

### 2. 命名空间

- 使用 `all` 查询所有命名空间
- 命名空间名称区分大小写
- 默认查询 `default` 命名空间

### 3. 集群切换

- 切换集群会影响所有后续操作
- 建议操作前先确认当前集群
- 使用 `列出所有集群` 查看当前集群

### 4. 权限限制

- 只能访问集群内已注册的节点
- 不能执行任意命令
- 所有操作都有审计日志

---

## 🚀 高级用法

### 批量操作

虽然不能直接批量操作，但可以通过脚本实现：

```bash
#!/bin/bash
# 巡检所有集群的节点状态
for cluster in test cdn jobss; do
    ./bin/cli -once "切换到 $cluster 然后列出所有节点"
done
```

### 定时任务

结合 cron 实现定时巡检：

```bash
# 每小时检查一次所有集群
0 * * * * /path/to/bin/cli -once "列出所有集群"
```

### 告警集成

结合监控系统实现自动诊断：

```bash
# 当节点 NotReady 时自动诊断
if [ "$NODE_STATUS" = "NotReady" ]; then
    ./bin/cli -once "诊断 $NODE_NAME 节点"
fi
```

---

## 📚 相关文档

- [功能特性](../FEATURES.md) - 详细功能列表
- [多集群管理](../MULTI_CLUSTER.md) - 多集群配置和使用
- [添加集群](ADD_CLUSTER.md) - 命令行添加集群
- [部署指南](../DEPLOY.md) - 部署配置
