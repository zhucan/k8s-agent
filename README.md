# K8s-Agent

An intelligent Kubernetes cluster operations assistant powered by Claude AI, supporting natural language interaction via Feishu (Lark) bot or command line.

**🆕 No-LLM mode supported!** You can invoke tools directly without AI, suitable for scripting and automation.

## ✨ Features

- 🤖 **Feishu Integration** - WebSocket long connection, no public IP required
- 🗣️ **Natural Language** - Manage clusters with natural language, no need to memorize commands (LLM mode)
- 🔧 **Direct Tool Invocation** - Call tools directly without AI, returns structured data (no-LLM mode)
- 🔄 **Multi-Cluster Management** - Manage and dynamically switch between multiple Kubernetes clusters
- 📊 **Node Monitoring** - View node status, resource usage, and hardware info
- 🔍 **Intelligent Diagnostics** - Automatically analyze node failures and provide remediation suggestions
- 🚀 **No SSH Required** - Run diagnostics via K8s privileged Pods, more secure
- ⏰ **Scheduled Health Checks** - Auto-detect unhealthy nodes and send Feishu alerts
- 🔥 **Hot Reload** - Automatically load/unload clusters when kubeconfig files change

## 🎯 Two Operation Modes

### 📋 Mode 1: LLM Natural Language Mode (default)
- Uses AI to understand natural language queries
- Suitable for interactive and exploratory use
- Requires `ANTHROPIC_API_KEY`

### 🔧 Mode 2: Direct Tool Invocation Mode (--no-llm)
- No LLM, invokes tools directly
- Returns structured JSON data
- Suitable for scripting, API calls, and automation
- **No `ANTHROPIC_API_KEY` required**
- Fast response, no API call cost

See [NO_LLM_MODE.md](./NO_LLM_MODE.md) for details.

## 🚀 Quick Start

### 1. Environment Setup

Create a `.env` file:

```bash
# Anthropic API (LLM mode only)
ANTHROPIC_API_KEY=sk-ant-xxx

# Feishu app credentials (bot mode only)
LARK_APP_ID=cli_xxx
LARK_APP_SECRET=xxx

# Scheduled alert (optional)
LARK_ALERT_CHAT_ID=oc_xxx                    # Alert group Chat ID
LARK_ALERT_MENTION_EMAILS=user@company.com   # Email addresses to @mention
LARK_ALERT_CRON=0 10 * * *                   # Check schedule
```

**Note:** `ANTHROPIC_API_KEY` is not required when using `--no-llm`.

### 2. Configure Clusters

**Single-cluster mode:**
```bash
export KUBECONFIG=~/.kube/config
```

**Multi-cluster mode:**
```bash
# Place kubeconfig files in the kubeconfigs directory (filename = cluster name)
mkdir -p kubeconfigs
cp /path/to/test-kubeconfig kubeconfigs/test.yaml
cp /path/to/cdn-kubeconfig kubeconfigs/cdn.yaml

# clusters.json is auto-generated/synced on startup
```

### 3. Build and Run

```bash
# Build
go build -o bin/bot ./cmd/bot
go build -o bin/cli ./cmd/cli

# Run Feishu bot (LLM mode, multi-cluster)
./bin/bot --multi-cluster --cluster-config clusters.json

# Run Feishu bot (no-LLM mode, multi-cluster)
./bin/bot --no-llm --multi-cluster --cluster-config clusters.json

# Run CLI (LLM mode, single-cluster)
./bin/cli --kubeconfig ~/.kube/config

# Run CLI (no-LLM mode, single-cluster)
./bin/cli --no-llm --kubeconfig ~/.kube/config
```

## 💬 Usage Examples

### LLM Mode (Natural Language)

#### Feishu Bot

Mention the bot in a Feishu group:

```
# Multi-cluster management
List all clusters
Switch to cdn cluster

# Node queries
List all nodes
List all unhealthy nodes
Check status of master-01

# Pod management
List pods in the default namespace
Which pods are on master-03?

# Diagnostics
Diagnose master-01
Why is 10.1.1.83 NotReady?

# Hardware info
Show CPU info for master-01
Memory usage on master-02
```

#### CLI

```bash
# Interactive mode
./bin/cli --multi-cluster --cluster-config clusters.json

# Single query
./bin/cli --once "List all nodes"
./bin/cli --once "List all unhealthy nodes"
./bin/cli --once "Check status of master-01"
```

### No-LLM Mode (Direct Tool Invocation)

#### Feishu Bot

Mention the bot and send tool commands:

```
# List all clusters
list_clusters

# Switch cluster
switch_cluster {"cluster":"cdn"}

# List all nodes
list_nodes

# List unhealthy nodes
list_nodes {"filter":"unhealthy"}

# Check node status
node_status {"node":"master-01"}

# Diagnose node
diagnose_node {"node":"10.1.1.83"}
```

#### CLI

```bash
# Show help
./bin/cli --help

# Single tool call
./bin/cli --no-llm --multi-cluster --tool list_clusters
./bin/cli --no-llm --kubeconfig config.yaml --tool list_nodes --input '{"filter":"unhealthy"}'
./bin/cli --no-llm --kubeconfig config.yaml --tool node_status --input '{"node":"master-01"}'

# Interactive mode
./bin/cli --no-llm --multi-cluster --cluster-config clusters.json
tool> list_nodes {"filter":"unhealthy"}
tool> switch_cluster {"cluster":"prod"}
tool> node_status {"node":"master-01"}
```

#### Script Integration

```bash
#!/bin/bash
# Monitor unhealthy nodes

UNHEALTHY=$(./bin/cli --no-llm --kubeconfig config.yaml \
  --tool list_nodes --input '{"filter":"unhealthy"}' 2>/dev/null)

UNHEALTHY_COUNT=$(echo "$UNHEALTHY" | grep "Unhealthy:" | awk '{print $4}')

if [ "$UNHEALTHY_COUNT" -gt 0 ]; then
    echo "⚠️ Found $UNHEALTHY_COUNT unhealthy nodes"
    echo "$UNHEALTHY"
    # Send alert...
fi
```

## 📚 Documentation

- [No-LLM Mode Guide](NO_LLM_MODE.md) - Direct tool invocation details
- [Deployment Guide](DEPLOY.md) - Docker and Kubernetes deployment
- [Features](FEATURES.md) - Full feature list
- [Multi-Cluster Management](MULTI_CLUSTER.md) - Multi-cluster configuration and usage
- [Natural Language Guide](docs/NATURAL_LANGUAGE.md) - Natural language interaction examples
- [Add Cluster](docs/ADD_CLUSTER.md) - Cluster management (auto-sync and hot reload)

## 🛠️ Available Capabilities

### Core
- ✅ List nodes (with status)
- ✅ View node details
- ✅ Cordon/Uncordon nodes
- ✅ List Pods (filter by node or namespace)
- ✅ List Namespaces

### Multi-Cluster
- ✅ List all clusters
- ✅ Dynamic cluster switching
- ✅ Auto-sync kubeconfigs directory
- ✅ Runtime hot reload (add/remove/update clusters without restart)

### Alerting
- ✅ Scheduled unhealthy node checks
- ✅ Feishu card alerts (with @mention support)
- ✅ Email-based @mention (auto-resolved to Open ID)

### Diagnostics
- ✅ Node fault diagnosis (via K8s Pod)
- ✅ Hardware info (CPU, memory, network, disk)
- ✅ containerd and kubelet status analysis
- ✅ System log collection

## 🏗️ Project Structure

```
.
├── cmd/
│   ├── bot/              # Feishu bot service
│   └── cli/              # Command-line tool
├── internal/
│   ├── alert/            # Scheduled health check alerts
│   ├── bot/              # Bot core logic
│   ├── cluster/          # Cluster management (auto-sync and hot reload)
│   ├── llm/              # LLM integration (Claude)
│   ├── k8s/              # Kubernetes client
│   ├── lark/             # Feishu message cards
│   ├── nodes/            # Node registry
│   └── tool/             # Tool framework
│       └── builtin/      # Built-in tools
├── kubeconfigs/          # Cluster kubeconfig files (auto-loaded on add)
└── clusters.json         # Cluster list config (auto-maintained)
```

## 🔒 Security

- **No SSH** - Diagnostics run via K8s native API and privileged Pods
- **Node Whitelist** - Only registered cluster nodes are accessible
- **Hardcoded Commands** - LLM cannot execute arbitrary commands; only predefined tools are available
- **RBAC** - Kubernetes RBAC permission management supported

## 🤝 Contributing

Issues and Pull Requests are welcome!

## 📄 License

MIT License
