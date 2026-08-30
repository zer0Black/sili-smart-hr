// Package handler 承载 HTTP 请求处理器，按业务域分包。
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"sili-smart-hr/backend/internal/api/middleware"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// AccountHandler 承载 account 域接口：登录、当前账号、公钥、账号台账管理。
type AccountHandler struct {
	svc    service.AccountService
	rsaMgr *rsakey.Manager
}

func NewAccountHandler(svc service.AccountService, rsaMgr *rsakey.Manager) *AccountHandler {
	return &AccountHandler{svc: svc, rsaMgr: rsaMgr}
}

type loginRequest struct {
	Username       string `json:"username" binding:"required"`
	PasswordCipher string `json:"passwordCipher" binding:"required"`
	KeyID          string `json:"keyId" binding:"required"`
}

// PublicKey 按需生成 RSA-OAEP 密钥对，公钥 PEM + keyId 返回前端，私钥写入 Redis。
func (h *AccountHandler) PublicKey(c *gin.Context) {
	pub, keyID, err := h.rsaMgr.Generate(c.Request.Context())
	if err != nil {
		slog.Error("rsa generate public key", "err", err)
		response.Fail(c, http.StatusInternalServerError, errcode.Internal)
		return
	}
	response.OKWithData(c, gin.H{
		"publicKey": pub,
		"keyId":     keyID,
		"expiresIn": 300,
	})
}

// Login 处理 POST /api/login。IP/username 两层限流由 router 中间件承载。
func (h *AccountHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Username, req.PasswordCipher, req.KeyID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

func (h *AccountHandler) Me(c *gin.Context) {
	accountID, ok := middleware.AccountIDFromContext(c)
	if !ok {
		response.Fail(c, http.StatusUnauthorized, errcode.Unauthorized)
		return
	}
	acc, err := h.svc.GetCurrent(c.Request.Context(), accountID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{"account": acc})
}

// handleServiceError 把 *service.Error 按 code 映射 HTTP status（鉴权 401，其余 200），
// message 透传 serr.Msg（覆盖 errcode 默认文案，承载 service 层动态原因，如 1304 失败说明、字段级提示），其余视为内部错误。
func handleServiceError(c *gin.Context, err error) {
	var serr *service.Error
	if errors.As(err, &serr) {
		response.FailWithMsg(c, httpStatusFor(serr.Code), serr.Code, serr.Msg)
		return
	}
	slog.Error("unexpected service error", "err", err)
	response.Fail(c, http.StatusInternalServerError, errcode.Internal)
}

func httpStatusFor(code int) int {
	if code == errcode.Unauthorized {
		return http.StatusUnauthorized
	}
	return http.StatusOK
}

// createAccountRequest：Enabled 用 *bool 区分缺省（按 true 处理）与显式 false。
type createAccountRequest struct {
	Username       string `json:"username" binding:"required"`
	Name           string `json:"name" binding:"required"`
	PasswordCipher string `json:"passwordCipher" binding:"required"`
	KeyID          string `json:"keyId" binding:"required"`
	Enabled        *bool  `json:"enabled"`
}

// updateAccountRequest：PasswordCipher 留空表示不改密码；ID 以 JSON string 传输规避 JS 精度坑。
type updateAccountRequest struct {
	ID             string `json:"id" binding:"required"`
	Name           string `json:"name" binding:"required"`
	PasswordCipher string `json:"passwordCipher"`
	KeyID          string `json:"keyId"`
	Enabled        bool   `json:"enabled"`
}

type deleteAccountRequest struct {
	ID string `json:"id" binding:"required"`
}

type toggleEnabledRequest struct {
	ID      string `json:"id" binding:"required"`
	Enabled bool   `json:"enabled"`
}

type resetPasswordRequest struct {
	ID             string `json:"id" binding:"required"`
	PasswordCipher string `json:"passwordCipher" binding:"required"`
	KeyID          string `json:"keyId" binding:"required"`
}

func parseID(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

// List 处理 GET /api/accounts。page/page_size 缺省或非法兜底 1/20。
func (h *AccountHandler) List(c *gin.Context) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	pageSize, err := strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = 20
	}
	keyword := c.Query("keyword")
	list, total, err := h.svc.ListAccounts(c.Request.Context(), keyword, page, pageSize)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithPage(c, list, total, page, pageSize)
}

func (h *AccountHandler) Create(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	res, err := h.svc.CreateAccount(c.Request.Context(), req.Username, req.Name, req.PasswordCipher, req.KeyID, enabled)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

func (h *AccountHandler) Update(c *gin.Context) {
	var req updateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	hasPassword := req.PasswordCipher != ""
	res, err := h.svc.UpdateAccount(c.Request.Context(), id, req.Name, req.PasswordCipher, req.KeyID, hasPassword, req.Enabled)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

func (h *AccountHandler) Delete(c *gin.Context) {
	var req deleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	if err := h.svc.DeleteAccount(c.Request.Context(), id); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{"id": req.ID})
}

func (h *AccountHandler) ToggleEnabled(c *gin.Context) {
	var req toggleEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	if err := h.svc.ToggleEnabled(c.Request.Context(), id, req.Enabled); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{"id": req.ID, "enabled": req.Enabled})
}

func (h *AccountHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	if err := h.svc.ResetPassword(c.Request.Context(), id, req.PasswordCipher, req.KeyID); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{"id": req.ID})
}
