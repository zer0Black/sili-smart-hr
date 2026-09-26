// 题目生成页四态容器（specs §4.3.1-§4.3.5 / §4A.3）。表单态：AI_MGMT 启用子能力多选卡
// + 题数（5-30 整数默认 10）；进行态：进度条 + 当前维度，离开需二次确认并取消；
// 完成态：批次号引导前往审核（search 参数携批次 ID）；失败态：error_code 固定文案。
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useBlocker } from '@tanstack/react-router';
import { toast } from 'sonner';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { useDimensionDetail } from '@/features/dimension/hooks';
import { useModuleDimensions } from '@/features/question-bank/dimension-options';
import {
  useCancelGeneration,
  useCreateGeneration,
  useGenerationProgress,
} from '@/features/question-bank/generate-hooks';
import type { DimensionBrief, GenerationProgress } from '@/lib/contracts';
import { cn } from '@/lib/utils';

/** 题数边界（specs §4.3.2：整数 5~30）。 */
const COUNT_DEFAULT = 10;
const COUNT_MIN = 5;
const COUNT_MAX = 30;

/** 页面四态（specs §4A.3）：form=表单，running=生成中，done=完成，failed=失败。 */
type Phase = 'form' | 'running' | 'done' | 'failed';

/** AI_MGMT 模块启用叶子维度（specs §4.3.2：当前启用的 AI 管理子能力）。 */
function useEnabledAiMgmtDimensions(): DimensionBrief[] {
  return useModuleDimensions('AI_MGMT', true);
}

/** 维度一句话说明：树轻量项无 description，逐卡取详情补全（卡片级懒取）。 */
function useDimensionDesc(dim: DimensionBrief): string | undefined {
  const q = useDimensionDetail(dim.id);
  return q.data?.description;
}

function DimensionCard({
  dim,
  selected,
  onToggle,
}: {
  dim: DimensionBrief;
  selected: boolean;
  onToggle: () => void;
}): JSX.Element {
  const { t } = useTranslation('questionBank');
  const desc = useDimensionDesc(dim);
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onToggle}
      className={cn(
        'flex flex-col items-start gap-1.5 rounded-lg border p-4 text-left transition-colors',
        selected ? 'border-primary bg-accent' : 'hover:bg-accent/50',
      )}
    >
      <span className="text-sm font-medium break-words">{dim.name}</span>
      <span className="text-muted-foreground line-clamp-2 text-xs">
        {desc ?? t('generate.descPlaceholder')}
      </span>
    </button>
  );
}

export function GenerateForm(): JSX.Element {
  const { t } = useTranslation('questionBank');
  const navigate = useNavigate();
  const dimensions = useEnabledAiMgmtDimensions();

  const [phase, setPhase] = useState<Phase>('form');
  const [generationId, setGenerationId] = useState<string | null>(null);
  // 选择保留语义（specs §4.3.3）：「再生成一批」/「重新生成」回表单态不清空
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [count, setCount] = useState(COUNT_DEFAULT);
  // 进行态离开二次确认开关（specs §4.3.4 规则 1）
  const [backConfirmOpen, setBackConfirmOpen] = useState(false);

  const createMut = useCreateGeneration();
  const cancelMut = useCancelGeneration();
  const progressQ = useGenerationProgress(phase === 'form' ? null : generationId);
  const progress: GenerationProgress | undefined = progressQ.data;

  // 轮询终态推进四态：COMPLETED→done，FAILED/CANCELED→failed（specs §4.3.5）
  useEffect(() => {
    if (!progress) return;
    if (progress.status === 'COMPLETED') setPhase('done');
    else if (progress.status === 'FAILED' || progress.status === 'CANCELED') setPhase('failed');
  }, [progress?.status]); // eslint-disable-line react-hooks/exhaustive-deps

  const countValid = Number.isInteger(count) && count >= COUNT_MIN && count <= COUNT_MAX;
  const canSubmit = dimensions.length > 0 && selectedIds.length > 0 && countValid;

  function toggleDimension(id: string) {
    setSelectedIds((prev) =>
      prev.includes(id) ? prev.filter((v) => v !== id) : [...prev, id],
    );
  }

  function onReset() {
    setSelectedIds([]);
    setCount(COUNT_DEFAULT);
  }

  function onStart() {
    if (!canSubmit) return;
    createMut.mutate(
      { dimension_ids: selectedIds, count },
      {
        onSuccess: (res) => {
          setGenerationId(res.generation_id);
          setPhase('running');
        },
        onError: () => toast.error(t('toastGeneric')),
      },
    );
  }

  function onBackClick() {
    // 生成进行态离开放弃本批，需二次确认；其余态直接返回（specs §4.3.3 返回题库）
    if (phase === 'running') setBackConfirmOpen(true);
    else void navigate({ to: '/question-bank' });
  }

  // 路由级离开拦截（specs §4.3.4 规则 1「离开即放弃」）：顶栏导航等任意跳转
  // 在 running 态同样弹二次确认，确认取消后放行，取消则留在本页。
  const blocker = useBlocker({
    shouldBlockFn: () => phase === 'running',
    withResolver: true,
  });
  const blockedLeave = blocker.status === 'blocked' ? blocker : null;
  useEffect(() => {
    if (!blockedLeave) return;
    setBackConfirmOpen(true);
  }, [blockedLeave]);

  function onConfirmBack() {
    const proceedLeave = () => {
      setBackConfirmOpen(false);
      if (blockedLeave) blockedLeave.proceed();
      else void navigate({ to: '/question-bank' });
    };
    const id = generationId;
    if (!id) {
      proceedLeave();
      return;
    }
    cancelMut.mutate(id, {
      onSuccess: proceedLeave,
      onError: () => {
        // 取消失败留在本页：关确认框并解除拦截，生成继续走终态推进
        setBackConfirmOpen(false);
        if (blockedLeave) blockedLeave.reset();
        toast.error(t('toastGeneric'));
      },
    });
  }

  function onDismissBack() {
    if (blockedLeave) blockedLeave.reset();
    setBackConfirmOpen(false);
  }

  function onRegenerate() {
    // 回表单态保留上次维度与题数（specs §4.3.3 再生成一批 / 重新生成）
    setGenerationId(null);
    setPhase('form');
  }

  function onGoReview() {
    if (progress?.batch_id) {
      void navigate({ to: '/question-bank', search: { review: progress.batch_id } });
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center gap-3">
        <Button variant="outline" size="sm" onClick={onBackClick}>
          {t('generate.back')}
        </Button>
        <div className="flex flex-col gap-0.5">
          <h1 className="text-xl font-semibold tracking-tight">{t('generate.pageTitle')}</h1>
          <p className="text-muted-foreground text-sm">{t('generate.pageSubtitle')}</p>
        </div>
      </div>

      {/* 说明区（specs §4.3.1） */}
      <div className="text-muted-foreground rounded-lg border p-4 text-sm leading-relaxed">
        <p className="text-foreground font-medium">{t('generate.introTitle')}</p>
        <p>{t('generate.introBody')}</p>
      </div>

      {phase === 'form' ? (
        <>
          {/* 维度多选卡区（specs §4.3.5：名称+一句话说明，选中高亮；无启用维度空态引导） */}
          <div className="flex flex-col gap-3">
            <span className="text-sm font-medium">{t('generate.dimensionLabel')}</span>
            <p className="text-muted-foreground text-xs">{t('generate.dimensionHint')}</p>
            {dimensions.length === 0 ? (
              <div className="flex flex-col items-center gap-3 rounded-md border border-dashed py-10 text-center">
                {/* 空态插画：与题库列表空态同款几何色块（specs §4.1.5 / DESIGN.md 粉彩点缀） */}
                <div aria-hidden className="flex items-end gap-1.5">
                  <span className="bg-block-cream h-6 w-4 rounded-sm" />
                  <span className="bg-block-lilac h-10 w-4 rounded-sm" />
                  <span className="bg-block-cream h-8 w-4 rounded-sm" />
                  <span className="bg-block-lilac h-14 w-4 rounded-sm" />
                  <span className="bg-block-cream h-5 w-4 rounded-sm" />
                </div>
                <p className="text-base font-medium">{t('generate.emptyTitle')}</p>
                <p className="text-muted-foreground text-sm">{t('generate.emptyDesc')}</p>
                <Button variant="outline" onClick={() => void navigate({ to: '/system/dimension' })}>
                  {t('generate.goDimension')}
                </Button>
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5">
                {dimensions.map((dim) => (
                  <DimensionCard
                    key={dim.id}
                    dim={dim}
                    selected={selectedIds.includes(dim.id)}
                    onToggle={() => toggleDimension(dim.id)}
                  />
                ))}
              </div>
            )}
          </div>

          {/* 题数（specs §4.3.2：整数 5-30 默认 10）+ 建议文案（specs §4.3.5） */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="generate-count" className="text-sm font-medium">
              {t('generate.countLabel')}
            </label>
            <div className="flex items-center gap-3">
              <Input
                id="generate-count"
                type="number"
                min={COUNT_MIN}
                max={COUNT_MAX}
                value={count}
                onChange={(e) => setCount(Number(e.target.value))}
                aria-label={t('generate.countLabel')}
                className="w-28"
              />
              <span className="text-muted-foreground text-sm">{t('generate.countHint')}</span>
            </div>
            {dimensions.length > 0 && selectedIds.length > 0 && !countValid && (
              <p className="text-destructive text-sm">
                {t('generate.countInvalid', { min: COUNT_MIN, max: COUNT_MAX })}
              </p>
            )}
          </div>

          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={onReset}>
              {t('generate.reset')}
            </Button>
            <Button disabled={!canSubmit || createMut.isPending} onClick={onStart}>
              {createMut.isPending ? t('generate.submitting') : t('generate.start')}
            </Button>
          </div>
        </>
      ) : phase === 'running' ? (
        /* 进行态（specs §4A.3）：进度条 + 当前维度，替换表单区 */
        (() => {
          const total = progress?.count ?? count;
          const percent = total > 0 ? Math.floor(((progress?.generated_count ?? 0) / total) * 100) : 0;
          return (
            <div className="flex flex-col gap-4 rounded-lg border p-6">
              <span className="font-medium">{t('generate.runningTitle')}</span>
              <span className="text-muted-foreground text-sm">
                {t('generate.progressCount', {
                  generated: progress?.generated_count ?? 0,
                  total,
                })}
              </span>
              <div
                role="progressbar"
                aria-valuenow={percent}
                aria-valuemin={0}
                aria-valuemax={100}
                className="bg-muted h-2 w-full max-w-xl overflow-hidden rounded-full"
              >
                <div
                  className="bg-primary h-full transition-[width]"
                  style={{ width: `${percent}%` }}
                />
              </div>
              {progress?.current_dimension_name ? (
                <span className="text-muted-foreground text-sm">
                  {t('generate.currentDimension', { name: progress.current_dimension_name })}
                </span>
              ) : null}
              <span className="text-muted-foreground text-xs">{t('generate.runningHint')}</span>
            </div>
          );
        })()
      ) : phase === 'done' ? (
        /* 完成态（specs §4A.3）：批次号 + 引导审核 */
        <div className="flex flex-col gap-4 rounded-lg border p-6">
          <span className="font-medium">{t('generate.doneTitle')}</span>
          <p className="text-muted-foreground text-sm">
            {t('generate.doneBody', {
              count: progress?.count ?? count,
              no: progress?.batch_no ?? '',
            })}
          </p>
          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={onRegenerate}>
              {t('generate.regenerateBatch')}
            </Button>
            <Button onClick={onGoReview}>{t('generate.goReview')}</Button>
          </div>
        </div>
      ) : (
        /* 失败态（specs §4A.3 / §4.3.4 规则 3）：固定失败文案，不透传底层错误串 */
        <div className="flex flex-col gap-4 rounded-lg border p-6">
          <span className="font-medium">{t('generate.failedTitle')}</span>
          <p className="text-muted-foreground text-sm">
            {t('generate.failedBody', {
              reason: t(`generate.errorCode.${progress?.error_code ?? 'INTERNAL'}`),
            })}
          </p>
          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={() => void navigate({ to: '/question-bank' })}>
              {t('generate.back')}
            </Button>
            <Button onClick={onRegenerate}>{t('generate.retry')}</Button>
          </div>
        </div>
      )}

      {/* 进行态离开二次确认（specs §4.3.4 规则 1）：覆盖页内返回与路由级离开 */}
      <ConfirmDialog
        open={backConfirmOpen}
        onOpenChange={(v) => {
          if (!v && !cancelMut.isPending) onDismissBack();
        }}
        title={t('generate.cancelTitle')}
        desc={t('generate.cancelDesc')}
        confirmText={t('generate.cancelConfirm')}
        cancelText={t('cancel', { ns: 'common' })}
        submittingText={t('generate.cancelSubmitting')}
        submitting={cancelMut.isPending}
        destructive
        onConfirm={onConfirmBack}
      />
    </div>
  );
}
