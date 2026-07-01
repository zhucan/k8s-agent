package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/k8s-inspect/internal/bot"
	"github.com/k8s-inspect/internal/cluster"
	"github.com/k8s-inspect/internal/tool"
)

func main() {
	bot.LoadDotEnv(".env")

	var (
		kubeconfigFlag string
		contextFlag    string
		once           string
		listContexts   bool
		addCluster     string
		clusterConfig  string
		multiCluster   bool
		noLLM          bool
		toolName       string
		toolInput      string
	)

	// Custom Usage
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "k8s-inspect - Kubernetes Cluster Operations Assistant\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [options]\n\n", os.Args[0])

		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "📋 Mode 1: LLM Natural Language Mode (default)\n")
		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "Uses AI to understand natural language queries and invoke tools automatically.\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  # Single-cluster interactive mode\n")
		fmt.Fprintf(os.Stderr, "  %s --kubeconfig ~/.kube/config\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Multi-cluster interactive mode\n")
		fmt.Fprintf(os.Stderr, "  %s --multi-cluster --cluster-config clusters.json\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Single query mode\n")
		fmt.Fprintf(os.Stderr, "  %s --once \"list all nodes\"\n\n", os.Args[0])

		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "🔧 Mode 2: Direct Tool Invocation Mode (--no-llm)\n")
		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "Invokes tools directly without LLM, returns structured JSON data.\n")
		fmt.Fprintf(os.Stderr, "Suitable for scripting, API calls, and automation.\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  # List all clusters\n")
		fmt.Fprintf(os.Stderr, "  %s --no-llm --multi-cluster --tool list_clusters\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # List unhealthy nodes\n")
		fmt.Fprintf(os.Stderr, "  %s --no-llm --kubeconfig config.yaml --tool list_nodes --input '{\"filter\":\"unhealthy\"}'\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Switch cluster\n")
		fmt.Fprintf(os.Stderr, "  %s --no-llm --multi-cluster --tool switch_cluster --input '{\"cluster\":\"prod\"}'\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Interactive mode (type tool name and args)\n")
		fmt.Fprintf(os.Stderr, "  %s --no-llm --multi-cluster\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  tool> list_nodes {\"filter\":\"unhealthy\"}\n\n")

		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "⚙️  General Options\n")
		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		flag.PrintDefaults()

		fmt.Fprintf(os.Stderr, "\n═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "🛠️  Cluster Management\n")
		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "  # List all available contexts\n")
		fmt.Fprintf(os.Stderr, "  %s --list-contexts\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Add cluster to config file\n")
		fmt.Fprintf(os.Stderr, "  %s --add-cluster <name> --kubeconfig <path>\n\n", os.Args[0])

		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "📚 Available Tools (--no-llm mode)\n")
		fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════════════════════\n")
		fmt.Fprintf(os.Stderr, "Cluster Management:\n")
		fmt.Fprintf(os.Stderr, "  - list_clusters          List all clusters\n")
		fmt.Fprintf(os.Stderr, "  - switch_cluster         Switch current cluster\n")
		fmt.Fprintf(os.Stderr, "  - add_cluster_to_config  Add a new cluster\n\n")
		fmt.Fprintf(os.Stderr, "Node Management:\n")
		fmt.Fprintf(os.Stderr, "  - list_nodes             List nodes (filter: all/healthy/unhealthy)\n")
		fmt.Fprintf(os.Stderr, "  - node_status            View detailed node status\n")
		fmt.Fprintf(os.Stderr, "  - cordon_node            Mark node as unschedulable\n")
		fmt.Fprintf(os.Stderr, "  - uncordon_node          Mark node as schedulable\n")
		fmt.Fprintf(os.Stderr, "  - diagnose_node          Diagnose node issues\n")
		fmt.Fprintf(os.Stderr, "  - find_node_in_clusters  Find node across all clusters\n\n")
		fmt.Fprintf(os.Stderr, "Resource Queries:\n")
		fmt.Fprintf(os.Stderr, "  - list_pods              List Pods\n")
		fmt.Fprintf(os.Stderr, "  - list_namespaces        List namespaces\n\n")
		fmt.Fprintf(os.Stderr, "Hardware Info:\n")
		fmt.Fprintf(os.Stderr, "  - k8s_hardware_info      View node hardware info\n")
		fmt.Fprintf(os.Stderr, "  - k8s_cpu_info           View CPU info\n")
		fmt.Fprintf(os.Stderr, "  - k8s_memory_info        View memory info\n")
		fmt.Fprintf(os.Stderr, "  - k8s_network_info       View network info\n\n")

		fmt.Fprintf(os.Stderr, "Environment Variables:\n")
		fmt.Fprintf(os.Stderr, "  ANTHROPIC_API_KEY        Anthropic API key (required for LLM mode)\n")
		fmt.Fprintf(os.Stderr, "  KUBECONFIG               Default kubeconfig path\n\n")
	}

	flag.StringVar(&kubeconfigFlag, "kubeconfig", "", "Path to kubeconfig (default $KUBECONFIG or ~/.kube/config)")
	flag.StringVar(&contextFlag, "context", "", "Kubernetes context to use (default: current-context in kubeconfig)")
	flag.StringVar(&once, "once", "", "Run a single query non-interactively and exit")
	flag.BoolVar(&listContexts, "list-contexts", false, "List all available contexts and exit")
	flag.StringVar(&addCluster, "add-cluster", "", "Add a cluster to config file (requires -kubeconfig and cluster name)")
	flag.StringVar(&clusterConfig, "cluster-config", "clusters.json", "Cluster config file path")
	flag.BoolVar(&multiCluster, "multi-cluster", false, "Enable multi-cluster mode")
	flag.BoolVar(&noLLM, "no-llm", false, "Disable LLM mode, use direct tool invocation")
	flag.StringVar(&toolName, "tool", "", "Tool name to execute (requires --no-llm)")
	flag.StringVar(&toolInput, "input", "{}", "Tool input as JSON string (default: {})")
	flag.Parse()

	// add-cluster mode
	if addCluster != "" {
		addClusterToConfig(kubeconfigFlag, addCluster, clusterConfig)
		return
	}

	// list-contexts mode: list all contexts and exit
	if listContexts {
		showContexts(kubeconfigFlag)
		return
	}

	ctx := context.Background()
	c := bot.Setup(ctx, bot.Options{
		Kubeconfig:    kubeconfigFlag,
		Context:       contextFlag,
		MultiCluster:  multiCluster,
		ClusterConfig: clusterConfig,
		NoLLM:         noLLM,
	})
	defer c.Stop()

	// Non-LLM mode: direct tool invocation
	if noLLM {
		if toolName != "" {
			// Invoke the specified tool directly
			runToolDirect(ctx, c, toolName, toolInput)
			return
		}

		// Non-LLM interactive mode
		fmt.Fprintln(os.Stderr, "\n──────────────────────────────────────────")
		fmt.Fprintln(os.Stderr, "k8s-cli ready (Direct Tool Mode)")
		fmt.Fprintf(os.Stderr, "%d nodes · %d tools\n", len(c.Nodes.List()), len(c.Tools.List()))
		fmt.Fprintln(os.Stderr, "Available tools:")
		for _, t := range c.Tools.List() {
			fmt.Fprintf(os.Stderr, "  - %s: %s\n", t.Name(), t.Description())
		}
		fmt.Fprint(os.Stderr, "──────────────────────────────────────────\n\n")
		fmt.Fprintln(os.Stderr, "Format: <tool_name> [json_input]")
		fmt.Fprintln(os.Stderr, "Example: list_nodes {\"filter\":\"unhealthy\"}")
		fmt.Fprint(os.Stderr, "Type 'exit' to quit\n\n")

		runDirectMode(ctx, c)
		return
	}

	// LLM mode
	fmt.Fprintf(os.Stderr, "\n──────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "k8s-cli ready · %d nodes · %d tools\n", len(c.Nodes.List()), len(c.Tools.List()))
	fmt.Fprintf(os.Stderr, "Available tools: ")
	names := make([]string, 0, len(c.Tools.List()))
	for _, t := range c.Tools.List() {
		names = append(names, t.Name())
	}
	fmt.Fprintf(os.Stderr, "%s\n", strings.Join(names, ", "))
	fmt.Fprintf(os.Stderr, "──────────────────────────────────────────\n\n")

	if once != "" {
		runOne(c, once)
		return
	}

	// REPL
	fmt.Fprintln(os.Stderr, "Interactive mode — type a question and press Enter; 'exit' to quit.")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     "/tmp/.k8s-inspect-history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		Stderr:          os.Stderr,
	})
	if err != nil {
		log.Fatalf("readline init: %v", err)
	}
	defer rl.Close()

	for {
		fmt.Fprintln(os.Stderr) // blank line before each prompt
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				break
			} else {
				continue
			}
		} else if err == io.EOF {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}
		runOne(c, line)
	}
}

func runOne(c *bot.Components, q string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Minute)
	defer cancel()
	start := time.Now()
	reply, err := c.LLM.Run(ctx, q)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("❌ error: %v", err)
		return
	}
	fmt.Println()
	fmt.Println(reply)
	fmt.Fprintf(os.Stderr, "\n[took %s]\n", elapsed.Round(10*time.Millisecond))
}

func showContexts(kubeconfig string) {
	kcfg := bot.EnvOr("KUBECONFIG", kubeconfig)
	if kcfg == "" {
		if home, err := os.UserHomeDir(); err == nil {
			kcfg = home + "/.kube/config"
		}
	}

	contexts, err := bot.ListContexts(kcfg)
	if err != nil {
		log.Fatalf("list contexts: %v", err)
	}

	fmt.Println("Available contexts:")
	for _, ctx := range contexts {
		marker := " "
		if ctx.Current {
			marker = "*"
		}
		fmt.Printf("%s %s (cluster: %s)\n", marker, ctx.Name, ctx.Cluster)
	}
}

func addClusterToConfig(kubeconfigPath, name, configFile string) {
	// Validate parameters
	if kubeconfigPath == "" {
		log.Fatal("❌ --kubeconfig is required")
	}
	if name == "" {
		log.Fatal("❌ --add-cluster requires a cluster name")
	}

	// Check kubeconfig file exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		log.Fatalf("❌ kubeconfig file not found: %s", kubeconfigPath)
	}

	// Read kubeconfig file content
	kubeconfigContent, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		log.Fatalf("❌ Failed to read kubeconfig: %v", err)
	}

	// Add cluster using AddClusterFromKubeconfig (saves kubeconfig and updates config)
	savedPath, contextName, err := cluster.AddClusterFromKubeconfig(configFile, name, kubeconfigContent)
	if err != nil {
		log.Fatalf("❌ Failed to add cluster: %v", err)
	}

	// Load config to get total cluster count
	cfg, err := cluster.LoadConfig(configFile)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	fmt.Println("\n✅ Cluster added successfully!")
	fmt.Println("\n📋 Cluster Details:")
	fmt.Printf("  Name:           %s\n", name)
	fmt.Printf("  Context:        %s\n", contextName)
	fmt.Printf("  Source:         %s\n", kubeconfigPath)
	fmt.Printf("  Saved to:       %s\n", savedPath)
	fmt.Printf("\n📝 Config saved to: %s\n", configFile)
	fmt.Printf("📊 Total clusters:  %d\n", len(cfg.Clusters))

	fmt.Println("\n📖 Next steps:")
	fmt.Println("1. Start in multi-cluster mode:")
	fmt.Printf("   ./bin/cli --multi-cluster --cluster-config %s\n", configFile)
	fmt.Println("\n2. Or add more clusters:")
	fmt.Printf("   ./bin/cli --add-cluster <name> --kubeconfig <path> --cluster-config %s\n", configFile)
}

// runToolDirect invokes a specific tool directly and prints the result.
func runToolDirect(ctx context.Context, c *bot.Components, toolName, inputJSON string) {
	// Handle the help command
	if toolName == "help" {
		// Try to extract a tool name from inputJSON
		specificTool := strings.Trim(inputJSON, `"{} `)
		if specificTool != "" && specificTool != "{}" {
			// help <tool_name> — show detailed info for that tool
			fmt.Println(showToolHelpCLI(c, specificTool))
		} else {
			// help — show all tools list
			fmt.Println(showAllToolsCLI(c))
		}
		return
	}

	// Find the tool
	var foundTool tool.Tool
	for _, t := range c.Tools.List() {
		if t.Name() == toolName {
			foundTool = t
			break
		}
	}

	if foundTool == nil {
		log.Fatalf("❌ Tool not found: %s\n\nType 'help' to see all available tools", toolName)
	}

	// Parse input
	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		log.Fatalf("❌ Invalid JSON input: %v", err)
	}

	// Execute the tool
	result, err := foundTool.Execute(ctx, input)
	if err != nil {
		log.Fatalf("❌ Tool execution failed: %v", err)
	}

	// Print result
	fmt.Println(result)
}

// runDirectMode runs the interactive direct tool invocation mode.
func runDirectMode(ctx context.Context, c *bot.Components) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "tool> ",
		HistoryFile:     "/tmp/.k8s-inspect-direct-history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		Stderr:          os.Stderr,
	})
	if err != nil {
		log.Fatalf("readline init: %v", err)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				break
			} else {
				continue
			}
		} else if err == io.EOF {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		// Parse command: <tool_name> [json_input]
		parts := strings.SplitN(line, " ", 2)
		toolName := parts[0]
		inputJSON := "{}"
		if len(parts) > 1 {
			inputJSON = strings.TrimSpace(parts[1])
		}

		// Handle help command
		if toolName == "help" {
			if len(parts) > 1 {
				// help <tool_name>
				fmt.Println(showToolHelpCLI(c, strings.TrimSpace(parts[1])))
			} else {
				// help
				fmt.Println(showAllToolsCLI(c))
			}
			continue
		}

		// Find the tool
		var foundTool tool.Tool
		for _, t := range c.Tools.List() {
			if t.Name() == toolName {
				foundTool = t
				break
			}
		}

		if foundTool == nil {
			fmt.Fprintf(os.Stderr, "❌ Tool not found: %s\n", toolName)
			fmt.Fprintln(os.Stderr, "\nType 'help' to see all available tools")
			continue
		}

		// Parse input
		var input map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Invalid JSON input: %v\n", err)
			continue
		}

		// Execute the tool
		start := time.Now()
		result, err := foundTool.Execute(ctx, input)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			continue
		}

		// Print result
		fmt.Println(result)
		fmt.Fprintf(os.Stderr, "\n[took %s]\n\n", elapsed.Round(10*time.Millisecond))
	}
}

// showAllToolsCLI returns a formatted list of all available tools grouped by category.
func showAllToolsCLI(c *bot.Components) string {
	var result strings.Builder
	result.WriteString("📚 Available Tools\n\n")
	result.WriteString("Usage: <tool_name> [json_input]\n")
	result.WriteString("Tool details: help <tool_name>\n\n")

	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("🌐 Cluster Management\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range c.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "cluster") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("🖥️  Node Management\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range c.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "node") || strings.Contains(name, "cordon") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("📦 Resource Queries\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range c.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "pod") || strings.Contains(name, "namespace") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("═══════════════════════════════════\n")
	result.WriteString("🔧 Hardware Info\n")
	result.WriteString("═══════════════════════════════════\n")
	for _, t := range c.Tools.List() {
		name := t.Name()
		if strings.Contains(name, "k8s_") {
			result.WriteString(fmt.Sprintf("  • %s\n", name))
		}
	}

	result.WriteString("💡 Tip: type 'help <tool_name>' for detailed parameter info\n")
	result.WriteString("   Example: help list_nodes\n")

	return result.String()
}

// showToolHelpCLI returns detailed help for a specific tool.
func showToolHelpCLI(c *bot.Components, toolName string) string {
	// Find the tool
	var foundTool tool.Tool
	for _, t := range c.Tools.List() {
		if t.Name() == toolName {
			foundTool = t
			break
		}
	}

	if foundTool == nil {
		return fmt.Sprintf("❌ Tool not found: %s\n\nType 'help' to see all available tools", toolName)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📖 Tool details: %s\n\n", toolName))
	result.WriteString(fmt.Sprintf("Description:\n  %s\n\n", foundTool.Description()))

	schema := foundTool.InputSchema()
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		result.WriteString("Parameters:\n")
		for paramName, paramSchema := range props {
			if ps, ok := paramSchema.(map[string]any); ok {
				result.WriteString(fmt.Sprintf("  • %s", paramName))

				if paramType, ok := ps["type"].(string); ok {
					result.WriteString(fmt.Sprintf(" (%s)", paramType))
				}

				if required, ok := schema["required"].([]any); ok {
					isRequired := false
					for _, r := range required {
						if r.(string) == paramName {
							isRequired = true
							break
						}
					}
					if isRequired {
						result.WriteString(" [required]")
					} else {
						result.WriteString(" [optional]")
					}
				} else {
					result.WriteString(" [optional]")
				}

				result.WriteString("\n")

				if desc, ok := ps["description"].(string); ok {
					result.WriteString(fmt.Sprintf("    %s\n", desc))
				}

				if enum, ok := ps["enum"].([]any); ok {
					result.WriteString("    Values: ")
					enumStrs := make([]string, len(enum))
					for i, e := range enum {
						enumStrs[i] = fmt.Sprintf("%v", e)
					}
					result.WriteString(strings.Join(enumStrs, ", "))
					result.WriteString("\n")
				}

				result.WriteString("\n")
			}
		}
	} else {
		result.WriteString("Parameters: none required\n\n")
	}

	result.WriteString("Examples:\n")
	switch toolName {
	case "list_nodes":
		result.WriteString("  # List all nodes\n")
		result.WriteString("  list_nodes\n\n")
		result.WriteString("  # List unhealthy nodes only\n")
		result.WriteString("  list_nodes {\"filter\":\"unhealthy\"}\n\n")
		result.WriteString("  # List healthy nodes only\n")
		result.WriteString("  list_nodes {\"filter\":\"healthy\"}\n")
	case "switch_cluster":
		result.WriteString("  switch_cluster {\"cluster\":\"prod\"}\n")
	case "node_status":
		result.WriteString("  node_status {\"node\":\"master-01\"}\n")
		result.WriteString("  node_status {\"node\":\"10.1.1.83\"}\n")
	case "list_pods":
		result.WriteString("  # List pods in all namespaces\n")
		result.WriteString("  list_pods {\"namespace\":\"all\"}\n\n")
		result.WriteString("  # List pods in a specific namespace\n")
		result.WriteString("  list_pods {\"namespace\":\"default\"}\n\n")
		result.WriteString("  # List pods on a specific node\n")
		result.WriteString("  list_pods {\"namespace\":\"all\",\"field_selector\":\"spec.nodeName=master-01\"}\n")
	case "cordon_node":
		result.WriteString("  cordon_node {\"name\":\"master-01\"}\n")
	case "uncordon_node":
		result.WriteString("  uncordon_node {\"name\":\"master-01\"}\n")
	case "diagnose_node":
		result.WriteString("  diagnose_node {\"node\":\"master-01\"}\n")
		result.WriteString("  diagnose_node {\"node\":\"10.1.1.83\"}\n")
	case "k8s_hardware_info", "k8s_cpu_info", "k8s_memory_info", "k8s_network_info":
		result.WriteString(fmt.Sprintf("  %s {\"node\":\"master-01\"}\n", toolName))
		result.WriteString(fmt.Sprintf("  %s {\"node\":\"10.1.1.83\"}\n", toolName))
	default:
		result.WriteString(fmt.Sprintf("  %s\n", toolName))
	}

	return result.String()
}

