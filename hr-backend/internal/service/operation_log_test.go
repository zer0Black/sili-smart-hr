// operation_log_test 对查询/导出 service 做黑盒单元测试（specs P4_LOG_001
// §5.3/§5.4、03 §3 A1/A2）。复用 account_test 的 wantCode 与
// profile_export_test 的 cellOf 辅助。
package service_test

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeOpLogQueryRepo 记录查询面收到的 filter/分页/limit，返回可控行集与 total。
type fakeOpLogQueryRepo struct {
	gotFilter   repository.OperationLogFilter
	gotPage     int
	gotPageSize int
	listCalls   int
	pageRows    []domain.OperationLog
	total       int64
	listErr     error

	gotLimit      int
	byFilterCalls int
	filterRows    []domain.OperationLog
	byFilterErr   error
}

var _ repository.OperationLogRepository = (*fakeOpLogQueryRepo)(nil)

func (f *fakeOpLogQueryRepo) InsertBatch(context.Context, []domain.OperationLog) error { return nil }
func (f *fakeOpLogQueryRepo) Insert(context.Context, *domain.OperationLog) error       { return nil }
func (f *fakeOpLogQueryRepo) DeleteBefore(context.Context, time.Time) (int64, error)   { return 0, nil }

func (f *fakeOpLogQueryRepo) ListPage(_ context.Context, flt repository.OperationLogFilter, page, pageSize int) ([]domain.OperationLog, int64, error) {
	f.gotFilter = flt
	f.gotPage, f.gotPageSize = page, pageSize
	f.listCalls++
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.pageRows, f.total, nil
}

func (f *fakeOpLogQueryRepo) ListByFilter(_ context.Context, flt repository.OperationLogFilter, limit int) ([]domain.OperationLog, error) {
	f.gotFilter = flt
	f.gotLimit = limit
	f.byFilterCalls++
	if f.byFilterErr != nil {
		return nil, f.byFilterErr
	}
	return f.filterRows, nil
}

// opExportNameRe 导出文件名格式（specs §4.1.4 规则4：YYYYMMDD_HHmmss）。
var opExportNameRe = regexp.MustCompile(`^操作日志_\d{8}_\d{6}\.xlsx$`)

// openOpExport 执行导出并解析 Sheet1，断言文件名格式后返回句柄。
func openOpExport(t *testing.T, svc service.OperationLogService, q service.OperationLogQuery) *excelize.File {
	t.Helper()
	data, filename, err := svc.Export(context.Background(), q)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("导出 bytes 应非空")
	}
	if !opExportNameRe.MatchString(filename) {
		t.Fatalf("文件名格式不符: %q", filename)
	}
	fx, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	t.Cleanup(func() { _ = fx.Close() })
	return fx
}

// opLogSampleRows 导出测试样例三行（成功/系统任务/登录失败）。
func opLogSampleRows() []domain.OperationLog {
	return []domain.OperationLog{
		{ID: 1790000000000001001, CreatedAt: time.Date(2026, 10, 7, 14, 23, 5, 0, time.Local),
			Operator: "李学涛", Module: domain.OpModuleDimension, Target: "维度「任务适配判断力」聚合权重",
			Summary: "调整维度聚合权重 30 → 50", Result: domain.OpResultSuccess},
		{ID: 1790000000000001002, CreatedAt: time.Date(2026, 10, 7, 14, 20, 11, 0, time.Local),
			Operator: domain.OpOperatorSystem, Module: domain.OpModuleSystemJob, Target: "批次 B20260928S01（2026-09-22 ~ 2026-09-28）",
			Summary: "区间执行完成：成功 96 人，失败 4 人", Result: domain.OpResultSuccess},
		{ID: 1790000000000001003, CreatedAt: time.Date(2026, 10, 7, 14, 15, 42, 0, time.Local),
			Operator: "admin", Module: domain.OpModuleLogin, Target: "登录",
			Summary: "凭证校验未通过", Result: domain.OpResultFail},
	}
}

// opLogWantHeader 导出表头六列（specs §4.1.4 规则4，与列表显示字段一致）。
var opLogWantHeader = []string{"操作时间", "操作人", "操作类型", "操作对象", "详情摘要", "结果"}

// TestOperationLogListValidation 核心断言：module 非八类枚举、时间只传一端、
// start > end 均返回 1400（specs §5.3.2 步骤2、03 A1 错误码表）。
func TestOperationLogListValidation(t *testing.T) {
	svc := service.NewOperationLogService(&fakeOpLogQueryRepo{})

	_, err := svc.List(context.Background(), service.OperationLogQuery{Module: "bad"})
	wantCode(t, err, errcode.BadRequest)

	_, err = svc.List(context.Background(), service.OperationLogQuery{StartDate: "2026-09-01"})
	wantCode(t, err, errcode.BadRequest)

	_, err = svc.List(context.Background(), service.OperationLogQuery{StartDate: "2026-09-05", EndDate: "2026-09-01"})
	wantCode(t, err, errcode.BadRequest)

	// 补充边界：只传 EndDate、result 非枚举、日期格式非法。
	_, err = svc.List(context.Background(), service.OperationLogQuery{EndDate: "2026-09-30"})
	wantCode(t, err, errcode.BadRequest)

	_, err = svc.List(context.Background(), service.OperationLogQuery{Result: "ok"})
	wantCode(t, err, errcode.BadRequest)

	_, err = svc.List(context.Background(), service.OperationLogQuery{StartDate: "2026/09/01", EndDate: "2026-09-30"})
	wantCode(t, err, errcode.BadRequest)

	// 八类枚举与两态结果全量合法通过，且正常触达仓储（BR1 枚举全集）。
	for _, m := range []string{domain.OpModuleLogin, domain.OpModuleAccount, domain.OpModuleDimension,
		domain.OpModuleSystemParams, domain.OpModuleLLMConfig, domain.OpModuleQuestionBank,
		domain.OpModuleAssessment, domain.OpModuleSystemJob} {
		repo := &fakeOpLogQueryRepo{}
		s := service.NewOperationLogService(repo)
		if _, err := s.List(context.Background(), service.OperationLogQuery{Module: m}); err != nil {
			t.Fatalf("module=%s 应合法: %v", m, err)
		}
		if repo.listCalls != 1 {
			t.Fatalf("module=%s 应触达仓储一次, got %d", m, repo.listCalls)
		}
	}
	for _, r := range []string{domain.OpResultSuccess, domain.OpResultFail} {
		if _, err := svc.List(context.Background(), service.OperationLogQuery{Result: r}); err != nil {
			t.Fatalf("result=%s 应合法: %v", r, err)
		}
	}

	// Export 共用同一校验链路（specs §5.4.2 步骤1 复用 5.3 校验）。
	_, _, err = svc.Export(context.Background(), service.OperationLogQuery{Module: "bad"})
	wantCode(t, err, errcode.BadRequest)
}

// TestOperationLogListBoundary 核心断言：起止两端以自然日边界收拢为双闭区间
//（起始 00:00:00、结束 23:59:59 本地时区，specs §4.1.2 A / 03 §1.8）。
func TestOperationLogListBoundary(t *testing.T) {
	repo := &fakeOpLogQueryRepo{}
	svc := service.NewOperationLogService(repo)

	if _, err := svc.List(context.Background(), service.OperationLogQuery{
		Operator: "张", Module: domain.OpModuleDimension, Result: domain.OpResultSuccess,
		StartDate: "2026-09-01", EndDate: "2026-09-30", Page: 2, PageSize: 20,
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := repo.gotFilter.StartAt.Format("2006-01-02 15:04:05"); got != "2026-09-01 00:00:00" {
		t.Fatalf("StartAt want 2026-09-01 00:00:00, got %s", got)
	}
	if got := repo.gotFilter.EndAt.Format("2006-01-02 15:04:05"); got != "2026-09-30 23:59:59" {
		t.Fatalf("EndAt want 2026-09-30 23:59:59, got %s", got)
	}
	if repo.gotFilter.Operator != "张" || repo.gotFilter.Module != domain.OpModuleDimension || repo.gotFilter.Result != domain.OpResultSuccess {
		t.Fatalf("filter 透传不符: %+v", repo.gotFilter)
	}

	// 无时间条件时两端保持零值（仓储跳过区间条件）。
	repo2 := &fakeOpLogQueryRepo{}
	if _, err := service.NewOperationLogService(repo2).List(context.Background(), service.OperationLogQuery{}); err != nil {
		t.Fatalf("List 空条件: %v", err)
	}
	if !repo2.gotFilter.StartAt.IsZero() || !repo2.gotFilter.EndAt.IsZero() {
		t.Fatalf("未传时间时 StartAt/EndAt 应为零值, got %v ~ %v", repo2.gotFilter.StartAt, repo2.gotFilter.EndAt)
	}
}

// TestOperationLogListDTO 核心断言：雪花 ID string 化、时间本地时区秒级直出、
// changes_json 解析与空串/非法 JSON 降级 nil（03 §1.7/§1.9、§2.4）。
func TestOperationLogListDTO(t *testing.T) {
	repo := &fakeOpLogQueryRepo{
		total: 3,
		pageRows: []domain.OperationLog{
			{ID: 1790000000000001001, CreatedAt: time.Date(2026, 10, 7, 14, 23, 5, 0, time.Local),
				Operator: "李学涛", Module: domain.OpModuleDimension, Target: "维度「任务适配判断力」聚合权重",
				Summary: "调整维度聚合权重 30 → 50", Result: domain.OpResultSuccess,
				ChangesJSON: `[{"field":"权重","before":"30","after":"50"}]`, Detail: ""},
			{ID: 1790000000000001002, CreatedAt: time.Date(2026, 10, 7, 14, 20, 11, 0, time.Local),
				Operator: domain.OpOperatorSystem, Module: domain.OpModuleSystemJob, Target: "批次 B20260928S01",
				Summary: "区间执行完成", Result: domain.OpResultSuccess, ChangesJSON: "",
				Detail: "周期批次区间执行完成，终态 partial_failed。"},
			{ID: 1790000000000001003, CreatedAt: time.Date(2026, 10, 7, 14, 15, 42, 0, time.Local),
				Operator: "admin", Module: domain.OpModuleLogin, Target: "登录",
				Summary: "凭证校验未通过", Result: domain.OpResultFail, ChangesJSON: "{bad"},
		},
	}
	res, err := service.NewOperationLogService(repo).List(context.Background(), service.OperationLogQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 3 || res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("分页四字段 want total=3 page=1 page_size=10, got %+v", res)
	}
	r0 := res.List[0]
	if r0.ID != "1790000000000001001" {
		t.Fatalf("ID want 字符串 1790000000000001001, got %q", r0.ID)
	}
	if r0.CreatedAt != "2026-10-07 14:23:05" {
		t.Fatalf("CreatedAt want 2026-10-07 14:23:05, got %q", r0.CreatedAt)
	}
	if len(r0.Changes) != 1 || r0.Changes[0].Field != "权重" || r0.Changes[0].Before != "30" || r0.Changes[0].After != "50" {
		t.Fatalf("Changes 解析不符: %+v", r0.Changes)
	}
	// changes_json 空串 → nil（json null，前端互斥渲染）。
	if res.List[1].Changes != nil {
		t.Fatalf("空串 ChangesJSON 应为 nil, got %+v", res.List[1].Changes)
	}
	if res.List[1].Detail != "周期批次区间执行完成，终态 partial_failed。" {
		t.Fatalf("Detail 应原样透传, got %q", res.List[1].Detail)
	}
	// 非法 JSON 容错降级 nil，不报错。
	if res.List[2].Changes != nil {
		t.Fatalf("非法 ChangesJSON 应降级 nil, got %+v", res.List[2].Changes)
	}
}

// TestOperationLogListPagination 补充：分页缺失兜底 1/10、<1 钳 1、>100 钳 100
//（profile 同款宽松超集，03 A1 page_size 说明）。
func TestOperationLogListPagination(t *testing.T) {
	repo := &fakeOpLogQueryRepo{}
	svc := service.NewOperationLogService(repo)
	res, err := svc.List(context.Background(), service.OperationLogQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if repo.gotPage != 1 || repo.gotPageSize != 10 || res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("缺省分页 want 1/10, repo got %d/%d res got %d/%d", repo.gotPage, repo.gotPageSize, res.Page, res.PageSize)
	}

	res, err = svc.List(context.Background(), service.OperationLogQuery{Page: -3, PageSize: 200})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if repo.gotPage != 1 || repo.gotPageSize != 100 || res.Page != 1 || res.PageSize != 100 {
		t.Fatalf("钳位分页 want 1/100, repo got %d/%d res got %d/%d", repo.gotPage, repo.gotPageSize, res.Page, res.PageSize)
	}
}

// TestOperationLogListRepoError 补充：仓储查询失败映射 1500（specs §5.3.5）。
func TestOperationLogListRepoError(t *testing.T) {
	svc := service.NewOperationLogService(&fakeOpLogQueryRepo{listErr: errors.New("db down")})
	_, err := svc.List(context.Background(), service.OperationLogQuery{})
	wantCode(t, err, errcode.Internal)
}

// TestOperationLogExportTruncation 核心断言：total 超 10000 时第 1 行备注实际
// 导出行数、第 2 行表头；未超限第 1 行直接表头；文件名 YYYYMMDD_HHmmss
//（specs §4.1.4 规则4、03 §1.10）。
func TestOperationLogExportTruncation(t *testing.T) {
	repo := &fakeOpLogQueryRepo{total: 12000, filterRows: opLogSampleRows()}
	fx := openOpExport(t, service.NewOperationLogService(repo), service.OperationLogQuery{})

	if got := cellOf(t, fx, 1, 1); got != "实际导出 10000 行（命中 12000 行，超出截断）" {
		t.Fatalf("A1 备注行 want 实际导出 10000 行（命中 12000 行，超出截断）, got %q", got)
	}
	for col, want := range opLogWantHeader {
		if got := cellOf(t, fx, 2, col+1); got != want {
			t.Fatalf("表头[%d] want %q, got %q", col, want, got)
		}
	}
	// 数据从第 3 行起，样例 3 行。
	if got := cellOf(t, fx, 3, 2); got != "李学涛" {
		t.Fatalf("第 3 行应为首条数据行, got operator=%q", got)
	}
	if got := cellOf(t, fx, 5, 2); got != "admin" {
		t.Fatalf("第 5 行应为末条数据行, got operator=%q", got)
	}
	// 取数上限收敛 10000，total 经 ListPage(1,1) 零扩面取回。
	if repo.gotLimit != 10000 || repo.byFilterCalls != 1 {
		t.Fatalf("ListByFilter want limit=10000 一次, got limit=%d calls=%d", repo.gotLimit, repo.byFilterCalls)
	}
	if repo.listCalls != 1 || repo.gotPage != 1 || repo.gotPageSize != 1 {
		t.Fatalf("ListPage want (1,1) 一次, got (%d,%d) calls=%d", repo.gotPage, repo.gotPageSize, repo.listCalls)
	}

	// 未超限：第 1 行直接表头，无备注行。
	repo2 := &fakeOpLogQueryRepo{total: 3, filterRows: opLogSampleRows()}
	fx2 := openOpExport(t, service.NewOperationLogService(repo2), service.OperationLogQuery{})
	for col, want := range opLogWantHeader {
		if got := cellOf(t, fx2, 1, col+1); got != want {
			t.Fatalf("未超限表头[%d] want %q, got %q", col, want, got)
		}
	}
	if got := cellOf(t, fx2, 2, 2); got != "李学涛" {
		t.Fatalf("未超限数据应从第 2 行起, got %q", got)
	}
}

// TestOperationLogExportColumns 核心断言：操作类型/结果列导出中文名
//（八类枚举中文名映射与前端 i18n 措辞对齐，03 A2）。
func TestOperationLogExportColumns(t *testing.T) {
	repo := &fakeOpLogQueryRepo{total: 3, filterRows: opLogSampleRows()}
	fx := openOpExport(t, service.NewOperationLogService(repo), service.OperationLogQuery{})

	want := [][3]string{
		{"李学涛", "维度与权重", "成功"},
		{domain.OpOperatorSystem, "系统任务", "成功"},
		{"admin", "登录", "失败"},
	}
	for i, w := range want {
		row := i + 2
		if got := cellOf(t, fx, row, 2); got != w[0] {
			t.Fatalf("行 %d 操作人 want %q, got %q", row, w[0], got)
		}
		if got := cellOf(t, fx, row, 3); got != w[1] {
			t.Fatalf("行 %d 操作类型中文名 want %q, got %q", row, w[1], got)
		}
		if got := cellOf(t, fx, row, 6); got != w[2] {
			t.Fatalf("行 %d 结果中文名 want %q, got %q", row, w[2], got)
		}
	}
	// 操作时间列格式与列表一致（秒级本地时区直出）。
	if got := cellOf(t, fx, 2, 1); got != "2026-10-07 14:23:05" {
		t.Fatalf("操作时间列 want 2026-10-07 14:23:05, got %q", got)
	}
}

// TestOperationLogExportRepoError 补充：total 取数与数据取数失败均 1500 且不产出文件。
func TestOperationLogExportRepoError(t *testing.T) {
	svc := service.NewOperationLogService(&fakeOpLogQueryRepo{listErr: errors.New("db down")})
	data, filename, err := svc.Export(context.Background(), service.OperationLogQuery{})
	wantCode(t, err, errcode.Internal)
	if data != nil || filename != "" {
		t.Fatalf("失败时应不产出文件, got len=%d name=%q", len(data), filename)
	}

	svc2 := service.NewOperationLogService(&fakeOpLogQueryRepo{byFilterErr: errors.New("db down")})
	data, filename, err = svc2.Export(context.Background(), service.OperationLogQuery{})
	wantCode(t, err, errcode.Internal)
	if data != nil || filename != "" {
		t.Fatalf("失败时应不产出文件, got len=%d name=%q", len(data), filename)
	}
}

// TestOperationLogExportEmpty 补充：空结果导出仅表头一行的合法 xlsx，不报错。
func TestOperationLogExportEmpty(t *testing.T) {
	repo := &fakeOpLogQueryRepo{total: 0}
	fx := openOpExport(t, service.NewOperationLogService(repo), service.OperationLogQuery{})
	if got := cellOf(t, fx, 1, 1); got != "操作时间" {
		t.Fatalf("空结果第 1 行应直接表头, got %q", got)
	}
	rows, err := fx.GetRows(fx.GetSheetName(0))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("空结果应仅表头一行, got %d", len(rows))
	}
}
