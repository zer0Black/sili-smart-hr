// profile_export A2 导出人员画像名单（specs P2_PRF_001 §4.1.3、03 §5.1.4 规则2）。
//
// 业务规则：
//   - BR1 字段与列表一致、全量命中行分页不截断、文件命名 人员画像名单_YYYYMMDD.xlsx（specs §4.1.3）
//   - BR2 数值缺失导出空串、不含降权标记与展示态修饰（specs §4.1.3）
//   - BR3 复用 A1 同一查询链路：透传筛选、跳过分页（specs §5.1.4 规则2）
package service

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"

	"sili-smart-hr/backend/internal/domain"
)

// enneagramTypeNames 九型数字串对应中文名，与前端 i18n 枚举值对齐（03 A2）。
var enneagramTypeNames = map[string]string{
	"1": "完美型",
	"2": "助人型",
	"3": "成就型",
	"4": "自我型",
	"5": "智慧型",
	"6": "忠诚型",
	"7": "活跃型",
	"8": "领袖型",
	"9": "和平型",
}

// exportActivityNames 活跃度枚举对应中文，与前端 i18n 对齐的导出侧常量（03 A2）。
var exportActivityNames = map[string]string{
	domain.ActiveLevelActive:  "活跃",
	domain.ActiveLevelLowFreq: "低频",
	domain.ActiveLevelUnused:  "未使用",
}

// exportHeaders 表头一行五列（specs §4.1.3：字段与列表显示字段一致）。
var exportHeaders = []string{"姓名", "活跃度", "AI 使用能力", "AI 管理能力", "九型主型"}

// Export 按筛选条件全量导出 xlsx（03 A2）：共享 assembleRows 链路（跳过分页），
// 返回 xlsx 二进制流与文件名 "人员画像名单_YYYYMMDD.xlsx"（日期取服务器当日）。
// 行数不设上限守卫：上游名单上限 1 万人（WalkStaffPages 100 页×100 行），
// 远低于 xlsx 单 sheet 1048575 数据行上限，全量口径（BR1）不截断。
func (s *profileService) Export(ctx context.Context, f ProfileFilter) ([]byte, string, error) {
	items, err := s.assembleRows(ctx, f)
	if err != nil {
		return nil, "", err
	}

	fx := excelize.NewFile()
	defer func() { _ = fx.Close() }()
	sheet := fx.GetSheetName(0)
	for col, h := range exportHeaders {
		cell, cerr := excelize.CoordinatesToCellName(col+1, 1)
		if cerr != nil {
			return nil, "", fmt.Errorf("export header cell: %w", cerr)
		}
		if serr := fx.SetCellValue(sheet, cell, h); serr != nil {
			return nil, "", fmt.Errorf("export header: %w", serr)
		}
	}
	for r, it := range items {
		usage, mgmt := "", ""
		if it.AIUsageScore != nil {
			usage = strconv.FormatFloat(*it.AIUsageScore, 'f', -1, 64)
		}
		if it.AIMGMTScore != nil {
			mgmt = strconv.FormatFloat(*it.AIMGMTScore, 'f', -1, 64)
		}
		enneagram := ""
		if it.EnneagramMainType != nil {
			enneagram = enneagramTypeNames[*it.EnneagramMainType]
		}
		// 缺失值全导出空串；不含降权标记与展示态修饰列（specs §4.1.3）。
		row := []string{it.StaffName, exportActivityNames[it.ActivityLevel], usage, mgmt, enneagram}
		for col, v := range row {
			cell, cerr := excelize.CoordinatesToCellName(col+1, r+2)
			if cerr != nil {
				return nil, "", fmt.Errorf("export cell: %w", cerr)
			}
			if serr := fx.SetCellValue(sheet, cell, v); serr != nil {
				return nil, "", fmt.Errorf("export cell value: %w", serr)
			}
		}
	}

	var buf bytes.Buffer
	if werr := fx.Write(&buf); werr != nil {
		return nil, "", fmt.Errorf("write xlsx: %w", werr)
	}
	return buf.Bytes(), "人员画像名单_" + time.Now().Format("20060102") + ".xlsx", nil
}
