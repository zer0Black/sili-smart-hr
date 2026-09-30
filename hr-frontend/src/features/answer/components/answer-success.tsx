// 作答页成功态卡片（specs §4.3 全节）：结果说明、任务号（等宽）、关闭窗口与手动关闭兜底。
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CircleCheck } from 'lucide-react';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import type { AnswerTestType } from '../answer-types';

/** window.close 仅对脚本打开的窗口生效；300ms 后仍未关闭提示手动关闭（specs §4.1.5）。 */
const CLOSE_FALLBACK_DELAY_MS = 300;

export function AnswerSuccessCard({
  testType,
  taskNo,
}: {
  testType: AnswerTestType;
  taskNo: string;
}): JSX.Element {
  const { t } = useTranslation('answer');
  const [fallbackShown, setFallbackShown] = useState(false);
  // 成功副标题按类型取与 hero 同源的标题文案。
  const title = t(testType === 'enneagram' ? 'meta.enne.title' : 'meta.ai.title');

  const handleClose = () => {
    // specs §4.3.3：window.close() 关闭标签页（纯浏览器行为，仅对脚本打开的窗口生效）。
    window.close();
    window.setTimeout(() => setFallbackShown(true), CLOSE_FALLBACK_DELAY_MS);
  };

  return (
    <Card className="mx-auto w-full max-w-xl py-10">
      <CardContent className="flex flex-col items-center gap-3 text-center">
        <CircleCheck className="text-success size-16" aria-hidden />
        <h1 className="text-lg font-semibold">{t('success.title')}</h1>
        <p className="text-muted-foreground text-sm">
          {t('success.subtitle', { title })}
        </p>
        <div className="text-muted-foreground mt-1 max-w-[480px] rounded-lg border border-success/20 bg-success/5 px-4 py-3.5 text-left text-[13px] leading-relaxed">
          {t('success.tip')}
        </div>
        <Button className="mt-3 px-8" onClick={handleClose}>
          {t('success.close')}
        </Button>
        {fallbackShown && (
          <p className="text-muted-foreground mt-2 text-xs">{t('success.closeFallback')}</p>
        )}
        <p className="text-muted-foreground mt-4 text-xs">
          {t('success.refPrefix')}
          <span className="font-mono">{taskNo}</span>
        </p>
      </CardContent>
    </Card>
  );
}
