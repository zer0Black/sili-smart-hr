// 作答页失效态卡片（specs §4.2 全节）：统一文案纯呈现，无任何操作入口。
import { useTranslation } from 'react-i18next';
import { TriangleAlert } from 'lucide-react';
import type { JSX } from 'react';

import { Card, CardContent } from '@/components/ui/card';

export function AnswerInvalidCard(): JSX.Element {
  const { t } = useTranslation('answer');

  return (
    <Card className="mx-auto w-full max-w-xl py-12">
      <CardContent className="flex flex-col items-center gap-3 text-center">
        <div className="flex size-14 items-center justify-center rounded-full bg-destructive/10">
          <TriangleAlert className="text-destructive size-7" aria-hidden />
        </div>
        <h1 className="text-lg font-semibold">{t('invalid.title')}</h1>
        <p className="text-muted-foreground max-w-md text-sm leading-relaxed">{t('invalid.desc')}</p>
      </CardContent>
    </Card>
  );
}
