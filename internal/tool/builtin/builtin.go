// Package builtin provides read-only diagnostic tools for nodes.
//
// Security constraints:
//   - Command strings are hardcoded inside each tool; the LLM cannot compose arbitrary commands.
//   - Node parameters must pass through nodes.Registry.Resolve; IPs not in the cluster whitelist are rejected.
package builtin

// nodeArgSchema is shared by all node tools that accept a single "node" parameter.
func nodeArgSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node IP address, hostname, or Kubernetes node name. Must be a registered node in the cluster.",
			},
		},
		"required": []string{"node"},
	}
}
