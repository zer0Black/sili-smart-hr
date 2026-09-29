// 发起弹窗主动测试分支表单（specs P2_TST_001 §4.2.2 B/§4.2.3）：单人对象 +
// ai_mgmt 子能力复选 / enneagram 量表只读。自 CreateBatchDialog 拆出（三分支
// 表单各自演进互不牵连）。
import { useEffect, useRef } from 'react';
import type { JSX, RefObject } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { Label } from '@/components/ui/label';
import {
  StaffSingleSelect,
  type StaffOption,
} from '@/features/assessment/components/staff-single-select';
import { useScaleStatus } from '@/features/assessment/test-task-hooks';
import { useEnabledAiMgmtDimensions } from '@/features/question-bank/dimension-options';

/** 主动测试分支表单值：单人对象 + 子能力集合（enneagram 不消费）。 */
export interface TestTaskFormValues {
  staff: StaffOption | null;
  dimensionIds: string[];
}

/** 表单实例控制面：弹窗读取判取消确认、提交经内部 handleSubmit 走本表单。 */
export interface TestTaskFormHandle {
  getValues: () => TestTaskFormValues;
  submit: (onValid: (values: TestTaskFormValues) => void) => ReturnType<
    ReturnType<typeof useForm<TestTaskFormValues>>['handleSubmit']
  >;
  staffAnchorRef: RefObject<HTMLDivElement | null>;
}

export interface TestTaskFormProps {
  /** 当前分支类型：决定子能力区块渲染与校验。 */
  activeType: 'ai_mgmt' | 'enneagram';
  /** 外部重置信号：类型切换时清空表单（值变化触发，§4.2.5 已填内容丢弃）。 */
  resetSignal: number;
  /** 挂载表单控制面（弹窗持 ref 读取 staff 与聚焦锚点、驱动提交）。 */
  onReady?: (handle: TestTaskFormHandle) => void;
}

/** 主动测试表单：校验 schema 按类型切换（ai_mgmt 子能力至少一项）。 */
export function TestTaskForm({
  activeType,
  resetSignal,
  onReady,
}: TestTaskFormProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const dims = useEnabledAiMgmtDimensions();
  const scaleQ = useScaleStatus();
  // 焦点落测评对象触发框（§4.2.5）
  const staffAnchorRef = useRef<HTMLDivElement>(null);

  // 子能力至少一项校验仅 ai_mgmt 适用（specs §4.2.2 B：九型表单无子能力字段）
  const schema = z.object({
    staff: z
      .object({ staff_id: z.string(), staff_name: z.string() })
      .nullable()
      .refine((v) => v !== null, { message: t('create.singleTargetRequired') }),
    dimensionIds:
      activeType === 'ai_mgmt'
        ? z.array(z.string()).refine((v) => v.length > 0, {
            message: t('create.dimsRequired'),
          })
        : z.array(z.string()),
  });

  const {
    control,
    handleSubmit,
    reset,
    setValue,
    getValues,
    formState: { errors },
  } = useForm<TestTaskFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { staff: null, dimensionIds: [] },
  });

  useEffect(() => {
    onReady?.({
      getValues,
      submit: handleSubmit,
      staffAnchorRef,
    });
  }, [onReady, getValues, handleSubmit, staffAnchorRef]);

  // 维度启用集合到达时按「全选启用项」对齐默认值（§4.2.2 A 默认全选；用户已手动
  // 改动过则保留用户选择）。
  const dimsTouchedRef = useRef(false);
  useEffect(() => {
    if (activeType !== 'ai_mgmt') {
      dimsTouchedRef.current = false;
      return;
    }
    if (dimsTouchedRef.current) return;
    const current = getValues().dimensionIds;
    // 初始化或维度集合变化且用户未改动时同步全选
    const allIds = dims.map((d) => d.id);
    if (current.length === 0 || current.every((id) => allIds.includes(id))) {
      if (current.length !== allIds.length) {
        setValue('dimensionIds', allIds, { shouldDirty: false });
      }
    }
  }, [activeType, dims, setValue, getValues]);

  // 类型切换清空表单（跳过首挂载，防与维度默认全选竞态）
  const lastResetSignalRef = useRef(resetSignal);
  useEffect(() => {
    if (lastResetSignalRef.current === resetSignal) return;
    lastResetSignalRef.current = resetSignal;
    reset({ staff: null, dimensionIds: [] });
    dimsTouchedRef.current = false;
  }, [resetSignal, reset]);

  return (
    <>
      <div className="flex flex-col gap-2" ref={staffAnchorRef}>
        <Label>{t('create.singleTargetLabel')}</Label>
        <Controller
          control={control}
          name="staff"
          render={({ field }) => (
            <StaffSingleSelect value={field.value} onChange={field.onChange} />
          )}
        />
        {errors.staff && <p className="text-destructive text-sm">{errors.staff.message}</p>}
      </div>

      {activeType === 'ai_mgmt' && (
        <div className="flex flex-col gap-2">
          <Label>{t('create.dimsLabel')}</Label>
          <Controller
            control={control}
            name="dimensionIds"
            render={({ field }) => (
              <div className="grid grid-cols-2 gap-2">
                {dims.map((d) => {
                  const checked = field.value.includes(d.id);
                  return (
                    <label
                      key={d.id}
                      className="hover:bg-accent flex items-center gap-2 rounded-md border px-3 py-2 text-sm"
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        aria-label={d.name}
                        onChange={(e) => {
                          dimsTouchedRef.current = true;
                          const next = e.target.checked
                            ? [...field.value, d.id]
                            : field.value.filter((id) => id !== d.id);
                          field.onChange(next);
                        }}
                      />
                      {d.name}
                    </label>
                  );
                })}
              </div>
            )}
          />
          {errors.dimensionIds && (
            <p className="text-destructive text-sm">{errors.dimensionIds.message}</p>
          )}
        </div>
      )}

      {activeType === 'enneagram' && (
        <div className="flex flex-col gap-2">
          <Label>{t('create.scaleLabel')}</Label>
          {scaleQ.isLoading ? (
            <p className="text-muted-foreground text-sm">{t('create.scaleLoading')}</p>
          ) : scaleQ.data?.ready ? (
            <p className="text-sm">
              {scaleQ.data.scale_name}
              <span className="text-muted-foreground">
                {' '}
                {t('create.scaleCount', { count: scaleQ.data.active_question_count })}
              </span>
            </p>
          ) : (
            // 未就绪提示不阻断：提交时报 1804（B2 口径）
            <p className="text-muted-foreground text-sm">{t('create.toastScaleNotReady')}</p>
          )}
        </div>
      )}
    </>
  );
}
