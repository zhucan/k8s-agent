package alert

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8s-inspect/internal/cluster"
	larkutil "github.com/k8s-inspect/internal/lark"
)

type Config struct {
	ChatID        string   // Alert group Chat ID
	MentionIDs    []string // Open IDs to @mention
	MentionEmails []string // Email addresses to @mention (resolved to Open IDs at startup)
	CronExpr      string   // Cron expression (currently only HH:MM is used)
	LarkClient    *lark.Client
	ClusterMgr    *cluster.Manager
}

type unhealthyNode struct {
	Name        string
	IP          string
	Roles       []string
	Ready       bool
	Schedulable bool
}

type clusterResult struct {
	Name           string
	Total          int
	UnhealthyNodes []unhealthyNode
	Error          string
}

func Start(ctx context.Context, cfg Config) {
	if cfg.ChatID == "" {
		log.Println("[alert] LARK_ALERT_CHAT_ID not set, skipping scheduled health check")
		return
	}

	// Resolve email addresses to Open IDs and merge into MentionIDs
	if len(cfg.MentionEmails) > 0 {
		resolved := resolveEmails(ctx, cfg.LarkClient, cfg.MentionEmails)
		cfg.MentionIDs = append(cfg.MentionIDs, resolved...)
	}

	hour, minute := parseCron(cfg.CronExpr)
	log.Printf("[alert] Scheduled health check at %02d:%02d daily, chat=%s, mentions=%v",
		hour, minute, cfg.ChatID, cfg.MentionIDs)

	go func() {
		for {
			next := nextRunTime(hour, minute)
			log.Printf("[alert] Next health check at %s", next.Format("2006-01-02 15:04:05"))

			select {
			case <-time.After(time.Until(next)):
				runHealthCheck(ctx, cfg)
			case <-ctx.Done():
				log.Println("[alert] Scheduler stopped")
				return
			}
		}
	}()
}

func runHealthCheck(ctx context.Context, cfg Config) {
	log.Println("[alert] Running scheduled health check across all clusters...")

	clusters := cfg.ClusterMgr.List()
	if len(clusters) == 0 {
		log.Println("[alert] No clusters configured, skipping")
		return
	}

	var results []clusterResult
	hasUnhealthy := false

	for _, info := range clusters {
		c, err := cfg.ClusterMgr.Get(info.Name)
		if err != nil {
			results = append(results, clusterResult{Name: info.Name, Error: err.Error()})
			continue
		}

		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		nodeList, err := c.CS.CoreV1().Nodes().List(checkCtx, metav1.ListOptions{})
		cancel()

		if err != nil {
			results = append(results, clusterResult{Name: info.Name, Error: err.Error()})
			continue
		}

		cr := clusterResult{Name: info.Name, Total: len(nodeList.Items)}

		for i := range nodeList.Items {
			node := &nodeList.Items[i]

			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady {
					ready = (cond.Status == corev1.ConditionTrue)
					break
				}
			}
			schedulable := !node.Spec.Unschedulable
			if ready && schedulable {
				continue
			}

			var ip string
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					ip = addr.Address
					break
				}
			}

			var roles []string
			for label := range node.Labels {
				if strings.HasPrefix(label, "node-role.kubernetes.io/") {
					role := strings.TrimPrefix(label, "node-role.kubernetes.io/")
					if role != "" {
						roles = append(roles, role)
					}
				}
			}

			cr.UnhealthyNodes = append(cr.UnhealthyNodes, unhealthyNode{
				Name:        node.Name,
				IP:          ip,
				Roles:       roles,
				Ready:       ready,
				Schedulable: schedulable,
			})
		}

		if len(cr.UnhealthyNodes) > 0 {
			hasUnhealthy = true
		}
		results = append(results, cr)
	}

	card := buildAlertCard(results, hasUnhealthy, cfg.MentionIDs)
	if err := sendMessage(ctx, cfg.LarkClient, cfg.ChatID, card); err != nil {
		log.Printf("[alert] Failed to send alert: %v", err)
	} else {
		log.Println("[alert] Health check alert sent successfully")
	}
}

func buildAlertCard(results []clusterResult, hasUnhealthy bool, mentionIDs []string) string {
	builder := larkutil.NewCardBuilder()

	if hasUnhealthy {
		builder.SetHeader("Cluster Node Health Check — Issues Found", "red")
	} else {
		builder.SetHeader("Cluster Node Health Check — All Healthy", "green")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Check Time:** %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**Clusters:** %d\n", len(results)))
	builder.AddMarkdown(sb.String())
	builder.AddDivider()

	for _, cr := range results {
		var section strings.Builder

		if cr.Error != "" {
			section.WriteString(fmt.Sprintf("**Cluster: %s** ❌ Connection failed\n", cr.Name))
			section.WriteString(fmt.Sprintf("Error: %s\n", cr.Error))
			builder.AddMarkdown(section.String())
			builder.AddDivider()
			continue
		}

		if len(cr.UnhealthyNodes) == 0 {
			section.WriteString(fmt.Sprintf("**Cluster: %s** ✅ All healthy (%d nodes)\n", cr.Name, cr.Total))
			builder.AddMarkdown(section.String())
			continue
		}

		section.WriteString(fmt.Sprintf("**Cluster: %s** ⚠️ %d unhealthy node(s) out of %d\n\n",
			cr.Name, len(cr.UnhealthyNodes), cr.Total))

		for _, n := range cr.UnhealthyNodes {
			status := ""
			if !n.Ready {
				status = "🔴 NotReady"
			} else if !n.Schedulable {
				status = "🟡 Unschedulable"
			}
			roleStr := ""
			if len(n.Roles) > 0 {
				roleStr = fmt.Sprintf(" [%s]", strings.Join(n.Roles, ","))
			}
			section.WriteString(fmt.Sprintf("• **%s** (%s)%s — %s\n", n.Name, n.IP, roleStr, status))
		}

		builder.AddMarkdown(section.String())
		builder.AddDivider()
	}

	if len(mentionIDs) > 0 {
		var mentionParts []string
		for i, openID := range mentionIDs {
			mentionParts = append(mentionParts, fmt.Sprintf("<at id=%s></at>", openID))
			_ = i
		}
		builder.AddMarkdown("**Attention:** " + strings.Join(mentionParts, " ") + " please review")
	}

	builder.AddNote(fmt.Sprintf("Auto health check by k8s-agent | %s", time.Now().Format("2006-01-02")))

	content, err := builder.Build()
	if err != nil {
		log.Printf("[alert] Failed to build card: %v", err)
		return ""
	}
	return content
}

func sendMessage(ctx context.Context, client *lark.Client, chatID, cardContent string) error {
	_, err := client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(cardContent).
			Build()).
		Build())
	return err
}

func parseCron(expr string) (hour, minute int) {
	hour, minute = 10, 0
	if expr == "" {
		return
	}
	parts := strings.Fields(expr)
	if len(parts) >= 2 {
		fmt.Sscanf(parts[0], "%d", &minute)
		fmt.Sscanf(parts[1], "%d", &hour)
	}
	return
}

func nextRunTime(hour, minute int) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func resolveEmails(ctx context.Context, client *lark.Client, emails []string) []string {
	resp, err := client.Contact.V3.User.BatchGetId(ctx,
		larkcontact.NewBatchGetIdUserReqBuilder().
			UserIdType("open_id").
			Body(larkcontact.NewBatchGetIdUserReqBodyBuilder().
				Emails(emails).
				Build()).
			Build())

	if err != nil {
		log.Printf("[alert] Failed to resolve emails to open_id: %v", err)
		return nil
	}
	if !resp.Success() {
		log.Printf("[alert] Failed to resolve emails: code=%d msg=%s", resp.Code, resp.Msg)
		return nil
	}

	var openIDs []string
	resolved := make(map[string]string)
	for _, u := range resp.Data.UserList {
		if u.UserId != nil && *u.UserId != "" {
			email := ""
			if u.Email != nil {
				email = *u.Email
			}
			resolved[email] = *u.UserId
			openIDs = append(openIDs, *u.UserId)
		}
	}

	for _, email := range emails {
		if id, ok := resolved[email]; ok {
			log.Printf("[alert] Resolved email %s -> %s", email, id)
		} else {
			log.Printf("[alert] Warning: could not resolve email %s to open_id", email)
		}
	}

	return openIDs
}
