// 空态插画（specs §4.1.5）：几何色块组合，DESIGN.md 空状态可用 block-* 粉彩点缀。
// 题库列表与生成页表单空态共用。
import type { JSX } from 'react';

import type { QuestionTab } from '@/features/question-bank/types';

export function EmptyStateArt({ tab }: { tab: QuestionTab }): JSX.Element {
  // AI 侧偏紫（生成/智能），量表侧偏薄荷（量表/校准），色块高度错落喻题库累积
  const accent = tab === 'AI' ? 'bg-block-lilac' : 'bg-block-mint';
  const others = 'bg-block-cream';
  return (
    <div aria-hidden className="flex items-end gap-1.5">
      <span className={`${others} h-6 w-4 rounded-sm`} />
      <span className={`${accent} h-10 w-4 rounded-sm`} />
      <span className={`${others} h-8 w-4 rounded-sm`} />
      <span className={`${accent} h-14 w-4 rounded-sm`} />
      <span className={`${others} h-5 w-4 rounded-sm`} />
    </div>
  );
}
