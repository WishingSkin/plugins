// Package hostclient provides a thin Bearer-authenticated client for WishingSkin Runtime v2 Host APIs.
package hostclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 16 << 20

// APIError is returned when the WishingSkin Host API responds with a non-2xx status code.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("host api %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("host api error (%d)", e.Status)
}

// Client invokes WishingSkin's internal loopback Host API v1.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// FromEnv constructs a Client from WS_HOST_API_URL and WS_HOST_API_TOKEN.
func FromEnv() (*Client, error) {
	return New(os.Getenv("WS_HOST_API_URL"), os.Getenv("WS_HOST_API_TOKEN"))
}

// New constructs a Client targeting the given baseURL and Bearer token.
func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, errors.New("host API URL and bearer token are required")
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}, nil
}

type SiteInfo struct {
	Name          string          `json:"name"`
	PublicURL     string          `json:"publicUrl"`
	PointsEnabled bool            `json:"pointsEnabled"`
	ContentPolicy json.RawMessage `json:"contentPolicy,omitempty"`
}

type CharacterInfo struct {
	ID                int64  `json:"id"`
	UUID              string `json:"uuid"`
	Name              string `json:"name"`
	Model             string `json:"model"`
	SkinHash          string `json:"skinHash"`
	CapeHash          string `json:"capeHash"`
	VerifiedMinecraft bool   `json:"verifiedMinecraft"`
	MCName            string `json:"mcName"`
}

type UserSummary struct {
	ID                int64           `json:"id"`
	Nickname          string          `json:"nickname"`
	IsAdmin           bool            `json:"isAdmin"`
	Banned            bool            `json:"banned"`
	EmailVerified     bool            `json:"emailVerified"`
	Score             string          `json:"score"`
	VerifiedMinecraft bool            `json:"verifiedMinecraft"`
	MCName            string          `json:"mcName"`
	MCUUID            string          `json:"mcUuid"`
	Characters        []CharacterInfo `json:"characters"`
}

type TextureRecord struct {
	ID           string   `json:"id"`
	FileHash     string   `json:"fileHash"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Kind         string   `json:"kind"`
	Model        string   `json:"model"`
	Public       bool     `json:"public"`
	Owned        bool     `json:"owned"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
	ReviewState  string   `json:"reviewState"`
	ReviewReason string   `json:"reviewReason"`
	CreatedAt    int64    `json:"createdAt"`
}

type UploadTextureRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Kind         string   `json:"kind"`
	Model        string   `json:"model,omitempty"`
	IsPublic     bool     `json:"isPublic"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	PNGBytes     []byte   `json:"-"`
	PNGBase64    string   `json:"pngBase64"`
	ExpectedCost *int64   `json:"expectedCost,omitempty"`
}

type SpendPointsResult struct {
	Balance  string `json:"balance"`
	Deducted bool   `json:"deducted"`
}

func (c *Client) Site(ctx context.Context) (SiteInfo, error) {
	var out SiteInfo
	err := c.doJSON(ctx, http.MethodGet, "/v1/site", nil, &out)
	return out, err
}

func (c *Client) CharacterByName(ctx context.Context, name string) (CharacterInfo, error) {
	var out CharacterInfo
	err := c.doJSON(ctx, http.MethodGet, "/v1/characters/by-name?name="+url.QueryEscape(name), nil, &out)
	return out, err
}

func (c *Client) TextureBytes(ctx context.Context, hash string, userID int64) ([]byte, error) {
	endpoint := "/v1/textures/" + url.PathEscape(hash) + "/bytes"
	if userID > 0 {
		endpoint += "?userId=" + strconv.FormatInt(userID, 10)
	}
	return c.doRaw(ctx, http.MethodGet, endpoint, nil)
}

func (c *Client) UserSummary(ctx context.Context, userID int64) (UserSummary, error) {
	var out UserSummary
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/users/%d/summary", userID), nil, &out)
	return out, err
}

func (c *Client) UserTextures(ctx context.Context, userID int64, kind string) ([]TextureRecord, error) {
	endpoint := fmt.Sprintf("/v1/users/%d/textures", userID)
	if kind != "" {
		endpoint += "?kind=" + url.QueryEscape(kind)
	}
	var wrap struct {
		Textures []TextureRecord `json:"textures"`
	}
	err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &wrap)
	return wrap.Textures, err
}

func (c *Client) UploadTexture(ctx context.Context, userID int64, req UploadTextureRequest) (TextureRecord, error) {
	if len(req.PNGBytes) > 0 && req.PNGBase64 == "" {
		req.PNGBase64 = base64.StdEncoding.EncodeToString(req.PNGBytes)
	}
	var out TextureRecord
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/users/%d/textures", userID), req, &out)
	return out, err
}

func (c *Client) BindCharacterTexture(ctx context.Context, userID, charID int64, kind, recordID, model string) error {
	payload := map[string]any{"recordId": recordID}
	if model != "" {
		payload["model"] = model
	}
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/v1/users/%d/characters/%d/textures/%s", userID, charID, url.PathEscape(kind)), payload, nil)
}

func (c *Client) SpendPoints(ctx context.Context, userID, amount int64, referenceID string) (SpendPointsResult, error) {
	var out SpendPointsResult
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/users/%d/points/spend", userID), map[string]any{
		"amount":      amount,
		"referenceId": referenceID,
	}, &out)
	return out, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody, dst any) error {
	data, err := c.doRaw(ctx, method, path, reqBody)
	if err != nil {
		return err
	}
	if dst == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

func (c *Client) doRaw(ctx context.Context, method, endpoint string, reqBody any) ([]byte, error) {
	var body io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("host api response exceeds 16 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &errPayload)
		return nil, &APIError{Status: resp.StatusCode, Message: errPayload.Error}
	}
	return data, nil
}
