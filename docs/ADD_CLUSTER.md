# 添加集群

## 快速开始

只需将 kubeconfig 文件放入 `kubeconfigs/` 目录即可，Bot 会自动识别并加载。

## 添加集群

```bash
# 将 kubeconfig 文件复制到 kubeconfigs 目录（文件名即为集群名）
cp /path/to/new-kubeconfig kubeconfigs/new-cluster.yaml
```

Bot 启动时会自动扫描 `kubeconfigs/` 目录，并与 `clusters.json` 同步：
- 新增的文件 → 自动添加到配置
- 已删除的文件 → 自动从配置移除

**Bot 运行中也会热加载**：无需重启，新增或删除文件后几秒内自动生效。

## 命名规则

| 文件名 | 集群名 |
|--------|--------|
| `kubeconfigs/test.yaml` | `test` |
| `kubeconfigs/cdn.yaml` | `cdn` |
| `kubeconfigs/prod-cluster.yml` | `prod-cluster` |

文件名（去掉 `.yaml`/`.yml` 扩展名）即为集群的显示名称。

## 目录结构

```
.
├── clusters.json              # 自动维护，无需手动编辑
├── kubeconfigs/              # 放入 kubeconfig 文件即可
│   ├── test.yaml
│   ├── cdn.yaml
│   └── jobss.yaml
└── bin/
    └── bot
```

## 工作原理

1. **启动时同步**：扫描 `kubeconfigs/` 目录，对比 `clusters.json`，自动增删条目
2. **运行时监听**：使用 fsnotify 监听目录变化，新增/删除/修改文件时热加载
3. **Context 提取**：从 kubeconfig 文件中自动解析 `current-context`

## 验证

```bash
# 启动 Bot
./bin/bot --multi-cluster --cluster-config clusters.json

# 查看日志确认加载
# [cluster/sync] added cluster "new-cluster" from kubeconfigs/new-cluster.yaml
# [cluster/watcher] watching kubeconfigs for changes
```

在飞书中：
```
列出所有集群
```

## 删除集群

直接删除 `kubeconfigs/` 目录中对应的文件即可：

```bash
rm kubeconfigs/old-cluster.yaml
```

Bot 会自动检测并卸载该集群。

## 注意事项

- kubeconfig 文件必须包含至少一个有效的 context
- 文件权限建议设为 `600`：`chmod 600 kubeconfigs/*.yaml`
- 支持 `.yaml` 和 `.yml` 两种扩展名

## 相关文档

- [多集群管理](../MULTI_CLUSTER.md) - 多集群使用指南
- [部署指南](../DEPLOY.md) - 部署配置
