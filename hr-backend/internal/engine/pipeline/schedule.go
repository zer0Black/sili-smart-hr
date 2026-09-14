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

// triggerGraceWindow 触发命中宽限窗：worker 队列积压时 tick 任务消费晚于
// 理论触发点几分钟仍应命中（spec v1.9 技术修正），窗宽取 2 分钟覆盖分钟级
// 重试半径；同窗重复建批由本周期已建批判定拦截。
const triggerGraceWindow = 2 * time.Minute

// TriggerHit 判定触发时刻是否命中：now 落在理论触发点 [触发点, 触发点+宽限窗)
// 内且当日为触发日（daily→每日、weekly→周日、monthly→当月最后一天）。
// triggerTime 为 HH:mm 字符串。
func TriggerHit(now time.Time, period, triggerTime string) bool {
	hh, mm, err := parseTriggerTime(triggerTime)
	if err != nil {
		return false
	}
	n := now.Local()
	if !isTriggerDay(n, period) {
		return false
	}
	fired := time.Date(n.Year(), n.Month(), n.Day(), hh, mm, 0, 0, time.Local)
	return !n.Before(fired) && n.Before(fired.Add(triggerGraceWindow))
}

// PrevTriggerAt 推算最近一个已过去的触发点（本期起点）：与 NextTriggerAt 同构
// 往回推一个周期。触发点当日已过当次（含宽限窗内）取当日触发点，否则取上一周期的。
func PrevTriggerAt(now time.Time, period, triggerTime string) (time.Time, error) {
	hh, mm, err := parseTriggerTime(triggerTime)
	if err != nil {
		return time.Time{}, err
	}
	n := now.Local()
	var last time.Time
	switch period {
	case periodDaily:
		last = time.Date(n.Year(), n.Month(), n.Day(), hh, mm, 0, 0, time.Local)
	case periodWeekly:
		last = prevWeekdayOnOrBefore(n, time.Sunday, hh, mm)
	case periodMonthly:
		last = monthEndAt(n, hh, mm)
	default:
		return time.Time{}, fmt.Errorf("未知周期: %q", period)
	}
	if last.After(n) {
		return prevPeriodTrigger(last, period, hh, mm), nil
	}
	return last, nil
}

// prevPeriodTrigger 由触发点回退一个周期的同位触发点（weekly 减 7 天，
// monthly 锚定月初回退避免 AddDate 月末溢出，daily 减 1 天）。
func prevPeriodTrigger(at time.Time, period string, hh, mm int) time.Time {
	switch period {
	case periodDaily:
		return at.AddDate(0, 0, -1)
	case periodMonthly:
		return monthEndAt(time.Date(at.Year(), at.Month(), 1, hh, mm, 0, 0, time.Local).AddDate(0, 0, -1), hh, mm)
	default: // weekly
		return at.AddDate(0, 0, -7)
	}
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
// monthly 须防 AddDate 月末溢出：1/31 锚点直接加月归一化到 3/3，越过后月真实
// 触发点（2/29），卡死批次阻塞下月触发；取加月结果与后月月末同刻的较早者钳位，
// 后月从 triggeredAt 所在月的次月初推（naive 溢出后所在月已失真，不可作锚）。
func StalledDeadline(triggeredAt time.Time, period string) time.Time {
	switch period {
	case periodDaily:
		return triggeredAt.AddDate(0, 0, 1)
	case periodMonthly:
		naive := triggeredAt.AddDate(0, 1, 0)
		hh, mm, ss := triggeredAt.Clock()
		// 后月（M+1）月末同刻：M+2 月初回退一天（月内任意锚推 M+2 月初都不溢出）。
		firstOfDeadlineMonth := time.Date(triggeredAt.Year(), triggeredAt.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0)
		lastOfDeadlineMonth := firstOfDeadlineMonth.AddDate(0, 1, 0).AddDate(0, 0, -1)
		monthEnd := time.Date(lastOfDeadlineMonth.Year(), lastOfDeadlineMonth.Month(), lastOfDeadlineMonth.Day(), hh, mm, ss, 0, time.Local)
		if monthEnd.Before(naive) {
			return monthEnd
		}
		return naive
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

// prevWeekdayOnOrBefore 返回 n 当日或之前最近一个目标星期几的 hh:mm。
func prevWeekdayOnOrBefore(n time.Time, weekday time.Weekday, hh, mm int) time.Time {
	offset := (int(n.Weekday()) - int(weekday) + 7) % 7
	d := n.AddDate(0, 0, -offset)
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, time.Local)
}
