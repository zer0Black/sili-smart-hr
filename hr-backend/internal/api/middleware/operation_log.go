package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// recordedMethods 写方法集合（03 §1.3：当前受保护组写接口全为 POST，
// 新增 PUT/DELETE 时在此扩展）。
var recordedMethods = map[string]bool{http.MethodPost: true}

// 摘要常量（03 §1.5 / §1.6）。
const (
	internalErrMsg = "服务内部错误"
	loginFailMsg   = "凭证校验未通过"
)

// fallbackModule 按路径前缀推断业务域，未命中归 system_params（03 §4.1 步骤3）。
var fallbackModule = []struct {
	prefix string
	module string
}{
	{"/api/accounts", domain.OpModuleAccount},
	{"/api/dimensions", domain.OpModuleDimension},
	{"/api/assessment-config", domain.OpModuleSystemParams},
	{"/api/llm-configs", domain.OpModuleLLMConfig},
	{"/api/integration-secret", domain.OpModuleLLMConfig},
	{"/api/questions", domain.OpModuleQuestionBank},
	{"/api/question-batches", domain.OpModuleQuestionBank},
	{"/api/question-generations", domain.OpModuleQuestionBank},
	{"/api/scales", domain.OpModuleQuestionBank},
	{"/api/assessment", domain.OpModuleAssessment},
}

// bodyCaptureWriter 包装 ResponseWriter 捕获响应体，供 Next 后解析 code/message。
type bodyCaptureWriter struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (w *bodyCaptureWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

// responseCode 从捕获的响应体解析 code 与 message，仅取两字段，
// 解析失败按 fail 兜底（03 §1.5）。
func responseCode(body []byte) (int, string) {
	var parsed struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return -1, ""
	}
	return parsed.Code, parsed.Message
}

// OperationLog 受保护组记录中间件：仅 POST 进入记录路径，其余方法直接放行
//（specs §5.1.2 步骤1/2）。POST 先注入请求级 OpSink 再透传业务 handler，
// 响应后在 defer 内组装 PendingLog 经 recorder 非阻塞投递（03 §4.1）：
// defer 保证 panic 路径也落记录，recover 后 re-panic 交外层 Recovery 写 500。
func OperationLog(recorder service.PendingLogRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !recordedMethods[c.Request.Method] {
			c.Next()
			return
		}

		sink := &service.OpSink{}
		c.Request = c.Request.WithContext(service.WithSink(c.Request.Context(), sink))
		cw := &bodyCaptureWriter{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = cw

		defer func() {
			r := recover()
			if recorder != nil {
				code, msg := responseCode(cw.buf.Bytes())
				success := r == nil && c.Writer.Status() == http.StatusOK && code == 0

				entry := sink.Snapshot()
				entry.AccountID, entry.FallbackUsername = operatorFromJWT(c)
				entry.RequestPath = c.Request.Method + " " + c.Request.URL.Path

				if success {
					// 兜底按字段级判定：sink 部分写入（如仅 SetSummary）时
					// 只补缺失字段，已写字段不覆盖。
					if entry.Module == "" {
						entry.Module, _, _ = fallbackSemantics(c, domain.OpResultSuccess)
					}
					if entry.Target == "" {
						_, entry.Target, _ = fallbackSemantics(c, domain.OpResultSuccess)
					}
					if entry.Summary == "" {
						_, _, entry.Summary = fallbackSemantics(c, domain.OpResultSuccess)
					}
					entry.Result = domain.OpResultSuccess
				} else {
					// 失败路径埋点不注入（specs §5.1.2 步骤3），直接走兜底组装，
					// summary 摘响应 message；panic 或 body 无可解析 message 摘
					//「服务内部错误」（03 §1.5：400 形态同记 fail 并摘错误信息）。
					if r != nil || msg == "" {
						msg = internalErrMsg
					}
					entry.Module, entry.Target, entry.Summary = fallbackSemantics(c, domain.OpResultFail)
					entry.Detail, entry.Changes = "", nil
					if msg != "" {
						entry.Summary = msg
					}
					entry.Result = domain.OpResultFail
				}

				recorder.Record(entry)
			}
			if r != nil {
				panic(r)
			}
		}()
		c.Next()
	}
}

// OperationLogLogin 登录记录中间件：无 JWT 上下文，操作人取请求 username 原值
//（specs §5.1.3）。失败摘要统一「凭证校验未通过」反枚举（03 §1.6）。组装在
// defer 内执行，panic 路径也落记录后 re-panic 交外层 Recovery（03 §1.5）。
func OperationLogLogin(recorder service.PendingLogRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := peekUsername(c)

		cw := &bodyCaptureWriter{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = cw

		defer func() {
			r := recover()
			if recorder != nil {
				code, _ := responseCode(cw.buf.Bytes())
				success := r == nil && c.Writer.Status() == http.StatusOK && code == 0

				summary := "登录成功"
				if !success {
					summary = loginFailMsg
				}
				recorder.Record(service.PendingLog{
					FallbackName: username,
					Module:       domain.OpModuleLogin,
					Target:       "登录",
					Summary:      summary,
					Result:       map[bool]string{true: domain.OpResultSuccess, false: domain.OpResultFail}[success],
					RequestPath:  c.Request.Method + " " + c.Request.URL.Path,
				})
			}
			if r != nil {
				panic(r)
			}
		}()
		c.Next()
	}
}

// operatorFromJWT 从 JWT 注入键取操作人素材（specs §5.1.3）。
func operatorFromJWT(c *gin.Context) (int64, string) {
	id, _ := AccountIDFromContext(c)
	return id, c.GetString("username")
}

// peekUsername 读请求体取 username 原值并回填 body（ratelimit JSONFieldKey 先例）。
func peekUsername(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Request.Body = io.NopCloser(bytes.NewReader(nil))
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var parsed struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Username
}

// fallbackSemantics 路径级兜底语义：module 按前缀推断，target 为 POST+路径，
// summary 为「{业务域中文}接口调用：{成功|失败}」（03 §4.1 步骤3）。
func fallbackSemantics(c *gin.Context, result string) (module, target, summary string) {
	path := c.Request.URL.Path
	module = domain.OpModuleSystemParams
	for _, m := range fallbackModule {
		if strings.HasPrefix(path, m.prefix) {
			module = m.module
			break
		}
	}
	outcome := "失败"
	if result == domain.OpResultSuccess {
		outcome = "成功"
	}
	return module, "POST " + path, service.OperationLogModuleName(module) + "接口调用：" + outcome
}
