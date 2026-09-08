package extractor

// Extract 全流程状态机测试（specs §2.3 ExtractionResult 与错误码表、§2.4 能力2/3、§3.3、§5.1 用例表）。
// 内部测试包：fake LLM 与 fake repo 直接实现接口注入。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor/rules"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// ---- fake LLM 客户端 ----

// fakeLLMStream 按 chunk 序列回放。
type fakeLLMStream struct {
	chunks []string
	idx    int
}

func (s *fakeLLMStream) Recv() (llm.StreamChunk, error) {
	if s.idx < len(s.chunks) {
		c := s.chunks[s.idx]
		s.idx++
		return llm.StreamChunk{Content: c}, nil
	}
	return llm.StreamChunk{}, io.EOF
}
func (s *fakeLLMStream) Close() error      { return nil }
func (s *fakeLLMStream) Usage() (int, int) { return 0, 0 }

// fakeLLMClient 按脚本回放 LLM 应答：err 非空时直接返回调用错误。
// 记录每次调用的完整 prompt 与调用计数，供脱敏与次数断言。
type fakeLLMClient struct {
	mu       sync.Mutex
	resps    []string // 每次调用的应答脚本（超出下标的复用最后一个元素）
	errs     []error  // 每次调用的错误脚本
	calls    []string // 捕获的 prompt
	failAll  bool
	failOnce int // 第 1 次调用失败（errs 脚本便捷形态）
}

func (f *fakeLLMClient) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req.Messages[len(req.Messages)-1].Content)
	idx := len(f.calls) - 1
	f.mu.Unlock()
	if f.failAll {
		return nil, &llm.Error{Code: "ErrRetryExhausted", Msg: "fake upstream down"}
	}
	if f.failOnce > 0 && idx < f.failOnce {
		return nil, &llm.Error{Code: "ErrTimeout", Msg: "fake timeout"}
	}
	if len(f.errs) > idx && f.errs[idx] != nil {
		return nil, f.errs[idx]
	}
	resp := "ok"
	if len(f.resps) > 0 {
		resp = f.resps[min(idx, len(f.resps)-1)]
	}
	chunks := []string{resp}
	return &fakeLLMStream{chunks: chunks}, nil
}

func (f *fakeLLMClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}
func (f *fakeLLMClient) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

// 编译期接口断言：fake 与真实依赖同契约，接口加方法时 fake 编译即报错防漂移。
var (
	_ llm.Client                          = (*fakeLLMClient)(nil)
	_ repository.SessionFeatureRepository = (*fakeFeatureRepo)(nil)
	_ ConversationlogDetailFetcher        = (*fakeDetailFetcher)(nil)
)

// ---- fake repo ----

// fakeFeatureRepo 内存实现 SessionFeatureRepository：Save 的状态机语义
// （success/skipped 终态复用、failed 原地翻转）与真实现同构。
type fakeFeatureRepo struct {
	mu       sync.Mutex
	rows     []*domain.SessionFeature
	nextID   int64
	saveErr  error
	raceRow  *domain.SessionFeature // 竞态注入：Save 读行前对端抢先写入的终态行
	flipLost bool                   // 竞态注入：Save 读行后 UPDATE 前对端把行翻为终态（RowsAffected=0 形态）
}

func (f *fakeFeatureRepo) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.SessionKey == sessionKey {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeFeatureRepo) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return false, f.saveErr
	}
	// 竞态注入：模拟对端在本端 Save 读既有行之前已把行翻转为终态。
	if f.raceRow != nil && f.raceRow.SessionKey == rec.SessionKey {
		for _, r := range f.rows {
			if r.SessionKey == rec.SessionKey {
				*r = *f.raceRow
				break
			}
		}
	}
	for _, r := range f.rows {
		if r.SessionKey == rec.SessionKey {
			if r.Status == domain.FeatureStatusSuccess || r.Status == domain.FeatureStatusSkipped {
				return true, nil
			}
			// flipLost：模拟真实现 updateRow 的 WHERE id AND status 双条件落空
			//（读行后 UPDATE 前对端已把行翻为终态，RowsAffected=0），重查按库内
			// 终态行收敛 reused=true（与真实现 updateRow 收敛分支同构）。
			if f.flipLost {
				r.Status = domain.FeatureStatusSuccess
				r.ProfileJSON = `{"Stats":{}}`
				return true, nil
			}
			// 与真实现 updateColumns 同构：token_name 与 client 随翻转覆盖（令牌名纠正后
			// 重抽归属须同步，client 随新探测结果覆盖），守卫测试见 repository 包
			// TestUpdateColumnsMatchesDomainModel。
			r.Status = rec.Status
			r.TokenName = rec.TokenName
			r.Client = rec.Client
			r.TurnCount = rec.TurnCount
			r.FirstTurnAt = rec.FirstTurnAt
			r.LastTurnAt = rec.LastTurnAt
			r.ProfileJSON = rec.ProfileJSON
			r.ErrorCode = rec.ErrorCode
			return false, nil
		}
	}
	f.nextID++
	cp := *rec
	cp.ID = f.nextID
	f.rows = append(f.rows, &cp)
	return false, nil
}

func (f *fakeFeatureRepo) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	return nil, nil
}

func (f *fakeFeatureRepo) rowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeFeatureRepo) get(t *testing.T, key string) *domain.SessionFeature {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.SessionKey == key {
			cp := *r
			return &cp
		}
	}
	t.Fatalf("fake repo 中无 session_key=%s 的行", key)
	return nil
}

// ---- 测试脚手架 ----

// newExtractFixture 组装全流程 Extractor：fake LLM + fake repo + 空 fake 参数 + nil secrets。
// 零值 fakeSysParams 的 ReadStringArray 返回 (nil, nil)，语义即出厂回退分支。
func newExtractFixture(fl *fakeLLMClient, fr *fakeFeatureRepo) *Extractor {
	return New(fl, nil, fr, &fakeSysParams{}, nil)
}

// mkFullDetail 构造带完整 Session 元数据的合格会话（1 真实输入 + 工具 + 叙述）。
func mkFullDetail(key string, msgs []conversationlog.Message) *conversationlog.SessionDetail {
	return &conversationlog.SessionDetail{
		Session: conversationlog.SessionSummary{
			SessionKey:    key,
			FirstTurnTime: 1700000000,
			LastTurnTime:  1700000300,
			TurnCount:     4,
			TokenName:     "张三",
		},
		Turns: []conversationlog.TurnMeta{
			{ID: 1, CreatedAt: 1700000000, TurnKind: "first"},
			{ID: 2, CreatedAt: 1700000100, TurnKind: "tool_round"},
			{ID: 3, CreatedAt: 1700000200, TurnKind: "normal"},
			{ID: 4, CreatedAt: 1700000300, TurnKind: "tool_round"},
		},
		Messages: msgs,
	}
}

// mkWorkMessages 构造放行会话的消息集：1 真实输入 + 5 工具 + 1 叙述。
func mkWorkMessages(userText string) []conversationlog.Message {
	return append([]conversationlog.Message{mkMsg(userText)},
		mkToolMsg("Read args=111"),
		mkToolMsg("Edit args=2055"),
		mkToolMsg("Bash command=ls"),
		mkToolMsg("Read args=52"),
		mkToolMsg("Edit args=88"),
		mkRoleMsg("assistant", "已完成修复并补齐测试。"),
	)
}

// mkOKResponse 构造合法 LLM 三块输出。
func mkOKResponse() string {
	return mkProfileJSON("会话围绕支付模块错误处理重构，产出回归测试。", []string{"帮我修复登录超时问题"}, validBeh)
}

// TestExtractFullFlow 核心锚点：fake LLM 合法输出 → success 落库、四块齐全、归属正确。
func TestExtractFullFlow(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-full", mkWorkMessages("帮我修复登录超时问题"))

	res, err := ext.Extract(context.Background(), "s-full", detail, "李四")
	if err != nil {
		t.Fatalf("正常路径不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess || res.Skipped || res.Reused {
		t.Errorf("res = %+v, want Status=success 且无跳过无复用", res)
	}
	if res.Profile == nil {
		t.Fatal("Profile 不应为 nil")
	}
	p := res.Profile
	if p.Summary == "" || len(p.Instruction) != 1 || p.Behavior != validBeh {
		t.Errorf("LLM 三块 = %q %+v %+v", p.Summary, p.Instruction, p.Behavior)
	}
	if p.Stats.UserMsgCount != 1 || p.Stats.TurnCount != 4 || p.Stats.DurationSec != 300 {
		t.Errorf("Stats = %+v", p.Stats)
	}
	if !reflect.DeepEqual(p.Stats.ToolCounts, map[string]int{"Read": 2, "Edit": 2, "Bash": 1}) {
		t.Errorf("ToolCounts = %v", p.Stats.ToolCounts)
	}
	row := fr.get(t, "s-full")
	if row.Status != domain.FeatureStatusSuccess {
		t.Errorf("落行 status = %q, want success", row.Status)
	}
	if row.TokenName != "李四" {
		t.Errorf("落行 token_name = %q, want 李四（归属回填）", row.TokenName)
	}
	if row.ErrorCode != "" {
		t.Errorf("success 行 error_code 应空串, got %q", row.ErrorCode)
	}
	if row.TurnCount != 4 || !row.FirstTurnAt.Equal(time.Unix(1700000000, 0)) || !row.LastTurnAt.Equal(time.Unix(1700000300, 0)) {
		t.Errorf("落行元数据 = tc%d first%v last%v", row.TurnCount, row.FirstTurnAt, row.LastTurnAt)
	}
	// profile_json 反序列化断言四块齐全（Stats 键在）。
	var m map[string]any
	if err := json.Unmarshal([]byte(row.ProfileJSON), &m); err != nil {
		t.Fatalf("profile_json 非法: %v", err)
	}
	for _, k := range []string{"Stats", "Summary", "Instruction", "Behavior"} {
		if _, ok := m[k]; !ok {
			t.Errorf("profile_json 缺少四块键 %s", k)
		}
	}
	// prompt 不含人名（BR5：TokenName 剥离后才组装 prompt）。
	if got := fl.lastPrompt(); strings.Contains(got, "李四") {
		t.Error("prompt 中出现了 token_name，违反人名不进 LLM 上下文")
	}
}

// TestExtractReuse 核心锚点：预置 success 行再调 → Reused=true、LLM 零调用。
func TestExtractReuse(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-reuse", TokenName: "李四", Status: domain.FeatureStatusSuccess,
		TurnCount: 2, FirstTurnAt: time.Unix(1700000000, 0), LastTurnAt: time.Unix(1700000100, 0),
		ProfileJSON: `{"Stats":{}}`, ErrorCode: "",
	}}}
	ext := newExtractFixture(fl, fr)

	res, err := ext.Extract(context.Background(), "s-reuse", mkFullDetail("s-reuse", mkWorkMessages("重抽输入")), "李四")
	if err != nil {
		t.Fatalf("复用路径不应报错: %v", err)
	}
	if !res.Reused || res.Status != domain.FeatureStatusSuccess {
		t.Errorf("res = %+v, want Reused=true Status=success", res)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("复用路径 LLM 调用次数 = %d, want 0", n)
	}
}

// TestExtractSkippedReuse 核心锚点：预置 skipped 行（error_code=empty_shell）→ 终态复用。
func TestExtractSkippedReuse(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-skipreuse", TokenName: "李四", Status: domain.FeatureStatusSkipped,
		TurnCount: 1, FirstTurnAt: time.Unix(1700000000, 0), LastTurnAt: time.Unix(1700000100, 0),
		ProfileJSON: "", ErrorCode: "empty_shell",
	}}}
	ext := newExtractFixture(fl, fr)

	res, err := ext.Extract(context.Background(), "s-skipreuse", mkFullDetail("s-skipreuse", mkWorkMessages("再输入")), "李四")
	if err != nil {
		t.Fatalf("skipped 终态复用不应报错: %v", err)
	}
	if !res.Reused || res.Status != domain.FeatureStatusSkipped {
		t.Errorf("res = %+v, want Reused=true Status=skipped", res)
	}
	// Skipped=true 与首次 persistSkipped 语义一致：worker 以 res.Skipped 记跳过日志，
	// 复用路径不置会让重放场景静默。
	if !res.Skipped {
		t.Error("skipped 复用应置 Skipped=true（与首次落行语义一致）")
	}
	if res.SkipReason != "empty_shell" {
		t.Errorf("SkipReason = %q, want 既有行 error_code=empty_shell", res.SkipReason)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("skipped 复用路径 LLM 调用次数 = %d, want 0", n)
	}
}

// TestExtractSkippedPersist 核心锚点：空壳会话 → 落 skipped 元数据行、LLM 零调用。
func TestExtractSkippedPersist(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	// 空壳：零输入保留类 5 条（< MinKeptMessages）。
	shell := mkShellMsgs(3)
	detail := mkFullDetail("s-shell", shell)

	res, err := ext.Extract(context.Background(), "s-shell", detail, "李四")
	if err != nil {
		t.Fatalf("跳过路径 err 应为 nil: %v", err)
	}
	if !res.Skipped || res.Status != domain.FeatureStatusSkipped {
		t.Errorf("res = %+v, want Skipped=true Status=skipped", res)
	}
	if res.SkipReason != SkipEmptyShell {
		t.Errorf("SkipReason = %q, want empty_shell", res.SkipReason)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("跳过路径 LLM 调用次数 = %d, want 0", n)
	}
	row := fr.get(t, "s-shell")
	if row.Status != domain.FeatureStatusSkipped || row.ProfileJSON != "" || row.ErrorCode != "empty_shell" {
		t.Errorf("落行 = status%q pj%q ec%q, want skipped/空串/empty_shell", row.Status, row.ProfileJSON, row.ErrorCode)
	}
	if row.TokenName != "李四" || row.TurnCount != 4 {
		t.Errorf("skipped 行元数据 = tn%q tc%d", row.TokenName, row.TurnCount)
	}
	if !row.FirstTurnAt.Equal(time.Unix(1700000000, 0)) || !row.LastTurnAt.Equal(time.Unix(1700000300, 0)) {
		t.Errorf("skipped 行时间列 = %v %v", row.FirstTurnAt, row.LastTurnAt)
	}
}

// TestExtractDetailInvalidPersist 边界补充：detail_invalid 同样落 skipped 元数据行（specs §2.3）。
func TestExtractDetailInvalidPersist(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := &conversationlog.SessionDetail{Session: conversationlog.SessionSummary{SessionKey: "s-bad", TurnCount: 3}}

	res, err := ext.Extract(context.Background(), "s-bad", detail, "李四")
	if err != nil {
		t.Fatalf("detail_invalid 应走跳过而非 error: %v", err)
	}
	if !res.Skipped || res.SkipReason != SkipDetailInvalid {
		t.Errorf("res = %+v, want Skipped=true detail_invalid", res)
	}
	row := fr.get(t, "s-bad")
	if row.Status != domain.FeatureStatusSkipped || row.ErrorCode != SkipDetailInvalid || row.ProfileJSON != "" {
		t.Errorf("落行 = %+v", row)
	}
}

// TestExtractNilDetailPersist 边界补充：nil detail 走规则0 detail_invalid，守卫零值落行不 panic。
func TestExtractNilDetailPersist(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)

	res, err := ext.Extract(context.Background(), "s-nil", nil, "李四")
	if err != nil {
		t.Fatalf("nil detail 应走跳过而非 error: %v", err)
	}
	if !res.Skipped || res.SkipReason != SkipDetailInvalid {
		t.Errorf("res = %+v, want Skipped=true detail_invalid", res)
	}
	row := fr.get(t, "s-nil")
	if row.Status != domain.FeatureStatusSkipped || row.ErrorCode != SkipDetailInvalid || row.ProfileJSON != "" {
		t.Errorf("落行 = %+v, want skipped/detail_invalid/空串", row)
	}
	if row.TokenName != "李四" || row.TurnCount != 0 {
		t.Errorf("nil detail 落行元数据 = tn%q tc%d, want 李四/0", row.TokenName, row.TurnCount)
	}
	// 时间列是 not null 列：零值 time.Time 序列化 0000-00-00 被 MySQL 严格模式拒绝，
	// nil detail 落 epoch（1970-01-01 UTC）守住落行契约。
	epoch := time.Unix(0, 0).UTC()
	if !row.FirstTurnAt.Equal(epoch) || !row.LastTurnAt.Equal(epoch) {
		t.Errorf("nil detail 时间列应取 epoch, got %v %v", row.FirstTurnAt, row.LastTurnAt)
	}
}

// TestExtractLLMFailDegraded 核心锚点：LLM 持续失败 → err=nil、failed 降级行仅 Stats 块。
func TestExtractLLMFailDegraded(t *testing.T) {
	fl := &fakeLLMClient{failAll: true}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-llmfail", mkWorkMessages("帮我修复导出超时"))

	res, err := ext.Extract(context.Background(), "s-llmfail", detail, "李四")
	if err != nil {
		t.Fatalf("LLM 失败降级是终态设计，err 应为 nil: %v", err)
	}
	if res.Status != domain.FeatureStatusFailed || res.Skipped || res.Reused {
		t.Errorf("res = %+v, want Status=failed", res)
	}
	if res.Profile == nil || res.Profile.Stats.UserMsgCount != 1 {
		t.Errorf("降级仍应返回含 Stats 的 Profile: %+v", res.Profile)
	}
	if res.Profile.Summary != "" || len(res.Profile.Instruction) != 0 {
		t.Errorf("降级 Profile 三块应空: %q %+v", res.Profile.Summary, res.Profile.Instruction)
	}
	row := fr.get(t, "s-llmfail")
	if row.Status != domain.FeatureStatusFailed {
		t.Errorf("落行 status = %q, want failed", row.Status)
	}
	if row.ErrorCode != ErrLLMUpstreamCode {
		t.Errorf("error_code = %q, want %q", row.ErrorCode, ErrLLMUpstreamCode)
	}
	// profile_json 仅 Stats 块：反序列化断言 Instruction 为空、无 Summary 键。
	var fp FeatureProfile
	if err := json.Unmarshal([]byte(row.ProfileJSON), &fp); err != nil {
		t.Fatalf("failed 行 profile_json 非法: %v", err)
	}
	if len(fp.Instruction) != 0 || fp.Summary != "" {
		t.Errorf("failed 行三块应为空: summary%q instr%+v", fp.Summary, fp.Instruction)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(row.ProfileJSON), &m); err != nil {
		t.Fatalf("failed 行 profile_json 非法: %v", err)
	}
	if _, ok := m["Stats"]; !ok {
		t.Error("failed 行 profile_json 缺 Stats 块")
	}
	if _, ok := m["Summary"]; ok {
		t.Error("failed 行 profile_json 不应含 Summary 键")
	}
	// failed 行仅 Stats 块（04 §3.1）：Instruction 零值 omitempty 省略，序列化无该键。
	if instr, ok := m["Instruction"]; ok {
		t.Errorf("failed 行 Instruction = %v, want 键省略（仅 Stats 块）", instr)
	}
}

// TestExtractSchemaRetry 核心锚点：先非法后合法 → 恰好 2 次调用、success。
func TestExtractSchemaRetry(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{"{not json", mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-schema", mkWorkMessages("帮我修复登录超时"))

	res, err := ext.Extract(context.Background(), "s-schema", detail, "李四")
	if err != nil {
		t.Fatalf("重试后成功不应报错: %v", err)
	}
	if n := fl.callCount(); n != 2 {
		t.Errorf("LLM 调用次数 = %d, want 2（首次 + schema 重试）", n)
	}
	if res.Status != domain.FeatureStatusSuccess || res.Profile == nil || res.Profile.Summary == "" {
		t.Errorf("res = %+v, want success 且三块齐全", res)
	}
	if fr.get(t, "s-schema").Status != domain.FeatureStatusSuccess {
		t.Error("落行应为 success")
	}
}

// TestExtractSchemaRetryExhausted 边界补充：schema 连续两次失败 → ErrSchemaInvalid 降级行。
func TestExtractSchemaRetryExhausted(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{"{not json", "still not json"}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-schfail", mkWorkMessages("帮我修复登录超时"))

	res, err := ext.Extract(context.Background(), "s-schfail", detail, "李四")
	if err != nil {
		t.Fatalf("schema 两次失败仍为降级终态: %v", err)
	}
	if n := fl.callCount(); n != 2 {
		t.Errorf("LLM 调用次数 = %d, want 2", n)
	}
	if res.Status != domain.FeatureStatusFailed {
		t.Errorf("res.Status = %q, want failed", res.Status)
	}
	row := fr.get(t, "s-schfail")
	if row.ErrorCode != ErrSchemaInvalidCode {
		t.Errorf("error_code = %q, want %q", row.ErrorCode, ErrSchemaInvalidCode)
	}
	if row.Status != domain.FeatureStatusFailed {
		t.Errorf("落行 status = %q, want failed", row.Status)
	}
}

// TestExtractFailedFlip 核心锚点：预置 failed 行 + LLM 恢复 → 原行翻转 success 无重复行。
func TestExtractFailedFlip(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 99, SessionKey: "s-flip", TokenName: "李四", Status: domain.FeatureStatusFailed,
		TurnCount: 2, FirstTurnAt: time.Unix(1700000000, 0), LastTurnAt: time.Unix(1700000100, 0),
		ProfileJSON: `{"Stats":{"TurnCount":2}}`, ErrorCode: ErrLLMUpstreamCode,
	}}}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-flip", mkWorkMessages("帮我修复登录超时"))

	res, err := ext.Extract(context.Background(), "s-flip", detail, "李四")
	if err != nil {
		t.Fatalf("failed 翻转路径不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess || res.Reused {
		t.Errorf("res = %+v, want Status=success 非复用", res)
	}
	if n := fl.callCount(); n != 1 {
		t.Errorf("failed 行应重抽，LLM 调用次数 = %d, want 1", n)
	}
	if n := fr.rowCount(); n != 1 {
		t.Errorf("表行数 = %d, want 1（原地翻转无重复行）", n)
	}
	row := fr.get(t, "s-flip")
	if row.ID != 99 || row.Status != domain.FeatureStatusSuccess || row.ErrorCode != "" {
		t.Errorf("翻转后 = id%d status%q ec%q, want 99 success 空串", row.ID, row.Status, row.ErrorCode)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(row.ProfileJSON), &m); err != nil {
		t.Fatalf("翻转后 profile_json 非法: %v", err)
	}
	if _, ok := m["Summary"]; !ok {
		t.Error("翻转后应补齐 LLM 三块（Summary 键缺失）")
	}
}

// TestExtractPersistRaceConvergesToTerminal 核心锚点：persist 竞态收敛。对端在
// 本端 Save 前已把行翻转为终态（success/skipped），Save 返回 reused=true 且库内
// 保持对端内容，本端三态判定作废，返回值按库内终态改写（failed 场景误报修复的回归锁）。
func TestExtractPersistRaceConvergesToTerminal(t *testing.T) {
	// 场景一：本端判 failed，对端已翻 success → 返回 Status=success 非 failed。
	t.Run("failed 竞态对端已翻 success", func(t *testing.T) {
		fl := &fakeLLMClient{failAll: true}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 7, SessionKey: "s-race-fail", TokenName: "李四", Status: domain.FeatureStatusFailed,
			ErrorCode: ErrLLMUpstreamCode,
		}}, raceRow: &domain.SessionFeature{
			ID: 7, SessionKey: "s-race-fail", TokenName: "李四", Status: domain.FeatureStatusSuccess,
			ProfileJSON: `{"Stats":{}}`,
		}}
		ext := newExtractFixture(fl, fr)
		detail := mkFullDetail("s-race-fail", mkWorkMessages("帮我修复登录超时"))

		res, err := ext.Extract(context.Background(), "s-race-fail", detail, "李四")
		if err != nil {
			t.Fatalf("竞态收敛不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess {
			t.Errorf("res.Status = %q, want 库内终态 success（failed 误报回归锁）", res.Status)
		}
		if !res.Reused {
			t.Error("竞态收敛应置 Reused=true")
		}
		row := fr.get(t, "s-race-fail")
		if row.Status != domain.FeatureStatusSuccess || row.ProfileJSON != `{"Stats":{}}` {
			t.Errorf("库内行应保持对端内容, got status%q pj%q", row.Status, row.ProfileJSON)
		}
	})

	// 场景二：本端判 success，对端已写 skipped 终态 → 返回 Status=skipped 且 SkipReason
	// 回传既有行 error_code（与 persistSkipped/reuseTerminal 的 skipped 语义一致）。
	t.Run("success 竞态对端已写 skipped", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{raceRow: &domain.SessionFeature{
			ID: 8, SessionKey: "s-race-ok", TokenName: "李四", Status: domain.FeatureStatusSkipped,
			ErrorCode: SkipEmptyShell,
		}}
		fr.rows = []*domain.SessionFeature{{
			ID: 8, SessionKey: "s-race-ok", TokenName: "李四", Status: domain.FeatureStatusFailed,
			ErrorCode: ErrLLMUpstreamCode,
		}}
		ext := newExtractFixture(fl, fr)
		detail := mkFullDetail("s-race-ok", mkWorkMessages("帮我修复登录超时"))

		res, err := ext.Extract(context.Background(), "s-race-ok", detail, "李四")
		if err != nil {
			t.Fatalf("竞态收敛不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSkipped || !res.Skipped || res.SkipReason != SkipEmptyShell {
			t.Errorf("res = %+v, want 库内终态 skipped 且回传 error_code", res)
		}
	})

	// 场景三：persistSkipped 同样消费 reused 保持对称（本端判 skipped，对端已写 success）。
	t.Run("skipped 竞态对端已写 success", func(t *testing.T) {
		fl := &fakeLLMClient{}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 9, SessionKey: "s-race-skip", TokenName: "李四", Status: domain.FeatureStatusFailed,
			ErrorCode: ErrLLMUpstreamCode,
		}}, raceRow: &domain.SessionFeature{
			ID: 9, SessionKey: "s-race-skip", TokenName: "李四", Status: domain.FeatureStatusSuccess,
			ProfileJSON: `{"Stats":{}}`,
		}}
		ext := newExtractFixture(fl, fr)
		shell := append(mkTools(3), mkRoleMsg("assistant", "空壳叙述。"))
		detail := mkFullDetail("s-race-skip", shell)

		res, err := ext.Extract(context.Background(), "s-race-skip", detail, "李四")
		if err != nil {
			t.Fatalf("竞态收敛不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess || res.Skipped {
			t.Errorf("res = %+v, want 库内终态 success 且无跳过", res)
		}
	})

	// 场景四：Save 读行后 UPDATE 落空（真实现 RowsAffected=0 收敛分支）：本端内容
	// 未落库，重查按库内终态行收敛 reused，返回值与库内一致（该路径此前无 fake 覆盖）。
	t.Run("failed 翻转落空对端已终态化", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 10, SessionKey: "s-flip-lost", TokenName: "李四", Status: domain.FeatureStatusFailed,
			ErrorCode: ErrLLMUpstreamCode,
		}}, flipLost: true}
		ext := newExtractFixture(fl, fr)
		detail := mkFullDetail("s-flip-lost", mkWorkMessages("帮我修复登录超时"))

		res, err := ext.Extract(context.Background(), "s-flip-lost", detail, "李四")
		if err != nil {
			t.Fatalf("翻转落空收敛不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess || !res.Reused {
			t.Errorf("res = %+v, want Reused=true Status=success（按库内终态收敛）", res)
		}
	})
}

// TestExtractStoreFail 核心锚点：Save 返回 err → error 上抛（ErrStoreWrite 语义）。
func TestExtractStoreFail(t *testing.T) {
	storeErr := errors.New("fake db write failure")
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{saveErr: storeErr}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-store", mkWorkMessages("帮我修复登录超时"))

	res, err := ext.Extract(context.Background(), "s-store", detail, "李四")
	if err == nil {
		t.Fatalf("落库失败应上抛 error, res=%+v", res)
	}
	if !errors.Is(err, storeErr) {
		t.Errorf("err = %v, 应包装底层落库错误", err)
	}
	if !strings.Contains(err.Error(), "ErrStoreWrite") {
		t.Errorf("err = %v, 缺 ErrStoreWrite 语义", err)
	}
}

// TestExtractSkippedStoreFail 边界补充：skipped 行落库失败同样上抛（BR4 不区分状态）。
func TestExtractSkippedStoreFail(t *testing.T) {
	storeErr := errors.New("fake db write failure")
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{saveErr: storeErr}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-skipstore", append(mkTools(3), mkRoleMsg("assistant", "空壳叙述。")))

	if _, err := ext.Extract(context.Background(), "s-skipstore", detail, "李四"); err == nil {
		t.Fatal("skipped 行落库失败同样应上抛 error")
	}
}

// TestStatsDeterministic 核心锚点：LLM 正常与失败两路径 Stats 深比较相等。
func TestStatsDeterministic(t *testing.T) {
	msgs := mkWorkMessages("报错堆栈：\nat main.go:32 panic\ngoroutine 1 [running]:")
	run := func(fl *fakeLLMClient) ProfileStats {
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)
		res, err := ext.Extract(context.Background(), "s-det", mkFullDetail("s-det", msgs), "李四")
		if err != nil {
			t.Fatalf("Extract 不应报错: %v", err)
		}
		return res.Profile.Stats
	}
	okStats := run(&fakeLLMClient{resps: []string{mkOKResponse()}})
	failStats := run(&fakeLLMClient{failAll: true})
	if !reflect.DeepEqual(okStats, failStats) {
		t.Errorf("两路径 Stats 不一致:\nok  = %+v\nfail= %+v", okStats, failStats)
	}
}

// TestInputSideRedact 核心锚点：用户指令携带 sk- 密钥 → prompt 已替换占位符、
// PasteCharCount 按原文长度计（specs §5.1 输入侧脱敏用例、§3.3 安全表）。
func TestInputSideRedact(t *testing.T) {
	paste := "密钥与堆栈：\n```\nsk-AbCd1234EfGh\ngoroutine 1 [running]:\n```"
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-redact", mkWorkMessages(paste))

	res, err := ext.Extract(context.Background(), "s-redact", detail, "李四")
	if err != nil {
		t.Fatalf("脱敏路径不应报错: %v", err)
	}
	prompt := fl.lastPrompt()
	if strings.Contains(prompt, "sk-AbCd1234EfGh") {
		t.Error("明文密钥进入了喂 LLM 的 prompt（敏感串不进 LLM 上下文）")
	}
	if !strings.Contains(prompt, "[SECRET]") {
		t.Error("prompt 中缺少 [SECRET] 占位符")
	}
	if got := res.Profile.Stats.PasteCharCount; got != utf8.RuneCountInString(paste) {
		t.Errorf("PasteCharCount = %d, want 原文长度 %d（按脱敏前原文计）", got, utf8.RuneCountInString(paste))
	}
}

// TestAssistantNarrativeRedact 回归锚点：assistant 叙述复述密钥与回显路径 →
// [AI] 行同口径脱敏后才进视图（specs §3.3 视图文本只见占位符，防 assistant
// 复述集成密钥时明文出域到外部 LLM）。
func TestAssistantNarrativeRedact(t *testing.T) {
	narrative := "已验证密钥 sk-AbCd1234EfGh 有效，配置在 D:\\secrets\\token.txt。"
	msgs := append(mkWorkMessages("帮我修复登录超时问题"),
		mkRoleMsg("assistant", narrative),
	)
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)

	_, err := ext.Extract(context.Background(), "s-ai-redact", mkFullDetail("s-ai-redact", msgs), "李四")
	if err != nil {
		t.Fatalf("脱敏路径不应报错: %v", err)
	}
	prompt := fl.lastPrompt()
	if strings.Contains(prompt, "sk-AbCd1234EfGh") {
		t.Error("assistant 复述的明文密钥进入了喂 LLM 的 prompt")
	}
	if !strings.Contains(prompt, "[SECRET]") {
		t.Error("assistant 叙述行应含 [SECRET] 占位符")
	}
	// 落库档案同样无明文（LLM 输出侧 + 视图侧双防线）。
	row := fr.get(t, "s-ai-redact")
	if strings.Contains(row.ProfileJSON, "sk-AbCd1234EfGh") {
		t.Error("落库档案含明文密钥")
	}
}

// TestExtractZeroInputInstructionBlocked 边界补充（BR2 延伸）：零输入会话 LLM 编造
// Instruction 被 parseProfile 拦 → 重试后仍编造落 failed（schema 校验防线，T10 BR2）。
func TestExtractZeroInputInstructionBlocked(t *testing.T) {
	// 零输入放行会话：continuation 摘要（噪音丢弃）+ 12 工具 + 2 叙述。
	msgs := append([]conversationlog.Message{mkMsg("This session is being continued from a previous conversation, proceed.")},
		append(mkTools(12),
			mkRoleMsg("assistant", "已完成数据迁移。"),
			mkRoleMsg("assistant", "回归验证通过。"),
		)...)
	fabricated := mkProfileJSON("续接推进会话。", []string{"编造的指令"}, validBeh)
	fl := &fakeLLMClient{resps: []string{fabricated}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)

	res, err := ext.Extract(context.Background(), "s-fab", mkFullDetail("s-fab", msgs), "李四")
	if err != nil {
		t.Fatalf("编造拦截走降级终态: %v", err)
	}
	if res.Status != domain.FeatureStatusFailed {
		t.Errorf("res.Status = %q, want failed", res.Status)
	}
	if row := fr.get(t, "s-fab"); row.ErrorCode != ErrSchemaInvalidCode {
		t.Errorf("error_code = %q, want ErrSchemaInvalid", row.ErrorCode)
	}
}

// TestExtractCtxCanceled 边界补充：ctx 取消属基础设施错误，走 error 通道交任务重试，
// 与 LLM 真实故障的 failed 终态降级分流（部署重启窗口不丢档案）。
func TestExtractCtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fl := &fakeLLMClient{errs: []error{context.Canceled}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-ctx", mkWorkMessages("帮我修复登录超时"))
	cancel() // 模拟任务 ctx 被 abort 后流调用返回取消错误

	res, err := ext.Extract(ctx, "s-ctx", detail, "李四")
	if err == nil {
		t.Fatal("ctx 取消应走 error 通道交任务重试")
	}
	if res != nil {
		t.Errorf("res = %+v, want nil（不落终态行）", res)
	}
	if n := fr.rowCount(); n != 0 {
		t.Errorf("ctx 取消不应落行, rows = %d, want 0", n)
	}
}

// TestExtractLLMErrorRouting 核心锚点：底座错误码三路分流。
// 超限落 failed 且 error_code=ErrContextLengthExceeded（与上游故障可区分）；
// ErrTimeout/ErrQueueFull/ErrRateLimited 瞬时错误（底座重试耗尽产物）同按
// ErrLLMUpstream 降级落 failed 行（specs §2.3：LLM 调用失败降级保留统计、
// 当期评估照常推进），补跑时重抽翻转。
func TestExtractLLMErrorRouting(t *testing.T) {
	makeErr := func(code string) error { return &llm.Error{Code: code, Msg: "fake " + code} }
	detail := mkFullDetail("s-route", mkWorkMessages("帮我修复登录超时"))

	t.Run("超限落failed且错误码可区分", func(t *testing.T) {
		fl := &fakeLLMClient{errs: []error{makeErr("ErrContextLengthExceeded")}}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)

		res, err := ext.Extract(context.Background(), "s-route", detail, "李四")
		if err != nil {
			t.Fatalf("超限属 failed 终态降级, err 应 nil: %v", err)
		}
		if res.Status != domain.FeatureStatusFailed {
			t.Errorf("res.Status = %q, want failed", res.Status)
		}
		row := fr.get(t, "s-route")
		if row.Status != domain.FeatureStatusFailed || row.ErrorCode != "ErrContextLengthExceeded" {
			t.Errorf("落行 = status%q ec%q, want failed/ErrContextLengthExceeded", row.Status, row.ErrorCode)
		}
		// 超限不重试：一次调用即终态（重试同样超限无意义）。
		if n := fl.callCount(); n != 1 {
			t.Errorf("LLM 调用次数 = %d, want 1（超限不进 schema 重试）", n)
		}
	})

	t.Run("密钥错误落failed终态", func(t *testing.T) {
		fl := &fakeLLMClient{errs: []error{makeErr("ErrAuth")}}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)

		res, err := ext.Extract(context.Background(), "s-route", detail, "李四")
		if err != nil {
			t.Fatalf("密钥类确定性错误走 failed 终态: %v", err)
		}
		if res.Status != domain.FeatureStatusFailed {
			t.Errorf("res.Status = %q, want failed", res.Status)
		}
		if row := fr.get(t, "s-route"); row.ErrorCode != ErrLLMUpstreamCode {
			t.Errorf("error_code = %q, want ErrLLMUpstream", row.ErrorCode)
		}
	})

	for _, code := range []string{"ErrTimeout", "ErrQueueFull", "ErrRateLimited"} {
		t.Run("瞬时错误"+code+"降级落failed", func(t *testing.T) {
			fl := &fakeLLMClient{errs: []error{makeErr(code)}}
			fr := &fakeFeatureRepo{}
			ext := newExtractFixture(fl, fr)

			res, err := ext.Extract(context.Background(), "s-route", detail, "李四")
			if err != nil {
				t.Fatalf("%s 应降级落 failed 终态: %v", code, err)
			}
			if res.Status != domain.FeatureStatusFailed {
				t.Errorf("res.Status = %q, want failed", res.Status)
			}
			row := fr.get(t, "s-route")
			if row.Status != domain.FeatureStatusFailed || row.ErrorCode != ErrLLMUpstreamCode {
				t.Errorf("落行 = status%q ec%q, want failed/ErrLLMUpstream", row.Status, row.ErrorCode)
			}
			if res.Profile == nil || res.Profile.Stats.UserMsgCount != 1 {
				t.Errorf("降级仍应返回含 Stats 的 Profile: %+v", res.Profile)
			}
		})
	}
}

// TestExtractReuseMetadataThreeStates 边界补充：三态行的 first/last_turn_at 时间转换
// 同源（time.Unix 口径），detail.Session 是唯一时间来源（04 §3.1）。
func TestExtractReuseMetadataThreeStates(t *testing.T) {
	base := time.Unix(1700000000, 0)
	last := time.Unix(1700000300, 0)
	detail := &conversationlog.SessionDetail{
		Session: conversationlog.SessionSummary{
			SessionKey: "s-meta", FirstTurnTime: 1700000000, LastTurnTime: 1700000300, TurnCount: 7,
		},
		Messages: mkWorkMessages("输入"),
	}
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)

	if _, err := ext.Extract(context.Background(), "s-meta", detail, "李四"); err != nil {
		t.Fatalf("success 路径: %v", err)
	}
	row := fr.get(t, "s-meta")
	if !row.FirstTurnAt.Equal(base) || !row.LastTurnAt.Equal(last) || row.TurnCount != 7 {
		t.Errorf("落行元数据 = %v %v tc%d, want %v %v tc7", row.FirstTurnAt, row.LastTurnAt, row.TurnCount, base, last)
	}
	if res := fr.rows[0]; res.SessionKey != "s-meta" || res.Status != domain.FeatureStatusSuccess {
		t.Errorf("落行 = %+v", res)
	}
}

// ---- ExtractByKey 测试（specs §2.4 能力6、03 §4.2）----

// fakeDetailFetcher 是 ConversationlogDetailFetcher 的 fake：按 sessionKey 回放详情或错误，
// 捕获收到的 secret 供 Bearer 传递断言。
type fakeDetailFetcher struct {
	mu      sync.Mutex
	detail  *conversationlog.SessionDetail
	err     error
	secrets []string
}

func (f *fakeDetailFetcher) GetSessionDetail(ctx context.Context, secret, sessionKey string) (*conversationlog.SessionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets = append(f.secrets, secret)
	if f.err != nil {
		return nil, f.err
	}
	if f.detail != nil {
		cp := *f.detail
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeDetailFetcher) lastSecret() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.secrets) == 0 {
		return ""
	}
	return f.secrets[len(f.secrets)-1]
}

func (f *fakeDetailFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.secrets)
}

// newKeyFixture 组装 ExtractByKey 用 Extractor：fake 拉取器 + SecretProvider 返回固定明文。
func newKeyFixture(fl *fakeLLMClient, fr *fakeFeatureRepo, fdf *fakeDetailFetcher) *Extractor {
	secrets := SecretProvider(func(ctx context.Context) (string, error) { return "plain-secret", nil })
	return New(fl, fdf, fr, &fakeSysParams{}, secrets)
}

// TestExtractByKeyFullChain 核心锚点：fake 拉取器 + fake LLM + fake repo → success 落库
// 归属正确、密钥明文传给拉取器（specs §5.2 单会话全链路）。
func TestExtractByKeyFullChain(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-key", mkWorkMessages("帮我修复登录超时问题"))}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-key", "王五")
	if err != nil {
		t.Fatalf("全链路不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess || res.Skipped || res.Reused {
		t.Errorf("res = %+v, want Status=success", res)
	}
	if res.Profile == nil || res.Profile.Summary == "" {
		t.Errorf("Profile = %+v, want 三块齐全", res.Profile)
	}
	row := fr.get(t, "s-key")
	if row.Status != domain.FeatureStatusSuccess {
		t.Errorf("落行 status = %q, want success", row.Status)
	}
	if row.TokenName != "王五" {
		t.Errorf("落行 token_name = %q, want 王五（specs §5.2 归属正确）", row.TokenName)
	}
	if got := fdf.lastSecret(); got != "plain-secret" {
		t.Errorf("传给拉取器的密钥 = %q, want SecretProvider 明文", got)
	}
}

// TestExtractByKeyNotFound 核心锚点：ErrUpstreamBusiness 载体 IsNotFound → 业务空，
// err=nil、无落行（specs §2.3 ErrNotFound 业务空口径）。
func TestExtractByKeyNotFound(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	// wrapErr 形态：sentinel + UpstreamError 载体（IsNotFound 白名单识别）。
	fdf := &fakeDetailFetcher{err: &upstreamWrap{carrier: &conversationlog.UpstreamError{Msg: "会话不存在"}}}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-gone", "王五")
	if err != nil {
		t.Fatalf("业务空应 err=nil: %v", err)
	}
	if res == nil {
		t.Fatal("res 不应为 nil")
	}
	if res.Skipped || res.Status != "" {
		t.Errorf("res = %+v, want Skipped=false Status=空串", res)
	}
	if n := fr.rowCount(); n != 0 {
		t.Errorf("落行数 = %d, want 0（业务空不落档案行）", n)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("业务空不应调 LLM, got %d", n)
	}
}

// upstreamWrap 复刻 conversationlog classifiedError 双链形态：sentinel + UpstreamError 载体，
// 供 errors.Is/As 与 IsNotFound 组合判定（与集成包 wrapErr 产物同构）。
type upstreamWrap struct {
	carrier *conversationlog.UpstreamError
}

func (u *upstreamWrap) Error() string { return u.carrier.Error() }
func (u *upstreamWrap) Unwrap() []error {
	return []error{conversationlog.ErrUpstreamBusiness, u.carrier}
}

// TestExtractByKeyUpstreamErr 核心锚点：ErrNetwork → err 非 nil（交重试）。
func TestExtractByKeyUpstreamErr(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("net: %w", conversationlog.ErrNetwork)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-net", "王五")
	if err == nil {
		t.Fatalf("网络错误应上抛交重试, res=%+v", res)
	}
	if !errors.Is(err, conversationlog.ErrNetwork) {
		t.Errorf("err = %v, want 保留 ErrNetwork 哨兵链", err)
	}
	if n := fr.rowCount(); n != 0 {
		t.Errorf("落行数 = %d, want 0", n)
	}
}

// TestExtractByKeySecretFail 边界补充：SecretProvider 未配置/解密失败 → error 通道上抛。
func TestExtractByKeySecretFail(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-nosec", mkWorkMessages("输入"))}
	secrets := SecretProvider(func(ctx context.Context) (string, error) { return "", errors.New("integration secret not configured") })
	ext := New(fl, fdf, fr, &fakeSysParams{}, secrets)

	res, err := ext.ExtractByKey(context.Background(), "s-nosec", "王五")
	if err == nil {
		t.Fatalf("密钥未配置应上抛 error, res=%+v", res)
	}
	if n := fdf.callCount(); n != 0 {
		t.Errorf("密钥缺失不应发起拉取, got %d", n)
	}
}

// TestExtractByKeyBusinessNonNotFound 边界补充：ErrUpstreamBusiness 未命中 IsNotFound
// 白名单 → 会话级失败，error 上抛交重试（specs §2.3：未命中按会话级失败）。
func TestExtractByKeyBusinessNonNotFound(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("wrap: %w", &conversationlog.UpstreamError{Msg: "数据库维护中"})}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-biz", "王五")
	if err == nil {
		t.Fatalf("非 not-found 业务错误应上抛, res=%+v", res)
	}
	if !errors.Is(err, conversationlog.ErrUpstreamBusiness) {
		t.Errorf("err = %v, want 保留 ErrUpstreamBusiness 哨兵", err)
	}
}

// TestExtractByKeySkippedPath 边界补充：拉取成功但空壳会话 → 走 Extract 跳过终态。
func TestExtractByKeySkippedPath(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	shell := mkShellMsgs(3)
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-keyskip", shell)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-keyskip", "王五")
	if err != nil {
		t.Fatalf("跳过终态 err 应为 nil: %v", err)
	}
	if !res.Skipped || res.SkipReason != SkipEmptyShell {
		t.Errorf("res = %+v, want Skipped empty_shell", res)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("空壳会话不应调 LLM, got %d", n)
	}
}

// TestExtractByKeyReuseShortCircuit 核心锚点：既有 success 行 → 拉详情前终态预检
// 直接复用，零拉取零 LLM（specs L249/L774 不重拉契约：补跑不重拉 success 行）。
func TestExtractByKeyReuseShortCircuit(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-keyreuse", TokenName: "王五", Status: domain.FeatureStatusSuccess,
		ProfileJSON: `{"Stats":{}}`,
	}}}
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-keyreuse", mkWorkMessages("重抽输入"))}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-keyreuse", "王五")
	if err != nil {
		t.Fatalf("复用路径不应报错: %v", err)
	}
	if !res.Reused || res.Status != domain.FeatureStatusSuccess {
		t.Errorf("res = %+v, want Reused success", res)
	}
	if n := fdf.callCount(); n != 0 {
		t.Errorf("终态预检应免拉详情, got %d 次 GetSessionDetail", n)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("复用路径 LLM 零调用, got %d", n)
	}
}

// TestExtractByKeySkippedNoRefetch 回归锚点：既有 skipped 终态行 → 同样拉详情前
// 复用，SkipReason 回传既有行 error_code（specs §2.4 能力3 skipped 终态不重拉不重判）。
func TestExtractByKeySkippedNoRefetch(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-keyskip-reuse", TokenName: "王五", Status: domain.FeatureStatusSkipped,
		ProfileJSON: "", ErrorCode: "empty_shell",
	}}}
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-keyskip-reuse", mkWorkMessages("输入"))}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-keyskip-reuse", "王五")
	if err != nil {
		t.Fatalf("skipped 终态复用不应报错: %v", err)
	}
	if !res.Reused || res.Status != domain.FeatureStatusSkipped || res.SkipReason != "empty_shell" {
		t.Errorf("res = %+v, want Reused skipped empty_shell", res)
	}
	if !res.Skipped {
		t.Error("skipped 终态复用应置 Skipped=true（与首次落行语义一致）")
	}
	if n := fdf.callCount(); n != 0 {
		t.Errorf("skipped 终态应免拉详情, got %d 次 GetSessionDetail", n)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("skipped 终态 LLM 零调用, got %d", n)
	}
}

// TestExtractByKeyFailedRefetches 边界补充：failed 行非终态 → 预检放行仍拉详情重抽
// （specs §2.4 能力3 补跑只重抽 failed 与新增）。
func TestExtractByKeyFailedRefetches(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-keyfail", TokenName: "王五", Status: domain.FeatureStatusFailed,
		ProfileJSON: `{"Stats":{}}`, ErrorCode: "ErrLLMUpstream",
	}}}
	fdf := &fakeDetailFetcher{detail: mkFullDetail("s-keyfail", mkWorkMessages("重抽输入"))}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-keyfail", "王五")
	if err != nil {
		t.Fatalf("failed 重抽不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess || res.Reused {
		t.Errorf("res = %+v, want 重抽翻转为 success", res)
	}
	if n := fdf.callCount(); n != 1 {
		t.Errorf("failed 行应拉详情重抽, got %d 次 GetSessionDetail", n)
	}
}

// ---- 评审第二轮修复的新行为锁定 ----

// TestExtractSummaryRedact 输出侧脱敏覆盖 summary 兜底：LLM 在 summary 复述
// 输入侧漏网密钥形态时，落库前同样过 Redact（specs 3.3 主库只见脱敏档案）。
func TestExtractSummaryRedact(t *testing.T) {
	resp := mkProfileJSON("会话在排查部署问题，涉及地址 192.168.1.100 与密钥 sk-abc123XYZdef 的排查。",
		[]string{"帮我排查部署问题"}, validBeh)
	fl := &fakeLLMClient{resps: []string{resp}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-sum-redact", mkWorkMessages("帮我排查部署问题"))

	res, err := ext.Extract(context.Background(), "s-sum-redact", detail, "李四")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess {
		t.Fatalf("res.Status = %q, want success", res.Status)
	}
	if strings.Contains(res.Profile.Summary, "192.168.1.100") || strings.Contains(res.Profile.Summary, "sk-abc123XYZdef") {
		t.Errorf("summary 未脱敏: %q", res.Profile.Summary)
	}
	if !strings.Contains(res.Profile.Summary, "[ADDR]") || !strings.Contains(res.Profile.Summary, "[SECRET]") {
		t.Errorf("summary 应含占位符: %q", res.Profile.Summary)
	}
	// 落库行同样脱敏。
	row := fr.get(t, "s-sum-redact")
	if strings.Contains(row.ProfileJSON, "192.168.1.100") || strings.Contains(row.ProfileJSON, "sk-abc123XYZdef") {
		t.Errorf("落库 profile_json 含明文: %q", row.ProfileJSON)
	}
}

// TestExtractByKeyHTTP404 HTTP 404 → 业务空双通道第二通道（ErrNotFound）：
// err=nil、不落行、LLM 零调用（确定性错误不入重试通道）。
func TestExtractByKeyHTTP404(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("记录不存在 (HTTP 404): %w", conversationlog.ErrNotFound)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-404", "王五")
	if err != nil {
		t.Fatalf("HTTP 404 业务空应 err=nil: %v", err)
	}
	if res == nil || res.Skipped || res.Status != "" {
		t.Errorf("res = %+v, want Skipped=false Status=空串", res)
	}
	if n := fr.rowCount(); n != 0 {
		t.Errorf("落行数 = %d, want 0", n)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("业务空不应调 LLM, got %d", n)
	}
}

// TestExtractByKeyNotFoundFinalizesFailedRow 业务空对残留 failed 行的终态化：
// 上游删除会话后既有 failed 行翻转为 skipped（reason=session_not_found），
// 防每轮重拉空转与 fail_ratio 分子虚高；无行保持业务空不落行。
// 终态化只翻状态列：turn_count 与时间列复用既有行原值（覆写为 epoch 会让行对
// 时间窗查询永久不可见，T5 静默丢行）。
func TestExtractByKeyNotFoundFinalizesFailedRow(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-stale", TokenName: "王五", Status: domain.FeatureStatusFailed,
		ProfileJSON: `{"Stats":{}}`, ErrorCode: "ErrLLMUpstream",
		TurnCount: 30, FirstTurnAt: time.Unix(1700000000, 0).UTC(),
		LastTurnAt: time.Unix(1700003600, 0).UTC(),
	}}}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("记录不存在 (HTTP 404): %w", conversationlog.ErrNotFound)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-stale", "王五")
	if err != nil {
		t.Fatalf("终态化不应报错: %v", err)
	}
	if res == nil || !res.Skipped || res.SkipReason != SkipSessionNotFound {
		t.Errorf("res = %+v, want Skipped=true SkipReason=session_not_found", res)
	}
	row := fr.get(t, "s-stale")
	if row.Status != domain.FeatureStatusSkipped || row.ErrorCode != SkipSessionNotFound {
		t.Errorf("行终态化失败: status=%q error_code=%q", row.Status, row.ErrorCode)
	}
	if row.TurnCount != 30 || row.FirstTurnAt.Unix() != 1700000000 || row.LastTurnAt.Unix() != 1700003600 {
		t.Errorf("元数据列被覆写: turn_count=%d first=%v last=%v, want 30/1700000000/1700003600",
			row.TurnCount, row.FirstTurnAt, row.LastTurnAt)
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("业务空终态化不应调 LLM, got %d", n)
	}
}

// TestExtractByKeyDeterministicFetchErr 确定性拉取错误（鉴权失效等）落 failed 终态行：
// 不交任务重试（重试恒失败烧预算），密钥修复后补跑重抽翻转。error_code 落标识符
// ErrDetailFetch（04 §3.1 值域，非 Error() 全串）；既有 failed 行元数据经 keepMeta
// 复用（覆写为 epoch 会让行对时间窗查询永久不可见）。
func TestExtractByKeyDeterministicFetchErr(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("conversationlog: %w", conversationlog.ErrUnauthorized)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-auth", "张三")
	if err != nil {
		t.Fatalf("确定性错误应落 failed 终态返回 nil err: %v", err)
	}
	if res == nil || res.Status != domain.FeatureStatusFailed {
		t.Fatalf("res = %+v, want Status=failed", res)
	}
	row := fr.get(t, "s-auth")
	if row.Status != domain.FeatureStatusFailed || row.ErrorCode != ErrDetailFetchCode {
		t.Errorf("行 = status=%q error_code=%q, want failed/ErrDetailFetch", row.Status, row.ErrorCode)
	}
	// 降级档案 map/slice 归一空值：序列化 {} 与 [] 而非 null（computeStats 同款契约）。
	var m map[string]any
	if err := json.Unmarshal([]byte(row.ProfileJSON), &m); err != nil {
		t.Fatalf("failed 行 profile_json 非法: %v", err)
	}
	stats, _ := m["Stats"].(map[string]any)
	for _, k := range []string{"ToolCounts", "TurnKindCounts", "SpecFingerprints", "CmdReuseHashes"} {
		if v, ok := stats[k]; !ok || v == nil {
			t.Errorf("Stats.%s = %v, want 空 {} / []（非 null）", k, stats[k])
		}
	}
	if n := fl.callCount(); n != 0 {
		t.Errorf("拉取失败不应调 LLM, got %d", n)
	}
}

// TestExtractByKeyFetchErrKeepsRowMeta 确定性拉取错误覆写既有 failed 行时元数据
// 复用原值：会话先因 LLM 失败落 failed 行（带真实时间窗），密钥吊销后补跑命中
// ErrUnauthorized，行时间列不得回退 epoch（否则对时间窗查询永久不可见）；
// client 同随元数据复用既有行原值（无 detail 无探测输入）。
func TestExtractByKeyFetchErrKeepsRowMeta(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-meta-keep", TokenName: "张三", Client: "omo",
		Status:      domain.FeatureStatusFailed,
		ProfileJSON: `{"Stats":{"TurnCount":19}}`, ErrorCode: "ErrLLMUpstream",
		TurnCount: 19, FirstTurnAt: time.Unix(1700000000, 0).UTC(), LastTurnAt: time.Unix(1700003600, 0).UTC(),
	}}}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("conversationlog: %w", conversationlog.ErrUnauthorized)}
	ext := newKeyFixture(fl, fr, fdf)

	if _, err := ext.ExtractByKey(context.Background(), "s-meta-keep", "张三"); err != nil {
		t.Fatalf("确定性错误落 failed 终态: %v", err)
	}
	row := fr.get(t, "s-meta-keep")
	if row.TurnCount != 19 || row.FirstTurnAt.Unix() != 1700000000 || row.LastTurnAt.Unix() != 1700003600 {
		t.Errorf("元数据列被覆写: turn_count=%d first=%v last=%v, want 19/1700000000/1700003600",
			row.TurnCount, row.FirstTurnAt, row.LastTurnAt)
	}
	if row.Client != "omo" {
		t.Errorf("client = %q, want omo（无 detail 终态化复用既有行原值）", row.Client)
	}
	if row.ErrorCode != ErrDetailFetchCode {
		t.Errorf("error_code = %q, want ErrDetailFetch（拉取错误覆盖 LLM 错误码）", row.ErrorCode)
	}
}

// TestExtractByKeyFetchErrLegacyEmptyClient 守护观测桶分离：legacy 失败行
// （client 列加列迁移兜底 ''）重抽再失败时落 unknown 而非 ''，'' 是 detail_invalid
// 专属空串口径。
func TestExtractByKeyFetchErrLegacyEmptyClient(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-legacy-empty", TokenName: "张三", Client: "",
		Status:    domain.FeatureStatusFailed,
		TurnCount: 5, FirstTurnAt: time.Unix(1700000000, 0).UTC(), LastTurnAt: time.Unix(1700003600, 0).UTC(),
	}}}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("conversationlog: %w", conversationlog.ErrUnauthorized)}
	ext := newKeyFixture(fl, fr, fdf)

	if _, err := ext.ExtractByKey(context.Background(), "s-legacy-empty", "张三"); err != nil {
		t.Fatalf("确定性错误落 failed 终态: %v", err)
	}
	if row := fr.get(t, "s-legacy-empty"); row.Client != rules.ClientUnknown {
		t.Errorf("client = %q, want unknown（legacy 空串归一，防混入 detail_invalid 桶）", row.Client)
	}
}

// TestExtractByKeyNotConfiguredFetchErr 部署配置缺失（ErrNotConfigured）与兜底
// 状态桶（ErrUnexpectedStatus）同为确定性错误：落 failed 终态行而非交任务重试
// 烧预算（底座两错误自述确定性不重试）。
func TestExtractByKeyNotConfiguredFetchErr(t *testing.T) {
	for name, target := range map[string]error{
		"ErrNotConfigured":    conversationlog.ErrNotConfigured,
		"ErrUnexpectedStatus": conversationlog.ErrUnexpectedStatus,
	} {
		t.Run(name, func(t *testing.T) {
			fl := &fakeLLMClient{}
			fr := &fakeFeatureRepo{}
			fdf := &fakeDetailFetcher{err: fmt.Errorf("wrap: %w", target)}
			ext := newKeyFixture(fl, fr, fdf)

			res, err := ext.ExtractByKey(context.Background(), "s-cfg", "张三")
			if err != nil {
				t.Fatalf("%s 应落 failed 终态返回 nil err: %v", name, err)
			}
			if res == nil || res.Status != domain.FeatureStatusFailed {
				t.Fatalf("res = %+v, want Status=failed", res)
			}
			if row := fr.get(t, "s-cfg"); row.ErrorCode != ErrDetailFetchCode {
				t.Errorf("error_code = %q, want ErrDetailFetch", row.ErrorCode)
			}
		})
	}
}

// TestExtractTurnTimeFallback session 时间字段缺失/为 0 时回退 turns 时间戳：
// 行时间列不再落 epoch 1970（对时间窗查询不可见）。
func TestExtractTurnTimeFallback(t *testing.T) {
	fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	// session 时间字段 0，turns 带真实时间戳（上游数据质量异常形态）。
	detail := &conversationlog.SessionDetail{
		Session:  conversationlog.SessionSummary{SessionKey: "s-notime", TurnCount: 2},
		Turns:    []conversationlog.TurnMeta{{ID: 1, CreatedAt: 1700000100}, {ID: 2, CreatedAt: 1700000200}},
		Messages: mkWorkMessages("带 turns 时间的会话"),
	}

	if _, err := ext.Extract(context.Background(), "s-notime", detail, "李四"); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	row := fr.get(t, "s-notime")
	if row.FirstTurnAt.Unix() != 1700000100 || row.LastTurnAt.Unix() != 1700000200 {
		t.Errorf("时间列 = %v/%v, want turns 回退 1700000100/1700000200", row.FirstTurnAt, row.LastTurnAt)
	}
}

// TestInjectPrefixesAppendSemantics 核心锚点（03 §5.3 消费侧追加语义）：
// 参数集是追加集而非替换集，生效集 = 出厂并集 ∪ 追加集。
func TestInjectPrefixesAppendSemantics(t *testing.T) {
	ext := newExtractFixture(&fakeLLMClient{}, &fakeFeatureRepo{})

	// 等价性锚：effective（测试侧独立 map 去重）排序后 == 出厂并集∪追加集（去重）。
	assertEffective := func(t *testing.T, param []string) {
		t.Helper()
		dedupe := func(in []string) []string {
			seen := make(map[string]struct{})
			var out []string
			for _, p := range in {
				if _, ok := seen[p]; !ok {
					seen[p] = struct{}{}
					out = append(out, p)
				}
			}
			return out
		}
		got := dedupe(appendEffectivePrefixes(ext.injectPrefixes(param)))
		want := dedupe(append(slices.Clone(InjectPrefixes()), blankEntriesRemoved(param)...))
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("effective 去重排序后 = %v, want 出厂并集∪追加集 %v", got, want)
		}
	}

	// 场景一：追加自定义前缀，自定义与出厂前缀同时生效（追加不替换）。
	assertEffective(t, []string{"ZZZ-custom-prefix"})
	msgs := []conversationlog.Message{
		mkMsg("ZZZ-custom-prefix 打头的注入噪音"),
		mkMsg("Note: 出厂前缀命中的文件回显"),
		mkMsg("正常用户指令"),
	}
	view2, stats2 := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		ParamKeyInjectPrefixes: {"ZZZ-custom-prefix"},
	}}).Trim(mkDetail(msgs))
	if len(view2.Lines) != 1 || !strings.HasPrefix(view2.Lines[0], "[USER] 正常用户指令") {
		t.Errorf("追加语义下两类前缀应同时 drop，Lines=%v", view2.Lines)
	}
	if stats2.InjectedDropped != 2 {
		t.Errorf("InjectedDropped=%d, want 2（自定义与出厂各命中 1 条）", stats2.InjectedDropped)
	}
	// 键缺失（fake 零值返回 nil）对照：出厂前缀恒生效，未配置的自定义前缀不拦截。
	view, stats := ext.Trim(mkDetail(msgs))
	if len(view.Lines) != 2 || stats.InjectedDropped != 1 {
		t.Errorf("键缺失时仅出厂前缀生效：Lines=%v dropped=%d", view.Lines, stats.InjectedDropped)
	}

	// 场景二：显式 [] 与 nil 统一空追加集，出厂并集全量生效（非放开全部过滤）。
	for name, param := range map[string][]string{"explicit-empty": {}, "nil-missing": nil} {
		got := appendEffectivePrefixes(ext.injectPrefixes(param))
		if !slices.Equal(got, InjectPrefixes()) {
			t.Errorf("%s: effective = %v, want 出厂并集全量", name, got)
		}
	}

	// 场景三：空串条目在读出侧被过滤（HasPrefix(text,"") 恒真会全量拦截）。
	got := ext.injectPrefixes([]string{"", "Note:"})
	if slices.Contains(got, "") {
		t.Error("空串条目应被过滤，不得进入追加集")
	}
	if !slices.Contains(got, "Note:") {
		t.Error("空串之外的有效追加条目应保留")
	}
}

// TestInjectPrefixesParamPathAppend 锚定读参链路：fake sysParams 返回追加集时
// readParams 透传不改写，injectPrefixes 过滤后经 appendEffectivePrefixes 组装的
// 生效集同时含追加与出厂条目。
func TestInjectPrefixesParamPathAppend(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		ParamKeyInjectPrefixes: {"ZZZ-custom-prefix"},
	}})
	raw, _ := ext.readParams()
	if raw == nil {
		t.Fatal("fake sysParams 应返回注入的追加集")
	}
	got := appendEffectivePrefixes(ext.injectPrefixes(raw))
	if !slices.Contains(got, "ZZZ-custom-prefix") || !slices.Contains(got, "Note:") {
		t.Errorf("读参链生效集应同时含追加与出厂条目, got %v", got)
	}
}

// TestPrepareCarriesClient 核心锚点（specs §2.4 能力7 注意事项、§6.1）：prepare
// 单趟产出探测结果：user_query 形态 detail 落 workbuddy；纯中文普通会话全零分落
// unknown（BR1/BR3）。prepareAndJudge 的 detail_invalid 短路路径不经探测，client
// 保持零值空串（specs §2.4 能力3：detail_invalid 跳过行 client 落空串）。
func TestPrepareCarriesClient(t *testing.T) {
	ext := newExtractFixture(&fakeLLMClient{}, &fakeFeatureRepo{})

	t.Run("user_query 形态 detail 落 workbuddy", func(t *testing.T) {
		detail := mkDetail([]conversationlog.Message{
			mkMsg("<user_query>\n帮我安排本周迭代评审\n</user_query>"),
			mkMsg("助手正常回复。"),
		})
		if got := ext.prepare("", detail).client; got != rules.ClientWorkBuddy {
			t.Errorf("preparedData.client = %q, want workbuddy", got)
		}
	})

	t.Run("纯中文普通会话落 unknown", func(t *testing.T) {
		detail := mkDetail([]conversationlog.Message{
			mkMsg("今天状态怎么样"),
			mkMsg("帮我把这封邮件改得委婉一点"),
			mkRoleMsg("assistant", "好的，已调整为更委婉的表述。"),
		})
		if got := ext.prepare("", detail).client; got != rules.ClientUnknown {
			t.Errorf("preparedData.client = %q, want unknown（全零分正常态）", got)
		}
	})

	t.Run("prepare 与 DetectClient 直调同值（单点裁决无漂移）", func(t *testing.T) {
		detail := mkDetail([]conversationlog.Message{
			mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
			mkMsg("<user_query>\n真实用户指令\n</user_query>"),
		})
		p := ext.prepare("", detail)
		if want := DetectClient(detail); p.client != want {
			t.Errorf("prepare client = %q, DetectClient = %q, 两路径须同值", p.client, want)
		}
	})

	t.Run("detail_invalid 短路不经探测 client 为零值", func(t *testing.T) {
		// nil detail 走规则0 短路：prepare 不执行，零值 preparedData.client 为空串。
		p, skip, reason := ext.prepareAndJudge("", nil, MinUserMessages, MinKeptMessages)
		if !skip || reason != SkipDetailInvalid {
			t.Fatalf("nil detail 应短路 detail_invalid, got (%v, %q)", skip, reason)
		}
		if p.client != "" {
			t.Errorf("短路路径 client = %q, want 空串（未经探测）", p.client)
		}
	})
}

// logRecord 捕获 slog 输出单条记录（msg + attrs 展开为 kv 序列）。
type logRecord struct {
	msg string
	kvs []string // 顺序展平：k1,v1,k2,v2...
}

// captureLogs 把 slog.Default 指向 buffer handler 收集记录，返回采集切片与复原函数。
func captureLogs(t *testing.T) (*[]logRecord, func()) {
	t.Helper()
	var recs []logRecord
	old := slog.Default()
	// LevelDebug：trim done 与 extract completed 是 DEBUG 口径（specs §6.1）。
	h := slog.NewJSONHandler(&logSink{t: t, recs: &recs}, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	return &recs, func() { slog.SetDefault(old) }
}

// logSink 把 handler 输出回解为 msg + kv 对。
type logSink struct {
	t    *testing.T
	recs *[]logRecord
}

func (s *logSink) Write(p []byte) (int, error) {
	var m map[string]any
	if err := json.Unmarshal(p, &m); err != nil {
		return len(p), nil // 非 JSON 行忽略
	}
	rec := logRecord{msg: m["msg"].(string)}
	for k, v := range m {
		if k == "msg" || k == "level" || k == "time" {
			continue
		}
		rec.kvs = append(rec.kvs, k, fmt.Sprint(v))
	}
	*s.recs = append(*s.recs, rec)
	return len(p), nil
}

// findLog 按前缀找首条命中记录，nil 表示无。
func findLog(recs *[]logRecord, msgPrefix string) *logRecord {
	for i := range *recs {
		if strings.HasPrefix((*recs)[i].msg, msgPrefix) {
			return &(*recs)[i]
		}
	}
	return nil
}

// logKV 断言记录含 kv 对。
func logKV(r *logRecord, key, val string) error {
	for i := 0; i+1 < len(r.kvs); i += 2 {
		if r.kvs[i] == key && r.kvs[i+1] == val {
			return nil
		}
	}
	return fmt.Errorf("日志 %q 缺 kv %s=%s（实有 %v）", r.msg, key, val, r.kvs)
}

// TestExtractLogsCarryClient 核心锚点（specs §6.1 日志规范）：trim done /
// session skipped / extract completed / extract failed 四场景日志追加 client 字段
// （枚举值非消息内容，BR2）；复用路径（extract reused）无 detail 不经探测，
// 不得伪造 client 字段。
func TestExtractLogsCarryClient(t *testing.T) {
	t.Run("trim done 与 extract completed 带 workbuddy", func(t *testing.T) {
		recs, restore := captureLogs(t)
		defer restore()
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)
		// user_query 打头消息给 workbuddy 唯一计分（探测 3 分，其余客户端零分）；
		// 分类侧该条走黑名单丢弃，另带真实用户输入与工作证据保放行。
		msgs := []conversationlog.Message{
			mkMsg("<user_query>\n帮我安排本周迭代评审\n</user_query>"),
			mkMsg("再同步一下评审材料清单"),
		}
		msgs = append(msgs, mkTools(12)...)
		msgs = append(msgs, mkRoleMsg("assistant", "已完成安排。"))
		detail := mkFullDetail("s-log-wb", msgs)

		if _, err := ext.Extract(context.Background(), "s-log-wb", detail, "李四"); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		trim := findLog(recs, "trim done")
		if trim == nil {
			t.Fatal("缺 trim done 日志")
		}
		if err := logKV(trim, "client", rules.ClientWorkBuddy); err != nil {
			t.Error(err)
		}
		done := findLog(recs, "extract completed")
		if done == nil {
			t.Fatal("缺 extract completed 日志")
		}
		if err := logKV(done, "client", rules.ClientWorkBuddy); err != nil {
			t.Error(err)
		}
	})

	t.Run("session skipped 带 client", func(t *testing.T) {
		recs, restore := captureLogs(t)
		defer restore()
		fl := &fakeLLMClient{}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)
		detail := mkFullDetail("s-log-skip", mkShellMsgs(3))

		if _, err := ext.Extract(context.Background(), "s-log-skip", detail, "李四"); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		skip := findLog(recs, "session skipped")
		if skip == nil {
			t.Fatal("缺 session skipped 日志")
		}
		if err := logKV(skip, "client", rules.ClientUnknown); err != nil {
			t.Error(err)
		}
	})

	t.Run("extract failed 带已探测 client", func(t *testing.T) {
		recs, restore := captureLogs(t)
		defer restore()
		fl := &fakeLLMClient{failAll: true}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)
		detail := mkFullDetail("s-log-fail", mkWorkMessages("帮我修复登录超时"))

		if _, err := ext.Extract(context.Background(), "s-log-fail", detail, "李四"); err != nil {
			t.Fatalf("降级终态 err 应 nil: %v", err)
		}
		fail := findLog(recs, "extract failed")
		if fail == nil {
			t.Fatal("缺 extract failed 日志")
		}
		if err := logKV(fail, "client", rules.ClientUnknown); err != nil {
			t.Error(err)
		}
		if err := logKV(fail, "code", ErrLLMUpstreamCode); err != nil {
			t.Error(err)
		}
	})

	t.Run("extract reused 无 client 字段（复用路径不伪造）", func(t *testing.T) {
		recs, restore := captureLogs(t)
		defer restore()
		fl := &fakeLLMClient{}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 1, SessionKey: "s-log-reuse", TokenName: "李四", Status: domain.FeatureStatusSuccess,
			ProfileJSON: `{"Stats":{}}`,
		}}}
		ext := newExtractFixture(fl, fr)

		if _, err := ext.Extract(context.Background(), "s-log-reuse", mkFullDetail("s-log-reuse", mkWorkMessages("输入")), "李四"); err != nil {
			t.Fatalf("复用路径不应报错: %v", err)
		}
		reused := findLog(recs, "extract reused")
		if reused == nil {
			t.Fatal("缺 extract reused 日志")
		}
		for i := 0; i+1 < len(reused.kvs); i += 2 {
			if reused.kvs[i] == "client" {
				t.Errorf("复用路径日志出现伪造 client=%s（无 detail 不经探测）", reused.kvs[i+1])
			}
		}
	})
}

// TestExtractClientColumnThreeStates 核心锚点（specs §2.4 能力3、§5.1 client 列
// 落库场景）：三态行均携带探测 client；(a) success 落新探测值 workbuddy；
// (b) 既有 success 行复用不覆盖；(c) failed 行翻转 client 随新探测覆盖；
// (d) detail_invalid 行未经探测落空串（七值之外显式例外）。
func TestExtractClientColumnThreeStates(t *testing.T) {
	// workbuddy 形态详情：user_query 打头（探测唯一计分）+ 真实输入 + 工作证据。
	mkWBDetail := func(key string) *conversationlog.SessionDetail {
		msgs := []conversationlog.Message{
			mkMsg("<user_query>\n帮我安排本周迭代评审\n</user_query>"),
			mkMsg("再同步一下评审材料清单"),
		}
		msgs = append(msgs, mkTools(12)...)
		msgs = append(msgs, mkRoleMsg("assistant", "已完成安排。"))
		return mkFullDetail(key, msgs)
	}

	t.Run("success 行落新探测 workbuddy", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)

		res, err := ext.Extract(context.Background(), "s-cli-ok", mkWBDetail("s-cli-ok"), "李四")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess {
			t.Fatalf("res.Status = %q, want success", res.Status)
		}
		if row := fr.get(t, "s-cli-ok"); row.Client != rules.ClientWorkBuddy {
			t.Errorf("success 行 client = %q, want workbuddy（探测结果随行落库）", row.Client)
		}
	})

	t.Run("既有 success 行复用 client 保持原值", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 1, SessionKey: "s-cli-reuse", TokenName: "李四", Client: "omo",
			Status: domain.FeatureStatusSuccess, ProfileJSON: `{"Stats":{}}`,
		}}}
		ext := newExtractFixture(fl, fr)

		res, err := ext.Extract(context.Background(), "s-cli-reuse", mkWBDetail("s-cli-reuse"), "李四")
		if err != nil {
			t.Fatalf("复用路径不应报错: %v", err)
		}
		if !res.Reused || res.Status != domain.FeatureStatusSuccess {
			t.Errorf("res = %+v, want Reused=true success", res)
		}
		if n := fl.callCount(); n != 0 {
			t.Errorf("复用路径 LLM 调用次数 = %d, want 0", n)
		}
		if row := fr.get(t, "s-cli-reuse"); row.Client != "omo" {
			t.Errorf("复用行 client = %q, want omo（复用不覆盖既有值）", row.Client)
		}
	})

	t.Run("failed 行翻转 client 随新探测覆盖", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
			ID: 1, SessionKey: "s-cli-flip", TokenName: "李四", Client: "unknown",
			Status: domain.FeatureStatusFailed, ErrorCode: ErrLLMUpstreamCode,
		}}}
		ext := newExtractFixture(fl, fr)

		res, err := ext.Extract(context.Background(), "s-cli-flip", mkWBDetail("s-cli-flip"), "李四")
		if err != nil {
			t.Fatalf("翻转路径不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess || res.Reused {
			t.Errorf("res = %+v, want 翻转 success 非复用", res)
		}
		row := fr.get(t, "s-cli-flip")
		if row.Status != domain.FeatureStatusSuccess || row.Client != rules.ClientWorkBuddy {
			t.Errorf("翻转后 = status%q client%q, want success/workbuddy（新探测覆盖）", row.Status, row.Client)
		}
	})

	t.Run("detail_invalid 行 client 落空串", func(t *testing.T) {
		fl := &fakeLLMClient{}
		fr := &fakeFeatureRepo{}
		ext := newExtractFixture(fl, fr)
		// 空 messages：规则0 短路，判定在 prepare 之前无消息可探测。
		detail := &conversationlog.SessionDetail{
			Session: conversationlog.SessionSummary{SessionKey: "s-cli-invalid", TurnCount: 3},
		}

		res, err := ext.Extract(context.Background(), "s-cli-invalid", detail, "李四")
		if err != nil {
			t.Fatalf("detail_invalid 应走跳过: %v", err)
		}
		if !res.Skipped || res.SkipReason != SkipDetailInvalid {
			t.Errorf("res = %+v, want Skipped detail_invalid", res)
		}
		if row := fr.get(t, "s-cli-invalid"); row.Client != "" {
			t.Errorf("detail_invalid 行 client = %q, want 空串（未经探测）", row.Client)
		}
	})
}

// TestFinalizeNotFoundKeepsClient 核心锚点（specs §2.4 能力3、04 §3.1）：残留
// failed 行终态化（session_not_found）client 随元数据复用既有行原值（上游已无
// 会话不重探测），只翻 status 与 error_code。
func TestFinalizeNotFoundKeepsClient(t *testing.T) {
	fl := &fakeLLMClient{}
	fr := &fakeFeatureRepo{rows: []*domain.SessionFeature{{
		ID: 1, SessionKey: "s-cli-stale", TokenName: "王五", Client: "automation",
		Status: domain.FeatureStatusFailed, ErrorCode: "ErrLLMUpstream",
		TurnCount: 30, FirstTurnAt: time.Unix(1700000000, 0).UTC(),
		LastTurnAt: time.Unix(1700003600, 0).UTC(),
	}}}
	fdf := &fakeDetailFetcher{err: fmt.Errorf("记录不存在 (HTTP 404): %w", conversationlog.ErrNotFound)}
	ext := newKeyFixture(fl, fr, fdf)

	res, err := ext.ExtractByKey(context.Background(), "s-cli-stale", "王五")
	if err != nil {
		t.Fatalf("终态化不应报错: %v", err)
	}
	if res == nil || !res.Skipped || res.SkipReason != SkipSessionNotFound {
		t.Fatalf("res = %+v, want Skipped session_not_found", res)
	}
	row := fr.get(t, "s-cli-stale")
	if row.Status != domain.FeatureStatusSkipped || row.ErrorCode != SkipSessionNotFound {
		t.Errorf("行终态化失败: status=%q error_code=%q", row.Status, row.ErrorCode)
	}
	if row.Client != "automation" {
		t.Errorf("终态化行 client = %q, want automation（复用既有行原值不重探测）", row.Client)
	}
}

// appendPrefixAppendParam 集成场景的参数页追加集（specs §5.2 场景2 的新前缀）。
const appendPrefixAppendParam = "ZZZ-append-prefix"

// mkAppendCCDetail 构造 claude_code 形态会话：追加前缀命中消息 + 出厂前缀噪音 +
// 探测特征（SR 开式与 command 壳）+ 真实输入 + 工作证据，探测侧输出 claude_code。
func mkAppendCCDetail(key string) *conversationlog.SessionDetail {
	msgs := []conversationlog.Message{
		mkMsg(appendPrefixAppendParam + " 追加前缀命中的注入噪音"),
		mkMsg("The following skills are available: 出厂技能清单回显"),
		mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
		mkMsg("<command-name>/review</command-name>"),
		mkMsg("真实用户指令：重构支付模块错误处理"),
	}
	msgs = append(msgs, mkTools(12)...)
	msgs = append(msgs, mkRoleMsg("assistant", "已完成重构并补齐回归测试。"))
	return mkFullDetail(key, msgs)
}

// mkAppendOMODetail 构造 omo 形态会话：追加前缀命中消息 + 出厂 OMO 转写噪音 +
// 真实输入 + 工作证据（探测侧 SYSTEM DIRECTIVE 给 omo 计分）。
func mkAppendOMODetail(key string) *conversationlog.SessionDetail {
	msgs := []conversationlog.Message{
		mkMsg(appendPrefixAppendParam + " OMO 会话里的追加前缀噪音"),
		mkMsg("[SYSTEM DIRECTIVE: 请立即执行自动化流程"),
		mkMsg("真实用户指令：整理本周期任务清单"),
	}
	msgs = append(msgs, mkTools(12)...)
	msgs = append(msgs, mkRoleMsg("assistant", "已完成整理。"))
	return mkFullDetail(key, msgs)
}

// TestAppendPrefixIntegration 核心锚点（specs §5.2 场景2 参数页全局追加在分层下
// 生效，BR1/BR2）：参数页注入追加前缀 "ZZZ-append-prefix"，对含该前缀的两类
// 客户端形态会话（claude_code 形态 + omo 形态，各一条命中消息）跑 Extract 端到端：
// 追加前缀经通用层并集对两类会话同时生效（命中消息 drop 且计入 InjectedDropped），
// 判定与基线参数联动行为一致（BR1）；对照组预置删除出厂前缀的配置形态
// （追加语义下配置集不承载出厂前缀，显式配置不含出厂前缀时出厂前缀仍拦截，
// 配置删除不收回代码前缀，BR2 specs 裁定）。
func TestAppendPrefixIntegration(t *testing.T) {
	appendParams := &fakeSysParams{values: map[string][]string{
		ParamKeyInjectPrefixes: {appendPrefixAppendParam},
	}}

	t.Run("追加前缀对 claude_code 形态会话生效", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{}
		ext := New(fl, nil, fr, appendParams, nil)
		detail := mkAppendCCDetail("s-apx-cc")

		res, err := ext.Extract(context.Background(), "s-apx-cc", detail, "李四")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess || res.Skipped {
			t.Fatalf("res = %+v, want success 非跳过（真实输入与证据保放行）", res)
		}
		// 探测口径守护：SR 开式 + command 壳特征命中，client 落 claude_code。
		if row := fr.get(t, "s-apx-cc"); row.Client != rules.ClientClaudeCode {
			t.Errorf("行 client = %q, want claude_code（SR 与 command 壳特征计分）", row.Client)
		}
		// 追加前缀命中消息被 drop：视图零残留，InjectedDropped 计 4
		//（追加 1 + 出厂技能清单 1 + 探测特征消息 2：SR 开式与 command 壳
		// 均属出厂前缀噪音面，探测计分与裁剪 drop 互不影响）。
		p := ext.prepare("s-apx-cc", detail)
		joined := strings.Join(p.view.Lines, "\n")
		if strings.Contains(joined, appendPrefixAppendParam) {
			t.Errorf("追加前缀命中消息应被 drop，视图残留: %v", p.view.Lines)
		}
		if p.trimStats.InjectedDropped != 4 {
			t.Errorf("InjectedDropped=%d, want 4（追加 1 + 出厂前缀噪音 3）", p.trimStats.InjectedDropped)
		}
		if got := countPrefix(p.view.Lines, "[USER] "); got != 1 {
			t.Errorf("[USER] 行数=%d, want 1（仅真实指令保留）\n实得 %v", got, p.view.Lines)
		}
	})

	t.Run("追加前缀对 omo 形态会话生效", func(t *testing.T) {
		fl := &fakeLLMClient{resps: []string{mkOKResponse()}}
		fr := &fakeFeatureRepo{}
		ext := New(fl, nil, fr, appendParams, nil)
		detail := mkAppendOMODetail("s-apx-omo")

		res, err := ext.Extract(context.Background(), "s-apx-omo", detail, "李四")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if res.Status != domain.FeatureStatusSuccess || res.Skipped {
			t.Fatalf("res = %+v, want success 非跳过", res)
		}
		p := ext.prepare("s-apx-omo", detail)
		joined := strings.Join(p.view.Lines, "\n")
		if strings.Contains(joined, appendPrefixAppendParam) {
			t.Errorf("追加前缀命中消息应被 drop，视图残留: %v", p.view.Lines)
		}
		if p.trimStats.InjectedDropped != 2 {
			t.Errorf("InjectedDropped=%d, want 2（追加与出厂 OMO 前缀各命中 1 条）", p.trimStats.InjectedDropped)
		}
		if got := countPrefix(p.view.Lines, "[USER] "); got != 1 {
			t.Errorf("[USER] 行数=%d, want 1\n实得 %v", got, p.view.Lines)
		}
	})

	t.Run("对照组：配置不含出厂前缀时出厂前缀仍拦截（配置删除不收回代码前缀）", func(t *testing.T) {
		// 配置集只含追加条目（显式不含任何出厂前缀，等价参数页删除出厂前缀的
		// 配置形态）：出厂前缀仍被代码并集拦截，基线替换语义下此配置会放开
		// 出厂过滤，追加语义按 specs 裁定不收回。
		ext := New(&fakeLLMClient{}, nil, &fakeFeatureRepo{}, appendParams, nil)
		msgs := []conversationlog.Message{
			mkMsg("TodoWrite 4 items"),                     // 出厂 OMO 前缀
			mkMsg("The following skills are available: x"), // 出厂 claude_code 前缀
			mkMsg(appendPrefixAppendParam + " 追加前缀命中"),     // 配置追加前缀
			mkMsg("正常用户指令"),                                // 对照：不误杀
		}
		p := ext.prepare("", mkDetail(msgs))
		if p.trimStats.InjectedDropped != 3 {
			t.Errorf("InjectedDropped=%d, want 3（出厂 2 条 + 追加 1 条均拦截）", p.trimStats.InjectedDropped)
		}
		if got := countPrefix(p.view.Lines, "[USER] "); got != 1 {
			t.Errorf("[USER] 行数=%d, want 1\n实得 %v", got, p.view.Lines)
		}
	})
}

// TestExtractZeroInputSuccess 零输入会话守规输出落 success 的完整链路（specs §5.1）：
// Instruction 空数组、Summary 与 Behavior 正常产出、四块落库。
func TestExtractZeroInputSuccess(t *testing.T) {
	// continuation 推进形态：零 user 输入、工具与叙述充足（规则3 放行）。
	msgs := []conversationlog.Message{
		mkToolMsg("Read args=111"),
		mkToolMsg("Edit args=2055"),
		mkToolMsg("Bash command=ls"),
		mkToolMsg("Read args=52"),
		mkToolMsg("Edit args=88"),
		mkToolMsg("Grep pattern=foo"),
		mkToolMsg("Glob pattern=**/*.go"),
		mkToolMsg("Bash command=go test"),
		mkToolMsg("Read args=30"),
		mkToolMsg("Write args=100"),
		mkRoleMsg("assistant", "已完成本轮续接推进，全部测试通过。"),
	}
	resp := mkProfileJSON("续接推进会话：工具链推进测试修复，无新增用户输入。",
		nil, ProfileBehavior{
			InstructionSpecificity: "no_evidence",
			InterruptStyle:         "rare",
			ReviewRatio:            "no_evidence",
			PasteScale:             "none",
		})
	fl := &fakeLLMClient{resps: []string{resp}}
	fr := &fakeFeatureRepo{}
	ext := newExtractFixture(fl, fr)
	detail := mkFullDetail("s-zeroinput", msgs)

	res, err := ext.Extract(context.Background(), "s-zeroinput", detail, "李四")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if res.Status != domain.FeatureStatusSuccess {
		t.Fatalf("res.Status = %q, want success（零输入守规输出落 success）", res.Status)
	}
	p := res.Profile
	if len(p.Instruction) != 0 {
		t.Errorf("Instruction = %+v, want 空数组", p.Instruction)
	}
	if p.Summary == "" || p.Behavior.InstructionSpecificity != "no_evidence" {
		t.Errorf("Summary/Behavior = %q %+v, want 正常产出且指令信号无证据", p.Summary, p.Behavior)
	}
	if p.Stats.UserMsgCount != 0 {
		t.Errorf("UserMsgCount = %d, want 0", p.Stats.UserMsgCount)
	}
	row := fr.get(t, "s-zeroinput")
	if row.Status != domain.FeatureStatusSuccess || !strings.Contains(row.ProfileJSON, `"Instruction":[]`) {
		t.Errorf("落库形态异常: status=%q json=%s", row.Status, row.ProfileJSON)
	}
}
