# K8s-Inspect 功能特性

## 🎯 核心功能

### 1. 多集群管理

#### 集群切换
- **CLI 模式** - 通过 `--context` 参数指定集群
- **Bot 模式** - 运行时动态切换集群
- **智能匹配** - 支持集群名称、context 名称、模糊匹配

#### 集群操作
- `list_clusters` - 列出所有可用集群及状态
- `switch_cluster` - 切换到指定集群
- 自动同步 `kubeconfigs/` 目录（启动时 + 运行时热加载）

**使用示例：**
```bash
# CLI 模式
./bin/cli --context dev-cluster -once "列出节点"

# Bot 模式（飞书）
列出所有集群
切换到 cdn 集群
```

---

### 2. 节点管理

#### 节点查询
- **列出节点** - 显示所有节点及状态（Ready/NotReady, Schedulable/Unschedulable）
- **节点详情** - 查看节点的 Conditions、Capacity、Allocatable 资源

#### 节点操作
- **Cordon** - 标记节点为不可调度（维护模式）
- **Uncordon** - 恢复节点调度

**使用示例：**
```
列出所有节点
查看 master-01 节点状态
cordon master-03
uncordon master-03
```

**输出示例：**
```
📋 当前集群共 8 个节点：

• master-01 (10.16.10.131)
  hostname: master-01
  roles: control-plane,master
  status: ✅ Ready, ✅ Schedulable

• master-03 (10.16.10.133)
  status: ✅ Ready, ⚠️ Unschedulable
```

---

### 3. Pod 管理

#### Pod 查询
- **按命名空间查询** - 查看指定 namespace 的 Pod
- **按节点查询** - 查看特定节点上的 Pod（使用 field_selector）
- **全局查询** - 查看所有 namespace 的 Pod
- **状态统计** - 按状态分组（Running, Pending, Failed, Succeeded）

**使用示例：**
```
列出 default 命名空间的 pod
查看 kube-system namespace 的 pod 数量
master-03 节点上有哪些 pod
查看所有 namespace 的 pod 数量
```

**输出示例：**
```
📦 Namespace: kube-system - 共 36 个 Pod

✅ Running (36):
  • coredns-7d89d9b6b8-abc12
  • kube-proxy-xyz89
  ...
```

---

### 4. Namespace 管理

#### Namespace 查询
- **列出所有 namespace** - 显示名称、状态、创建时间
- **状态标识** - Active/Terminating 状态图标

**使用示例：**
```
列出所有 namespace
查看集群有哪些命名空间
```

**输出示例：**
```
📁 集群共 8 个 Namespace

✅ default - age: 22896h0m0s
✅ kube-system - age: 22896h0m0s
✅ monitoring - age: 22752h0m0s
```

---

### 5. 节点诊断（无需 SSH）

#### 诊断工具
通过 K8s 特权 Pod 执行诊断，无需 SSH 访问：

- **diagnose_node** - 综合诊断节点问题
  - Containerd 状态和日志
  - Kubelet 状态和日志
  - 系统日志（dmesg, journalctl）
  - 磁盘、内存、网络状态

**使用示例：**
```
诊断 master-01 节点
分析 10.1.1.83 节点为什么 NotReady
master-02 有什么问题
```

**诊断内容：**
1. **容器运行时** - containerd 服务状态和最近日志
2. **Kubelet** - kubelet 服务状态和错误日志
3. **系统资源** - 磁盘、内存、网络状态
4. **系统日志** - dmesg 和 journalctl 错误信息
5. **AI 分析** - Claude 分析根本原因并提供修复建议

---

### 6. 硬件信息查询（无需 SSH）

通过 K8s 特权 Pod 查询节点硬件信息：

#### 综合硬件信息
- **k8s_hardware_info** - CPU、内存、网卡、磁盘概览

#### 详细信息
- **k8s_cpu_info** - CPU 型号、核心数、频率、缓存、虚拟化支持
- **k8s_memory_info** - 内存容量、使用情况、Swap、硬件详情
- **k8s_network_info** - 网卡列表、IP、MAC、速率、驱动、路由表

**使用示例：**
```
查看 master-01 的硬件信息
master-02 的 CPU 信息
master-03 的内存使用情况
查看 master-01 的网络配置
```

**输出示例：**
```
=== CPU ===
Model name: Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz
Architecture: x86_64
CPU(s): 56
Thread(s) per core: 2
Core(s) per socket: 14

=== Memory ===
Total: 125Gi
Available: 98Gi
Used: 27Gi

=== Network Interfaces ===
eth0: 10.16.10.131/24 (UP, 10000 Mbps)
```

---

### 7. 定时巡检告警

#### 自动健康巡检
- **定时检测** - 按 cron 表达式定时巡检所有集群的节点健康状态
- **飞书卡片** - 发现异常时发送飞书卡片消息到指定群聊
- **@ 相关人员** - 支持通过邮箱或 Open ID 配置需要 @ 的人
- **全集群覆盖** - 遍历所有已配置的集群

#### 配置方式

在 `.env` 中配置：
```bash
LARK_ALERT_CHAT_ID=oc_xxx          # 告警群 Chat ID
LARK_ALERT_MENTION_EMAILS=a@co.com  # 要 @的人的邮箱（逗号分隔）
LARK_ALERT_CRON=0 10 * * *          # 巡检时间（cron 格式）
```

#### 告警内容
- 集群连接状态
- NotReady 节点列表
- Unschedulable 节点列表
- 节点角色和 IP 信息

---

### 8. 集群自动同步与热加载

#### 启动同步
- 扫描 `kubeconfigs/` 目录
- 与 `clusters.json` 对比，自动增删条目
- 无需手动维护配置文件

#### 运行时热加载
- 监听 `kubeconfigs/` 目录文件变化
- 新增文件 → 自动加载集群
- 删除文件 → 自动卸载集群
- 修改文件 → 自动重载集群

---

## 🔧 技术实现

### 安全机制

1. **节点白名单** - 只能访问集群内已注册的节点
2. **命令硬编码** - 所有命令在工具内部预定义，LLM 不能拼接任意命令
3. **无 SSH 依赖** - 通过 K8s 原生 API 和特权 Pod 执行
4. **RBAC 控制** - 支持 Kubernetes RBAC 权限管理

### 工具架构

```
LLM (Claude) 
  ↓ 选择工具 + 参数
Tool Registry
  ↓ 执行
K8s API / 特权 Pod
  ↓ 返回结果
LLM 分析总结
  ↓ 用户友好的回复
用户
```

### 特权 Pod 机制

诊断和硬件查询通过临时特权 Pod 实现：

1. 创建特权 Pod（hostNetwork, hostPID, privileged）
2. 挂载节点根文件系统（/host）
3. 执行诊断命令（chroot /host）
4. 返回结果并自动清理 Pod

**优势：**
- ✅ 无需 SSH 配置和密钥管理
- ✅ 使用 K8s RBAC 统一权限控制
- ✅ 自动清理，不留残留资源
- ✅ 更安全，审计日志完整

---

## 📊 可用工具列表

### 基础工具（所有模式）
1. `list_nodes` - 列出所有节点（含状态）
2. `node_status` - 查看节点详细状态
3. `cordon_node` - 禁止节点调度
4. `uncordon_node` - 恢复节点调度
5. `list_pods` - 查看 Pod（支持 field_selector）
6. `list_namespaces` - 列出所有 namespace

### 诊断工具（K8s Pod 方式）
7. `diagnose_node` - 综合诊断节点问题
8. `k8s_hardware_info` - 查看硬件信息概览
9. `k8s_cpu_info` - 查看 CPU 详细信息
10. `k8s_memory_info` - 查看内存详细信息
11. `k8s_network_info` - 查看网络详细信息

### 多集群工具（仅多集群模式）
12. `list_clusters` - 列出所有集群
13. `switch_cluster` - 切换集群
14. `find_node_in_clusters` - 跨集群查找节点

---

## 🚀 使用场景

### 场景 1：节点维护

```
1. 列出所有节点                    # 查看节点状态
2. cordon master-03                # 标记不可调度
3. [手动驱逐 Pod 或等待自然迁移]
4. [执行维护操作]
5. uncordon master-03              # 恢复调度
6. 列出所有节点                    # 验证状态
```

### 场景 2：故障排查

```
用户: master-01 节点 NotReady，帮我看看

Bot: [自动执行 diagnose_node]
     
     🔍 诊断结果：
     
     ❌ Containerd 服务异常
     - 状态: inactive (dead)
     - 错误: failed to start containerd
     
     ✅ Kubelet 运行正常
     
     💡 建议：
     1. 重启 containerd: systemctl restart containerd
     2. 检查磁盘空间是否充足
     3. 查看 containerd 配置文件
```

### 场景 3：多集群巡检

```bash
#!/bin/bash
for cluster in test cdn jobss; do
    echo "=== $cluster 集群 ==="
    ./bin/cli --multi-cluster --cluster-config clusters.json \
        -once "切换到 $cluster 然后列出所有节点"
done
```

### 场景 4：Pod 排查

```
用户: master-03 节点上的 Pod 有问题吗

Bot: [使用 field_selector 查询]
     
     📦 master-03 节点共 15 个 Pod
     
     ✅ Running: 14
     ❌ Failed: 1
       • nginx-abc123 (CrashLoopBackOff)
```

---

## ⚠️ 注意事项

### 权限要求

**基础查询（只读）：**
- nodes: get, list, watch
- pods: get, list, watch
- namespaces: get, list, watch

**节点操作（写）：**
- nodes: patch (cordon/uncordon)

**诊断功能（特权 Pod）：**
- pods: create, delete
- pods/exec: create

### 资源消耗

- **特权 Pod** - 每次诊断创建临时 Pod，完成后自动删除
- **节点刷新** - 每个集群每 5 分钟自动刷新节点列表
- **内存占用** - 多集群模式下每个集群独立维护状态

### 最佳实践

1. **生产环境** - 使用只读权限的 ServiceAccount
2. **诊断操作** - 在维护窗口期执行，避免影响业务
3. **多集群** - 为不同环境部署独立 Bot 实例
4. **日志审计** - 记录所有操作日志，便于追溯

---

## 🎯 后续规划

- [ ] 支持 Drain 节点（驱逐 Pod）
- [ ] Pod 日志查询
- [ ] 事件查询和分析
- [ ] 资源配额查询
- [ ] 跨集群资源对比
- [ ] 集群健康度评分
- [ ] 自动故障转移建议
