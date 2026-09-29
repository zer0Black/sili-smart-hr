package userapi

// paginate.go ListStaffs 翻页遍历共享助手：service 对象校验（逐页查找）与
// pipeline 全员名单全量拉取共用同一套页大小/页数上限/双终止口径，防两处漂移。

import (
	"context"
	"fmt"
)

// StaffPageSize 每页条数（上游 page_size 上限 100）。
const StaffPageSize = 100

// StaffMaxPages 翻页页数上限：防上游分页失效（每页恒满页、total 恒追不上）
// 导致无界翻页。
const StaffMaxPages = 100

// ListStaffPager ListStaffs 的窄面（*Client 与消费方 fake 均鸭子满足）。
type ListStaffPager interface {
	ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) ([]Staff, int64, error)
}

// WalkStaffPages 逐页遍历上游人员：每页回调 onPage，返回非 nil 提前终止。
// 终止口径：短页/空页是唯一可信信号；total>0 时按原始行数累计收齐亦可终止
//（上游跨页重复行会让去重后计数永远追不上 total，须与 total 同基）。
// 翻超 StaffMaxPages 页返 ErrStaffPageExceeded。
func WalkStaffPages(ctx context.Context, p ListStaffPager, secret, keyword string, onPage func(page []Staff) error) error {
	fetched := 0
	for page := 1; page <= StaffMaxPages; page++ {
		items, total, err := p.ListStaffs(ctx, secret, keyword, page, StaffPageSize)
		if err != nil {
			return err
		}
		if err := onPage(items); err != nil {
			return err
		}
		fetched += len(items)
		if len(items) < StaffPageSize || len(items) == 0 {
			return nil
		}
		if total > 0 && int64(fetched) >= total {
			return nil
		}
	}
	return ErrStaffPageExceeded
}

// ErrStaffPageExceeded 翻页超上限（上游分页疑似失效）。
var ErrStaffPageExceeded = fmt.Errorf("userapi: staff pagination exceeded %d pages", StaffMaxPages)

// FindStaff 按 staff_id+staff_name 双匹配在 keyword 过滤结果中查找对象
//（keyword 通常传 staff_name，上游模糊过滤缩小翻页范围）。命中返回 true。
// 上游错误原样上抛，由调用方映射业务错误码。
func FindStaff(ctx context.Context, p ListStaffPager, secret, staffID, staffName string) (bool, error) {
	found := false
	err := WalkStaffPages(ctx, p, secret, staffName, func(page []Staff) error {
		for i := range page {
			if page[i].StaffID == staffID && page[i].StaffName == staffName {
				found = true
				return errStopWalk
			}
		}
		return nil
	})
	if err == errStopWalk {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return found, nil
}

// errStopWalk 命中后的提前终止信号（FindStaff 内部用，WalkStaffPages 不感知）。
var errStopWalk = fmt.Errorf("userapi: walk stopped")

// FetchAllStaff 全量拉取去重名单（保持首次出现序）：pipeline all 模式名单
// 展开消费。翻页超上限原样上抛 ErrStaffPageExceeded。
func FetchAllStaff(ctx context.Context, p ListStaffPager, secret string) ([]string, error) {
	seen := make(map[string]struct{})
	names := make([]string, 0)
	err := WalkStaffPages(ctx, p, secret, "", func(page []Staff) error {
		for i := range page {
			if _, ok := seen[page[i].StaffName]; ok {
				continue
			}
			seen[page[i].StaffName] = struct{}{}
			names = append(names, page[i].StaffName)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}
