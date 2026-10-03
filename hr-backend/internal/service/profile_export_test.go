// profile_export_test 对 A2 导出做黑盒单元测试（specs P2_PRF_001 §4.1.3、03 A2）。
//
// 复用 profile_test 的 fake 体系，覆盖：
//   - 表头五列与数据行取值：全字段直出、缺失列空串、九型数字串转中文名（specs §4.1.3）
//   - 全量导出分页不截断：Export 不消费分页参数（specs §5.1.4 规则2）
//   - 上游名单失败整体 1305 不产出文件（specs §5.1.4 规则1）
package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// nowDateCompact 服务器当日 YYYYMMDD（与实现侧文件名同源口径）。
func nowDateCompact() string { return time.Now().Format("20060102") }

// openExportSheet 执行导出并解析 Sheet1，返回 xlsx 句柄、数据行总数与错误。
func openExportSheet(t *testing.T, svc service.ProfileService, f service.ProfileFilter) (*excelize.File, int) {
	t.Helper()
	data, filename, err := svc.Export(context.Background(), f)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("导出 bytes 应非空")
	}
	wantName := "人员画像名单_" + nowDateCompact() + ".xlsx"
	if filename != wantName {
		t.Fatalf("文件名 want %s, got %s", wantName, filename)
	}
	fx, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	t.Cleanup(func() { _ = fx.Close() })
	rows, err := fx.GetRows(fx.GetSheetName(0))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) < 1 {
		t.Fatal("xlsx 应至少含表头行")
	}
	return fx, len(rows) - 1
}

// cellOf 取 Sheet1 指定行列单元格值（越界按空串）。
func cellOf(t *testing.T, fx *excelize.File, row, col int) string {
	t.Helper()
	rows, err := fx.GetRows(fx.GetSheetName(0))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if row-1 >= len(rows) {
		return ""
	}
	cells := rows[row-1]
	if col-1 >= len(cells) {
		return ""
	}
	return cells[col-1]
}

// TestProfileExport_ColumnsAndRows 2 人（张敏全字段、张伟全缺失）：表头五列、
// 全字段行取值正确（九型 "3" → 成就型）、缺失行活跃度未使用 + 数值九型空串。
func TestProfileExport_ColumnsAndRows(t *testing.T) {
	ps, pe := profilePeriod()
	usage, mgmt := 82.35, 76.5
	ds := &fakeProfileDimScores{}
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张三", Module: domain.ModuleAIUsage, ModuleScore: &usage, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "张三", Module: domain.ModuleAIMgmt, ModuleScore: &mgmt, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ac := &fakeProfileActivityStats{latest: []domain.ActivityStat{
		{TokenName: "张三", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	rs := &fakeProfileResults{byStaff: map[string]domain.AssessmentTestResult{
		"张三": {MainType: "3"},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张三"}, {StaffID: "2", StaffName: "李四"},
	}, total: 2}

	fx, dataRows := openExportSheet(t, newProfileSvc(&fakeProfileDimensionRepo{}, ds, ag, ac, rs, ua), service.ProfileFilter{})
	if dataRows != 2 {
		t.Fatalf("数据行 want 2, got %d", dataRows)
	}
	// 表头 A1-E1（张 U+5F20 < 李 U+674E，张三全字段行在前）。
	wantHeader := []string{"姓名", "活跃度", "AI 使用能力", "AI 管理能力", "九型主型"}
	for col, want := range wantHeader {
		if got := cellOf(t, fx, 1, col+1); got != want {
			t.Fatalf("表头[%d] want %q, got %q", col, want, got)
		}
	}
	// 第二行张三全字段：active→活跃、82.35/76.5 直出、3→成就型。
	wantRow := []string{"张三", "活跃", "82.35", "76.5", "成就型"}
	for col, want := range wantRow {
		if got := cellOf(t, fx, 2, col+1); got != want {
			t.Fatalf("张三行[%d] want %q, got %q", col, want, got)
		}
	}
	// 第三行李四全缺失：无 activity 行按 unused 导出未使用、分数九型空串。
	wantMissing := []string{"李四", "未使用", "", "", ""}
	for col, want := range wantMissing {
		if got := cellOf(t, fx, 3, col+1); got != want {
			t.Fatalf("张伟行[%d] want %q, got %q", col, want, got)
		}
	}
}

// TestProfileExport_NoPaginationTruncation 15 人名单 + PageSize 无关：
// Export 全量导出 15 行，不消费分页参数（specs §5.1.4 规则2）。
func TestProfileExport_NoPaginationTruncation(t *testing.T) {
	staffs := make([]userapi.Staff, 0, 15)
	for i := 0; i < 15; i++ {
		staffs = append(staffs, userapi.Staff{StaffID: fmt.Sprintf("%d", i+1), StaffName: fmt.Sprintf("员工%02d", i+1)})
	}
	ua := &fakeUserapiClient{staffs: staffs, total: 15}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	_, dataRows := openExportSheet(t, svc, service.ProfileFilter{Page: 2, PageSize: 10})
	if dataRows != 15 {
		t.Fatalf("全量导出数据行 want 15（分页不截断）, got %d", dataRows)
	}
}

// TestProfileExport_UpstreamFail fake userapi err：断言 Code==1305 且不产出文件。
func TestProfileExport_UpstreamFail(t *testing.T) {
	ua := &fakeUserapiClient{err: errors.New("upstream timeout")}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	data, filename, err := svc.Export(context.Background(), service.ProfileFilter{})
	wantCode(t, err, errcode.StaffListUnavailable)
	if data != nil || filename != "" {
		t.Fatalf("失败时应不产出文件, got len=%d name=%q", len(data), filename)
	}
}

// TestProfileExport_FilterPassthrough 验证筛选透传：activity_level=active 仅导出命中行
//（specs §5.1.4 规则2：查询链路与 A1 完全复用）。
func TestProfileExport_FilterPassthrough(t *testing.T) {
	ps, pe := profilePeriod()
	ac := &fakeProfileActivityStats{latest: []domain.ActivityStat{
		{TokenName: "张敏", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "李四", ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "李四"},
	}, total: 2}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, ac, &fakeProfileResults{}, ua)

	_, dataRows := openExportSheet(t, svc, service.ProfileFilter{ActivityLevel: "active"})
	if dataRows != 1 {
		t.Fatalf("activity_level=active 应仅导出张敏 1 行, got %d", dataRows)
	}

	// 非法枚举走同一校验链路返 1400（specs 03 A2 错误码表）。
	_, _, err := svc.Export(context.Background(), service.ProfileFilter{ActivityLevel: "frequent"})
	wantCode(t, err, errcode.BadRequest)
}

// TestProfileExport_EmptyRoster 空名单：仅表头一行的合法 xlsx，不报错。
func TestProfileExport_EmptyRoster(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{}, total: 0}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	_, dataRows := openExportSheet(t, svc, service.ProfileFilter{})
	if dataRows != 0 {
		t.Fatalf("空名单数据行 want 0, got %d", dataRows)
	}
}

// TestProfileExport_EnneagramAllTypes 九型 1-9 全量映射为中文名，防措辞漂移。
func TestProfileExport_EnneagramAllTypes(t *testing.T) {
	want := map[string]string{
		"1": "完美型", "2": "助人型", "3": "成就型", "4": "自我型", "5": "智慧型",
		"6": "忠诚型", "7": "活跃型", "8": "领袖型", "9": "和平型",
	}
	for mt, name := range want {
		rs := &fakeProfileResults{byStaff: map[string]domain.AssessmentTestResult{
			"张敏": {MainType: mt},
		}}
		ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
		fx, _ := openExportSheet(t, newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
			&fakeProfileAggScores{}, &fakeProfileActivityStats{}, rs, ua), service.ProfileFilter{})
		if got := cellOf(t, fx, 2, 5); got != name {
			t.Fatalf("九型 %s want %q, got %q", mt, name, got)
		}
	}
}

// TestProfileExport_NoDecorationColumns 验证不含降权标记列：全字段仅五列，
// F 列恒空（specs §4.1.3：不含降权标记与展示态修饰）。
func TestProfileExport_NoDecorationColumns(t *testing.T) {
	ps, pe := profilePeriod()
	score := 82.0
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 82,
			Status: domain.ScoreStatusSuccess, Insufficient: true, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}

	data, _, err := newProfileSvc(&fakeProfileDimensionRepo{}, ds, ag,
		&fakeProfileActivityStats{}, &fakeProfileResults{}, ua).Export(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	fx, cerr := excelize.OpenReader(bytes.NewReader(data))
	if cerr != nil {
		t.Fatalf("OpenReader: %v", cerr)
	}
	defer func() { _ = fx.Close() }()
	rows, gerr := fx.GetRows(fx.GetSheetName(0))
	if gerr != nil {
		t.Fatalf("GetRows: %v", gerr)
	}
	for i, row := range rows {
		if len(row) > 5 {
			t.Fatalf("行 %d 出现第六列（应不含降权标记等修饰列）: %v", i+1, row)
		}
		// 数据行（第 2 行起）分数列直出原值 82，降权不影响导出内容。
		if i >= 1 && len(row) == 5 && strings.TrimSpace(row[2]) != "82" {
			t.Fatalf("行 %d 分数列 want 82, got %q", i+1, row[2])
		}
	}
}
