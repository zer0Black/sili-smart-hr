// 评估区间下拉（specs P2_PRF_001 §4.2.3：区间新到旧、首项本期标注，切换回调 PeriodRange）。
// periods 由后端按落库周期推导排序，组件按 is_current 标注「本期」不自行推断。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { ProfilePeriod, ProfilePeriodRange } from '@/lib/contracts';

/** 区间 value 编码：start~end。日期为 yyyy-MM-dd 无波浪号冲突。 */
function periodValue(p: ProfilePeriodRange): string {
  return `${p.period_start}~${p.period_end}`;
}

export function PeriodSelect(props: {
  periods: ProfilePeriod[];
  selected: ProfilePeriodRange | null;
  onChange: (period: ProfilePeriodRange) => void;
}): JSX.Element {
  const { t } = useTranslation('profile');
  const { periods, selected, onChange } = props;
  const value = selected ? periodValue(selected) : undefined;
  return (
    <Select
      value={value}
      onValueChange={(v) => {
        const hit = periods.find((p) => periodValue(p) === v);
        if (hit) onChange({ period_start: hit.period_start, period_end: hit.period_end });
      }}
    >
      <SelectTrigger className="w-64" aria-label={t('periodSelect.label')}>
        <SelectValue placeholder={t('periodSelect.placeholder')} />
      </SelectTrigger>
      <SelectContent>
        {periods.map((p) => (
          <SelectItem key={periodValue(p)} value={periodValue(p)}>
            {p.period_start} ~ {p.period_end}
            {p.is_current ? `（${t('periodSelect.current')}）` : ''}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
