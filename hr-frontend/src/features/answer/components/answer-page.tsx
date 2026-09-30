// 作答页三态编排（specs §3.1/§6.1：loading/answering/invalid/error/success 为呈现分支）。
// 品牌头与 max-w-860 居中容器在顶层，三态共享（specs §3.1 页面形式说明：无导航、无 demo 切换器）。
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { LoaderCircle, RefreshCw } from 'lucide-react';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { ErrCode } from '@/lib/contracts';
import { AnswerApiError } from '../answer-api';
import type { AnswerContextResult } from '../answer-types';
import { useAnswerContext } from '../answer-hooks';
import { progressPercent } from '../script';
import { AnswerChat } from './answer-chat';
import { AnswerHero } from './answer-hero';
import { AnswerInvalidCard } from './answer-invalid';
import { AnswerSuccessCard } from './answer-success';

/** 提交成功的置位标记：success 态在 ctx 数据之外唯一需记忆的事实（task_no 同源于 ctx）。 */
type AnswerPageInnerState = { submitted: boolean; forceInvalid: boolean };

export function AnswerPage({ token }: { token: string }): JSX.Element {
  const { t } = useTranslation('answer');
  const ctxQ = useAnswerContext(token);
  const [inner, setInner] = useState<AnswerPageInnerState>({ submitted: false, forceInvalid: false });

  // 提交成功后滚动顶部（specs §4.1.5/§4.3.5）。
  const phase = derivePhase(ctxQ, inner);
  useEffect(() => {
    if (phase === 'success') {
      window.scrollTo?.(0, 0);
    }
  }, [phase]);

  const onRetry = () => void ctxQ.refetch();

  return (
    <div className="bg-muted/40 min-h-svh">
      <BrandHeader />
      <main className="mx-auto w-full max-w-[860px] px-4 py-8 sm:px-6">
        {phase === 'loading' && (
          <div className="flex flex-col items-center gap-3 py-24 text-center" role="status">
            <LoaderCircle className="text-muted-foreground size-8 animate-spin" aria-hidden />
            <p className="text-muted-foreground text-sm">{t('common:loading')}</p>
          </div>
        )}
        {phase === 'invalid' && <AnswerInvalidCard />}
        {phase === 'error' && (
          <Card className="mx-auto w-full max-w-xl py-12">
            <CardContent className="flex flex-col items-center gap-4 text-center">
              <p className="text-muted-foreground text-sm">{t('error.loadFailed')}</p>
              <Button variant="outline" disabled={ctxQ.isFetching} onClick={onRetry}>
                <RefreshCw aria-hidden />
                {t('error.retry')}
              </Button>
            </CardContent>
          </Card>
        )}
        {phase === 'answering' && ctxQ.data && (
          <AnsweringState
            token={token}
            ctx={ctxQ.data}
            onInvalid={() => setInner((s) => ({ ...s, forceInvalid: true }))}
            onSubmitSuccess={() => setInner((s) => ({ ...s, submitted: true }))}
          />
        )}
        {phase === 'success' && ctxQ.data && (
          <AnswerSuccessCard testType={ctxQ.data.test_type} taskNo={ctxQ.data.task_no} />
        )}
      </main>
    </div>
  );
}

/** 内部状态机：error 为加载失败（可重试），invalid 为令牌失效终态（specs §5.1.5 前端面）。 */
type Phase = 'loading' | 'answering' | 'invalid' | 'error' | 'success';

/** phase 单向派生：query 状态（isPending/error/data）不镜像进 state，真状态只有
 *  submitted（提交成功）与 forceInvalid（作答中令牌失效）两枚置位标记。 */
function derivePhase(
  ctxQ: ReturnType<typeof useAnswerContext>,
  inner: AnswerPageInnerState,
): Phase {
  if (inner.submitted && ctxQ.data) return 'success';
  if (inner.forceInvalid) return 'invalid';
  if (ctxQ.isPending) return 'loading';
  if (ctxQ.error) {
    // AnswerApiError code=1901 转 invalid（统一失效文案）；其余（含通道级 code=0）转 error 可重试。
    return ctxQ.error instanceof AnswerApiError && ctxQ.error.code === ErrCode.AnswerTokenInvalid
      ? 'invalid'
      : 'error';
  }
  return 'answering';
}

/** 品牌头（specs §3.3 线框）：logo 方块 S + sili-smart-hr + 副标题，无导航。品牌名双语一致不进语言包。 */
function BrandHeader() {
  const { t } = useTranslation('answer');
  return (
    <header className="bg-background sticky top-0 z-10 flex h-[60px] items-center border-b px-4 sm:px-6">
      <div className="flex items-center gap-3">
        <div className="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-md text-[17px] font-bold">
          S
        </div>
        <div className="text-base font-semibold tracking-wide">sili-smart-hr</div>
        <div className="text-muted-foreground hidden border-l pl-3 text-xs sm:block">
          {t('brand.subtitle')}
        </div>
      </div>
    </header>
  );
}

/** 作答须知条目：t 的 returnObjects 返回宽类型，窄化为 string[]（zh/en 均为数组字面量）。 */
function noticeItems(t: (key: string, opts?: Record<string, unknown>) => string): string[] {
  return t('notice.items', { returnObjects: true }) as unknown as string[];
}

/**
 * 作答态骨架（specs §3.3 作答态线框）：hero + 卡片主体（须知 + 进度 + 对话区）。
 * ctx 由顶层注入（同一份 context 数据驱动进度与对话区，避免二次请求）；
 * 进度计数以 ctx.answered_count 为初值、reply 成功后按服务端回传推进（specs §4.1.2 B 每题确认后更新）。
 */
function AnsweringState({
  token,
  ctx,
  onInvalid,
  onSubmitSuccess,
}: {
  token: string;
  ctx: AnswerContextResult;
  onInvalid: () => void;
  onSubmitSuccess: (taskNo: string) => void;
}): JSX.Element {
  const { t } = useTranslation('answer');
  const [answered, setAnswered] = useState(ctx.answered_count);

  return (
    <div className="shadow-xs overflow-hidden rounded-xl border">
      <AnswerHero testType={ctx.test_type} questionTotal={ctx.question_total} />
      <Card className="gap-0 rounded-none border-0 py-0 shadow-none">
        <CardContent className="flex flex-col gap-6 px-6 py-7 sm:px-8">
          <section>
            <h2 className="text-sm font-semibold">{t('notice.title')}</h2>
            <ul className="text-muted-foreground mt-2 list-disc space-y-1 pl-5 text-[13px] leading-[1.9]">
              {noticeItems(t).map((item, i) => (
                <li key={i}>{item}</li>
              ))}
            </ul>
          </section>
          <section className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between">
              <span className="text-sm font-medium">{t('progress.label')}</span>
              <span className="text-muted-foreground text-[13px]">
                {t('progress.count', { answered, total: ctx.question_total })}
              </span>
            </div>
            <Progress value={answered} total={ctx.question_total} />
          </section>
          <AnswerChat
            token={token}
            ctx={ctx}
            onAnsweredChange={setAnswered}
            onInvalid={onInvalid}
            onSubmitSuccess={onSubmitSuccess}
          />
        </CardContent>
      </Card>
    </div>
  );
}

/** 细进度条：radix Progress 原语的私有薄封装（components/ui 无 progress.tsx，任务内不越界新建公共件）。 */
function Progress({ value, total }: { value: number; total: number }) {
  const percent = progressPercent(value, total);
  return (
    <div
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
      className="bg-muted h-2 w-full overflow-hidden rounded-full"
    >
      <div className="bg-primary h-full rounded-full transition-all" style={{ width: `${percent}%` }} />
    </div>
  );
}
