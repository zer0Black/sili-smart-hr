package grading

// grading_test.go 契约测试：Grader.Run 双类型阅卷编排（specs TST §5.2.2/§5.2.4/§5.2.5，
// 03 §4.4）。核心断言用真 :memory: SQLite 落任务/题目/维度/评分/聚合/判型行验证。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// ---- 内存库基建（仿 questiongen 测试的反射版雪花回调）----

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_grading_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignTestSnowflakeID(tx.Statement.Dest)
	})
	if err := db.AutoMigrate(
		&domain.AssessmentTestTask{}, &domain.AssessmentTestResult{},
		&domain.Question{}, &domain.Dimension{},
		&domain.DimensionScore{}, &domain.AggregateScore{}, &domain.AssessmentConfig{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func assignTestSnowflakeID(dest any) {
	v := reflect.ValueOf(dest)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		setTestID(v)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				setTestID(elem)
			}
		}
	}
}

func setTestID(v reflect.Value) {
	f := v.FieldByName("ID")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 && f.Int() == 0 {
		f.SetInt(snowflake.NextID())
	}
}

// ---- 测试数据 ----

// 任务创建时刻固定在 2026-09-28（周一）10:00 本地时区，weekly 窗口即
// [2026-09-28 00:00, 2026-10-05 00:00)。
var taskCreated = time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)

// seedAIMgmt 建 ai_mgmt 任务 + 2 题 + 2 个 AI_MGMT 子能力维度 + weekly 周期配置。
func seedAIMgmt(t *testing.T, db *gorm.DB) domain.AssessmentTestTask {
	t.Helper()
	dim1 := domain.Dimension{
		Code: "MGT_DELEGATE", Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt,
		DataSource: domain.SourceTest, Anchor: "90+ 充分授权；<40 事必躬亲",
		Weight: 60, IncludeOverview: true, Enabled: true, Version: 1,
	}
	dim2 := domain.Dimension{
		Code: "MGT_REVIEW", Name: "把关与审查", ModuleCode: domain.ModuleAIMgmt,
		DataSource: domain.SourceTest, Anchor: "90+ 严格把关；<40 放任输出",
		Weight: 40, IncludeOverview: true, Enabled: true, Version: 1,
	}
	if err := db.Create(&[]domain.Dimension{dim1, dim2}).Error; err != nil {
		t.Fatalf("seed dimensions: %v", err)
	}
	q1 := domain.Question{
		QuestionNo: "Q-AG-0001", Source: domain.QuestionSourceAI, DimensionID: dim1.ID,
		Scenario: "情境一：团队接到紧急交付。", Requirement: "要求一：选出处置方式。",
		FocusPoint: "授权判断", Status: domain.QuestionStatusActive, BatchID: 1, Version: 1,
	}
	q2 := domain.Question{
		QuestionNo: "Q-AG-0002", Source: domain.QuestionSourceAI, DimensionID: dim2.ID,
		Scenario: "情境二：AI 产出存在争议。", Requirement: "要求二：选出把关方式。",
		FocusPoint: "审查判断", Status: domain.QuestionStatusActive, BatchID: 1, Version: 1,
	}
	if err := db.Create(&[]domain.Question{q1, q2}).Error; err != nil {
		t.Fatalf("seed questions: %v", err)
	}
	task := domain.AssessmentTestTask{
		TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt,
		StaffID: "u-9001", StaffName: "张敏",
		Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusGrading,
		QuestionIDsJSON: fmt.Sprintf("[%d,%d]", q1.ID, q2.ID), ScaleKey: "",
		DimensionCodesJSON: `["MGT_DELEGATE","MGT_REVIEW"]`,
		CreatedAt:          taskCreated,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	seedConfig(t, db)
	return task
}

// seedEnneagram 建 enneagram 任务 + 2 道量表题（计分键在题面自含）。
func seedEnneagram(t *testing.T, db *gorm.DB) domain.AssessmentTestTask {
	t.Helper()
	q1 := domain.Question{
		QuestionNo: "Q-Scale-0001", Source: domain.QuestionSourceScale, ScaleKey: domain.ScaleKeyRisoHudson,
		Scenario: "我倾向于看到事物积极的一面。", Requirement: "按认同程度作答。",
		FocusPoint: "E9", Status: domain.QuestionStatusActive, BatchID: 1, Version: 1,
	}
	q2 := domain.Question{
		QuestionNo: "Q-Scale-0002", Source: domain.QuestionSourceScale, ScaleKey: domain.ScaleKeyRisoHudson,
		Scenario: "我做事追求正确与秩序。", Requirement: "按认同程度作答。",
		FocusPoint: "E1", Status: domain.QuestionStatusActive, BatchID: 1, Version: 1,
	}
	if err := db.Create(&[]domain.Question{q1, q2}).Error; err != nil {
		t.Fatalf("seed scale questions: %v", err)
	}
	task := domain.AssessmentTestTask{
		TaskNo: "E202609280001", TestType: domain.TestTypeEnneagram,
		StaffID: "u-9001", StaffName: "张敏",
		Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusGrading,
		QuestionIDsJSON: fmt.Sprintf("[%d,%d]", q1.ID, q2.ID),
		ScaleKey:        domain.ScaleKeyRisoHudson, DimensionCodesJSON: "[]",
		CreatedAt: taskCreated,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	seedConfig(t, db)
	return task
}

// seedConfig weekly 周期单例（grading 按任务创建时刻推窗口）。
func seedConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	cfg := domain.AssessmentConfig{
		ID: domain.SingleRowID, Period: domain.PeriodWeekly,
		TriggerTime: "23:00", TargetMode: "all", Version: 1,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// ---- fake 依赖 ----

// fakeLLM 可编程 LLM：fn 每次调用返回一段回复或错误。
type fakeLLM struct {
	mu      sync.Mutex
	fn      func(call int) (string, error)
	calls   int
	prompts []string
}

func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	prompt := ""
	for _, m := range req.Messages {
		prompt += m.Content + "\n"
	}
	f.prompts = append(f.prompts, prompt)
	if f.fn == nil {
		return nil, errors.New("fake llm not programmed")
	}
	resp, err := f.fn(f.calls)
	if err != nil {
		return nil, err
	}
	return &fakeStream{content: resp}, nil
}

type fakeStream struct {
	done    bool
	content string
}

func (s *fakeStream) Recv() (llm.StreamChunk, error) {
	if s.done {
		return llm.StreamChunk{}, io.EOF
	}
	s.done = true
	return llm.StreamChunk{Content: s.content}, nil
}

func (s *fakeStream) Close() error      { return nil }
func (s *fakeStream) Usage() (int, int) { return 0, 0 }

var _ llm.Client = (*fakeLLM)(nil)

// fakeModelProvider 排他启用模型解析 fake。
type fakeModelProvider struct {
	cfg llm.ModelConfig
}

func (f *fakeModelProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	return f.cfg, nil
}

var _ llm.EnabledModelProvider = (*fakeModelProvider)(nil)

// fakeSysParams 脱敏正则 fake：返回 nil 走出厂规则集（sk- 命中 [SECRET]）。
type fakeSysParams struct{}

func (f *fakeSysParams) ReadStringArray(key string) ([]string, error) { return nil, nil }
func (f *fakeSysParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	return nil, nil
}

var _ repository.SystemParamReader = (*fakeSysParams)(nil)

// gradingFixture 聚合一次 Run 测试的全部注入件（真仓储 + fake LLM/provider/params）。
type gradingFixture struct {
	db   *gorm.DB
	llm  *fakeLLM
	g    *Grader
	task domain.AssessmentTestTask
}

func newFixture(t *testing.T, seed func(*testing.T, *gorm.DB) domain.AssessmentTestTask) *gradingFixture {
	t.Helper()
	db := newTestDB(t)
	f := &gradingFixture{
		db:  db,
		llm: &fakeLLM{},
	}
	f.task = seed(t, db)
	f.g = New(
		f.llm,
		&fakeModelProvider{cfg: llm.ModelConfig{Provider: "openai", ModelID: "gpt-test", BaseURL: "http://x", APIKey: "k"}},
		repository.NewAssessmentTestTaskRepository(db),
		repository.NewQuestionRepository(db),
		repository.NewAssessmentTestResultRepository(db),
		repository.NewDimensionRepository(db),
		repository.NewDimensionScoreRepository(db),
		scorer.New(repository.NewDimensionScoreRepository(db), repository.NewAggregateScoreRepository(db)),
		repository.NewAssessmentConfigRepository(db),
		&fakeSysParams{},
	)
	return f
}

// loadTask 重读任务行断言终态。
func loadTask(t *testing.T, db *gorm.DB, id int64) domain.AssessmentTestTask {
	t.Helper()
	var task domain.AssessmentTestTask
	if err := db.First(&task, id).Error; err != nil {
		t.Fatalf("load task %d: %v", id, err)
	}
	return task
}

// aiMgmtScoreJSON 两维度合法评分输出。
func aiMgmtScoreJSON() string {
	return `{"dimensions":[` +
		`{"code":"MGT_DELEGATE","score":82,"insufficient":false,"rationale":"授权表述清晰"},` +
		`{"code":"MGT_REVIEW","score":66,"insufficient":false,"rationale":"把关有据"}]}`
}

// enneagramJSON 合法判型输出（分布恰 100）。
func enneagramJSON() string {
	return `{"main_type":9,"wing_type":8,` +
		`"distribution":{"1":10.0,"2":10.0,"3":10.0,"4":10.0,"5":10.0,"6":10.0,"7":10.0,"8":15.0,"9":15.0},` +
		`"rationale":"倾向和平与自我主张并存"}`
}

// ---- 核心断言 ----

// TestRunAIMgmtScoresAndAggregates 核心断言（BR1/BR3/BR4/BR8）：两维度评分行
// source=active_test、score/token_name 与任务对齐、evidence 携口径快照、
// 聚合行出现 AI_MGMT 模块行、任务 scored。
func TestRunAIMgmtScoresAndAggregates(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}

	var rows []domain.DimensionScore
	if err := f.db.Where("token_name = ?", "张敏").Find(&rows).Error; err != nil {
		t.Fatalf("load scores: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("dimension_scores 落 %d 行, want 2", len(rows))
	}
	byCode := map[string]domain.DimensionScore{}
	for _, r := range rows {
		byCode[r.DimensionCode] = r
	}
	d := byCode["MGT_DELEGATE"]
	if d.Score != 82 || d.Source != domain.ScoreSourceActiveTest {
		t.Errorf("MGT_DELEGATE score=%d source=%s, want 82/active_test", d.Score, d.Source)
	}
	if d.TokenName != "张敏" || d.Module != domain.ModuleAIMgmt {
		t.Errorf("token_name=%q module=%s, want 张敏/AI_MGMT", d.TokenName, d.Module)
	}
	if d.ModelName != "gpt-test" || d.PromptVersion != PromptVersion {
		t.Errorf("model_name=%q prompt_version=%q, want gpt-test/%s", d.ModelName, d.PromptVersion, PromptVersion)
	}
	// evidence 携维度口径快照（weight/in_overview）。
	if !strings.Contains(d.EvidenceJSON, `"dimension_specs"`) ||
		!strings.Contains(d.EvidenceJSON, `"MGT_DELEGATE"`) ||
		!strings.Contains(d.EvidenceJSON, `"weight":60`) {
		t.Errorf("evidence_json 缺口径快照: %s", d.EvidenceJSON)
	}
	// period 经 CurrentPeriodWindow(任务创建时刻, weekly) 推算。
	wantStart, wantEnd := weeklyWindow()
	if d.PeriodStartAt.Unix() != wantStart || d.PeriodEndAt.Unix() != wantEnd {
		t.Errorf("period = %d~%d, want %d~%d", d.PeriodStartAt.Unix(), d.PeriodEndAt.Unix(), wantStart, wantEnd)
	}

	var aggRows []domain.AggregateScore
	if err := f.db.Where("token_name = ? AND module = ?", "张敏", domain.ModuleAIMgmt).Find(&aggRows).Error; err != nil {
		t.Fatalf("load aggregates: %v", err)
	}
	if len(aggRows) != 1 || aggRows[0].ModuleScore == nil || *aggRows[0].ModuleScore != 75.6 {
		t.Fatalf("AI_MGMT 聚合行缺失或分值不符: %+v", aggRows)
	}

	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusScored {
		t.Fatalf("grading_status = %s, want scored", task.GradingStatus)
	}
}

// weeklyWindow 与 pipeline.CurrentPeriodWindow 同口径的期望窗口（单测独立计算防镜像）。
func weeklyWindow() (int64, int64) {
	n := taskCreated.Local()
	offset := (int(n.Weekday()) - int(time.Monday) + 7) % 7
	d := n.AddDate(0, 0, -offset)
	s := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)
	return s.Unix(), s.AddDate(0, 0, 7).Unix()
}

// TestRunEnneagramWritesResult 核心断言（BR2/BR4）：判型四字段 + grading_status=scored
// 落 assessment_test_results，任务 scored，不写 dimension_scores、不产聚合行。
func TestRunEnneagramWritesResult(t *testing.T) {
	f := newFixture(t, seedEnneagram)
	f.llm.fn = func(call int) (string, error) { return enneagramJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var results []domain.AssessmentTestResult
	if err := f.db.Find(&results).Error; err != nil {
		t.Fatalf("load results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("assessment_test_results 落 %d 行, want 1", len(results))
	}
	r := results[0]
	if r.TaskID != f.task.ID || r.MainType != "9" || r.WingType != "8" {
		t.Errorf("判型字段不符: %+v", r)
	}
	if !strings.Contains(r.DistributionJSON, `"9":15`) {
		t.Errorf("distribution_json 缺 9 型占比: %s", r.DistributionJSON)
	}
	if r.ModelName != "gpt-test" || r.PromptVersion != PromptVersion || r.GradingStatus != domain.GradingStatusScored {
		t.Errorf("留痕字段不符: model=%q version=%q status=%s", r.ModelName, r.PromptVersion, r.GradingStatus)
	}

	var scoreCount, aggCount int64
	f.db.Model(&domain.DimensionScore{}).Count(&scoreCount)
	f.db.Model(&domain.AggregateScore{}).Count(&aggCount)
	if scoreCount != 0 || aggCount != 0 {
		t.Errorf("enneagram 不进聚合: score=%d agg=%d, want 0/0", scoreCount, aggCount)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusScored {
		t.Fatalf("grading_status = %s, want scored", task.GradingStatus)
	}
}

// TestRunTerminalGuard 核心断言（BR3）：scored/degraded 终态任务直接返回 nil 且 LLM 零调用。
func TestRunTerminalGuard(t *testing.T) {
	for _, status := range []string{domain.GradingStatusScored, domain.GradingStatusDegraded} {
		f := newFixture(t, seedAIMgmt)
		f.db.Model(&domain.AssessmentTestTask{}).Where("id = ?", f.task.ID).
			Update("grading_status", status)
		if err := f.g.Run(context.Background(), f.task.ID); err != nil {
			t.Fatalf("%s 终态任务 Run 应直接 nil, got %v", status, err)
		}
		if f.llm.calls != 0 {
			t.Errorf("%s 终态任务 LLM 被调 %d 次, want 0", status, f.llm.calls)
		}
	}
}

// TestRunLLMErrorPropagates 核心断言（BR6）：LLM 失败上抛原错误，终态停留 grading。
func TestRunLLMErrorPropagates(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	boom := errors.New("llm upstream down")
	f.llm.fn = func(call int) (string, error) { return "", boom }

	err := f.g.Run(context.Background(), f.task.ID)
	if !errors.Is(err, boom) {
		t.Fatalf("Run 应上抛同 error, got %v", err)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusGrading {
		t.Fatalf("grading_status = %s, want 停留 grading", task.GradingStatus)
	}
}

// TestRunSchemaInvalidPropagates 核心断言（BR6）：非法 JSON 视同调用失败上抛，无评分行落库。
func TestRunSchemaInvalidPropagates(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.llm.fn = func(call int) (string, error) { return "这不是 JSON", nil }

	if err := f.g.Run(context.Background(), f.task.ID); err == nil {
		t.Fatal("非法 JSON 应上抛")
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Count(&n)
	if n != 0 {
		t.Fatalf("评分行落库 %d, want 0", n)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusGrading {
		t.Fatalf("grading_status = %s, want 停留 grading", task.GradingStatus)
	}
}

// TestRationaleRedacted 核心断言：rationale 含 sk-abc123 落库行被替换为 [SECRET]。
func TestRationaleRedacted(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.llm.fn = func(call int) (string, error) {
		return `{"dimensions":[` +
			`{"code":"MGT_DELEGATE","score":70,"insufficient":false,"rationale":"密钥 sk-abc123 泄露"},` +
			`{"code":"MGT_REVIEW","score":70,"insufficient":false,"rationale":"正常理由"}]}`, nil
	}
	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var row domain.DimensionScore
	if err := f.db.Where("dimension_code = ?", "MGT_DELEGATE").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if !strings.Contains(row.Rationale, "[SECRET]") || strings.Contains(row.Rationale, "sk-abc123") {
		t.Errorf("rationale 未脱敏: %q", row.Rationale)
	}
}

// TestRunAggregateFailureNonBlocking 补充（BR7）：聚合仓储故障时评分已落库、
// Run 仍返回 nil、任务推进 scored。
func TestRunAggregateFailureNonBlocking(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	// 预插同键冲突行制造 upsert 失败不可行，改用 drop 表制造聚合写失败。
	if err := f.db.Migrator().DropTable(&domain.AggregateScore{}); err != nil {
		t.Fatalf("drop aggregate table: %v", err)
	}
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("聚合失败应仅记 ERROR 不阻断, got %v", err)
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Where("token_name = ?", "张敏").Count(&n)
	if n != 2 {
		t.Fatalf("评分行 %d, want 2（聚合失败前已落库）", n)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusScored {
		t.Fatalf("grading_status = %s, want scored", task.GradingStatus)
	}
}

// TestRunCanceledTaskStillGrades 补充（BR5）：任务 status=canceled 但阅卷在途，
// 评分照常落库并推进终态。
func TestRunCanceledTaskStillGrades(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.db.Model(&domain.AssessmentTestTask{}).Where("id = ?", f.task.ID).
		Update("status", domain.TestTaskStatusCanceled)
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Where("token_name = ?", "张敏").Count(&n)
	if n != 2 {
		t.Fatalf("取消任务评分行 %d, want 2", n)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusScored {
		t.Fatalf("grading_status = %s, want scored", task.GradingStatus)
	}
}

// TestRunIdempotentUpsert 补充（BR3）：scored 终态守卫外，重复对 grading 态任务
// Run 落库经唯一索引 upsert 收敛到同一份（仍 2 行非 4 行）。
func TestRunIdempotentUpsert(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }
	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	// 手动拨回 grading 模拟重复触发（守卫放行路径），评分行 upsert 收敛。
	f.db.Model(&domain.AssessmentTestTask{}).Where("id = ?", f.task.ID).
		Update("grading_status", domain.GradingStatusGrading)
	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Where("token_name = ?", "张敏").Count(&n)
	if n != 2 {
		t.Fatalf("重复阅卷评分行 %d, want 2（upsert 收敛）", n)
	}
}

// TestRunDisabledDimensionStillScored 核心锚点（specs §5.1.4 规则2 快照不可变）：
// 快照内维度在阅卷前被停用仍参与评分，不静默出列。
func TestRunDisabledDimensionStillScored(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.db.Model(&domain.Dimension{}).Where("code = ?", "MGT_REVIEW").
		Update("enabled", false)
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Where("token_name = ?", "张敏").Count(&n)
	if n != 2 {
		t.Fatalf("停用维度任务评分行 %d, want 2（快照口径全量落分）", n)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusScored {
		t.Fatalf("grading_status = %s, want scored", task.GradingStatus)
	}
}

// TestRunEmptyDimensionSnapshotErrors 核心锚点：快照解析出 0 个 AI_MGMT 维度时
// 报错上抛，不落 0 行评分假推 scored。
func TestRunEmptyDimensionSnapshotErrors(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	f.db.Model(&domain.AssessmentTestTask{}).Where("id = ?", f.task.ID).
		Update("dimension_codes_json", `[]`)
	f.llm.fn = func(call int) (string, error) { return aiMgmtScoreJSON(), nil }

	if err := f.g.Run(context.Background(), f.task.ID); err == nil {
		t.Fatal("空快照应报错上抛")
	}
	var n int64
	f.db.Model(&domain.DimensionScore{}).Count(&n)
	if n != 0 {
		t.Fatalf("评分行 %d, want 0", n)
	}
	if task := loadTask(t, f.db, f.task.ID); task.GradingStatus != domain.GradingStatusGrading {
		t.Fatalf("grading_status = %s, want 停留 grading", task.GradingStatus)
	}
}

// TestRunTaskNotFound 补充异常分支：任务不存在上抛交重试通道外由 handler 处置。
func TestRunTaskNotFound(t *testing.T) {
	f := newFixture(t, seedAIMgmt)
	if err := f.g.Run(context.Background(), 999999); err == nil {
		t.Fatal("任务不存在应报错")
	}
}
