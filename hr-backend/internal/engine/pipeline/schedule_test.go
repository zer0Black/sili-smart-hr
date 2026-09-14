package pipeline

// schedule 纯函数测试。固定基准日期 2026-09-12 为周六，2026-09-13 为周日，2026-09-30 为 9 月最后一天。
// 全部用 time.Local 构造时刻，与实现取 now.Local() 的日历语义一致，测试结果不随运行环境时区变化。

import (
	"testing"
	"time"
)

// local 构造本地时区时刻，测试表达更紧凑。
func local(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.Local)
}

func TestTriggerHit(t *testing.T) {
	tests := []struct {
		name        string
		now         time.Time
		period      string
		triggerTime string
		want        bool
	}{
		{"分钟不匹配不命中", local(2026, 9, 13, 23, 5), "weekly", "23:00", false},
		{"周日且时刻匹配命中", local(2026, 9, 13, 23, 0), "weekly", "23:00", true},
		{"周六非周触发日不命中", local(2026, 9, 12, 23, 0), "weekly", "23:00", false},
		{"月末且时刻匹配命中", local(2026, 9, 30, 23, 0), "monthly", "23:00", true},
		{"月内非末日不命中", local(2026, 9, 12, 23, 0), "monthly", "23:00", false},
		{"每日任意日时刻匹配命中", local(2026, 10, 31, 23, 0), "daily", "23:00", true},
		{"每日时刻不匹配不命中", local(2026, 10, 31, 8, 0), "daily", "23:00", false},
		{"triggerTime 非法不命中", local(2026, 9, 13, 23, 0), "weekly", "25:00", false},
		{"未知 period 不命中", local(2026, 9, 13, 23, 0), "quarterly", "23:00", false},
		{"闰年 2 月末日命中", local(2024, 2, 29, 23, 0), "monthly", "23:00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TriggerHit(tt.now, tt.period, tt.triggerTime); got != tt.want {
				t.Errorf("TriggerHit(%v, %q, %q) = %v, want %v", tt.now, tt.period, tt.triggerTime, got, tt.want)
			}
		})
	}
}

func TestCurrentPeriodWindow(t *testing.T) {
	tests := []struct {
		name      string
		now       time.Time
		period    string
		wantStart time.Time
		wantEnd   time.Time
	}{
		{"daily 当日", local(2026, 9, 12, 10, 30), "daily", local(2026, 9, 12, 0, 0), local(2026, 9, 13, 0, 0)},
		{"weekly 周六归本周一至下周一", local(2026, 9, 12, 23, 59), "weekly", local(2026, 9, 7, 0, 0), local(2026, 9, 14, 0, 0)},
		{"weekly 周日仍归同一周", local(2026, 9, 13, 0, 0), "weekly", local(2026, 9, 7, 0, 0), local(2026, 9, 14, 0, 0)},
		{"weekly 周一起始", local(2026, 9, 7, 0, 0), "weekly", local(2026, 9, 7, 0, 0), local(2026, 9, 14, 0, 0)},
		{"monthly 月中", local(2026, 9, 12, 10, 0), "monthly", local(2026, 9, 1, 0, 0), local(2026, 10, 1, 0, 0)},
		{"monthly 月末日", local(2026, 9, 30, 23, 59), "monthly", local(2026, 9, 1, 0, 0), local(2026, 10, 1, 0, 0)},
		{"monthly 跨年 12 月", local(2026, 12, 15, 0, 0), "monthly", local(2026, 12, 1, 0, 0), local(2027, 1, 1, 0, 0)},
		{"daily 跨零点后归新一日", local(2026, 9, 13, 0, 0), "daily", local(2026, 9, 13, 0, 0), local(2026, 9, 14, 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := CurrentPeriodWindow(tt.now, tt.period)
			if got := time.Unix(start, 0).In(time.Local); !got.Equal(tt.wantStart) {
				t.Errorf("start = %v, want %v", got, tt.wantStart)
			}
			if got := time.Unix(end, 0).In(time.Local); !got.Equal(tt.wantEnd) {
				t.Errorf("end = %v, want %v", got, tt.wantEnd)
			}
			if start >= end {
				t.Errorf("窗口应左闭右开非空: start=%d end=%d", start, end)
			}
		})
	}
}

func TestNextTriggerAt(t *testing.T) {
	tests := []struct {
		name        string
		now         time.Time
		period      string
		triggerTime string
		want        time.Time
	}{
		{"weekly 周六白天指到本周日", local(2026, 9, 12, 10, 0), "weekly", "23:00", local(2026, 9, 13, 23, 0)},
		{"weekly 周六深夜未过本周日当次不顺延", local(2026, 9, 12, 23, 30), "weekly", "23:00", local(2026, 9, 13, 23, 0)},
		{"weekly 周日触发时刻已过顺延一周", local(2026, 9, 13, 23, 30), "weekly", "23:00", local(2026, 9, 20, 23, 0)},
		{"daily 当日触发时刻未过指到当日", local(2026, 9, 12, 10, 0), "daily", "23:00", local(2026, 9, 12, 23, 0)},
		{"daily 当日触发时刻已过顺延次日", local(2026, 9, 12, 23, 30), "daily", "23:00", local(2026, 9, 13, 23, 0)},
		{"monthly 月初指到本月末日", local(2026, 9, 5, 10, 0), "monthly", "23:00", local(2026, 9, 30, 23, 0)},
		{"monthly 月末触发已过顺延下月末日", local(2026, 9, 30, 23, 30), "monthly", "23:00", local(2026, 10, 31, 23, 0)},
		{"monthly 1 月触发已过顺延 2 月闰日", local(2024, 1, 31, 23, 30), "monthly", "23:00", local(2024, 2, 29, 23, 0)},
		{"monthly 12 月跨年顺延", local(2026, 12, 31, 23, 30), "monthly", "23:00", local(2027, 1, 31, 23, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextTriggerAt(tt.now, tt.period, tt.triggerTime)
			if err != nil {
				t.Fatalf("NextTriggerAt 返回意外错误: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("NextTriggerAt(%v, %q, %q) = %v, want %v", tt.now, tt.period, tt.triggerTime, got, tt.want)
			}
		})
	}
}

func TestNextTriggerAtInvalid(t *testing.T) {
	tests := []struct {
		name        string
		now         time.Time
		period      string
		triggerTime string
	}{
		{"triggerTime 非法格式", local(2026, 9, 12, 10, 0), "weekly", "25:00"},
		{"triggerTime 非 HH:mm", local(2026, 9, 12, 10, 0), "weekly", "23点"},
		{"triggerTime 空串", local(2026, 9, 12, 10, 0), "weekly", ""},
		{"未知 period", local(2026, 9, 12, 10, 0), "quarterly", "23:00"},
		{"period 空串", local(2026, 9, 12, 10, 0), "", "23:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NextTriggerAt(tt.now, tt.period, tt.triggerTime); err == nil {
				t.Errorf("NextTriggerAt(%v, %q, %q) 应返回 error", tt.now, tt.period, tt.triggerTime)
			}
		})
	}
}

func TestStalledDeadline(t *testing.T) {
	base := local(2026, 9, 13, 23, 0)
	tests := []struct {
		name   string
		period string
		want   time.Time
	}{
		{"daily 加 1 天", "daily", base.AddDate(0, 0, 1)},
		{"weekly 加 7 天", "weekly", base.AddDate(0, 0, 7)},
		{"monthly 加 1 日历月", "monthly", base.AddDate(0, 1, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StalledDeadline(base, tt.period); !got.Equal(tt.want) {
				t.Errorf("StalledDeadline(%v, %q) = %v, want %v", base, tt.period, got, tt.want)
			}
		})
	}
}

func TestIsStalled(t *testing.T) {
	triggered := local(2026, 9, 13, 23, 0)
	tests := []struct {
		name        string
		now         time.Time
		triggeredAt time.Time
		period      string
		status      string
		want        bool
	}{
		{"weekly running 超过 7 天判停滞", local(2026, 9, 20, 23, 1), triggered, "weekly", "running", true},
		{"weekly success 超期不判停滞", local(2026, 9, 20, 23, 1), triggered, "weekly", "success", false},
		{"weekly running 未超期不停滞", local(2026, 9, 14, 0, 0), triggered, "weekly", "running", false},
		{"weekly running 恰在边界未超过不停滞", local(2026, 9, 20, 23, 0), triggered, "weekly", "running", false},
		{"daily running 超过 1 天判停滞", local(2026, 9, 14, 23, 1), triggered, "daily", "running", true},
		{"monthly running 超过 1 日历月判停滞", local(2026, 10, 13, 23, 1), triggered, "monthly", "running", true},
		{"monthly running 未过 1 日历月不停滞", local(2026, 10, 13, 23, 0), triggered, "monthly", "running", false},
		{"failed 状态超期不判停滞", local(2026, 9, 20, 23, 1), triggered, "weekly", "failed", false},
		{"partial_failed 状态超期不判停滞", local(2026, 9, 20, 23, 1), triggered, "weekly", "partial_failed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStalled(tt.now, tt.triggeredAt, tt.period, tt.status); got != tt.want {
				t.Errorf("IsStalled(%v, %v, %q, %q) = %v, want %v", tt.now, tt.triggeredAt, tt.period, tt.status, got, tt.want)
			}
		})
	}
}

// TestTriggerHitGraceWindow 宽限窗（spec v1.9）：触发点后 2 分钟内仍命中，
// 超窗不命中；worker 队列积压导致 tick 消费晚几分钟不丢整周期触发。
func TestTriggerHitGraceWindow(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"触发点后 1 分钟命中", local(2026, 9, 13, 23, 1), true},
		{"触发点后 2 分钟边界不命中", local(2026, 9, 13, 23, 2), false},
		{"触发点后 10 分钟不命中", local(2026, 9, 13, 23, 10), false},
		{"触发点前 1 分钟不命中", local(2026, 9, 13, 22, 59), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TriggerHit(tt.now, "weekly", "23:00"); got != tt.want {
				t.Errorf("TriggerHit(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

// TestStalledDeadlineMonthEndClamp 月末钳位：1/31 触发的停滞边界钳到 2/28 同刻，
// 不越过下月真实触发点（AddDate 直接加月会溢出到 3/3 阻塞下月触发）。
func TestStalledDeadlineMonthEndClamp(t *testing.T) {
	triggered := local(2024, 1, 31, 23, 0) // monthly 触发日恒为月末
	got := StalledDeadline(triggered, "monthly")
	want := local(2024, 2, 29, 23, 0) // 闰年 2 月末
	if !got.Equal(want) {
		t.Errorf("StalledDeadline(1/31) = %v, want %v（钳位到下月月末）", got, want)
	}
	// 钳位后：2/29 23:01 已判停滞，下月（2/29 触发）建批不被阻塞。
	if !IsStalled(local(2024, 2, 29, 23, 1), triggered, "monthly", "running") {
		t.Error("钳位边界次日应判停滞放行下月触发")
	}
}

// TestPrevTriggerAt 本期触发点推算：与 NextTriggerAt 关于 now 对称，
// monthly 月末锚定不溢出。
func TestPrevTriggerAt(t *testing.T) {
	tests := []struct {
		name        string
		now         time.Time
		period      string
		triggerTime string
		want        time.Time
	}{
		{"weekly 周六指本周日 23:00", local(2026, 9, 12, 10, 0), "weekly", "23:00", local(2026, 9, 6, 23, 0)},
		{"weekly 周日宽限窗内仍取本周日", local(2026, 9, 13, 23, 1), "weekly", "23:00", local(2026, 9, 13, 23, 0)},
		{"daily 未到当日触发点取昨日", local(2026, 9, 12, 10, 0), "daily", "23:00", local(2026, 9, 11, 23, 0)},
		{"monthly 月中指上月月末", local(2026, 9, 15, 10, 0), "monthly", "23:00", local(2026, 8, 31, 23, 0)},
		{"monthly 闰年 3 月指 2/29", local(2024, 3, 15, 10, 0), "monthly", "23:00", local(2024, 2, 29, 23, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PrevTriggerAt(tt.now, tt.period, tt.triggerTime)
			if err != nil {
				t.Fatalf("PrevTriggerAt: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("PrevTriggerAt(%v, %q, %q) = %v, want %v", tt.now, tt.period, tt.triggerTime, got, tt.want)
			}
		})
	}
}
