// 评估周期联动纯函数：触发日、评估区间、成本节流估算
// 依据 specs §4.1.2（只读字段）、§4.1.4 规则3（周期与触发联动）、§4.1.5（区间说明随周期刷新）
// 返回的 key 相对 systemParams ns（组件 useTranslation('systemParams')），不带 ns 前缀

export type Period = 'daily' | 'weekly' | 'monthly';

// 触发日只读联动：日→每日、周→每周日、月→每月最后一天（§4.1.4 规则3）
export function triggerDayOf(period: Period): string {
  switch (period) {
    case 'daily':
      return 'triggerDay.daily';
    case 'weekly':
      return 'triggerDay.weekly';
    case 'monthly':
      return 'triggerDay.monthly';
  }
}

// 评估区间只读说明：日→抽取当日、周→周一至周日、月→1日至月末（§4.1.4 规则3）
export function intervalDescOf(period: Period): string {
  switch (period) {
    case 'daily':
      return 'interval.daily';
    case 'weekly':
      return 'interval.weekly';
    case 'monthly':
      return 'interval.monthly';
  }
}

// 成本节流估算：'all' 返全员提示 key，数字返含人数估算 key（组件层 t(key, { count }) 插值）
// key 同样相对 systemParams ns
export function estimateCount(memberCount: number | 'all'): string {
  if (memberCount === 'all') {
    return 'estimate.all';
  }
  return 'estimate.count';
}
