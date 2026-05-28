package builtin

import (
	"context"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/nodes"
)

// K8sHardwareInfo: 通过 K8s Pod 查询节点硬件信息（不需要 SSH）
type K8sHardwareInfo struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *K8sHardwareInfo) Name() string { return "k8s_hardware_info" }

func (t *K8sHardwareInfo) Description() string {
	return "Show comprehensive hardware information on a node. Includes CPU model/cores, total memory, network interfaces, and disk info."
}

func (t *K8sHardwareInfo) InputSchema() map[string]any { return nodeArgSchema() }

func (t *K8sHardwareInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	executor := &NodeShellExecutor{
		CS:         t.CS,
		RestConfig: t.RestConfig,
		Nodes:      t.Nodes,
	}

	cmd := `echo "=== CPU ===" && \
lscpu | grep -E "Model name|Architecture|CPU\(s\)|Thread|Socket|Core|MHz" && \
echo && echo "=== Memory ===" && \
free -h && \
grep -E "MemTotal|SwapTotal" /proc/meminfo && \
echo && echo "=== Network Interfaces ===" && \
ip -br addr show | grep -v "^lo" && \
echo && echo "=== Disk ===" && \
lsblk -d -o NAME,SIZE,TYPE,MODEL | grep disk`

	return executor.execOnNodeViaPod(ctx, n.Name, cmd)
}

// K8sCPUInfo: 通过 K8s Pod 查询节点 CPU 详细信息
type K8sCPUInfo struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *K8sCPUInfo) Name() string { return "k8s_cpu_info" }

func (t *K8sCPUInfo) Description() string {
	return "Show detailed CPU information on a node. Includes model, architecture, cores, threads, frequency, cache, and virtualization support."
}

func (t *K8sCPUInfo) InputSchema() map[string]any { return nodeArgSchema() }

func (t *K8sCPUInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	executor := &NodeShellExecutor{
		CS:         t.CS,
		RestConfig: t.RestConfig,
		Nodes:      t.Nodes,
	}

	cmd := `lscpu && echo && echo "=== CPU Flags (partial) ===" && grep -m1 "^flags" /proc/cpuinfo | cut -d: -f2 | tr ' ' '\n' | grep -E "vmx|svm|sse|avx" | head -20`

	return executor.execOnNodeViaPod(ctx, n.Name, cmd)
}

// K8sNetworkInfo: 通过 K8s Pod 查询节点网卡信息
type K8sNetworkInfo struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *K8sNetworkInfo) Name() string { return "k8s_network_info" }

func (t *K8sNetworkInfo) Description() string {
	return "Show detailed network interface information on a node. Includes IP addresses, MAC addresses, link status, speed, and routing table."
}

func (t *K8sNetworkInfo) InputSchema() map[string]any { return nodeArgSchema() }

func (t *K8sNetworkInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	executor := &NodeShellExecutor{
		CS:         t.CS,
		RestConfig: t.RestConfig,
		Nodes:      t.Nodes,
	}

	cmd := `echo "=== Network Interfaces ===" && \
ip -br addr show && \
echo && echo "=== Interface Details ===" && \
for iface in $(ls /sys/class/net/ | grep -v "^lo$"); do \
  echo "--- $iface ---"; \
  echo -n "  MAC: "; cat /sys/class/net/$iface/address 2>/dev/null || echo "N/A"; \
  echo -n "  State: "; cat /sys/class/net/$iface/operstate 2>/dev/null || echo "N/A"; \
  echo -n "  Speed: "; cat /sys/class/net/$iface/speed 2>/dev/null | awk '{print $1 " Mbps"}' || echo "N/A"; \
  echo -n "  Driver: "; readlink /sys/class/net/$iface/device/driver 2>/dev/null | xargs basename || echo "N/A"; \
done && \
echo && echo "=== Routing Table ===" && \
ip route show`

	return executor.execOnNodeViaPod(ctx, n.Name, cmd)
}

// K8sMemoryInfo: 通过 K8s Pod 查询节点内存信息
type K8sMemoryInfo struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

func (t *K8sMemoryInfo) Name() string { return "k8s_memory_info" }

func (t *K8sMemoryInfo) Description() string {
	return "Show detailed memory information on a node. Includes total/available/used memory, swap, cache, buffers, and memory hardware details."
}

func (t *K8sMemoryInfo) InputSchema() map[string]any { return nodeArgSchema() }

func (t *K8sMemoryInfo) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, _ := input["node"].(string)
	n, err := t.Nodes.Resolve(raw)
	if err != nil {
		return "", err
	}

	executor := &NodeShellExecutor{
		CS:         t.CS,
		RestConfig: t.RestConfig,
		Nodes:      t.Nodes,
	}

	cmd := `echo "=== Memory Usage ===" && \
free -h && \
echo && echo "=== Memory Details ===" && \
cat /proc/meminfo && \
echo && echo "=== Memory Hardware (dmidecode) ===" && \
(dmidecode -t memory 2>/dev/null | grep -A5 "Memory Device" | grep -E "Size|Speed|Type|Manufacturer" | head -20 || echo "dmidecode not available or requires root")`

	return executor.execOnNodeViaPod(ctx, n.Name, cmd)
}
