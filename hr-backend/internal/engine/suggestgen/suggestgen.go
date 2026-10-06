package suggestgen

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// ErrSchemaInvalid 建议输出未通过 schema 校验（specs §5.1.5：同 LLM 失败路径，
// 重试一次后仍失败上抛交任务级重试，耗尽落 failed）。
var ErrSchemaInvalid = errors.New("suggestgen: schema invalid")

// suggestgenMaxTokens 单次生成输出上限：两模块各 4 条建议 + 综合研判最坏合法
// 输出约 1500 中文字 ≈ 3000 token，留余量。MaxTokens 取 0 时由底座默认兜底。
const suggestgenMaxTokens = 6000

// Output LLM 输出结构（specs §5.1.2 步4），modules_json 落库由 T6 承接。
type Output struct {
	Modules []ModuleOutput `json:"modules"`
	Summary string         `json:"summary"`
}

// ModuleOutput 单模块建议集合。
type ModuleOutput struct {
	Module      string             `json:"module"`
	Suggestions []SuggestionOutput `json:"suggestions"`
}

// SuggestionOutput 单条培训方向建议：方向名称与说明。
type SuggestionOutput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ParseOutput 宽容解析 + 结构校验（specs §5.1.2 步5）：extractor.FirstJSONObject
// 共享骨架剥围栏并防前导示例对象劫持；恒两模块且 module 集合 = {AI_USAGE, AI_MGMT}、
// 每模块建议 2-4 条、每条 name/description 非空，违例返 ErrSchemaInvalid。
func ParseOutput(raw string) (Output, error) {
	var out Output
	if !extractor.FirstJSONObject(raw, &out, func() bool { return out.Modules != nil }) {
		return Output{}, ErrSchemaInvalid
	}
	seen := make(map[string]bool, len(out.Modules))
	for _, mod := range out.Modules {
		if seen[mod.Module] {
			return Output{}, ErrSchemaInvalid
		}
		seen[mod.Module] = true
		if len(mod.Suggestions) < 2 || len(mod.Suggestions) > 4 {
			return Output{}, ErrSchemaInvalid
		}
		for _, s := range mod.Suggestions {
			if s.Name == "" || s.Description == "" {
				return Output{}, ErrSchemaInvalid
			}
		}
	}
	if len(seen) != 2 || !seen[domain.ModuleAIUsage] || !seen[domain.ModuleAIMgmt] {
		return Output{}, ErrSchemaInvalid
	}
	return out, nil
}

// Generator 团队培训建议生成器（Run 由 T6 编排驱动，本包只做 LLM 段）。
type Generator struct {
	llm           llm.Client
	modelProvider llm.EnabledModelProvider
	sysParams     repository.SystemParamReader
}

// New 构造 Generator：llmClient 注入建议生成专用 client，sysParams 读脱敏正则集。
func New(llmClient llm.Client, provider llm.EnabledModelProvider,
	sysParams repository.SystemParamReader) *Generator {
	return &Generator{llm: llmClient, modelProvider: provider, sysParams: sysParams}
}

// Generate 一次生成（specs §5.1.2 步4-5）：BuildPrompt → callLLM → ParseOutput，
// ErrSchemaInvalid 且 ctx 未取消重试一次；成功后对每条 Description 与 Summary
// 逐条 Redact 脱敏返回。modelName 取启用模型快照（排他启用唯一）。
// LLM 失败/超时/重试耗尽返 err 上抛交任务级重试。
func (g *Generator) Generate(ctx context.Context, m Material) (Output, string, error) {
	prompt := BuildPrompt(m)
	callOnce := func() (Output, error) {
		raw, err := g.callLLM(ctx, prompt)
		if err != nil {
			return Output{}, err
		}
		return ParseOutput(raw)
	}
	out, err := callOnce()
	if err != nil && errors.Is(err, ErrSchemaInvalid) && ctx.Err() == nil {
		slog.Warn("suggestgen schema invalid, retrying", "period_start", m.PeriodStart)
		out, err = callOnce()
	}
	if err != nil {
		return Output{}, "", err
	}

	modelID := ""
	if mc, merr := g.modelProvider.GetEnabledModel(ctx); merr == nil {
		modelID = mc.ModelID
	} else {
		slog.Warn("suggestgen model resolve failed, model_name will be empty", "err", merr)
	}
	g.redactOutput(ctx, &out)
	return out, modelID, nil
}

// callLLM 单次 LLM 调用并流式收集全文（grading callLLM 同构；传输重试由
// client 内建承载）。
func (g *Generator) callLLM(ctx context.Context, prompt string) (string, error) {
	stream, err := g.llm.StreamChat(ctx, llm.ChatRequest{
		Messages:  []llm.ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens: suggestgenMaxTokens,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var b strings.Builder
	for {
		chunk, rerr := stream.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return "", rerr
		}
		b.WriteString(chunk.Content)
	}
	return b.String(), nil
}

// redactPatterns 读脱敏正则集：DB 故障回退出厂（安全机制不失效，记 WARN）。
func (g *Generator) redactPatterns(ctx context.Context) []string {
	loaded, err := g.sysParams.ReadStringArrays(extractor.ParamKeyRedactPatterns)
	if err != nil {
		slog.Warn("read redact patterns failed, fallback to factory set", "err", err)
		return nil
	}
	return loaded[extractor.ParamKeyRedactPatterns]
}

// redactOutput 产出文本逐条脱敏（specs §5.1.2 步5、§5.1.4 规则7）：Description
// 与 Summary 过 Redact，patterns 空集恒回退出厂集（extractor.Redact 既有口径）。
// prompt 段不脱敏：进程内一次性消费，无落库面。
func (g *Generator) redactOutput(ctx context.Context, out *Output) {
	patterns := g.redactPatterns(ctx)
	for i := range out.Modules {
		for j := range out.Modules[i].Suggestions {
			out.Modules[i].Suggestions[j].Description =
				extractor.Redact(out.Modules[i].Suggestions[j].Description, patterns)
		}
	}
	out.Summary = extractor.Redact(out.Summary, patterns)
}
