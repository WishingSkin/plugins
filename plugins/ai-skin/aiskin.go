package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/WishingSkin/plugins/shared/hostclient"
	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxPromptRunes = 500
	maxJobsRetained = 64
	maxAuditLogs    = 50
	jobTTL          = 15 * time.Minute
)

const (
	engineStructuredLLM = "structured_llm"
	engineImageAPI      = "image_api"
	engineAsyncWorker   = "async_worker"
	engineLocalDemo     = "local_demo"
)

type aiConfig struct {
	DefaultEngine      string
	LLMApiURL          string
	LLMApiKey          string
	LLMModel           string
	ImageApiURL        string
	ImageApiKey        string
	ImageModel         string
	WorkerURL          string
	WorkerSecret       string
	AllowPrivateWorker bool
	AllowLocalFallback bool
	PointsCost         int64
	DailyLimit         int
	CooldownSeconds    int
	AllowPublicSave    bool
	Timeout            time.Duration
}

type engineAvailability struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint"`
}

type aiStats struct {
	TotalGenerated int `json:"totalGenerated"`
	TotalFailed    int `json:"totalFailed"`
	TotalSaved     int `json:"totalSaved"`
}

type aiAuditEntry struct {
	Time       string `json:"time"`
	UserID     int64  `json:"userId"`
	Engine     string `json:"engine"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Detail     string `json:"detail"`
}

type aiJob struct {
	ID                 string      `json:"jobId"`
	UserID             int64       `json:"-"`
	Engine             string      `json:"engine"`
	Model              string      `json:"model"`
	Prompt             string      `json:"prompt"`
	Status             string      `json:"status"` // queued, running, succeeded, failed
	Stage              string      `json:"stage"`
	Summary            string      `json:"summary,omitempty"`
	Recipe             *SkinRecipe `json:"recipe,omitempty"`
	PNGBase64          string      `json:"pngBase64,omitempty"`
	FrontPreviewBase64 string      `json:"frontPreviewBase64,omitempty"`
	BackPreviewBase64  string      `json:"backPreviewBase64,omitempty"`
	PointsSpent        int64       `json:"pointsSpent,omitempty"`
	Balance            string      `json:"balance,omitempty"`
	Error              string      `json:"error,omitempty"`
	DateKey            string      `json:"-"`
	CreatedAt          time.Time   `json:"-"`
	UpdatedAt          time.Time   `json:"-"`
	DurationMs         int64       `json:"durationMs,omitempty"`
	NeedsFlush         bool        `json:"-"`
	Counted            bool        `json:"-"`
}

type jobManager struct {
	mu           sync.Mutex
	hydrated     bool
	jobs         map[string]*aiJob
	activeByUser map[int64]string
	lastSubmit   map[int64]time.Time
	dailyUsage   map[string]map[int64]int
	stats        aiStats
	logs         []aiAuditEntry
	dirty        bool
}

var manager = newJobManager()

func newJobManager() *jobManager {
	return &jobManager{
		jobs:         map[string]*aiJob{},
		activeByUser: map[int64]string{},
		lastSubmit:   map[int64]time.Time{},
		dailyUsage:   map[string]map[int64]int{},
		logs:         []aiAuditEntry{},
	}
}

func resetJobManagerForTest() {
	manager = newJobManager()
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	if req.Kind != "route" {
		return errResp(400, "unsupported request"), nil
	}
	cfg := parseAIConfig(req.Settings)
	manager.hydrateLocked(req.Data)

	switch {
	case req.Method == http.MethodGet && req.Path == "/config":
		return routeGetConfig(ctx, cfg, req), nil
	case req.Method == http.MethodPost && req.Path == "/generate":
		return routePostGenerate(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/jobs":
		return routeGetJob(req), nil
	case req.Method == http.MethodPost && req.Path == "/save":
		return routePostSave(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/admin/status":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminStatus(cfg), nil
	case req.Method == http.MethodPost && req.Path == "/admin/test":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminTest(ctx, cfg, req), nil
	case req.Method == http.MethodPost && req.Path == "/admin/clear-logs":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminClearLogs(), nil
	default:
		return errResp(404, "not found"), nil
	}
}

func parseAIConfig(settings map[string]any) aiConfig {
	cfg := aiConfig{
		DailyLimit:      5,
		CooldownSeconds: 15,
		Timeout:         60 * time.Second,
	}
	if settings == nil {
		return cfg
	}
	str := func(key string) string {
		if v, ok := settings[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	cfg.DefaultEngine = strings.ToLower(str("default_engine"))
	cfg.LLMApiURL = str("llm_api_url")
	cfg.LLMApiKey = str("llm_api_key")
	cfg.LLMModel = str("llm_model")
	cfg.ImageApiURL = str("image_api_url")
	cfg.ImageApiKey = str("image_api_key")
	cfg.ImageModel = str("image_model")
	cfg.WorkerURL = str("worker_url")
	cfg.WorkerSecret = str("worker_secret")
	if v, ok := settings["allow_private_worker"].(bool); ok {
		cfg.AllowPrivateWorker = v
	}
	if v, ok := settings["allow_local_fallback"].(bool); ok {
		cfg.AllowLocalFallback = v
	}
	if v, ok := settings["allow_public_save"].(bool); ok {
		cfg.AllowPublicSave = v
	}
	if v, ok := settings["points_cost"].(float64); ok && v > 0 {
		cfg.PointsCost = int64(v)
	}
	if v, ok := settings["daily_limit"].(float64); ok && v >= 0 {
		cfg.DailyLimit = int(v)
	}
	if v, ok := settings["cooldown_seconds"].(float64); ok && v >= 0 {
		cfg.CooldownSeconds = int(v)
	}
	if v, ok := settings["timeout_seconds"].(float64); ok {
		sec := int(v)
		if sec < 10 {
			sec = 10
		} else if sec > 180 {
			sec = 180
		}
		cfg.Timeout = time.Duration(sec) * time.Second
	}
	return cfg
}

func availableEngines(cfg aiConfig) ([]engineAvailability, string) {
	list := []engineAvailability{
		{
			ID:         engineStructuredLLM,
			Label:      "纯文本 AI 配方合成",
			Configured: cfg.LLMApiURL != "" && (cfg.LLMApiKey != "" || isLoopbackURL(cfg.LLMApiURL) || cfg.AllowPrivateWorker),
			Hint:       "零显卡文本大模型配色配方 + 100% 合法 64×64 像素渲染，支持连续微调",
		},
		{
			ID:         engineImageAPI,
			Label:      "云端图像生成 API",
			Configured: cfg.ImageApiURL != "" && (cfg.ImageApiKey != "" || isLoopbackURL(cfg.ImageApiURL) || cfg.AllowPrivateWorker),
			Hint:       "对接 OpenAI Images 兼容接口，经 UV 掩码清洗与覆盖度校验后输出",
		},
		{
			ID:         engineAsyncWorker,
			Label:      "外部专用算力节点",
			Configured: cfg.WorkerURL != "",
			Hint:       "对接自定义 ComfyUI / LoRA HTTP Worker，支持异步队列与轮询",
		},
		{
			ID:         engineLocalDemo,
			Label:      "本地关键词演示引擎",
			Configured: cfg.AllowLocalFallback,
			Hint:       "纯本地关键词色彩合成，无需外部 API Key，适合开发演示",
		},
	}

	preferred := cfg.DefaultEngine
	for _, item := range list {
		if item.ID == preferred && item.Configured {
			return list, preferred
		}
	}
	for _, item := range list {
		if item.Configured {
			return list, item.ID
		}
	}
	return list, ""
}

func routeGetConfig(ctx context.Context, cfg aiConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	engines, defaultEng := availableEngines(cfg)
	today := utcDateKey(time.Now())
	usedToday := manager.userDailyCount(today, req.Principal.ID)
	activeJobID := manager.userActiveJob(req.Principal.ID)

	balance := ""
	pointsEnabled := false
	if hc, err := hostclient.FromEnv(); err == nil {
		if site, err := hc.Site(ctx); err == nil {
			pointsEnabled = site.PointsEnabled
		}
		if req.Principal.ID > 0 {
			if summary, err := hc.UserSummary(ctx, req.Principal.ID); err == nil {
				balance = summary.Score
			}
		}
	}

	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"engines":         engines,
			"defaultEngine":   defaultEng,
			"pointsCost":      cfg.PointsCost,
			"pointsEnabled":   pointsEnabled,
			"balance":         balance,
			"dailyLimit":      cfg.DailyLimit,
			"usedToday":       usedToday,
			"cooldownSeconds": cfg.CooldownSeconds,
			"allowPublicSave": cfg.AllowPublicSave,
			"activeJobId":     activeJobID,
		}),
	}
}

func routePostGenerate(ctx context.Context, cfg aiConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	if req.Principal.ID < 1 {
		return errResp(401, "请先登录后使用 AI 皮肤工坊")
	}
	var body struct {
		Prompt     string      `json:"prompt"`
		Engine     string      `json:"engine"`
		Model      string      `json:"model"`
		Style      string      `json:"style"`
		BaseRecipe *SkinRecipe `json:"baseRecipe"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) > maxPromptRunes {
		return errResp(400, fmt.Sprintf("请输入 1–%d 字的角色皮肤描述", maxPromptRunes))
	}
	skinModel := "default"
	if body.Model == "slim" {
		skinModel = "slim"
	}

	engines, defaultEng := availableEngines(cfg)
	chosen := strings.ToLower(strings.TrimSpace(body.Engine))
	if chosen == "" {
		chosen = defaultEng
	}
	configured := false
	for _, e := range engines {
		if e.ID == chosen && e.Configured {
			configured = true
			break
		}
	}
	if !configured {
		return errResp(503, "所选 AI 引擎尚未在后台配置或启用，请联系管理员配置 API 或开启演示模式")
	}
	if body.BaseRecipe != nil && chosen != engineStructuredLLM && chosen != engineLocalDemo {
		return errResp(400, "仅纯文本配方或本地演示引擎支持基于现有配方的迭代微调")
	}

	// Pre-check points availability via Host API before claiming quota/lock.
	if cfg.PointsCost > 0 {
		hc, err := hostclient.FromEnv()
		if err != nil {
			return errResp(503, "宿主服务暂不可用，请稍后重试")
		}
		site, err := hc.Site(ctx)
		if err != nil {
			return errResp(503, "无法读取站点积分状态")
		}
		if !site.PointsEnabled {
			return errResp(409, "站点未启用积分功能，但当前配置了积分消耗，请联系管理员调整")
		}
		summary, err := hc.UserSummary(ctx, req.Principal.ID)
		if err != nil {
			return errResp(503, "无法校验账户积分余额")
		}
		score, err := strconv.ParseInt(strings.TrimSpace(summary.Score), 10, 64)
		if err != nil || score < cfg.PointsCost {
			return errResp(409, fmt.Sprintf("积分不足：生成需消耗 %d 积分，当前可用 %s 积分", cfg.PointsCost, summary.Score))
		}
	}

	now := time.Now()
	today := utcDateKey(now)
	job, errMsg, status := manager.reserveJob(req.Principal.ID, req.Principal.Admin, chosen, skinModel, prompt, today, cfg.DailyLimit, cfg.CooldownSeconds, now)
	if status != 0 {
		return errResp(status, errMsg)
	}

	in := aiGenerateInput{
		Prompt:     prompt,
		Style:      strings.TrimSpace(body.Style),
		Model:      skinModel,
		BaseRecipe: body.BaseRecipe,
	}

	go runGenerationWorker(job.ID, cfg, in)

	return sdk.RuntimeResponse{
		Status: 202,
		Body: mustJSON(map[string]any{
			"jobId":  job.ID,
			"status": job.Status,
			"engine": job.Engine,
			"stage":  job.Stage,
		}),
	}
}

func runGenerationWorker(jobID string, cfg aiConfig, in aiGenerateInput) {
	started := time.Now()
	manager.updateStage(jobID, "running", "正在连接 AI 引擎构思皮肤…")

	bgCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+15*time.Second)
	defer cancel()

	var out aiGenerateOutput
	var err error
	switch manager.jobEngine(jobID) {
	case engineStructuredLLM:
		out, err = generateStructuredLLM(bgCtx, cfg, in)
	case engineImageAPI:
		out, err = generateImageAPI(bgCtx, cfg, in)
	case engineAsyncWorker:
		out, err = generateAsyncWorker(bgCtx, cfg, in)
	case engineLocalDemo:
		recipe := BuildLocalDemoRecipe(in.Prompt, in.Style, in.Model, in.BaseRecipe)
		out = aiGenerateOutput{
			Image:   RenderRecipe(recipe),
			Recipe:  &recipe,
			Summary: recipe.Summary,
		}
	default:
		err = errors.New("未知 AI 引擎")
	}

	if err != nil {
		manager.failJob(jobID, err.Error(), time.Since(started))
		return
	}

	manager.updateStage(jobID, "running", "正在进行 Minecraft 64×64 UV 规范清洗与质检…")
	cleaned, err := SanitizeAndValidateSkin(out.Image, in.Model == "slim")
	if err != nil {
		manager.failJob(jobID, err.Error(), time.Since(started))
		return
	}

	skinBytes, err := skinpng.EncodePNG(cleaned)
	if err != nil {
		manager.failJob(jobID, "无法编码 64×64 PNG 皮肤（未扣费）", time.Since(started))
		return
	}
	frontBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyFront(cleaned, 6, in.Model == "slim", true))
	backBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyBack(cleaned, 6, in.Model == "slim", true))

	var pointsSpent int64
	var balance string
	if cfg.PointsCost > 0 {
		manager.updateStage(jobID, "running", "校验通过，正在结算积分…")
		hc, hcErr := hostclient.FromEnv()
		if hcErr != nil {
			manager.failJob(jobID, "连接宿主积分结算失败，本次生成未扣费", time.Since(started))
			return
		}
		uid := manager.jobUserID(jobID)
		spendRes, spendErr := hc.SpendPoints(bgCtx, uid, cfg.PointsCost, "aiskin-"+jobID)
		if spendErr != nil {
			var apiErr *hostclient.APIError
			if errors.As(spendErr, &apiErr) {
				manager.failJob(jobID, apiErr.Message, time.Since(started))
			} else {
				manager.failJob(jobID, "积分结算失败，无法释放生成结果", time.Since(started))
			}
			return
		}
		pointsSpent = cfg.PointsCost
		balance = spendRes.Balance
	}

	manager.succeedJob(jobID, aiJobSuccess{
		Summary:     out.Summary,
		Recipe:      out.Recipe,
		PNGBase64:   base64.StdEncoding.EncodeToString(skinBytes),
		FrontBase64: base64.StdEncoding.EncodeToString(frontBytes),
		BackBase64:  base64.StdEncoding.EncodeToString(backBytes),
		PointsSpent: pointsSpent,
		Balance:     balance,
	}, time.Since(started))
}

func routeGetJob(req sdk.RuntimeRequest) sdk.RuntimeResponse {
	q, _ := url.ParseQuery(req.Query)
	jobID := strings.TrimSpace(q.Get("id"))
	if jobID == "" {
		return errResp(400, "缺少任务 id")
	}
	snapshot, writes, status, msg := manager.getJobForUser(jobID, req.Principal.ID, req.Principal.Admin)
	if status != 0 {
		return errResp(status, msg)
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(snapshot),
		Writes: writes,
	}
}

func routePostSave(ctx context.Context, cfg aiConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		JobID       string `json:"jobId"`
		Name        string `json:"name"`
		Model       string `json:"model"`
		IsPublic    bool   `json:"isPublic"`
		CharacterID int64  `json:"characterId"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return errResp(400, "请填写有效的皮肤名称")
	}
	job, errMsg, status := manager.getCompletedSkin(body.JobID, req.Principal.ID, req.Principal.Admin)
	if status != 0 {
		return errResp(status, errMsg)
	}
	pngBytes, err := base64.StdEncoding.DecodeString(job.PNGBase64)
	if err != nil {
		return errResp(500, "任务图像缓存损坏")
	}
	model := job.Model
	if body.Model == "slim" {
		model = "slim"
	} else if body.Model == "default" {
		model = "default"
	}
	isPublic := body.IsPublic && cfg.AllowPublicSave

	hc, err := hostclient.FromEnv()
	if err != nil {
		return errResp(503, "宿主服务未连接")
	}
	rec, err := hc.UploadTexture(ctx, req.Principal.ID, hostclient.UploadTextureRequest{
		Name:        name,
		Description: job.Summary,
		Kind:        "skin",
		Model:       model,
		IsPublic:    isPublic,
		PNGBytes:    pngBytes,
	})
	if err != nil {
		var apiErr *hostclient.APIError
		if errors.As(err, &apiErr) {
			return errResp(apiErr.Status, apiErr.Message)
		}
		return errResp(503, "保存 AI 皮肤失败")
	}

	bound := false
	pendingReview := rec.ReviewState != "" && rec.ReviewState != "approved"
	if body.CharacterID > 0 && !pendingReview {
		if err := hc.BindCharacterTexture(ctx, req.Principal.ID, body.CharacterID, "skin", rec.ID, model); err == nil {
			bound = true
		}
	}

	writes := manager.recordSaved()
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"record":        rec,
			"bound":         bound,
			"pendingReview": pendingReview,
		}),
		Writes: writes,
	}
}

func routeAdminStatus(cfg aiConfig) sdk.RuntimeResponse {
	engines, defaultEng := availableEngines(cfg)
	stats, logs, activeCount, todayTotal := manager.adminSnapshot(utcDateKey(time.Now()))
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"engines":       engines,
			"defaultEngine": defaultEng,
			"pointsCost":    cfg.PointsCost,
			"dailyLimit":    cfg.DailyLimit,
			"activeJobs":    activeCount,
			"todayTotal":    todayTotal,
			"stats":         stats,
			"logs":          logs,
		}),
	}
}

func routeAdminTest(ctx context.Context, cfg aiConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		Engine string `json:"engine"`
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "蓝发少年穿着白色科技卫衣"
	}
	model := "default"
	if body.Model == "slim" {
		model = "slim"
	}
	engines, defaultEng := availableEngines(cfg)
	target := strings.ToLower(strings.TrimSpace(body.Engine))
	if target == "" {
		target = defaultEng
	}
	configured := false
	for _, e := range engines {
		if e.ID == target && e.Configured {
			configured = true
			break
		}
	}
	if !configured {
		return errResp(400, "指定引擎尚未配置或未启用")
	}

	started := time.Now()
	testCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	in := aiGenerateInput{Prompt: prompt, Model: model}
	var out aiGenerateOutput
	var err error
	switch target {
	case engineStructuredLLM:
		out, err = generateStructuredLLM(testCtx, cfg, in)
	case engineImageAPI:
		out, err = generateImageAPI(testCtx, cfg, in)
	case engineAsyncWorker:
		out, err = generateAsyncWorker(testCtx, cfg, in)
	case engineLocalDemo:
		recipe := BuildLocalDemoRecipe(prompt, "", model, nil)
		out = aiGenerateOutput{Image: RenderRecipe(recipe), Recipe: &recipe, Summary: recipe.Summary}
	}
	if err != nil {
		return sdk.RuntimeResponse{
			Status: 200,
			Body: mustJSON(map[string]any{
				"ok":         false,
				"engine":     target,
				"durationMs": time.Since(started).Milliseconds(),
				"error":      err.Error(),
			}),
		}
	}
	cleaned, err := SanitizeAndValidateSkin(out.Image, model == "slim")
	if err != nil {
		return sdk.RuntimeResponse{
			Status: 200,
			Body: mustJSON(map[string]any{
				"ok":         false,
				"engine":     target,
				"durationMs": time.Since(started).Milliseconds(),
				"error":      err.Error(),
			}),
		}
	}
	frontBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyFront(cleaned, 6, model == "slim", true))
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"ok":                 true,
			"engine":             target,
			"durationMs":         time.Since(started).Milliseconds(),
			"summary":            out.Summary,
			"frontPreviewBase64": base64.StdEncoding.EncodeToString(frontBytes),
		}),
	}
}

func routeAdminClearLogs() sdk.RuntimeResponse {
	writes := manager.clearLogs()
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(map[string]bool{"cleared": true}),
		Writes: writes,
	}
}

type aiJobSuccess struct {
	Summary     string
	Recipe      *SkinRecipe
	PNGBase64   string
	FrontBase64 string
	BackBase64  string
	PointsSpent int64
	Balance     string
}

func (m *jobManager) hydrateLocked(data map[string]json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hydrated || len(data) == 0 {
		return
	}
	m.hydrated = true

	if raw, ok := data["stats"]; ok && len(raw) > 0 {
		var s aiStats
		if json.Unmarshal(raw, &s) == nil {
			m.stats.TotalGenerated = max(m.stats.TotalGenerated, s.TotalGenerated)
			m.stats.TotalFailed = max(m.stats.TotalFailed, s.TotalFailed)
			m.stats.TotalSaved = max(m.stats.TotalSaved, s.TotalSaved)
		}
	}
	if len(m.logs) == 0 {
		if raw, ok := data["audit_log"]; ok && len(raw) > 0 {
			var entries []aiAuditEntry
			if json.Unmarshal(raw, &entries) == nil {
				if len(entries) > maxAuditLogs {
					entries = entries[:maxAuditLogs]
				}
				m.logs = entries
			}
		}
	}
	if raw, ok := data["daily_usage"]; ok && len(raw) > 0 {
		var parsed map[string]map[string]int
		if json.Unmarshal(raw, &parsed) == nil {
			cutoff := utcDateKey(time.Now().AddDate(0, 0, -2))
			for day, perUser := range parsed {
				if day < cutoff {
					continue
				}
				if m.dailyUsage[day] == nil {
					m.dailyUsage[day] = map[int64]int{}
				}
				for uidStr, cnt := range perUser {
					if uid, err := strconv.ParseInt(uidStr, 10, 64); err == nil && cnt > m.dailyUsage[day][uid] {
						m.dailyUsage[day][uid] = cnt
					}
				}
			}
		}
	}
}

func (m *jobManager) userDailyCount(day string, uid int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if per := m.dailyUsage[day]; per != nil {
		return per[uid]
	}
	return 0
}

func (m *jobManager) userActiveJob(uid int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if jid := m.activeByUser[uid]; jid != "" {
		if j := m.jobs[jid]; j != nil && (j.Status == "queued" || j.Status == "running") {
			return jid
		}
		delete(m.activeByUser, uid)
	}
	return ""
}

func (m *jobManager) reserveJob(uid int64, isAdmin bool, engine, model, prompt, today string, dailyLimit, cooldownSec int, now time.Time) (*aiJob, string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneLocked(now)

	if prevID := m.activeByUser[uid]; prevID != "" {
		if prev := m.jobs[prevID]; prev != nil && (prev.Status == "queued" || prev.Status == "running") {
			return nil, "你已有一个正在进行的 AI 皮肤任务，请等待完成后再提交", 409
		}
		delete(m.activeByUser, uid)
	}

	if !isAdmin && cooldownSec > 0 {
		if last, ok := m.lastSubmit[uid]; ok {
			rem := time.Duration(cooldownSec)*time.Second - now.Sub(last)
			if rem > 0 {
				sec := int(rem.Seconds()) + 1
				return nil, fmt.Sprintf("请求过于频繁，请在 %d 秒后再试", sec), 429
			}
		}
	}

	if !isAdmin {
		if dailyLimit == 0 {
			return nil, "管理员已暂关闭普通用户的 AI 生成限额", 403
		}
		if m.dailyUsage[today] != nil && m.dailyUsage[today][uid] >= dailyLimit {
			return nil, fmt.Sprintf("今日 AI 生成次数已达上限（%d 次），请明天再试", dailyLimit), 429
		}
	}

	jobID := randomJobID()
	job := &aiJob{
		ID:        jobID,
		UserID:    uid,
		Engine:    engine,
		Model:     model,
		Prompt:    prompt,
		Status:    "queued",
		Stage:     "任务已入队…",
		DateKey:   today,
		CreatedAt: now,
		UpdatedAt: now,
		Counted:   true,
	}
	m.jobs[jobID] = job
	m.activeByUser[uid] = jobID
	m.lastSubmit[uid] = now
	if m.dailyUsage[today] == nil {
		m.dailyUsage[today] = map[int64]int{}
	}
	m.dailyUsage[today][uid]++
	return job, "", 0
}

func (m *jobManager) updateStage(jobID, status, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[jobID]; j != nil {
		j.Status = status
		j.Stage = stage
		j.UpdatedAt = time.Now()
	}
}

func (m *jobManager) jobEngine(jobID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[jobID]; j != nil {
		return j.Engine
	}
	return ""
}

func (m *jobManager) jobUserID(jobID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[jobID]; j != nil {
		return j.UserID
	}
	return 0
}

func (m *jobManager) failJob(jobID, errMsg string, dur time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil {
		return
	}
	now := time.Now()
	j.Status = "failed"
	j.Stage = "生成未完成"
	j.Error = errMsg
	j.PNGBase64 = ""
	j.FrontPreviewBase64 = ""
	j.BackPreviewBase64 = ""
	j.DurationMs = dur.Milliseconds()
	j.UpdatedAt = now
	j.NeedsFlush = true

	if m.activeByUser[j.UserID] == jobID {
		delete(m.activeByUser, j.UserID)
	}
	// Release daily quota reservation since generation failed without producing a skin.
	if j.Counted && m.dailyUsage[j.DateKey] != nil && m.dailyUsage[j.DateKey][j.UserID] > 0 {
		m.dailyUsage[j.DateKey][j.UserID]--
		j.Counted = false
	}
	m.stats.TotalFailed++
	m.appendLogLocked(aiAuditEntry{
		Time:       now.UTC().Format(time.RFC3339),
		UserID:     j.UserID,
		Engine:     j.Engine,
		Status:     "failed",
		DurationMs: j.DurationMs,
		Detail:     truncateRunes(errMsg, 80),
	})
	m.dirty = true
}

func (m *jobManager) succeedJob(jobID string, res aiJobSuccess, dur time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil {
		return
	}
	now := time.Now()
	j.Status = "succeeded"
	j.Stage = "皮肤生成与 UV 校验完成"
	j.Summary = res.Summary
	j.Recipe = res.Recipe
	j.PNGBase64 = res.PNGBase64
	j.FrontPreviewBase64 = res.FrontBase64
	j.BackPreviewBase64 = res.BackBase64
	j.PointsSpent = res.PointsSpent
	j.Balance = res.Balance
	j.Error = ""
	j.DurationMs = dur.Milliseconds()
	j.UpdatedAt = now
	j.NeedsFlush = true

	if m.activeByUser[j.UserID] == jobID {
		delete(m.activeByUser, j.UserID)
	}
	m.stats.TotalGenerated++
	m.appendLogLocked(aiAuditEntry{
		Time:       now.UTC().Format(time.RFC3339),
		UserID:     j.UserID,
		Engine:     j.Engine,
		Status:     "succeeded",
		DurationMs: j.DurationMs,
		Detail:     truncateRunes(res.Summary, 80),
	})
	m.dirty = true
}

func (m *jobManager) getJobForUser(jobID string, uid int64, isAdmin bool) (aiJob, map[string]json.RawMessage, int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j := m.jobs[jobID]
	if j == nil {
		return aiJob{}, nil, 404, "任务不存在或已过期"
	}
	if j.UserID != uid && !isAdmin {
		return aiJob{}, nil, 403, "无权查看该任务"
	}
	var writes map[string]json.RawMessage
	if m.dirty || j.NeedsFlush {
		j.NeedsFlush = false
		writes = m.buildPersistenceWritesLocked()
		m.dirty = false
	}
	copyJob := *j
	if copyJob.Status != "succeeded" {
		copyJob.PNGBase64 = ""
		copyJob.FrontPreviewBase64 = ""
		copyJob.BackPreviewBase64 = ""
	}
	return copyJob, writes, 0, ""
}

func (m *jobManager) getCompletedSkin(jobID string, uid int64, isAdmin bool) (*aiJob, string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil {
		return nil, "生成任务不存在或已过期", 404
	}
	if j.UserID != uid && !isAdmin {
		return nil, "无权操作该任务结果", 403
	}
	if j.Status != "succeeded" || j.PNGBase64 == "" {
		return nil, "当前任务尚未生成成功", 409
	}
	copyJob := *j
	return &copyJob, "", 0
}

func (m *jobManager) recordSaved() map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.TotalSaved++
	writes := m.buildPersistenceWritesLocked()
	m.dirty = false
	return writes
}

func (m *jobManager) adminSnapshot(today string) (aiStats, []aiAuditEntry, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := 0
	for _, j := range m.jobs {
		if j.Status == "queued" || j.Status == "running" {
			active++
		}
	}
	todayTotal := 0
	for _, cnt := range m.dailyUsage[today] {
		todayTotal += cnt
	}
	logsCopy := slices.Clone(m.logs)
	return m.stats, logsCopy, active, todayTotal
}

func (m *jobManager) clearLogs() map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = []aiAuditEntry{}
	writes := m.buildPersistenceWritesLocked()
	m.dirty = false
	return writes
}

func (m *jobManager) buildPersistenceWritesLocked() map[string]json.RawMessage {
	cutoff := utcDateKey(time.Now().AddDate(0, 0, -2))
	usageOut := map[string]map[string]int{}
	for day, perUser := range m.dailyUsage {
		if day < cutoff {
			delete(m.dailyUsage, day)
			continue
		}
		if len(perUser) == 0 {
			continue
		}
		inner := map[string]int{}
		for uid, cnt := range perUser {
			if cnt > 0 {
				inner[strconv.FormatInt(uid, 10)] = cnt
			}
		}
		if len(inner) > 0 {
			usageOut[day] = inner
		}
	}
	return map[string]json.RawMessage{
		"stats":       mustJSON(m.stats),
		"audit_log":   mustJSON(m.logs),
		"daily_usage": mustJSON(usageOut),
	}
}

func (m *jobManager) appendLogLocked(entry aiAuditEntry) {
	m.logs = append([]aiAuditEntry{entry}, m.logs...)
	if len(m.logs) > maxAuditLogs {
		m.logs = m.logs[:maxAuditLogs]
	}
}

func (m *jobManager) pruneLocked(now time.Time) {
	for id, j := range m.jobs {
		if (j.Status == "succeeded" || j.Status == "failed") && now.Sub(j.UpdatedAt) > jobTTL {
			delete(m.jobs, id)
		}
	}
	if len(m.jobs) <= maxJobsRetained {
		return
	}
	type pair struct {
		id string
		at time.Time
	}
	var finished []pair
	for id, j := range m.jobs {
		if j.Status == "succeeded" || j.Status == "failed" {
			finished = append(finished, pair{id: id, at: j.UpdatedAt})
		}
	}
	slices.SortFunc(finished, func(a, b pair) int {
		return a.at.Compare(b.at)
	})
	for i := 0; i < len(finished) && len(m.jobs) > maxJobsRetained; i++ {
		delete(m.jobs, finished[i].id)
	}
}

func randomJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "job_" + hex.EncodeToString(b[:])
	}
	return "job_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func utcDateKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
