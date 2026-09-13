// 评估对象多选（发起评测弹窗专用，specs §4.2.2/§4.2.3/§4.2.4 规则1）
//
// 与 system-params 的 MemberMultiSelect 刻意不复用：降级语义相反。
// 本组件 isError 时在下拉内展示错误态与重试入口，不自动切全员、不置灰，
// 人员搜索失败不阻断弹窗其余操作（§4.2.3 人员搜索）。
// 「全员」为固定首项：选中即 mode='all' 且清空 staffs；再选具体人员自动移除全员（§4.2.4 规则1）。
import { useEffect, useMemo, useRef, useState } from 'react';
import type { JSX } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, ChevronDown, X } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useStaffs } from '@/features/system-params/hooks';
import type { StaffItem } from '@/lib/contracts';
import { useDebouncedValue } from '@/lib/use-debounced';
import { cn } from '@/lib/utils';

export interface StaffSelectValue {
  mode: 'all' | 'specified';
  staffs: StaffItem[];
}

export interface StaffMultiSelectProps {
  value: StaffSelectValue;
  onChange: (next: StaffSelectValue) => void;
}

const PAGE_SIZE = 20;

/** 多选下拉：固定首项「全员」与指定人员互斥；已选 Badge 平铺可单独移除。 */
export function StaffMultiSelect({ value, onChange }: StaffMultiSelectProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const [page, setPage] = useState(1);
  useEffect(() => {
    setPage(1);
  }, [debouncedKeyword]);
  const wrapRef = useRef<HTMLDivElement>(null);

  const staffsQ = useStaffs(debouncedKeyword, page, PAGE_SIZE);
  const total = staffsQ.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, [open]);

  const list = staffsQ.data?.list ?? [];

  // 合并已选 + 当页选项，防翻页丢失；staff_id 去重
  const merged = useMemo(() => {
    const map = new Map<string, StaffItem>();
    for (const s of value.staffs) map.set(s.staff_id, s);
    for (const s of list) if (!map.has(s.staff_id)) map.set(s.staff_id, s);
    return Array.from(map.values());
  }, [value.staffs, list]);

  const selectedIds = useMemo(
    () => new Set(value.staffs.map((s) => s.staff_id)),
    [value.staffs],
  );
  const isAll = value.mode === 'all';

  const toggleAll = () => {
    if (isAll) {
      onChange({ mode: 'specified', staffs: [] });
    } else {
      // 全员互斥（§4.2.4 规则1）：选中全员自动清空其余
      onChange({ mode: 'all', staffs: [] });
    }
  };

  const toggle = (s: StaffItem) => {
    if (isAll) {
      onChange({ mode: 'specified', staffs: [s] });
      return;
    }
    if (selectedIds.has(s.staff_id)) {
      onChange({
        mode: 'specified',
        staffs: value.staffs.filter((x) => x.staff_id !== s.staff_id),
      });
    } else {
      onChange({ mode: 'specified', staffs: [...value.staffs, s] });
    }
  };

  const remove = (staffId: string) => {
    onChange({
      mode: 'specified',
      staffs: value.staffs.filter((x) => x.staff_id !== staffId),
    });
  };

  return (
    <div ref={wrapRef} className="relative">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setOpen((v) => !v)}
          className="h-8"
        >
          {t('create.targetPlaceholder')}
          <ChevronDown className="size-4 opacity-50" />
        </Button>

        {isAll ? (
          <Badge variant="secondary" className="h-7 gap-1 pr-1">
            {t('create.targetAll')}
            <button
              type="button"
              aria-label={t('create.targetRemove', { name: t('create.targetAll') })}
              className="hover:bg-accent rounded-sm"
              onClick={toggleAll}
            >
              <X className="size-3" />
            </button>
          </Badge>
        ) : (
          value.staffs.length > 0 && (
            <>
              {value.staffs.map((s) => (
                <Badge key={s.staff_id} variant="secondary" className="h-7 gap-1 pr-1">
                  {s.staff_name}
                  <button
                    type="button"
                    aria-label={t('create.targetRemove', { name: s.staff_name })}
                    className="hover:bg-accent rounded-sm"
                    onClick={() => remove(s.staff_id)}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
              <span className="text-muted-foreground text-xs">
                {t('create.targetSelected', { count: value.staffs.length })}
              </span>
            </>
          )
        )}
      </div>

      {open && (
        <div className="absolute top-full left-0 z-50 mt-1 flex max-h-80 w-72 flex-col rounded-md border bg-popover p-1 shadow-md">
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder={t('create.targetSearchPlaceholder')}
            className="h-8"
          />
          <div className="mt-1 flex-1 overflow-y-auto">
            <button
              type="button"
              onClick={toggleAll}
              className="hover:bg-accent flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm font-medium"
            >
              <span>{t('create.targetAll')}</span>
              <Check className={cn('size-4', isAll ? 'opacity-100' : 'opacity-0')} />
            </button>
            {staffsQ.isLoading ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('create.targetLoading')}
              </div>
            ) : staffsQ.isError ? (
              // 降级（§4.2.3）：错误态 + 重试入口限在下拉内，不置灰组件、不自动切全员
              <div className="flex flex-col items-center gap-2 px-2 py-3">
                <span className="text-muted-foreground text-sm">{t('create.targetError')}</span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-7 px-2 text-xs"
                  onClick={() => void staffsQ.refetch()}
                >
                  {t('create.targetRetry')}
                </Button>
              </div>
            ) : merged.length === 0 ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('create.targetEmpty')}
              </div>
            ) : (
              merged.map((s) => {
                const checked = selectedIds.has(s.staff_id);
                return (
                  <button
                    type="button"
                    key={s.staff_id}
                    onClick={() => toggle(s)}
                    className="hover:bg-accent flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm"
                  >
                    <span>{s.staff_name}</span>
                    <Check className={cn('size-4', checked ? 'opacity-100' : 'opacity-0')} />
                  </button>
                );
              })
            )}
          </div>
          {total > PAGE_SIZE && (
            <div className="flex items-center justify-between gap-2 border-t px-1 pt-1 text-xs">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-6 px-2"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                {t('create.targetPrev')}
              </Button>
              <span className="text-muted-foreground">
                {page} / {totalPages}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-6 px-2"
                disabled={page >= totalPages}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              >
                {t('create.targetNext')}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
