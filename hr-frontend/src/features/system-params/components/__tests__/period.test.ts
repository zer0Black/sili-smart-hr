import { describe, it, expect } from 'vitest';
import { triggerDayOf, intervalDescOf, estimateCount } from '../../period';

describe('triggerDayOf（§4.1.4 规则3 触发日联动）', () => {
  it("daily → 每日 i18n key", () => {
    expect(triggerDayOf('daily')).toBe('triggerDay.daily');
  });
  it("weekly → 每周日 i18n key", () => {
    expect(triggerDayOf('weekly')).toBe('triggerDay.weekly');
  });
  it("monthly → 每月最后一天 i18n key", () => {
    expect(triggerDayOf('monthly')).toBe('triggerDay.monthly');
  });
  it('三种周期返回值互不相同', () => {
    const keys = new Set([
      triggerDayOf('daily'),
      triggerDayOf('weekly'),
      triggerDayOf('monthly'),
    ]);
    expect(keys.size).toBe(3);
  });
});

describe('intervalDescOf（§4.1.4 规则3 区间说明联动）', () => {
  it("daily → 抽取当日 i18n key", () => {
    expect(intervalDescOf('daily')).toBe('interval.daily');
  });
  it("weekly → 周一至周日 i18n key（区间不重叠不遗漏）", () => {
    expect(intervalDescOf('weekly')).toBe('interval.weekly');
  });
  it("monthly → 1日至月末 i18n key", () => {
    expect(intervalDescOf('monthly')).toBe('interval.monthly');
  });
  it('三种周期返回值互不相同', () => {
    const keys = new Set([
      intervalDescOf('daily'),
      intervalDescOf('weekly'),
      intervalDescOf('monthly'),
    ]);
    expect(keys.size).toBe(3);
  });
});

describe('estimateCount（§4.1.5 成本节流告示）', () => {
  it("'all' → 全员提示 i18n key", () => {
    expect(estimateCount('all')).toBe('estimate.all');
  });
  it('50 → 含人数估算 i18n key', () => {
    expect(estimateCount(50)).toBe('estimate.count');
  });
  it("'all' 与数字返不同 key", () => {
    expect(estimateCount('all')).not.toBe(estimateCount(50));
  });
  it('0 → 走含人数 key 分支（边界：零值不误判为全员）', () => {
    expect(estimateCount(0)).toBe('estimate.count');
  });
  it('大人数（极值）仍走含人数 key 分支', () => {
    expect(estimateCount(100000)).toBe('estimate.count');
  });
});
