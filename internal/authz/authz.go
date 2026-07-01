// Package authz provides caller identity propagation and permission checks
// for privileged tool operations (e.g. mutating node taints).
package authz

import (
	"context"
	"os"
	"strings"
	"sync"
)

type ctxKey struct{}

// WithUserID attaches the caller's stable user identifier (e.g. Feishu open_id)
// to the context so that downstream tools can enforce per-user permissions.
func WithUserID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// UserIDFrom returns the caller identifier previously stored with WithUserID,
// or "" if none is present.
func UserIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

var (
	allowMu       sync.RWMutex
	extraAllowed  = map[string]struct{}{} // runtime-registered open_ids (e.g. resolved from emails)
	allowlistOnce bool                    // true once any explicit allowlist is configured
)

// RegisterAllowedOpenIDs adds open_ids to the taint operation allowlist at
// runtime. Callers typically resolve these from configured emails at startup.
// Passing any ids activates enforcement even if LARK_TAINT_ALLOWED_OPENIDS is
// unset.
func RegisterAllowedOpenIDs(ids ...string) {
	allowMu.Lock()
	defer allowMu.Unlock()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		extraAllowed[id] = struct{}{}
		allowlistOnce = true
	}
}

type clusterKey struct{}

// WithClusterName attaches the current cluster name to the context so that
// tools can include it in their output (helpful in multi-cluster deployments
// where the LLM's cluster switching may be ambiguous to the caller).
func WithClusterName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, clusterKey{}, name)
}

// ClusterNameFrom returns the cluster name previously stored with
// WithClusterName, or "" if none is present.
func ClusterNameFrom(ctx context.Context) string {
	if v, ok := ctx.Value(clusterKey{}).(string); ok {
		return v
	}
	return ""
}

// TaintAllowed reports whether the caller may execute taint/untaint operations.
//
// Enforcement is on when either LARK_TAINT_ALLOWED_OPENIDS is set OR ids have
// been added via RegisterAllowedOpenIDs (typically resolved from
// LARK_TAINT_ALLOWED_EMAILS at startup). When enforcement is off, all callers
// are allowed (default for local CLI / dev usage).
func TaintAllowed(userID string) bool {
	raw := strings.TrimSpace(os.Getenv("LARK_TAINT_ALLOWED_OPENIDS"))

	allowMu.RLock()
	hasExtras := allowlistOnce
	inExtras := false
	if userID != "" {
		_, inExtras = extraAllowed[userID]
	}
	allowMu.RUnlock()

	if raw == "" && !hasExtras {
		return true
	}
	if userID == "" {
		return false
	}
	if inExtras {
		return true
	}
	for _, id := range strings.Split(raw, ",") {
		if strings.TrimSpace(id) == userID {
			return true
		}
	}
	return false
}
