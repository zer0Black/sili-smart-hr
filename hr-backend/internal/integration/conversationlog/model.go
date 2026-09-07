package conversationlog

// ListSessionsRequest 是会话列表查询条件。UserID 非 0 时组件层内存过滤，
// total 保持上游口径；时间窗为 Unix 秒；Page/PageSize 越界由 buildListQuery 钳制。
type ListSessionsRequest struct {
	Username  string
	UserID    int
	StartTime int64 // Unix 秒
	EndTime   int64
	Page      int
	PageSize  int
}

// SessionSummary 是会话聚合元数据（列表接口 items 元素，specs §2.3 领域模型）。
// 全部为值类型，无雪花 ID 精度问题。
type SessionSummary struct {
	SessionKey    string // 会话标识，调详情的唯一凭据
	FirstTurnTime int64  // 窗口内首轮时间（Unix 秒）
	LastTurnTime  int64  // 窗口内末轮时间（Unix 秒）
	TurnCount     int    // 窗口内轮次数
	TokenName     string // 调用令牌名
	Username      string // 用户名（人员标识）
	UserID        int    // 上游用户 ID
	ModelName     string // 模型名
}

// apiEnvelope 是上游信封 {success, message, data}，data 由泛型注入。
// Success 用指针：区分显式 false（业务失败）与键缺失/null（契约破坏，归 ErrDecode）。
type apiEnvelope[T any] struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// apiListData 是列表响应 data。
type apiListData struct {
	Total int64           `json:"total"`
	Items []apiSessionRaw `json:"items"`
}

// apiSessionRaw 是上游 items 元素，字段名同兄弟仓库接口定义。
type apiSessionRaw struct {
	SessionKey    string `json:"session_key"`
	FirstTurnTime int64  `json:"first_turn_time"`
	LastTurnTime  int64  `json:"last_turn_time"`
	TurnCount     int    `json:"turn_count"`
	TokenName     string `json:"token_name"`
	Username      string `json:"username"`
	UserID        int    `json:"user_id"`
	ModelName     string `json:"model_name"`
}

// toSessionSummary 把上游元素映射为对外 DTO。
func toSessionSummary(r apiSessionRaw) SessionSummary {
	return SessionSummary{
		SessionKey:    r.SessionKey,
		FirstTurnTime: r.FirstTurnTime,
		LastTurnTime:  r.LastTurnTime,
		TurnCount:     r.TurnCount,
		TokenName:     r.TokenName,
		Username:      r.Username,
		UserID:        r.UserID,
		ModelName:     r.ModelName,
	}
}

// SessionDetail 是会话详情。对话原文（Message.Text）仅在内存临时持有，
// 禁止写入数据库、日志、缓存（specs §3.3）。
type SessionDetail struct {
	Session  SessionSummary // 会话聚合元数据，字段同列表
	Turns    []TurnMeta     // 逐轮元数据，全量不分页
	Messages []Message      // 完整对话序列，按时间升序，全量不分页
}

// TurnMeta 是逐轮元数据。ID 为上游回填的会话内序号，非全局唯一，
// 业务标识用 RequestID。
type TurnMeta struct {
	ID        int    // 会话内连续序号从 1 起，非全局唯一，业务标识用 RequestID
	CreatedAt int64  // 轮次时间（Unix 秒）
	RequestID string // 请求级业务标识
	TurnKind  string // first / normal / tool_round
}

// Message 是完整对话消息。Text 对 tool_use/tool_result 只存元信息。
type Message struct {
	Role string // user / assistant / tool / system
	Kind string // text / tool_use / tool_result
	Text string // text 存原文；tool_use/tool_result 存元信息
}

// apiDetailData 是详情响应 data：{session, turns, messages}。
type apiDetailData struct {
	Session  apiSessionRaw   `json:"session"`
	Turns    []apiTurnRaw    `json:"turns"`
	Messages []apiMessageRaw `json:"messages"`
}

// apiTurnRaw 是上游 turns 元素，id 为查询侧回填序号。
type apiTurnRaw struct {
	ID        int    `json:"id"`
	CreatedAt int64  `json:"created_at"`
	RequestID string `json:"request_id"`
	TurnKind  string `json:"turn_kind"`
}

// apiMessageRaw 是上游 messages 元素（role/kind/text 三字段）。
type apiMessageRaw struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}
