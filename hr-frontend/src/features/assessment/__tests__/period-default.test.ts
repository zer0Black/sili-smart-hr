import { describe, it, expect } from 'vitest';

import { previousPeriodRange } from '../period-default';

describe('previousPeriodRange（specs §4.2.4 规则2 上一完整周期默认窗口）', () => {
  // 锚点用例：2026-09-12 周六
  it('daily → 昨日（含起止同日）', () => {
    expect(previousPeriodRange(new Date('2026-09-12T10:00:00'), 'daily')).toEqual({
      start: '2026-09-11',
      end: '2026-09-11',
    });
  });

  it('weekly → 上周一至上周日（周一为一周起点）', () => {
    expect(previousPeriodRange(new Date('2026-09-12T10:00:00'), 'weekly')).toEqual({
      start: '2026-08-31',
      end: '2026-09-06',
    });
  });

  it('monthly → 上月 1 日至上月月末', () => {
    expect(previousPeriodRange(new Date('2026-09-12T10:00:00'), 'monthly')).toEqual({
      start: '2026-08-01',
      end: '2026-08-31',
    });
  });

  // 锚点边界：闰年 2 月（2026-03-15 monthly → 2026-02-01 ~ 2026-02-28，2026 非闰年）
  it('monthly 边界：3 月回看 2 月，止日取上月月末', () => {
    expect(previousPeriodRange(new Date('2026-03-15T00:00:00'), 'monthly')).toEqual({
      start: '2026-02-01',
      end: '2026-02-28',
    });
  });

  // 补充边界
  it('daily 跨月：月初昨日落入上月', () => {
    expect(previousPeriodRange(new Date('2026-09-01T08:00:00'), 'daily')).toEqual({
      start: '2026-08-31',
      end: '2026-08-31',
    });
  });

  it('daily 跨年：1 月 1 日昨日为上年 12 月 31 日', () => {
    expect(previousPeriodRange(new Date('2026-01-01T12:00:00'), 'daily')).toEqual({
      start: '2025-12-31',
      end: '2025-12-31',
    });
  });

  it('weekly 周一当天回看上周一至上周日', () => {
    // 2026-09-07 是周一，上一周应为 2026-08-31 ~ 2026-09-06
    expect(previousPeriodRange(new Date('2026-09-07T09:00:00'), 'weekly')).toEqual({
      start: '2026-08-31',
      end: '2026-09-06',
    });
  });

  it('weekly 周日当天回看上周一至上周日（不把本周开始日错当上周）', () => {
    // 2026-09-13 是周日，上一周仍为 2026-08-31 ~ 2026-09-06
    expect(previousPeriodRange(new Date('2026-09-13T09:00:00'), 'weekly')).toEqual({
      start: '2026-08-31',
      end: '2026-09-06',
    });
  });

  it('weekly 跨年：1 月初回看上年末一周', () => {
    // 2026-01-01 是周四，所在周周一 2025-12-29，上一周 2025-12-22 ~ 2025-12-28
    expect(previousPeriodRange(new Date('2026-01-01T00:00:00'), 'weekly')).toEqual({
      start: '2025-12-22',
      end: '2025-12-28',
    });
  });

  it('monthly 跨年：1 月回看上年 12 月', () => {
    expect(previousPeriodRange(new Date('2026-01-10T00:00:00'), 'monthly')).toEqual({
      start: '2025-12-01',
      end: '2025-12-31',
    });
  });

  it('monthly 闰年：2024 年 3 月回看 2024 年 2 月 29 日', () => {
    expect(previousPeriodRange(new Date('2024-03-01T00:00:00'), 'monthly')).toEqual({
      start: '2024-02-01',
      end: '2024-02-29',
    });
  });

  // 月末锚点：dayjs subtract(1,'month') 是前滚归一（5/31-1月=5/1），须先 startOf
  // 消偏移，否则默认窗口落到本月（未来），命中 periodFuture 校验死锁。
  it('monthly 月末日期不前滚：5/31 回看 4 月整月而非 5 月', () => {
    expect(previousPeriodRange(new Date('2026-05-31T10:00:00'), 'monthly')).toEqual({
      start: '2026-04-01',
      end: '2026-04-30',
    });
  });

  it('monthly 月末边界：3/31、7/31、12/31 均回看上月整月', () => {
    for (const [now, want] of [
      ['2026-03-31T23:59:59', { start: '2026-02-01', end: '2026-02-28' }],
      ['2026-07-31T00:00:00', { start: '2026-06-01', end: '2026-06-30' }],
      ['2026-12-31T12:00:00', { start: '2026-11-01', end: '2026-11-30' }],
    ] as const) {
      expect(previousPeriodRange(new Date(now), 'monthly')).toEqual(want);
    }
  });

  it('返回值为 yyyy-MM-dd 双值且含止日', () => {
    const { start, end } = previousPeriodRange(new Date('2026-09-12T23:59:59'), 'daily');
    expect(start).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    expect(end).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});
