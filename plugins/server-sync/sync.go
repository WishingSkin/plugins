package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WishingSkin/plugins/shared/hostclient"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxSyncLogs     = 50
	deliveryTimeout = 4 * time.Second
)

var characterNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

type syncConfig struct {
	TargetType    string
	WebhookURL    string
	WebhookSecret string
	KookChannelID string
	EnabledEvents []string
	ServerAPIKey  string
}

type syncStats struct {
	Sent   int64 `json:"sent"`
	Failed int64 `json:"failed"`
}

type syncLog struct {
	Timestamp int64  `json:"ts"`
	Event     string `json:"event"`
	Target    string `json:"target"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	cfg := parseSyncConfig(req.Settings)
	switch req.Kind {
	case "event":
		return handleEvent(ctx, cfg, req)
	case "route":
		return handleRoute(ctx, cfg, req), nil
	default:
		return errResp(400, "unsupported request kind"), nil
	}
}

func parseSyncConfig(settings map[string]any) syncConfig {
	cfg := syncConfig{TargetType: "generic"}
	if settings == nil {
		return cfg
	}
	if t := strings.ToLower(settingStr(settings, "target_type")); t == "discord" || t == "kook" {
		cfg.TargetType = t
	}
	cfg.WebhookURL = settingStr(settings, "webhook_url")
	cfg.WebhookSecret = settingStr(settings, "webhook_secret")
	cfg.KookChannelID = settingStr(settings, "kook_channel_id")
	cfg.ServerAPIKey = settingStr(settings, "server_api_key")
	if rawEv := settingStr(settings, "enabled_events"); rawEv != "" {
		clean := strings.NewReplacer("，", ",", "\r\n", ",", "\n", ",").Replace(rawEv)
		for part := range strings.SplitSeq(clean, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !slices.Contains(cfg.EnabledEvents, part) {
				cfg.EnabledEvents = append(cfg.EnabledEvents, part)
			}
		}
	}
	return cfg
}

func settingStr(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return strings.TrimSpace(v)
}

func handleEvent(ctx context.Context, cfg syncConfig, req sdk.RuntimeRequest) (sdk.RuntimeResponse, error) {
	if req.Event == nil {
		return errResp(400, "missing event payload"), nil
	}
	ev := *req.Event
	if cfg.WebhookURL == "" || (len(cfg.EnabledEvents) > 0 && !slices.Contains(cfg.EnabledEvents, ev.Name)) {
		return sdk.RuntimeResponse{Status: 200, Body: mustJSON(map[string]bool{"skipped": true})}, nil
	}

	var userInfo *hostclient.UserSummary
	if ev.UserID > 0 {
		if hc, err := hostclient.FromEnv(); err == nil {
			if summary, err := hc.UserSummary(ctx, ev.UserID); err == nil {
				userInfo = &summary
			}
		}
	}

	ok, code, msg := deliverNotification(ctx, cfg, ev, userInfo)
	stats, logs := loadSyncState(req.Data)
	if ok {
		stats.Sent++
	} else {
		stats.Failed++
	}
	logs = append(logs, syncLog{
		Timestamp: time.Now().Unix(),
		Event:     ev.Name,
		Target:    cfg.TargetType,
		OK:        ok,
		Status:    code,
		Message:   msg,
	})
	if len(logs) > maxSyncLogs {
		logs = logs[len(logs)-maxSyncLogs:]
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(map[string]any{"ok": ok, "statusCode": code, "message": msg}),
		Writes: map[string]json.RawMessage{
			"sync_stats": mustJSON(stats),
			"sync_logs":  mustJSON(logs),
		},
	}, nil
}

func handleRoute(ctx context.Context, cfg syncConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	switch {
	case req.Method == http.MethodPost && req.Path == "/query":
		return routeQuery(ctx, cfg, req)
	case req.Method == http.MethodGet && req.Path == "/status":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限")
		}
		stats, logs := loadSyncState(req.Data)
		slices.Reverse(logs)
		return sdk.RuntimeResponse{
			Status: 200,
			Body: mustJSON(map[string]any{
				"targetType":        cfg.TargetType,
				"webhookConfigured": cfg.WebhookURL != "",
				"queryConfigured":   cfg.ServerAPIKey != "",
				"enabledEvents":     cfg.EnabledEvents,
				"stats":             stats,
				"logs":              logs,
			}),
		}
	case req.Method == http.MethodPost && req.Path == "/test-notify":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限")
		}
		if cfg.WebhookURL == "" {
			return errResp(400, "请先保存有效的推送地址")
		}
		ev := sdk.Event{Name: "server_sync.test", UserID: req.Principal.ID, ResourceID: "test-notification"}
		ok, code, msg := deliverNotification(ctx, cfg, ev, nil)
		stats, logs := loadSyncState(req.Data)
		if ok {
			stats.Sent++
		} else {
			stats.Failed++
		}
		logs = append(logs, syncLog{
			Timestamp: time.Now().Unix(),
			Event:     ev.Name,
			Target:    cfg.TargetType,
			OK:        ok,
			Status:    code,
			Message:   msg,
		})
		if len(logs) > maxSyncLogs {
			logs = logs[len(logs)-maxSyncLogs:]
		}
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]any{"ok": ok, "statusCode": code, "message": msg}),
			Writes: map[string]json.RawMessage{
				"sync_stats": mustJSON(stats),
				"sync_logs":  mustJSON(logs),
			},
		}
	case req.Method == http.MethodPost && req.Path == "/clear-logs":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限")
		}
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]bool{"cleared": true}),
			Writes: map[string]json.RawMessage{
				"sync_stats": mustJSON(syncStats{}),
				"sync_logs":  mustJSON([]syncLog{}),
			},
		}
	default:
		return errResp(404, "not found")
	}
}

func routeQuery(ctx context.Context, cfg syncConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	if cfg.ServerAPIKey == "" {
		return errResp(503, "server query API key not configured")
	}
	var body struct {
		APIKey string `json:"apiKey"`
		Name   string `json:"name"`
		UserID int64  `json:"userId"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "invalid JSON body")
	}
	if subtle.ConstantTimeCompare([]byte(body.APIKey), []byte(cfg.ServerAPIKey)) != 1 {
		return errResp(401, "invalid apiKey")
	}
	hc, err := hostclient.FromEnv()
	if err != nil {
		return errResp(503, "host api unavailable")
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		if !characterNamePattern.MatchString(name) {
			return errResp(400, "invalid character name")
		}
		ch, err := hc.CharacterByName(ctx, name)
		if err != nil {
			var apiErr *hostclient.APIError
			if errors.As(err, &apiErr) {
				return errResp(apiErr.Status, apiErr.Message)
			}
			return errResp(503, "query failed")
		}
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]any{"character": ch}),
		}
	}
	if body.UserID > 0 {
		u, err := hc.UserSummary(ctx, body.UserID)
		if err != nil {
			var apiErr *hostclient.APIError
			if errors.As(err, &apiErr) {
				return errResp(apiErr.Status, apiErr.Message)
			}
			return errResp(503, "query failed")
		}
		// Never expose email or private secrets in public server query output.
		sanitized := map[string]any{
			"id":                u.ID,
			"nickname":          u.Nickname,
			"banned":            u.Banned,
			"verifiedMinecraft": u.VerifiedMinecraft,
			"mcName":            u.MCName,
			"mcUuid":            u.MCUUID,
			"characters":        u.Characters,
		}
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]any{"user": sanitized}),
		}
	}
	return errResp(400, "name or userId is required")
}

func deliverNotification(ctx context.Context, cfg syncConfig, ev sdk.Event, userInfo *hostclient.UserSummary) (bool, int, string) {
	client, err := newSafeWebhookClient(cfg.WebhookURL)
	if err != nil {
		return false, 0, "推送地址无效或不符合安全规范"
	}

	var payload []byte
	headers := map[string]string{"Content-Type": "application/json"}

	actorText := fmt.Sprintf("User #%d", ev.UserID)
	if userInfo != nil {
		if userInfo.Nickname != "" {
			actorText = userInfo.Nickname + " (#" + strconv.FormatInt(ev.UserID, 10) + ")"
		}
		if userInfo.VerifiedMinecraft && userInfo.MCName != "" {
			actorText += " [MC: " + userInfo.MCName + "]"
		}
	}

	switch cfg.TargetType {
	case "discord":
		payload, _ = json.Marshal(map[string]any{
			"username": "WishingSkin",
			"embeds": []map[string]any{{
				"title":       "WishingSkin 事件: " + ev.Name,
				"description": fmt.Sprintf("用户: %s\n资源标识: %s", actorText, ev.ResourceID),
				"color":       0x2563eb,
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
			}},
		})
	case "kook":
		if cfg.KookChannelID == "" {
			return false, 0, "KOOK 模式须配置目标频道 ID"
		}
		if cfg.WebhookSecret != "" {
			headers["Authorization"] = "Bot " + cfg.WebhookSecret
		}
		payload, _ = json.Marshal(map[string]any{
			"type":      1,
			"target_id": cfg.KookChannelID,
			"content":   fmt.Sprintf("[WishingSkin] %s | %s | 资源: %s", ev.Name, actorText, ev.ResourceID),
		})
	default:
		body := map[string]any{
			"event":      ev.Name,
			"userId":     ev.UserID,
			"resourceId": ev.ResourceID,
			"timestamp":  time.Now().Unix(),
		}
		if userInfo != nil {
			body["user"] = map[string]any{
				"nickname":          userInfo.Nickname,
				"verifiedMinecraft": userInfo.VerifiedMinecraft,
				"mcName":            userInfo.MCName,
				"mcUuid":            userInfo.MCUUID,
			}
		}
		payload, _ = json.Marshal(body)
		if cfg.WebhookSecret != "" {
			mac := hmac.New(sha256.New, []byte(cfg.WebhookSecret))
			_, _ = mac.Write(payload)
			headers["X-WishingSkin-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return false, 0, "无法构建推送请求"
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, "网络超时或目标不可达"
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.StatusCode, fmt.Sprintf("目标返回状态码 %d", resp.StatusCode)
	}
	return true, resp.StatusCode, "投递成功"
}

func newSafeWebhookClient(rawURL string) (*http.Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || len(rawURL) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid webhook URL")
	}
	host := strings.ToLower(u.Hostname())
	isLoopbackHost := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost) {
		return nil, errors.New("must use https or loopback http")
	}
	dialer := &net.Dialer{Timeout: deliveryTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, p, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, h)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("dns lookup failed")
			}
			for _, ipAddr := range ips {
				ip := ipAddr.IP
				if isLoopbackHost {
					if !ip.IsLoopback() {
						return nil, errors.New("loopback hostname resolved outside loopback")
					}
				} else if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
					return nil, errors.New("refusing private or link-local target")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), p))
		},
	}
	return &http.Client{
		Timeout:   deliveryTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func loadSyncState(data map[string]json.RawMessage) (syncStats, []syncLog) {
	var stats syncStats
	var logs []syncLog
	if data != nil {
		if raw, ok := data["sync_stats"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &stats)
		}
		if raw, ok := data["sync_logs"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &logs)
		}
	}
	if logs == nil {
		logs = []syncLog{}
	}
	return stats, logs
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
