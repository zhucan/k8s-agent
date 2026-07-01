package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ResolveOpenIDsByEmail resolves a list of Feishu account emails to their
// corresponding open_ids via the tenant-scoped contact API.
//
// It requires the app to have the "contact:user.base:readonly" (or equivalent)
// scope granted. Emails that cannot be resolved are returned in the second
// slice so the caller can log them.
func ResolveOpenIDsByEmail(ctx context.Context, appID, appSecret string, emails []string) (openIDs []string, unresolved []string, err error) {
	emails = dedupNonEmpty(emails)
	if len(emails) == 0 {
		return nil, nil, nil
	}

	token, err := fetchTenantAccessToken(ctx, appID, appSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("get tenant access token: %w", err)
	}

	// The API accepts at most 50 identifiers per call.
	const chunkSize = 50
	seenOpen := make(map[string]struct{}, len(emails))
	resolved := make(map[string]bool, len(emails))

	for start := 0; start < len(emails); start += chunkSize {
		end := start + chunkSize
		if end > len(emails) {
			end = len(emails)
		}
		chunk := emails[start:end]

		ids, err := batchGetIDs(ctx, token, chunk)
		if err != nil {
			return nil, nil, err
		}
		for email, oid := range ids {
			resolved[email] = true
			if _, dup := seenOpen[oid]; dup {
				continue
			}
			seenOpen[oid] = struct{}{}
			openIDs = append(openIDs, oid)
		}
	}

	for _, e := range emails {
		if !resolved[e] {
			unresolved = append(unresolved, e)
		}
	}
	return openIDs, unresolved, nil
}

func dedupNonEmpty(xs []string) []string {
	seen := make(map[string]struct{}, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

func fetchTenantAccessToken(ctx context.Context, appID, appSecret string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("tenant_access_token api error: code=%d msg=%q", out.Code, out.Msg)
	}
	return out.TenantAccessToken, nil
}

func batchGetIDs(ctx context.Context, token string, emails []string) (map[string]string, error) {
	body, _ := json.Marshal(map[string]any{
		"emails": emails,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/contact/v3/users/batch_get_id?user_id_type=open_id",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			UserList []struct {
				Email  string `json:"email"`
				UserID string `json:"user_id"`
			} `json:"user_list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode batch_get_id response: %w (body=%s)", err, string(raw))
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("batch_get_id api error: code=%d msg=%q", out.Code, out.Msg)
	}
	result := make(map[string]string, len(out.Data.UserList))
	for _, u := range out.Data.UserList {
		if u.Email != "" && u.UserID != "" {
			result[u.Email] = u.UserID
		}
	}
	return result, nil
}
