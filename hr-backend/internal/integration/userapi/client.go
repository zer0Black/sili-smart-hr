// Package userapi 封装 sili-smart-api 用户信息客户端。
//
// 对接 sili-smart-api 的用户信息字典接口（P1_TECH_002_RFC）：
//   - 路径：GET /api/conversation-log/users
//   - 鉴权：请求头 Authorization: Bearer <集成密钥>
//   - 查询：username（用户名模糊匹配）、p（页码）、page_size（每页条数，上限 100）
//   - 响应：外层 {success, message, data}，data 内 {page, page_size, total, items}，
//     每个 item 是 {user_id, username, tokens:[...]}。HR 侧评估对象是人，只取 user_id 与 username，
//     不解析 tokens。
//
// Client 只做 HTTP 请求与 JSON 解析，不可达/超时/鉴权失败/非 2xx/success:false/解码失败统一返 error，
// 由 service 层映射为 errcode.StaffListUnavailable(1305)。响应体经 LimitReader 限 10MB，
// 超阈值视为异常，规避上游超大响应把进程撑爆的 OOM 风险。
package userapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Staff 是 sili-smart-api 用户体系在 HR 侧的极简投影。
//
// StaffID 装的是上游 user_id（int）的字符串形式，StaffName 装 username，
// 对外字段名（staff_id/staff_name）保持稳定降低调用方改动面。
// 系统无工号约束：人员下拉只取人员标识与人名，绝不提取员工编号字段。
type Staff struct {
	StaffID   string `json:"staff_id"`
	StaffName string `json:"staff_name"`
}

// httpTimeout 是 Client 调用上游的默认超时。
const httpTimeout = 5 * time.Second

// maxBodySize 是响应体大小上限，超出视为异常，配合 io.LimitReader 规避 OOM。
const maxBodySize = 10 << 20 // 10MB

// Client 是 sili-smart-api 用户体系 HTTP 客户端。
//
// baseURL 来自 config.Integration.SmartAPIBaseURL；
// httpClient 承载超时与传输配置，由 NewClient 装配默认值。
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 构造一个指向 sili-smart-api 的客户端。
//
// baseURL 为空时仍返回 Client，调用 ListStaffs 时直接返 error，
// 便于 service 层据 config 缺省值降级。
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// apiUsersResponse 对齐 sili-smart-api 用户信息字典响应（P1_TECH_002_RFC）。
//
// 外层 {success, message, data}，data 内 {page, page_size, total, items}。
// items 只解码 user_id 与 username，tokens 不解析（评估对象是人，不需要 API key 信息）。
type apiUsersResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Page     int       `json:"page"`
		PageSize int       `json:"page_size"`
		Total    int64     `json:"total"`
		Items    []apiUser `json:"items"`
	} `json:"data"`
}

// apiUser 是上游 items 元素，只取 HR 侧需要的两个字段。
type apiUser struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
}

// ListStaffs 代理查询 sili-smart-api 用户信息字典，按 username 过滤、分页。
//
// 调用 GET {baseURL}/api/conversation-log/users?username=&p=&page_size=，
// 请求头 Authorization: Bearer <secret>。success==true 返回 staff 列表与 total；
// 401/403（鉴权失败）、非 2xx、success==false、超时、JSON 解析失败或响应体超 maxBodySize
// 统一返 error，由 service 层映射为 errcode.StaffListUnavailable(1305)。
func (c *Client) ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) (items []Staff, total int64, err error) {
	if c.baseURL == "" {
		return nil, 0, fmt.Errorf("userapi: baseURL not configured")
	}

	q := url.Values{}
	q.Set("username", keyword)
	q.Set("p", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	reqURL := c.baseURL + "/api/conversation-log/users?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("userapi: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("userapi: request: %w", err)
	}
	defer resp.Body.Close()

	// 401 密钥缺失或无效、403 服务端未配置密钥，均为鉴权失败，返回带语义的 error。
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, 0, fmt.Errorf("userapi: unauthorized (invalid or missing secret)")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, 0, fmt.Errorf("userapi: forbidden (server key not configured)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("userapi: upstream status %d", resp.StatusCode)
	}

	// LimitReader 限响应体大小，超过 maxBodySize 视为异常，规避超大响应撑爆内存。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("userapi: read body: %w", err)
	}
	if int64(len(body)) > maxBodySize {
		return nil, 0, fmt.Errorf("userapi: response body too large (>%d bytes)", maxBodySize)
	}

	var parsed apiUsersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("userapi: decode body: %w", err)
	}

	// success==false 视为业务错误，带上游 message 便于排查。
	if !parsed.Success {
		msg := parsed.Message
		if msg == "" {
			msg = "unknown upstream error"
		}
		return nil, 0, fmt.Errorf("userapi: upstream business error: %s", msg)
	}

	// user_id(int) 转 string 存入 StaffID，username 存入 StaffName。
	items = make([]Staff, 0, len(parsed.Data.Items))
	for _, u := range parsed.Data.Items {
		items = append(items, Staff{
			StaffID:   strconv.Itoa(u.UserID),
			StaffName: u.Username,
		})
	}
	return items, parsed.Data.Total, nil
}
