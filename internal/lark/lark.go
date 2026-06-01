// Package lark provides Feishu (Lark) self-built app webhook integration and OpenAPI calls.
//
// Only the subset required for the MVP is implemented:
//   - URL verification (Feishu sends a challenge when the callback URL is first configured)
//   - Encrypted event decryption (AES-256-CBC)
//   - im.message.receive_v1 event parsing
//   - tenant_access_token retrieval (with built-in caching)
//   - Reply message (reply_in_thread)
//
// Feishu Open Platform docs: https://open.feishu.cn/document/
package lark

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config holds self-built app credentials and encryption parameters from the Feishu developer console.
type Config struct {
	AppID         string
	AppSecret     string
	EncryptKey    string // Encrypt Key configured when event encryption is enabled
	VerifyToken   string // Verification Token on the app's "Event Subscriptions" tab
}

// =============== Event encryption / decryption ===============

// decryptEvent decrypts a Feishu encrypted event body: { "encrypt": "<base64>" }
// Algorithm: AES-256-CBC, key = sha256(EncryptKey), iv = cipher[:16], payload = cipher[16:]
func decryptEvent(encryptKey, encrypted string) ([]byte, error) {
	buf, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if len(buf) < aes.BlockSize*2 {
		return nil, errors.New("ciphertext too short")
	}
	keySum := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keySum[:])
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	iv := buf[:aes.BlockSize]
	ct := buf[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext not multiple of block size")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	// PKCS7 unpad
	if len(pt) == 0 {
		return nil, errors.New("plaintext empty")
	}
	padLen := int(pt[len(pt)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(pt) {
		return nil, errors.New("invalid padding")
	}
	return pt[:len(pt)-padLen], nil
}

// =============== Event schema ===============

// EncryptedPayload is the body Feishu sends when event encryption is enabled.
type EncryptedPayload struct {
	Encrypt string `json:"encrypt"`
}

// URLVerification is the challenge body sent in plain mode.
type URLVerification struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
}

// EventV2 is the Feishu event v2 schema.
type EventV2 struct {
	Schema string `json:"schema"`
	Header struct {
		EventID    string `json:"event_id"`
		EventType  string `json:"event_type"`
		AppID      string `json:"app_id"`
		TenantKey  string `json:"tenant_key"`
		Token      string `json:"token"`
		CreateTime string `json:"create_time"`
	} `json:"header"`
	Event json.RawMessage `json:"event"`
}

// MessageReceiveEvent is the body of an im.message.receive_v1 event.
type MessageReceiveEvent struct {
	Sender struct {
		SenderID struct {
			OpenID string `json:"open_id"`
		} `json:"sender_id"`
		SenderType string `json:"sender_type"`
		TenantKey  string `json:"tenant_key"`
	} `json:"sender"`
	Message struct {
		MessageID   string `json:"message_id"`
		ChatID      string `json:"chat_id"`
		ChatType    string `json:"chat_type"` // p2p | group
		MessageType string `json:"message_type"`
		Content     string `json:"content"` // JSON string, e.g. {"text":"@_user_1 hello"}
		Mentions    []struct {
			Key    string `json:"key"`
			ID     struct {
				OpenID string `json:"open_id"`
			} `json:"id"`
			Name string `json:"name"`
		} `json:"mentions"`
	} `json:"message"`
}

// =============== Webhook handler ===============

// EventHandler is the callback invoked when a message event arrives.
type EventHandler func(ctx Context)

// Context is a simplified context passed to the event handler.
type Context struct {
	Req          *http.Request
	MessageID    string
	ChatID       string
	ChatType     string
	SenderOpenID string
	Text         string // plain text with @bot mention stripped
	MessageType  string // message type: text, file, image, etc.
	FileKey      string // file_key for file messages
}

// Webhook implements http.Handler and receives Feishu event callbacks.
type Webhook struct {
	cfg   Config
	OnMsg EventHandler
}

func NewWebhook(cfg Config, onMsg EventHandler) *Webhook {
	return &Webhook{cfg: cfg, OnMsg: onMsg}
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, "read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Debug log: print request body
	fmt.Printf("[webhook] Request body: %s\n", string(body))

	plain := body

	// Encrypted mode: decrypt first
	var enc EncryptedPayload
	if err := json.Unmarshal(body, &enc); err == nil && enc.Encrypt != "" {
		if w.cfg.EncryptKey == "" {
			http.Error(rw, "encrypted body but no encrypt key configured", http.StatusBadRequest)
			return
		}
		plain, err = decryptEvent(w.cfg.EncryptKey, enc.Encrypt)
		if err != nil {
			http.Error(rw, "decrypt: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// URL verification
	var urlVerify URLVerification
	if err := json.Unmarshal(plain, &urlVerify); err == nil && urlVerify.Type == "url_verification" {
		if w.cfg.VerifyToken != "" && urlVerify.Token != w.cfg.VerifyToken {
			http.Error(rw, "verify token mismatch", http.StatusForbidden)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]string{"challenge": urlVerify.Challenge})
		return
	}

	// Event v2
	var ev EventV2
	if err := json.Unmarshal(plain, &ev); err != nil {
		http.Error(rw, "parse event: "+err.Error(), http.StatusBadRequest)
		return
	}
	if w.cfg.VerifyToken != "" && ev.Header.Token != "" && ev.Header.Token != w.cfg.VerifyToken {
		http.Error(rw, "verify token mismatch", http.StatusForbidden)
		return
	}

	switch ev.Header.EventType {
	case "im.message.receive_v1":
		var msg MessageReceiveEvent
		if err := json.Unmarshal(ev.Event, &msg); err != nil {
			http.Error(rw, "parse message event", http.StatusBadRequest)
			return
		}
		text := extractText(msg.Message.Content, msg.Message.Mentions)
		fileKey := extractFileKey(msg.Message.MessageType, msg.Message.Content)
		if w.OnMsg != nil {
			go w.OnMsg(Context{
				Req:          r,
				MessageID:    msg.Message.MessageID,
				ChatID:       msg.Message.ChatID,
				ChatType:     msg.Message.ChatType,
				SenderOpenID: msg.Sender.SenderID.OpenID,
				Text:         text,
				MessageType:  msg.Message.MessageType,
				FileKey:      fileKey,
			})
		}
	default:
		// Ignore other event types
	}

	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte(`{"code":0}`))
}

// extractText extracts the text field from message.content (a JSON string) and strips @bot mentions.
// content looks like {"text":"@_user_1 check disk on 192.168.1.10"}
func extractText(contentJSON string, mentions []struct {
	Key string `json:"key"`
	ID  struct {
		OpenID string `json:"open_id"`
	} `json:"id"`
	Name string `json:"name"`
}) string {
	var c struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		return contentJSON
	}
	t := c.Text
	for _, m := range mentions {
		// Feishu @mention placeholder is @_user_<index> — strip it
		t = strings.ReplaceAll(t, "@"+m.Key, "")
	}
	return strings.TrimSpace(t)
}

// extractFileKey extracts the file_key from a file message.
// File message content looks like {"file_key":"xxx"}
func extractFileKey(messageType, contentJSON string) string {
	if messageType != "file" {
		return ""
	}
	var c struct {
		FileKey string `json:"file_key"`
	}
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		return ""
	}
	return c.FileKey
}

// =============== OpenAPI client ===============

// Client calls the Feishu OpenAPI (token retrieval, message reply).
type Client struct {
	cfg  Config
	http *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}}
}

// tenantToken returns the tenant_access_token, caching it until 60s before expiry.
func (c *Client) tenantToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-60*time.Second)) {
		return c.token, nil
	}
	reqBody, _ := json.Marshal(map[string]string{
		"app_id":     c.cfg.AppID,
		"app_secret": c.cfg.AppSecret,
	})
	req, _ := http.NewRequest(http.MethodPost,
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if out.Code != 0 {
		return "", fmt.Errorf("token api: code=%d msg=%s", out.Code, out.Msg)
	}
	c.token = out.TenantAccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(out.Expire) * time.Second)
	return c.token, nil
}

// ReplyText replies with plain text under the specified message.
// Feishu reply API: POST /open-apis/im/v1/messages/{message_id}/reply
func (c *Client) ReplyText(messageID, text string) error {
	token, err := c.tenantToken()
	if err != nil {
		return err
	}
	contentJSON, _ := json.Marshal(map[string]string{"text": text})
	body, _ := json.Marshal(map[string]string{
		"msg_type": "text",
		"content":  string(contentJSON),
	})
	url := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/reply", messageID)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reply: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("reply decode: %w", err)
	}
	if out.Code != 0 {
		return fmt.Errorf("reply api: code=%d msg=%s", out.Code, out.Msg)
	}
	return nil
}

// DownloadFile downloads file content.
// Feishu file download API: GET /open-apis/im/v1/messages/{message_id}/resources/{file_key}
func (c *Client) DownloadFile(messageID, fileKey string) ([]byte, error) {
	token, err := c.tenantToken()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/resources/%s?type=file", messageID, fileKey)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download file: status=%d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
