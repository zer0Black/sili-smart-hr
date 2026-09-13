// 发起评测弹窗的评估时段默认窗口纯函数。
// 依据 specs §4.2.4 规则2：默认回填上一个完整周期窗口，结束日期禁选今天及未来。

import dayjs, { type Dayjs } from 'dayjs';

export type BatchPeriod = 'daily' | 'weekly' | 'monthly';

/** 上一个完整周期窗口（含止日，yyyy-MM-dd 双值）。日→昨日、周→上周一至上周日、月→上月 1 日至月末。 */
export function previousPeriodRange(
  now: Date,
  period: BatchPeriod,
): { start: string; end: string } {
  const d = dayjs(now);
  let start: Dayjs;
  let end: Dayjs;
  switch (period) {
    case 'daily': {
      start = d.subtract(1, 'day').startOf('day');
      end = start;
      break;
    }
    case 'weekly': {
      // 周一起点：dayjs day() 周日=0，换算为周一=0 的偏移后 startOf('day') 定位本周一。
      const mondayOffset = (d.day() + 6) % 7;
      const thisMonday = d.subtract(mondayOffset, 'day').startOf('day');
      start = thisMonday.subtract(7, 'day');
      end = thisMonday.subtract(1, 'day');
      break;
    }
    case 'monthly': {
      const lastMonth = d.subtract(1, 'month');
      start = lastMonth.startOf('month');
      end = lastMonth.endOf('month').startOf('day');
      break;
    }
  }
  return { start: start.format('YYYY-MM-DD'), end: end.format('YYYY-MM-DD') };
}
