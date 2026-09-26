// 题目编辑弹窗（specs §4.1.2 E / §4.1.4 规则9 / §4A.4）。RHF+Zod，mode=edit/resubmit（重新提交
// 复用同表单换标题与提交接口，specs §4.1.4 规则6），预载详情回填。维度下拉限当前启用 AI_MGMT
// 集合；保存成功 toast 后留在列表态（关闭弹窗）。
import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { useDimensionTree } from '@/features/dimension/hooks';
import { useResubmitQuestion, useUpdateQuestion } from '@/features/question-bank/hooks';
import type { QuestionDetail } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export interface QuestionFormDialogProps {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  question: QuestionDetail | null;
  onSaved: () => void;
  /** edit=编辑保存；resubmit=驳回题修正后重新送审（specs §4.1.4 规则6）。 */
  mode?: 'edit' | 'resubmit';
}

interface QuestionFormValues {
  dimension_id: string;
  scenario: string;
  requirement: string;
  focus_point: string;
}

/** AI_MGMT 模块下当前启用的子能力（specs §4.1.2 E：改选限当前启用集合）。 */
function useEnabledAiMgmtDimensions() {
  const treeQ = useDimensionTree();
  const mod = treeQ.data?.modules.find((m) => m.module_code === 'AI_MGMT');
  if (!mod) return [] as { id: string; name: string }[];
  const leaves = mod.groups ? mod.groups.flatMap((g) => g.dimensions) : (mod.dimensions ?? []);
  return leaves.filter((d) => d.enabled).map((d) => ({ id: d.id, name: d.name }));
}

export function QuestionFormDialog({ open, onOpenChange, question, onSaved, mode = 'edit' }: QuestionFormDialogProps) {
  const { t } = useTranslation('questionBank');
  const updateMut = useUpdateQuestion();
  const resubmitMut = useResubmitQuestion();
  const mut = mode === 'resubmit' ? resubmitMut : updateMut;
  const submitting = mode === 'resubmit' ? t('form.resubmitting') : t('form.submitting');
  const dimensions = useEnabledAiMgmtDimensions();

  const schema = useMemo(
    () =>
      z.object({
        dimension_id: z.string().min(1, t('form.dimensionRequired')),
        scenario: z.string().min(1, t('form.scenarioRequired')).max(1000, t('form.scenarioMaxLength')),
        requirement: z.string().min(1, t('form.requirementRequired')).max(2000, t('form.requirementMaxLength')),
        focus_point: z.string().min(1, t('form.focusPointRequired')).max(500, t('form.focusPointMaxLength')),
      }),
    [t],
  );

  const {
    register,
    handleSubmit,
    control,
    reset,
    setError,
    formState: { errors },
  } = useForm<QuestionFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { dimension_id: '', scenario: '', requirement: '', focus_point: '' },
  });

  // 弹窗打开时预载详情回填（specs §4.1.2 E）
  useEffect(() => {
    if (!open) return;
    if (question) {
      reset({
        dimension_id: question.dimension_id,
        scenario: question.scenario,
        requirement: question.requirement,
        focus_point: question.focus_point,
      });
    } else {
      reset({ dimension_id: '', scenario: '', requirement: '', focus_point: '' });
    }
  }, [open, question, reset]);

  const onSubmit = (values: QuestionFormValues) => {
    if (!question) return;
    // 原维度已停用且未改选时前置拦截，内联报错替代后端 1400 兜底（§4.1.2 E / §4.4）
    if (!dimensions.some((d) => d.id === values.dimension_id)) {
      setError('dimension_id', { message: t('form.dimensionDisabledSubmit') });
      return;
    }
    mut.mutate(
      {
        id: question.id,
        dimension_id: values.dimension_id,
        scenario: values.scenario,
        requirement: values.requirement,
        focus_point: values.focus_point,
        version: question.version,
      },
      {
        onSuccess: () => {
          // 重新提交成功 toast 去向文案（specs §4.1.4 规则6 / §4.1.5）
          toast.success(mode === 'resubmit' ? t('form.resubmitted') : t('form.saved'));
          onOpenChange(false);
          onSaved();
        },
        onError: (err) => {
          const code = err instanceof ApiError ? err.code : undefined;
          // 并发冲突（1713）：toast 后废弃缓存刷新列表，弹窗保留（specs §4.1.4 规则9）
          if (code === ErrCode.QuestionVersionConflict) {
            toast.error(t('toastConflict'));
            void queryClient.invalidateQueries({ queryKey: ['question-bank'] });
            return;
          }
          toast.error(t('toastGeneric'));
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[640px]">
        <DialogHeader>
          <DialogTitle>{mode === 'resubmit' ? t('form.resubmitTitle') : t('form.title')}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          {/* 所属维度：下拉限当前启用集合；原维度已停用时保留原值展示（§4.1.2 E） */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="q-edit-dimension">{t('form.dimension')}</Label>
            <Controller
              control={control}
              name="dimension_id"
              render={({ field }) => {
                const current =
                  dimensions.find((d) => d.id === field.value) ??
                  (question && question.dimension_id === field.value
                    ? { id: field.value, name: `${question.dimension_name}（${t('form.dimensionDisabled')}）` }
                    : undefined);
                return (
                  <Select value={field.value} onValueChange={field.onChange}>
                    <SelectTrigger id="q-edit-dimension" className="w-full">
                      <SelectValue>{current?.name}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {dimensions.map((d) => (
                        <SelectItem key={d.id} value={d.id}>
                          {d.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                );
              }}
            />
            {errors.dimension_id && (
              <p className="text-destructive text-sm">{errors.dimension_id.message}</p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="q-edit-scenario">{t('form.scenario')}</Label>
            <Textarea id="q-edit-scenario" rows={4} {...register('scenario')} />
            {errors.scenario && <p className="text-destructive text-sm">{errors.scenario.message}</p>}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="q-edit-requirement">{t('form.requirement')}</Label>
            <Textarea id="q-edit-requirement" rows={4} {...register('requirement')} />
            <p className="text-muted-foreground text-xs">{t('form.requirementHint')}</p>
            {errors.requirement && <p className="text-destructive text-sm">{errors.requirement.message}</p>}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="q-edit-focus-point">{t('form.focusPoint')}</Label>
            <Textarea id="q-edit-focus-point" rows={3} {...register('focus_point')} />
            {errors.focus_point && <p className="text-destructive text-sm">{errors.focus_point.message}</p>}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={mut.isPending}>
              {t('form.cancel', { ns: 'common' })}
            </Button>
            <Button type="submit" disabled={mut.isPending}>
              {mut.isPending ? submitting : mode === 'resubmit' ? t('form.resubmitSubmit') : t('form.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
