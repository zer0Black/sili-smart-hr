// dimension 能力维度域业务层：维度树组装、CRUD、编码生成、模块联动校验、活跃度阈值。
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"

	"github.com/mozillazg/go-pinyin"
	"gorm.io/gorm"
)

// 字段长度上限（specs §4.1.2 B + §4.2.2）。
const (
	dimNameMax        = 30
	dimNameMin        = 2
	dimAnchorMax      = 500
	dimPromptMax      = 2000
	dimDescriptionMax = 300
	dimCodeMax        = 40
	dimWeightMax      = 100
	activityThMax     = 999
	activityThMin     = 1
	dimCodeRetryMax   = 10 // 编码同名去重重试上限
)

// modulePreset 模块联动派生属性，模块不入库，service 据此派生默认值与校验。
type modulePreset struct {
	Name        string
	DataSource  string
	Weight      int
	Include     bool
	IsReference bool
}

// modulePresets 四模块固定顺序与属性（specs §4.2.4 规则1 + §4.1.5）。
var modulePresets = map[string]modulePreset{
	domain.ModuleActivity:  {Name: "使用活跃度", DataSource: domain.SourceRule, Weight: 0, Include: false, IsReference: false},
	domain.ModuleAIUsage:   {Name: "AI 使用能力", DataSource: domain.SourceConversation, Weight: 5, Include: true, IsReference: false},
	domain.ModuleAIMgmt:    {Name: "AI 管理能力", DataSource: domain.SourceTest, Weight: 5, Include: true, IsReference: false},
	domain.ModuleEnneagram: {Name: "九型人格", DataSource: domain.SourceTest, Weight: 0, Include: false, IsReference: true},
}

// moduleWeightLocked 报告模块的 weight/include_overview 是否锁死为 0/false（specs 规则5）：
// ACTIVITY（基线分级）与 ENNEAGRAM（参考性维度）不参与聚合；未知模块按锁死从严兜底。
func moduleWeightLocked(moduleCode string) bool {
	p, ok := modulePresets[moduleCode]
	return !ok || (p.Weight == 0 && !p.Include)
}

// resolveWeightInclude 联动归一 weight/include_overview（specs 规则5 + 03 §3.3），Create/Update 共用：
// 锁死模块强制 0/false，显式传非默认值返 1400；自由模块显式传值校验 0~100，缺省回退 defW/defI。
func resolveWeightInclude(moduleCode string, defW int, defI bool, weight *int, include *bool) (int, bool, error) {
	if moduleWeightLocked(moduleCode) {
		if (weight != nil && *weight != 0) || (include != nil && *include) {
			return 0, false, NewError(errcode.BadRequest)
		}
		return 0, false, nil
	}
	w, i := defW, defI
	if weight != nil {
		if *weight < 0 || *weight > dimWeightMax {
			return 0, false, NewError(errcode.BadRequest)
		}
		w = *weight
	}
	if include != nil {
		i = *include
	}
	return w, i, nil
}

// moduleOrder 树响应模块固定顺序（specs §4.1.5）。
var moduleOrder = []string{
	domain.ModuleActivity,
	domain.ModuleAIUsage,
	domain.ModuleAIMgmt,
	domain.ModuleEnneagram,
}

// groupPresets AI_USAGE 下两层分组固定顺序与名称，不入库。
var groupPresets = map[string]string{
	domain.GroupBase:  "底层能力",
	domain.GroupUpper: "上层能力",
}

var groupOrder = []string{domain.GroupBase, domain.GroupUpper}

// codePrefix 模块前缀映射（specs 规则4）。
var codePrefix = map[string]string{
	domain.ModuleActivity:  "ACT",
	domain.ModuleAIUsage:   "AI",
	domain.ModuleAIMgmt:    "MGT",
	domain.ModuleEnneagram: "ENN",
}

// 树响应轻量 DTO。

// ModuleNode 树响应模块节点。
type ModuleNode struct {
	ModuleCode  string                `json:"module_code"`
	Name        string                `json:"name"`
	DataSource  string                `json:"data_source"`
	IsReference bool                  `json:"is_reference"`
	Groups      []*GroupNode          `json:"groups"`
	Dimensions  []*DimensionBriefItem `json:"dimensions"`
}

// GroupNode 分组节点，仅 AI_USAGE 出现。
type GroupNode struct {
	GroupCode  string                `json:"group_code"`
	Name       string                `json:"name"`
	Dimensions []*DimensionBriefItem `json:"dimensions"`
}

// DimensionBriefItem 树叶子轻量字段（specs §3.1 长文本字段不在树）。
type DimensionBriefItem struct {
	ID              int64   `json:"id,string"`
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	ModuleCode      string  `json:"module_code"`
	GroupCode       *string `json:"group_code"`
	DataSource      string  `json:"data_source"`
	Weight          int     `json:"weight"`
	IncludeOverview bool    `json:"include_overview"`
	Enabled         bool    `json:"enabled"`
}

// DimensionTreeNode 树响应根。
type DimensionTreeNode struct {
	Modules []*ModuleNode `json:"modules"`
}

// DimensionDetail 详情全字段（specs §3.2）。
type DimensionDetail struct {
	ID              int64   `json:"id,string"`
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	ModuleCode      string  `json:"module_code"`
	GroupCode       *string `json:"group_code"`
	DataSource      string  `json:"data_source"`
	Prompt          string  `json:"prompt"`
	Anchor          string  `json:"anchor"`
	Weight          int     `json:"weight"`
	IncludeOverview bool    `json:"include_overview"`
	Enabled         bool    `json:"enabled"`
	IsReference     bool    `json:"is_reference"`
	Description     string  `json:"description"`
	Version         int     `json:"version"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// CreateDimensionInput 新增请求体。
type CreateDimensionInput struct {
	Name            string  `json:"name"`
	ModuleCode      string  `json:"module_code"`
	GroupCode       *string `json:"group_code"`
	DataSource      string  `json:"data_source"`
	Prompt          string  `json:"prompt"`
	Anchor          string  `json:"anchor"`
	Weight          *int    `json:"weight"`
	IncludeOverview *bool   `json:"include_overview"`
	Description     string  `json:"description"`
}

// UpdateDimensionInput 编辑请求体，不可变字段（code/module_code/group_code/data_source）不接收。
// weight/include_overview/enabled 缺省与显式零值需区分（如 ACTIVITY 合法 weight=0），用指针承载。
type UpdateDimensionInput struct {
	ID              int64  `json:"id,string"`
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Anchor          string `json:"anchor"`
	Weight          *int   `json:"weight"`
	IncludeOverview *bool  `json:"include_overview"`
	Enabled         *bool  `json:"enabled"`
	Description     string `json:"description"`
	Version         int    `json:"version"`
}

// DeleteDimensionInput 删除请求体。
type DeleteDimensionInput struct {
	ID      int64 `json:"id,string"`
	Version int   `json:"version"`
}

// DimensionMutationResult 新增/编辑响应（specs §3.3/§3.4）。
type DimensionMutationResult struct {
	ID              int64   `json:"id,string"`
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	ModuleCode      string  `json:"module_code"`
	GroupCode       *string `json:"group_code"`
	DataSource      string  `json:"data_source"`
	Weight          int     `json:"weight"`
	IncludeOverview bool    `json:"include_overview"`
	Enabled         bool    `json:"enabled"`
	Version         int     `json:"version"`
	UpdatedAt       string  `json:"updated_at,omitempty"` // 仅编辑响应
}

// ActivityRuleDTO 活跃度规则响应。
type ActivityRuleDTO struct {
	ActiveThreshold       int    `json:"active_threshold"`
	LowFrequencyThreshold int    `json:"low_frequency_threshold"`
	UpdatedAt             string `json:"updated_at"`
}

// ActivityRuleInput 活跃度规则保存入参。
type ActivityRuleInput struct {
	ActiveThreshold       int `json:"active_threshold"`
	LowFrequencyThreshold int `json:"low_frequency_threshold"`
}

// DimensionService 能力维度域业务接口。
type DimensionService interface {
	GetTree(ctx context.Context) (*DimensionTreeNode, error)
	GetDetail(ctx context.Context, id int64) (*DimensionDetail, error)
	CreateDimension(ctx context.Context, in CreateDimensionInput) (*DimensionMutationResult, error)
	UpdateDimension(ctx context.Context, in UpdateDimensionInput) (*DimensionMutationResult, error)
	DeleteDimension(ctx context.Context, in DeleteDimensionInput) error
	GetActivityRule(ctx context.Context) (*ActivityRuleDTO, error)
	SaveActivityRule(ctx context.Context, in ActivityRuleInput) (*ActivityRuleDTO, error)
}

type dimensionService struct {
	repo repository.DimensionRepository
}

// NewDimensionService 构造维度域 service。
func NewDimensionService(repo repository.DimensionRepository) DimensionService {
	return &dimensionService{repo: repo}
}

// GenerateDimensionCode 维度编码生成纯函数（specs 规则4）：模块前缀 + 名称全大写拼音，
// 各字拼音连写（如「需求澄清能力」→AI_XUQIUCHENGQINGNENGLI），无分词能力时不加字间下划线，
// 截断 ≤40 字符；同名去重由调用方追加序号。
func GenerateDimensionCode(moduleCode, name string) string {
	prefix := codePrefix[moduleCode]
	parts := pinyin.LazyPinyin(name, pinyin.NewArgs())
	body := strings.Builder{}
	for i := range parts {
		body.WriteString(strings.ToUpper(parts[i]))
	}
	whole := body.String()
	if prefix != "" {
		if whole != "" {
			whole = prefix + "_" + whole
		} else {
			whole = prefix
		}
	}
	// 截断 ≤40 字符（按 rune，避免截断多字节）。
	if utf8.RuneCountInString(whole) > dimCodeMax {
		runes := []rune(whole)
		whole = string(runes[:dimCodeMax])
	}
	return whole
}

// GetTree 组装四模块固定顺序维度树，AI_USAGE 下挂 BASE/UPPER，其余模块维度直属（specs §4.1.5 + §3.1）。
func (s *dimensionService) GetTree(ctx context.Context) (*DimensionTreeNode, error) {
	list, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dimensions: %w", err)
	}
	byModule := make(map[string][]domain.Dimension)
	for i := range list {
		byModule[list[i].ModuleCode] = append(byModule[list[i].ModuleCode], list[i])
	}
	mods := make([]*ModuleNode, 0, len(moduleOrder))
	for _, mc := range moduleOrder {
		preset, ok := modulePresets[mc]
		if !ok {
			continue
		}
		node := &ModuleNode{
			ModuleCode:  mc,
			Name:        preset.Name,
			DataSource:  preset.DataSource,
			IsReference: preset.IsReference,
		}
		dims := byModule[mc]
		if mc == domain.ModuleAIUsage {
			node.Groups = buildGroups(dims)
			node.Dimensions = nil
		} else {
			node.Dimensions = toBriefList(dims)
			node.Groups = nil
		}
		mods = append(mods, node)
	}
	return &DimensionTreeNode{Modules: mods}, nil
}

func buildGroups(dims []domain.Dimension) []*GroupNode {
	byGroup := make(map[string][]domain.Dimension)
	for i := range dims {
		gc := ""
		if dims[i].GroupCode != nil {
			gc = *dims[i].GroupCode
		}
		byGroup[gc] = append(byGroup[gc], dims[i])
	}
	groups := make([]*GroupNode, 0, len(groupOrder))
	for _, gc := range groupOrder {
		g := &GroupNode{
			GroupCode:  gc,
			Name:       groupPresets[gc],
			Dimensions: toBriefList(byGroup[gc]),
		}
		groups = append(groups, g)
	}
	return groups
}

func toBriefList(dims []domain.Dimension) []*DimensionBriefItem {
	items := make([]*DimensionBriefItem, 0, len(dims))
	for i := range dims {
		items = append(items, &DimensionBriefItem{
			ID:              dims[i].ID,
			Code:            dims[i].Code,
			Name:            dims[i].Name,
			ModuleCode:      dims[i].ModuleCode,
			GroupCode:       dims[i].GroupCode,
			DataSource:      dims[i].DataSource,
			Weight:          dims[i].Weight,
			IncludeOverview: dims[i].IncludeOverview,
			Enabled:         dims[i].Enabled,
		})
	}
	return items
}

// GetDetail 详情，不存在返 1201（specs §3.2）。
func (s *dimensionService) GetDetail(ctx context.Context, id int64) (*DimensionDetail, error) {
	d, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.DimensionNotFound)
		}
		return nil, fmt.Errorf("find dimension by id: %w", err)
	}
	return toDetail(d), nil
}

func toDetail(d *domain.Dimension) *DimensionDetail {
	// is_reference 与 data_source 同属「可派生但落库冗余存储」字段（specs §3.1 说明），
	// 读存储值与写路径同源，避免 preset 变更后存量行列值与响应漂移。
	return &DimensionDetail{
		ID:              d.ID,
		Code:            d.Code,
		Name:            d.Name,
		ModuleCode:      d.ModuleCode,
		GroupCode:       d.GroupCode,
		DataSource:      d.DataSource,
		Prompt:          d.Prompt,
		Anchor:          d.Anchor,
		Weight:          d.Weight,
		IncludeOverview: d.IncludeOverview,
		Enabled:         d.Enabled,
		IsReference:     d.IsReference,
		Description:     d.Description,
		Version:         d.Version,
		CreatedAt:       d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       d.UpdatedAt.Format(time.RFC3339),
	}
}

// validateDimensionMutableFields 编辑态共享字段校验（specs §4.1.2 B + 规则6/7 + 03 §5.2 逐项校验），Create/Update 共用。
// prompt 判空仅 CONVERSATION，长度 ≤2000 对所有 data_source 生效；name 先 trim 再判空与 2~30 rune。
func validateDimensionMutableFields(name, anchor, prompt, description, dataSource string) error {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) < dimNameMin || utf8.RuneCountInString(n) > dimNameMax {
		return NewError(errcode.DimensionNameInvalid)
	}
	a := strings.TrimSpace(anchor)
	if a == "" {
		return NewError(errcode.DimensionAnchorRequired)
	}
	if utf8.RuneCountInString(a) > dimAnchorMax || utf8.RuneCountInString(description) > dimDescriptionMax {
		return NewError(errcode.BadRequest)
	}
	if dataSource == domain.SourceConversation && strings.TrimSpace(prompt) == "" {
		return NewError(errcode.DimensionPromptRequired)
	}
	if utf8.RuneCountInString(prompt) > dimPromptMax {
		return NewError(errcode.BadRequest)
	}
	return nil
}

// CreateDimension 新增：校验 → 联动派生 → 编码生成（含冲突去重）→ 入库（specs §4.2 + 规则1~9）。
func (s *dimensionService) CreateDimension(ctx context.Context, in CreateDimensionInput) (*DimensionMutationResult, error) {
	if err := validateDimensionMutableFields(in.Name, in.Anchor, in.Prompt, in.Description, in.DataSource); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	anchor := strings.TrimSpace(in.Anchor)
	description := strings.TrimSpace(in.Description)
	// 模块存在性。
	preset, ok := modulePresets[in.ModuleCode]
	if !ok {
		return nil, NewError(errcode.BadRequest)
	}
	// group_code 仅 AI_USAGE 接受 BASE/UPPER，其余模块统一 nil（specs §4.2.2）。
	// 非 AI_USAGE 传非空则 1400，传空串指针则归一化为 nil，避免落库 "" 后序列化成 "" 而非 null。
	groupCode := in.GroupCode
	if in.ModuleCode == domain.ModuleAIUsage {
		if groupCode == nil || (*groupCode != domain.GroupBase && *groupCode != domain.GroupUpper) {
			return nil, NewError(errcode.BadRequest)
		}
	} else if groupCode != nil {
		if *groupCode != "" {
			return nil, NewError(errcode.BadRequest)
		}
		groupCode = nil
	}
	// data_source 联动（specs 规则8 + §4.2.4 规则1）：显式传值校验一致，否则派生默认。
	if in.DataSource != "" && in.DataSource != preset.DataSource {
		return nil, NewError(errcode.BadRequest)
	}
	ds := preset.DataSource
	// prompt 必填校验依赖派生后的 ds（AI_USAGE 未显式传 data_source 时 in.DataSource 为空）。
	if ds == domain.SourceConversation && strings.TrimSpace(in.Prompt) == "" {
		return nil, NewError(errcode.DimensionPromptRequired)
	}
	// weight/include_overview 联动：锁死模块强制 0/false，自由模块缺省回退 preset（specs 规则5）。
	weight, include, werr := resolveWeightInclude(in.ModuleCode, preset.Weight, preset.Include, in.Weight, in.IncludeOverview)
	if werr != nil {
		return nil, werr
	}

	// 编码生成 + 同名冲突去重（specs 规则4 + §4.2.4 规则2）：
	// 候选依次取 base、base_2..base_N，查到可用即用；追加序号后超 40 字符或候选耗尽返 1209。
	base := GenerateDimensionCode(in.ModuleCode, name)
	code := ""
	found := false
	for i := 1; i <= dimCodeRetryMax+1; i++ {
		if i == 1 {
			code = base
		} else {
			code = fmt.Sprintf("%s_%d", base, i)
			if utf8.RuneCountInString(code) > dimCodeMax {
				break
			}
		}
		exist, ferr := s.repo.FindByCodeExcludingDeleted(ctx, code)
		if ferr != nil {
			if !errors.Is(ferr, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("find by code: %w", ferr)
			}
			found = true
			break
		}
		// ferr == nil 且 exist != nil：编码冲突，换下一候选。
		// exist == nil 且 err == nil 违反 repository 契约，防御上报避免空转写冲突码。
		if exist == nil {
			return nil, fmt.Errorf("find by code: repository returned nil without error")
		}
	}
	if !found {
		return nil, NewError(errcode.DimensionCodeUnavailable)
	}

	d := &domain.Dimension{
		ID:              snowflake.NextID(),
		Code:            code,
		Name:            name,
		ModuleCode:      in.ModuleCode,
		GroupCode:       groupCode,
		DataSource:      ds,
		Prompt:          in.Prompt,
		Anchor:          anchor,
		Weight:          weight,
		IncludeOverview: include,
		Enabled:         true, // specs §6.1 新维度强制启用
		IsReference:     preset.IsReference,
		Description:     description,
		Version:         1, // specs §6.1 version 初始 1
	}
	if err := s.repo.Create(ctx, d); err != nil {
		// 查重与 Create 间无锁，并发同名请求撞 uk_dimension_code 时由索引兜底，映射 1202。
		if dberr.UniqueViolation(err) {
			return nil, NewError(errcode.DimensionCodeExists)
		}
		return nil, fmt.Errorf("create dimension: %w", err)
	}
	return &DimensionMutationResult{
		ID:              d.ID,
		Code:            d.Code,
		Name:            d.Name,
		ModuleCode:      d.ModuleCode,
		GroupCode:       d.GroupCode,
		DataSource:      d.DataSource,
		Weight:          d.Weight,
		IncludeOverview: d.IncludeOverview,
		Enabled:         d.Enabled,
		Version:         d.Version,
	}, nil
}

// UpdateDimension 编辑：存在校验 → 字段校验 → 模块联动兜底 → 乐观锁更新（specs §3.4 + 规则9）。
func (s *dimensionService) UpdateDimension(ctx context.Context, in UpdateDimensionInput) (*DimensionMutationResult, error) {
	cur, err := s.repo.FindByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.DimensionNotFound)
		}
		return nil, fmt.Errorf("find dimension by id: %w", err)
	}
	if err := validateDimensionMutableFields(in.Name, in.Anchor, in.Prompt, in.Description, cur.DataSource); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	anchor := strings.TrimSpace(in.Anchor)
	description := strings.TrimSpace(in.Description)
	// weight/include_overview 联动兜底（specs 规则5）：锁死模块强制 0/false，
	// 自由模块缺省回退当前行存量值（specs §3.4 三字段必填，缺省仅防御 API 客户端）。
	weight, include, werr := resolveWeightInclude(cur.ModuleCode, cur.Weight, cur.IncludeOverview, in.Weight, in.IncludeOverview)
	if werr != nil {
		return nil, werr
	}
	// enabled 缺省回退当前行存量值，避免省略字段被静默当作 false 落库。
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	updates := map[string]any{
		"name":             name,
		"prompt":           in.Prompt,
		"anchor":           anchor,
		"weight":           weight,
		"include_overview": include,
		"enabled":          enabled,
		"description":      description,
	}
	rows, err := s.repo.UpdateWithVersion(ctx, in.ID, in.Version, updates)
	if err != nil {
		return nil, fmt.Errorf("update dimension: %w", err)
	}
	if rows == 0 {
		return nil, NewError(errcode.DimensionVersionConflict)
	}
	// 回读最新行构造响应（携带新 version 与 updated_at）。
	updated, err := s.repo.FindByID(ctx, in.ID)
	if err != nil {
		// NotFound：刚更新成功即被并发删除的极端情况，按版本冲突提示刷新；其他 DB 错误上报 1500。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.DimensionVersionConflict)
		}
		return nil, fmt.Errorf("reload dimension: %w", err)
	}
	return &DimensionMutationResult{
		ID:              updated.ID,
		Code:            updated.Code,
		Name:            updated.Name,
		ModuleCode:      updated.ModuleCode,
		GroupCode:       updated.GroupCode,
		DataSource:      updated.DataSource,
		Weight:          updated.Weight,
		IncludeOverview: updated.IncludeOverview,
		Enabled:         updated.Enabled,
		Version:         updated.Version,
		UpdatedAt:       updated.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// DeleteDimension 软删除：存在校验 → 启用态拦截 → 乐观锁软删（specs §3.5 + 规则3）。
func (s *dimensionService) DeleteDimension(ctx context.Context, in DeleteDimensionInput) error {
	cur, err := s.repo.FindByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.DimensionNotFound)
		}
		return fmt.Errorf("find dimension by id: %w", err)
	}
	if cur.Enabled {
		return NewError(errcode.DimensionEnabledNotDeletable)
	}
	rows, err := s.repo.SoftDeleteWithVersion(ctx, in.ID, in.Version)
	if err != nil {
		return fmt.Errorf("soft delete dimension: %w", err)
	}
	if rows == 0 {
		return NewError(errcode.DimensionVersionConflict)
	}
	return nil
}

// GetActivityRule 读单行表首行（specs §3.6）。
func (s *dimensionService) GetActivityRule(ctx context.Context) (*ActivityRuleDTO, error) {
	setting, err := s.repo.GetActivitySetting(ctx)
	if err != nil {
		// 活跃度配置缺失属系统级配置初始化异常（无专用码），统一走 1500；其他 DB 错误同样 wrap 上报。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("activity setting not initialized: %w", err)
		}
		return nil, fmt.Errorf("get activity setting: %w", err)
	}
	return &ActivityRuleDTO{
		ActiveThreshold:       setting.ActiveThreshold,
		LowFrequencyThreshold: setting.LowFrequencyThreshold,
		UpdatedAt:             setting.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// SaveActivityRule 校验 1≤low<active≤999 后保存单行（specs §3.7 + §4.1.2 C）。
func (s *dimensionService) SaveActivityRule(ctx context.Context, in ActivityRuleInput) (*ActivityRuleDTO, error) {
	if in.ActiveThreshold < activityThMin || in.ActiveThreshold > activityThMax {
		return nil, NewError(errcode.ActivityThresholdInvalid)
	}
	if in.LowFrequencyThreshold < activityThMin || in.LowFrequencyThreshold > activityThMax {
		return nil, NewError(errcode.ActivityThresholdInvalid)
	}
	if in.LowFrequencyThreshold >= in.ActiveThreshold {
		return nil, NewError(errcode.ActivityThresholdInvalid)
	}
	if err := s.repo.UpdateActivitySetting(ctx, in.ActiveThreshold, in.LowFrequencyThreshold); err != nil {
		return nil, fmt.Errorf("update activity setting: %w", err)
	}
	// 回读失败（含配置缺失的 NotFound）与 GetActivityRule 保持一致，统一 wrap 上报 1500。
	setting, err := s.repo.GetActivitySetting(ctx)
	if err != nil {
		return nil, fmt.Errorf("reload activity setting: %w", err)
	}
	return &ActivityRuleDTO{
		ActiveThreshold:       setting.ActiveThreshold,
		LowFrequencyThreshold: setting.LowFrequencyThreshold,
		UpdatedAt:             setting.UpdatedAt.Format(time.RFC3339),
	}, nil
}
