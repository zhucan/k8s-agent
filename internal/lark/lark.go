// Package lark 提供飞书自建应用 webhook 接入和 OpenAPI 调用。
//
// 仅实现 MVP 必需的子集:
//   - URL 验证(应用首次配置回调地址时飞书会发挑战)
//   - 加密事件解密(AES-256-CBC)
//   - im.message.receive_v1 事件解析
//   - tenant_access_token 获取(自带缓存)
//   - 回复消息 (reply_in_thread)
//
// 飞书开放平台文档: https://open.feishu.cn/document/
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

// Config: 自建应用凭证 + 加密参数。来自飞书后台「凭证与基础信息」+「事件订阅」。
type Config struct {
	AppID         string
	AppSecret     string
	EncryptKey    string // 事件订阅启用加密时填的 Encrypt Key
	VerifyToken   string // 应用「事件订阅」页签 Verification Token
}

// =============== 事件加密 / 解密 ===============

// decryptEvent 飞书事件加密体: { "encrypt": "<base64>" }
// 算法: AES-256-CBC, key = sha256(EncryptKey), iv = cipher[:16], payload = cipher[16:]
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
	// PKCS7 去填充
	if len(pt) == 0 {
		return nil, errors.New("plaintext empty")
	}
	padLen := int(pt[len(pt)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(pt) {
		return nil, errors.New("invalid padding")
	}
	return pt[:len(pt)-padLen], nil
}

// =============== 事件 schema ===============

// EncryptedPayload: 当应用启用了加密时,飞书发来的 body 长这样
type EncryptedPayload struct {
	Encrypt string `json:"encrypt"`
}

// URLVerification: 用 plain 模式时也可能直接收到挑战
type URLVerification struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
}

// EventV2: 飞书事件 v2 schema
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

// MessageReceiveEvent: im.message.receive_v1 事件 body
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
		Content     string `json:"content"` // JSON string,如 {"text":"@_user_1 hello"}
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

// EventHandler 解析后的回调,在 message 事件到来时执行业务逻辑
type EventHandler func(ctx Context)

// Context 给 handler 用的简化上下文
type Context struct {
	Req          *http.Request
	MessageID    string
	ChatID       string
	ChatType     string
	SenderOpenID string
	Text         string // 已经去掉 @bot mention 的纯文本
	MessageType  string // 消息类型: text, file, image 等
	FileKey      string // 文件消息的 file_key
}

// Webhook: 实现 http.Handler 接收飞书事件回调
type Webhook struct {
	cfg     Config
	OnMsg   EventHandler
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

	// 调试日志：打印请求体
	fmt.Printf("[webhook] Request body: %s\n", string(body))

	plain := body

	// 加密模式:先解密
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

	// URL 验证
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

	// 事件 v2
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
		// 其它事件忽略
	}

	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte(`{"code":0}`))
}

// extractText 从 message.content(JSON 字符串)里抽出 text,并把 @bot 的 mention 替换掉
// content 形如 {"text":"@_user_1 看下192.168.1.10的磁盘"}
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
		// 飞书 @ 的占位符是 @_user_<index>,我们把它去掉
		t = strings.ReplaceAll(t, "@"+m.Key, "")
	}
	return strings.TrimSpace(t)
}

// extractFileKey 从文件消息中提取 file_key
// 文件消息的 content 形如 {"file_key":"xxx"}
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

// Client: 调飞书 OpenAPI(获取 token, 回消息)
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

// tenantToken 拿 tenant_access_token,缓存到过期前 60s
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

// ReplyText 在指定 message 下回复纯文本。
// 飞书 reply 接口: POST /open-apis/im/v1/messages/{message_id}/reply
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

// DownloadFile 下载文件内容
// 飞书文件下载接口: GET /open-apis/im/v1/messages/{message_id}/resources/{file_key}
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
