package pipeline

import (
	"fmt"
	"time"
)

// 合法周期取值集合，与 specs §5.1.4 及 03 上游契约一致。
const (
	periodDaily   = "daily"
	periodWeekly  = "weekly"
	periodMonthly = "monthly"
)

// TriggerHit 判定触发时刻是否命中：now 的 HH:mm 等于 triggerTime 且今日为触发日
// （daily→每日、weekly→周日、monthly→当月最后一天）。triggerTime 为 HH:mm 字符串。
func TriggerHit(now time.Time, period, triggerTime string) bool {
	hh, mm, err := parseTriggerTime(triggerTime)
	if err != nil {
		return false
	}
	n := now.Local()
	if n.Hour() != hh || n.Minute() != mm {
		return false
	}
	return isTriggerDay(n, period)
}

// CurrentPeriodWindow 推算当前周期窗口（specs §5.1.4 规则1）：
// daily→[当日00:00, 次日00:00)、weekly→[本周一00:00, 下周一00:00)、
// monthly→[本月1日00:00, 下月1日00:00)。返回 Unix 秒（左闭右开）。
func CurrentPeriodWindow(now time.Time, period string) (start, end int64) {
	n := now.Local()
	switch period {
	case periodDaily:
		s := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
		e := s.AddDate(0, 0, 1)
		return s.Unix(), e.Unix()
	case periodWeekly:
		s := weekStart(n)
		e := s.AddDate(0, 0, 7)
		return s.Unix(), e.Unix()
	default: // monthly，未知 period 由 TriggerHit/NextTriggerAt 侧拒绝
		s := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.Local)
		e := s.AddDate(0, 1, 0)
		return s.Unix(), e.Unix()
	}
}

// NextTriggerAt 推算下次执行时点（计划卡，03 A3）：由 period + triggerTime 推算，
// 已过当次则顺延一个周期。返回格式化前的 time.Time（展示格式化归调用方）。
func NextTriggerAt(now time.Time, period, triggerTime string) (time.Time, error) {
	hh, mm, err := parseTriggerTime(triggerTime)
	if err != nil {
		return time.Time{}, err
	}
	n := now.Local()
	var first time.Time
	switch period {
	case periodDaily:
		first = time.Date(n.Year(), n.Month(), n.Day(), hh, mm, 0, 0, time.Local)
	case periodWeekly:
		first = nextWeekdayOnOrAfter(n, time.Sunday, hh, mm)
	case periodMonthly:
		first = monthEndAt(n, hh, mm)
	default:
		return time.Time{}, fmt.Errorf("未知周期: %q", period)
	}
	if first.After(n) {
		return first, nil
	}
	// 已过当次，顺延一个周期（BR3 §4.1.2B）
	switch period {
	case periodDaily:
		return first.AddDate(0, 0, 1), nil
	case periodWeekly:
		return first.AddDate(0, 0, 7), nil
	default: // monthly
		// AddDate 对 1/31+1月 会归一化到 3/3，须先锚定月初再推下月月末
		return monthEndAt(time.Date(first.Year(), first.Month(), 1, hh, mm, 0, 0, time.Local).AddDate(0, 1, 0), hh, mm), nil
	}
}

// StalledDeadline 停滞边界（03 §4.5）：daily→+1天、weekly→+7天、monthly→+1日历月。
func StalledDeadline(triggeredAt time.Time, period string) time.Time {
	switch period {
	case periodDaily:
		return triggeredAt.AddDate(0, 0, 1)
	case periodMonthly:
		return triggeredAt.AddDate(0, 1, 0)
	default: // weekly
		return triggeredAt.AddDate(0, 0, 7)
	}
}

// IsStalled 停滞判定：status=running 且 now 超过 StalledDeadline（specs §5.1.4 规则2）。
func IsStalled(now, triggeredAt time.Time, period, status string) bool {
	if status != "running" {
		return false
	}
	return now.After(StalledDeadline(triggeredAt, period))
}

// parseTriggerTime 解析 HH:mm 触发时刻，返回时分。
func parseTriggerTime(triggerTime string) (hh, mm int, err error) {
	t, err := time.Parse("15:04", triggerTime)
	if err != nil {
		return 0, 0, fmt.Errorf("触发时刻格式非法: %q", triggerTime)
	}
	return t.Hour(), t.Minute(), nil
}

// isTriggerDay 判定日期 n 是否为该周期的触发日。
func isTriggerDay(n time.Time, period string) bool {
	switch period {
	case periodDaily:
		return true
	case periodWeekly:
		return n.Weekday() == time.Sunday
	case periodMonthly:
		// 当月最后一天：加一天月份变化即月末
		return n.AddDate(0, 0, 1).Month() != n.Month()
	default:
		return false
	}
}

// weekStart 返回 n 所在周的周一 00:00（本地时区，周一起始）。
func weekStart(n time.Time) time.Time {
	offset := (int(n.Weekday()) - int(time.Monday) + 7) % 7
	d := n.AddDate(0, 0, -offset)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)
}

// monthEndAt 返回 n 所在月最后一天的 hh:mm（本地时区）。
func monthEndAt(n time.Time, hh, mm int) time.Time {
	firstOfNext := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0)
	last := firstOfNext.AddDate(0, 0, -1)
	return time.Date(last.Year(), last.Month(), last.Day(), hh, mm, 0, 0, time.Local)
}

// nextWeekdayOnOrAfter 返回从 n（含当日零点起）最近一个目标星期几的 hh:mm。
func nextWeekdayOnOrAfter(n time.Time, weekday time.Weekday, hh, mm int) time.Time {
	offset := (int(weekday) - int(n.Weekday()) + 7) % 7
	d := n.AddDate(0, 0, offset)
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, time.Local)
}
