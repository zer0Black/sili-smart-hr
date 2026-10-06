package suggestgen

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// validOutputJSON 两模块各 2 条的合法输出（specs §5.1.2 步4 输出结构）。
const validOutputJSON = `{
  "modules": [
    {
      "module": "AI_USAGE",
      "suggestions": [
        {"name": "提示词工程专项培训", "description": "针对提示词编写维度均分偏低，组织提示词工程工作坊。"},
        {"name": "AI 工具实操演练", "description": "结合高频业务场景开展 AI 工具实操演练，提升工具应用熟练度。"}
      ]
    },
    {
      "module": "AI_MGMT",
      "suggestions": [
        {"name": "AI 项目管理研修", "description": "围绕场景题暴露的项目编排短板，开展 AI 项目管理案例研修。"},
        {"name": "风险治理机制共建", "description": "针对风险识别与合规短板，共创 AI 应用风险治理清单与流程。"}
      ]
    }
  ],
  "summary": "团队整体处于 AI 应用爬坡期，使用能力短板集中在提示词与工具熟练度，管理能力短板集中在项目编排与风险治理，建议按上述方向分层推进培训。"
}`

// parsedValid 断言用的期望结构（与 validOutputJSON 逐字段对应）。
func parsedValid() Output {
	return Output{
		Modules: []ModuleOutput{
			{
				Module: "AI_USAGE",
				Suggestions: []SuggestionOutput{
					{Name: "提示词工程专项培训", Description: "针对提示词编写维度均分偏低，组织提示词工程工作坊。"},
					{Name: "AI 工具实操演练", Description: "结合高频业务场景开展 AI 工具实操演练，提升工具应用熟练度。"},
				},
			},
			{
				Module: "AI_MGMT",
				Suggestions: []SuggestionOutput{
					{Name: "AI 项目管理研修", Description: "围绕场景题暴露的项目编排短板，开展 AI 项目管理案例研修。"},
					{Name: "风险治理机制共建", Description: "针对风险识别与合规短板，共创 AI 应用风险治理清单与流程。"},
				},
			},
		},
		Summary: "团队整体处于 AI 应用爬坡期，使用能力短板集中在提示词与工具熟练度，管理能力短板集中在项目编排与风险治理，建议按上述方向分层推进培训。",
	}
}

// assertOutputEqual 字段保真断言。
func assertOutputEqual(t *testing.T, got, want Output) {
	t.Helper()
	if got.Summary != want.Summary {
		t.Errorf("Summary = %q, want %q", got.Summary, want.Summary)
	}
	if len(got.Modules) != len(want.Modules) {
		t.Fatalf("modules 数 = %d, want %d", len(got.Modules), len(want.Modules))
	}
	for i, m := range got.Modules {
		w := want.Modules[i]
		if m.Module != w.Module {
			t.Errorf("modules[%d].module = %q, want %q", i, m.Module, w.Module)
		}
		if len(m.Suggestions) != len(w.Suggestions) {
			t.Fatalf("modules[%d] 建议数 = %d, want %d", i, len(m.Suggestions), len(w.Suggestions))
		}
		for j, s := range m.Suggestions {
			if s != w.Suggestions[j] {
				t.Errorf("modules[%d].suggestions[%d] = %+v, want %+v", i, j, s, w.Suggestions[j])
			}
		}
	}
}

// TestParseOutput_Valid 两模块各 2 条的合法 JSON 解析成功且字段保真。
func TestParseOutput_Valid(t *testing.T) {
	out, err := ParseOutput(validOutputJSON)
	if err != nil {
		t.Fatalf("合法输出应解析成功, got %v", err)
	}
	assertOutputEqual(t, out, parsedValid())
}

// TestParseOutput_ModuleMissing 缺 AI_MGMT 模块时返 ErrSchemaInvalid。
func TestParseOutput_ModuleMissing(t *testing.T) {
	raw := `{"modules":[{"module":"AI_USAGE","suggestions":[
		{"name":"a","description":"d"},{"name":"b","description":"d"}]}],"summary":"s"}`
	if _, err := ParseOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("缺 AI_MGMT 应返 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseOutput_CountOutOfRange 某模块建议 1 条或 5 条时返 ErrSchemaInvalid。
func TestParseOutput_CountOutOfRange(t *testing.T) {
	one := `{"modules":[{"module":"AI_USAGE","suggestions":[{"name":"a","description":"d"}]},
		{"module":"AI_MGMT","suggestions":[{"name":"a","description":"d"},{"name":"b","description":"d"}]}],"summary":"s"}`
	if _, err := ParseOutput(one); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("1 条建议应返 ErrSchemaInvalid, got %v", err)
	}

	fiveJSON := `{"modules":[{"module":"AI_USAGE","suggestions":[` +
		`{"name":"a","description":"d"},{"name":"b","description":"d"}]},` +
		`{"module":"AI_MGMT","suggestions":[` +
		`{"name":"1","description":"d"},{"name":"2","description":"d"},{"name":"3","description":"d"},` +
		`{"name":"4","description":"d"},{"name":"5","description":"d"}]}],"summary":"s"}`
	if _, err := ParseOutput(fiveJSON); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("5 条建议应返 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseOutput_EmptyField 单条建议 name/description 为空时返 ErrSchemaInvalid。
func TestParseOutput_EmptyField(t *testing.T) {
	raw := `{"modules":[{"module":"AI_USAGE","suggestions":[
		{"name":"","description":"d"},{"name":"b","description":"d"}]},
		{"module":"AI_MGMT","suggestions":[
		{"name":"a","description":"d"},{"name":"b","description":"d"}]}],"summary":"s"}`
	if _, err := ParseOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("空 name 应返 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseOutput_DuplicatedModule 模块条目重复（AI_USAGE×2）时返 ErrSchemaInvalid。
func TestParseOutput_DuplicatedModule(t *testing.T) {
	raw := `{"modules":[{"module":"AI_USAGE","suggestions":[
		{"name":"a","description":"d"},{"name":"b","description":"d"}]},
		{"module":"AI_USAGE","suggestions":[
		{"name":"c","description":"d"},{"name":"e","description":"d"}]},
		{"module":"AI_MGMT","suggestions":[
		{"name":"f","description":"d"},{"name":"g","description":"d"}]}],"summary":"s"}`
	if _, err := ParseOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("重复模块条目应返 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseOutput_FencedMarkdown ```json 围栏包裹输出解析成功。
func TestParseOutput_FencedMarkdown(t *testing.T) {
	fenced := "```json\n" + validOutputJSON + "\n```"
	out, err := ParseOutput(fenced)
	if err != nil {
		t.Fatalf("围栏输出应解析成功, got %v", err)
	}
	assertOutputEqual(t, out, parsedValid())
}

// TestBuildPrompt_NoPersonalData BuildPrompt 产物不包含素材结构外的任何单人标识。
func TestBuildPrompt_NoPersonalData(t *testing.T) {
	avg := 62.5
	low := 30.0
	agg := 68.0
	m := Material{
		PeriodStart: "2026-09-28",
		PeriodEnd:   "2026-10-05",
		Modules: []ModuleMaterial{
			{
				Module: "AI_USAGE",
				DimAverages: []DimAvg{
					{Code: "AIU_PROMPT", Name: "提示词编写", Avg: &avg, LowRatio: &low},
				},
				AggScore: &agg,
			},
		},
		Activity:       ActivityMaterial{Active: 40, LowFreq: 20, Unused: 40, StaffTotal: 100},
		WeakDimensions: []WeakDimMaterial{{Module: "AI_USAGE", Code: "AIU_PROMPT", Name: "提示词编写", Avg: 55.2}},
		DimSpecs:       []DimSpec{{Code: "AIU_PROMPT", Name: "提示词编写", Anchor: "能编写结构化提示词获取准确输出"}},
	}
	prompt := BuildPrompt(m)

	for _, want := range []string{"2026-09-28", "2026-10-05", "AI_USAGE", "AIU_PROMPT", "提示词编写", "62.5", "55.2", "40", "100", "68"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt 应包含素材字段 %q:\n%s", want, prompt)
		}
	}
	// 素材结构外字段：Material 类型没有任何单人标识字段（token 名、工号、姓名等），
	// 单人明细与对话原文无处注入（specs §5.1.4 规则4）；同时模板不含单人数据指令。
	for _, banned := range []string{"token", "Token", "姓名", "工号", "对话原文"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("prompt 不应包含单人标识 %q", banned)
		}
	}
}

// ---- fake 依赖 ----

// fakeLLM 可编程 LLM：按调用序号返回预置回复。
type fakeLLM struct {
	mu      sync.Mutex
	replies []string
	err     error
	calls   int
	prompt  string
}

func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	for _, m := range req.Messages {
		f.prompt += m.Content
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.calls > len(f.replies) {
		return nil, errors.New("fake llm replies exhausted")
	}
	return &fakeStream{content: f.replies[f.calls-1]}, nil
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

// fakeSysParams 脱敏正则 fake：nil 恒回退出厂集（extractor.Redact 既有口径）。
type fakeSysParams struct{}

func (f *fakeSysParams) ReadStringArray(key string) ([]string, error) { return nil, nil }
func (f *fakeSysParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	return nil, nil
}

var _ repository.SystemParamReader = (*fakeSysParams)(nil)

// newGenerator 测试装配。
func newGenerator(fl *fakeLLM, modelID string) *Generator {
	return New(fl, &fakeModelProvider{cfg: llm.ModelConfig{ModelID: modelID}}, &fakeSysParams{})
}

// testMaterial 最小合法素材。
func testMaterial() Material {
	return Material{
		PeriodStart: "2026-09-28",
		PeriodEnd:   "2026-10-05",
		Activity:    ActivityMaterial{Active: 40, LowFreq: 20, Unused: 40, StaffTotal: 100},
	}
}

// TestGenerate_Success 一次成功调用产出两模块输出，modelName 取启用模型快照。
func TestGenerate_Success(t *testing.T) {
	fl := &fakeLLM{replies: []string{validOutputJSON}}
	g := newGenerator(fl, "kimi-k2.7-code")

	out, model, err := g.Generate(context.Background(), testMaterial())
	if err != nil {
		t.Fatalf("Generate 应成功, got %v", err)
	}
	assertOutputEqual(t, out, parsedValid())
	if model != "kimi-k2.7-code" {
		t.Errorf("modelName = %q, want kimi-k2.7-code", model)
	}
	if fl.calls != 1 {
		t.Errorf("LLM 调用数 = %d, want 1", fl.calls)
	}
	if !strings.Contains(fl.prompt, "2026-09-28") {
		t.Errorf("prompt 应包含素材区间")
	}
}

// TestGenerate_RetryOnSchemaInvalid 首次返坏结构、第二次合法时 Generate 成功（重试一次）。
func TestGenerate_RetryOnSchemaInvalid(t *testing.T) {
	fl := &fakeLLM{replies: []string{"抱歉，无法完成。", validOutputJSON}}
	g := newGenerator(fl, "m")

	out, _, err := g.Generate(context.Background(), testMaterial())
	if err != nil {
		t.Fatalf("schema 失败后重试一次应成功, got %v", err)
	}
	assertOutputEqual(t, out, parsedValid())
	if fl.calls != 2 {
		t.Errorf("LLM 调用数 = %d, want 2（初次 + 重试一次）", fl.calls)
	}
}

// TestGenerate_SchemaExhausted 重试后仍失败返 ErrSchemaInvalid。
func TestGenerate_SchemaExhausted(t *testing.T) {
	fl := &fakeLLM{replies: []string{"坏结构一", "坏结构二"}}
	g := newGenerator(fl, "m")

	_, _, err := g.Generate(context.Background(), testMaterial())
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("重试耗尽应返 ErrSchemaInvalid, got %v", err)
	}
	if fl.calls != 2 {
		t.Errorf("LLM 调用数 = %d, want 2（不超一次重试）", fl.calls)
	}
}

// TestGenerate_RedactsOutput fake 返回文本含 /etc/passwd 时被替换为 [PATH]（出厂正则集）。
func TestGenerate_RedactsOutput(t *testing.T) {
	dirty := `{
  "modules": [
    {"module": "AI_USAGE", "suggestions": [
      {"name": "安全培训", "description": "检查配置文件 /etc/passwd 排查风险。"},
      {"name": "b", "description": "d"}]},
    {"module": "AI_MGMT", "suggestions": [
      {"name": "a", "description": "d"},
      {"name": "b", "description": "密钥 sk-abc123defg456 需轮换。"}]}
  ],
  "summary": "全员安全意识待提升，参考 /etc/hosts 配置。"
}`
	fl := &fakeLLM{replies: []string{dirty}}
	g := newGenerator(fl, "m")

	out, _, err := g.Generate(context.Background(), testMaterial())
	if err != nil {
		t.Fatalf("Generate 应成功, got %v", err)
	}
	if strings.Contains(out.Summary, "/etc/") {
		t.Errorf("Summary 脱敏失败: %q", out.Summary)
	}
	if !strings.Contains(out.Summary, "[PATH]") {
		t.Errorf("Summary 应含 [PATH] 占位符: %q", out.Summary)
	}
	for _, mod := range out.Modules {
		for _, s := range mod.Suggestions {
			if strings.Contains(s.Description, "/etc/") || strings.Contains(s.Description, "sk-") {
				t.Errorf("description 脱敏失败: %q", s.Description)
			}
		}
	}
	mgmt := out.Modules[1].Suggestions[1].Description
	if !strings.Contains(mgmt, "[SECRET]") {
		t.Errorf("sk- 密钥应替换为 [SECRET]: %q", mgmt)
	}
}
