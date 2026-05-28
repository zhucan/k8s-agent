// Package builtin provides read-only diagnostic tools for nodes.
//
// 安全约束:
//   - 命令字符串硬编码在 tool 内,LLM 不能拼。
//   - 节点参数必走 nodes.Registry.Resolve,不在 K8s 集群白名单内的 IP 一律拒绝。
package builtin

// nodeArgSchema: 所有节点工具都收一个 node 参数,schema 共用
func nodeArgSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "节点的 IP 地址 / hostname / Kubernetes node 名,必须是集群内已注册的节点",
			},
		},
		"required": []string{"node"},
	}
}
