// operation_log 查询与导出 service（specs P4_LOG_001 §5.3/§5.4、03 §3 A1/A2）：
// 四维筛选校验 + 双闭区间换算 + 分页查询 + xlsx 六列导出（超限截断首行备注）。
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/xuri/excelize/v2"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

const (
	opLogDefaultPageSize = 10
	opLogMaxPageSize     = 100
	// exportMaxRows 单次导出行数上限（specs §4.1.4 规则4）。
	exportMaxRows = 10000
)

// opModuleNames 八类操作类型中文名，与前端 i18n 措辞对齐。
var opModuleNames = map[string]string{
	domain.OpModuleLogin:        "登录",
	domain.OpModuleAccount:      "用户管理",
	domain.OpModuleDimension:    "维度与权重",
	domain.OpModuleSystemParams: "系统参数",
	domain.OpModuleLLMConfig:    "大模型配置",
	domain.OpModuleQuestionBank: "题库管理",
	domain.OpModuleAssessment:   "评估运营",
	domain.OpModuleSystemJob:    "系统任务",
}

// OperationLogModuleName 取操作类型中文名（导出 xlsx 与中间件兜底 summary
// 共用单点，未知名返回空串）。
func OperationLogModuleName(module string) string {
	return opModuleNames[module]
}

// opResultNames 结果两态中文名。
var opResultNames = map[string]string{
	domain.OpResultSuccess: "成功",
	domain.OpResultFail:    "失败",
}

// OperationLogService 操作日志只读查询服务（specs §5.3/§5.4；查询导出均只读
// 不记操作日志，specs §5.3.4/§5.4.4 规则1）。
type OperationLogService interface {
	List(ctx context.Context, q OperationLogQuery) (*OperationLogListResult, error)
	Export(ctx context.Context, q OperationLogQuery) ([]byte, string, error)
}

// OperationLogQuery A1/A2 共用查询条件（03 A1）：Operator 已由 handler 层
// 去首尾空格；Module/Result/日期空串即跳过该维度。
type OperationLogQuery struct {
	Operator  string
	Module    string // 空=全部
	Result    string // 空=全部
	StartDate string // yyyy-MM-dd，与 EndDate 成对
	EndDate   string
	Page      int
	PageSize  int
}

// OperationLogListResult A1 响应 data（分页四字段）。
type OperationLogListResult struct {
	List     []OperationLogItem `json:"list"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

// OperationLogItem A1 行：内嵌详情弹窗全量字段（03 §1.9），雪花 ID string 化（§2.4）。
type OperationLogItem struct {
	ID        string              `json:"id"`
	CreatedAt string              `json:"created_at"`
	Operator  string              `json:"operator"`
	Module    string              `json:"module"`
	Target    string              `json:"target"`
	Summary   string              `json:"summary"`
	Result    string              `json:"result"`
	Changes   []domain.ChangeItem `json:"changes"` // changes_json 空串时 nil（json null）
	Detail    string              `json:"detail"`
}

type operationLogService struct {
	repo repository.OperationLogRepository
}

func NewOperationLogService(repo repository.OperationLogRepository) OperationLogService {
	return &operationLogService{repo: repo}
}

// validateAndBuild 校验筛选参数并组装仓储 filter（List/Export 共用）：
// 任一非法返 1400（specs §5.3.2 步骤2、03 A1 错误码表）。
func validateOpLogQuery(q OperationLogQuery) (repository.OperationLogFilter, error) {
	if q.Module != "" {
		if _, ok := opModuleNames[q.Module]; !ok {
			return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
		}
	}
	if q.Result != "" && q.Result != domain.OpResultSuccess && q.Result != domain.OpResultFail {
		return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
	}
	var startAt, endAt time.Time
	if q.StartDate != "" || q.EndDate != "" {
		if q.StartDate == "" || q.EndDate == "" {
			return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
		}
		sd, err := time.Parse("2006-01-02", q.StartDate)
		if err != nil {
			return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
		}
		ed, err := time.Parse("2006-01-02", q.EndDate)
		if err != nil {
			return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
		}
		if sd.After(ed) {
			return repository.OperationLogFilter{}, NewError(errcode.BadRequest)
		}
		// 自然日边界收拢双闭区间（specs §4.1.2 A：起始 00:00:00、结束 23:59:59）。
		// 结束端补满纳秒：落库 created_at 带亚秒（SQLite 微秒），裸 23:59:59.000
		// 会让结束日最后一秒内的行漏出 <= 区间。
		startAt = time.Date(sd.Year(), sd.Month(), sd.Day(), 0, 0, 0, 0, time.Local)
		endAt = time.Date(ed.Year(), ed.Month(), ed.Day(), 23, 59, 59, 999999999, time.Local)
	}
	return repository.OperationLogFilter{
		Operator: q.Operator,
		Module:   q.Module,
		Result:   q.Result,
		StartAt:  startAt,
		EndAt:    endAt,
	}, nil
}

// normalizeOpLogPage 分页兜底与钳位（1-100 宽松超集，profile 同款）。
func normalizeOpLogPage(page, pageSize int) (int, int) {
	if pageSize <= 0 {
		pageSize = opLogDefaultPageSize
	}
	if pageSize > opLogMaxPageSize {
		pageSize = opLogMaxPageSize
	}
	if page <= 0 {
		page = 1
	}
	return page, pageSize
}

// List 编排（specs §5.3.2）：校验 → 分页兜底钳位 → 仓储分页查询 → DTO 组装。
func (s *operationLogService) List(ctx context.Context, q OperationLogQuery) (*OperationLogListResult, error) {
	filter, err := validateOpLogQuery(q)
	if err != nil {
		return nil, err
	}
	page, pageSize := normalizeOpLogPage(q.Page, q.PageSize)

	rows, total, rerr := s.repo.ListPage(ctx, filter, page, pageSize)
	if rerr != nil {
		slog.Error("operation log list failed", "err", rerr)
		return nil, NewError(errcode.Internal)
	}
	list := make([]OperationLogItem, 0, len(rows))
	for i := range rows {
		list = append(list, buildOperationLogItem(rows[i]))
	}
	return &OperationLogListResult{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// buildOperationLogItem 行 DTO：时间本地时区秒级直出；changes_json 空串保持
// nil（json null），非法 JSON 容错降级 nil 并记 ERROR（specs §5.3.5）。
func buildOperationLogItem(l domain.OperationLog) OperationLogItem {
	item := OperationLogItem{
		ID:        fmt.Sprintf("%d", l.ID),
		CreatedAt: l.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		Operator:  l.Operator,
		Module:    l.Module,
		Target:    l.Target,
		Summary:   l.Summary,
		Result:    l.Result,
		Detail:    l.Detail,
	}
	if l.ChangesJSON != "" {
		var changes []domain.ChangeItem
		if err := json.Unmarshal([]byte(l.ChangesJSON), &changes); err != nil {
			slog.Error("operation log changes parse failed", "id", l.ID, "err", err)
		} else {
			item.Changes = changes
		}
	}
	return item
}

// opLogExportHeaders 导出表头六列（specs §4.1.4 规则4，与列表显示字段一致）。
var opLogExportHeaders = []string{"操作时间", "操作人", "操作类型", "操作对象", "详情摘要", "结果"}

// Export 按筛选条件导出 xlsx（specs §5.4）：total 经 ListPage(1,1) 零扩面取回，
// 数据经 ListByFilter 截前 10000 行；超限时第 1 行备注实际导出行数、第 2 行表头。
// 返回 xlsx 流与文件名 操作日志_YYYYMMDD_HHmmss.xlsx（导出时刻，03 §1.10）。
func (s *operationLogService) Export(ctx context.Context, q OperationLogQuery) ([]byte, string, error) {
	filter, err := validateOpLogQuery(q)
	if err != nil {
		return nil, "", err
	}

	_, total, perr := s.repo.ListPage(ctx, filter, 1, 1)
	if perr != nil {
		slog.Error("operation log export count failed", "err", perr)
		return nil, "", NewError(errcode.Internal)
	}
	rows, lerr := s.repo.ListByFilter(ctx, filter, exportMaxRows)
	if lerr != nil {
		slog.Error("operation log export load failed", "err", lerr)
		return nil, "", NewError(errcode.Internal)
	}

	fx := excelize.NewFile()
	defer func() { _ = fx.Close() }()
	sheet := fx.GetSheetName(0)
	rowIdx := 1
	if total > exportMaxRows {
		note := fmt.Sprintf("实际导出 %d 行（命中 %d 行，超出截断）", exportMaxRows, total)
		if serr := fx.SetSheetRow(sheet, "A1", &[]any{note}); serr != nil {
			return nil, "", fmt.Errorf("export note: %w", serr)
		}
		rowIdx = 2
	}
	if serr := fx.SetSheetRow(sheet, cellName(1, rowIdx), &opLogExportHeaders); serr != nil {
		return nil, "", fmt.Errorf("export header: %w", serr)
	}
	for r, l := range rows {
		row := []any{
			l.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			l.Operator,
			opModuleNames[l.Module],
			l.Target,
			l.Summary,
			opResultNames[l.Result],
		}
		if serr := fx.SetSheetRow(sheet, cellName(1, rowIdx+r+1), &row); serr != nil {
			return nil, "", fmt.Errorf("export row: %w", serr)
		}
	}

	var buf bytes.Buffer
	if werr := fx.Write(&buf); werr != nil {
		return nil, "", fmt.Errorf("write xlsx: %w", werr)
	}
	return buf.Bytes(), "操作日志_" + time.Now().Format("20060102_150405") + ".xlsx", nil
}

// cellName (col,row) 转 A1 地址（1 起始），失败由调用方兜底。
func cellName(col, row int) string {
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return "A1"
	}
	return name
}
