// 测评对象单选（发起评测弹窗主动测试分支专用，specs P2_TST_001 §4.2.2 A）
//
// 与 StaffMultiSelect 刻意不复用：单选无「全员」语义（决策 12 限一人），
// 选中即收起、换选替换；错误态与重试限在下拉内（§4.2.3 人员搜索同口径）。
import { useEffect, useRef, useState } from 'react';
import type { JSX } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, ChevronDown, X } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useStaffs } from '@/features/system-params/hooks';
import { useDebouncedValue } from '@/lib/use-debounced';
import { cn } from '@/lib/utils';

export interface StaffSingleSelectProps {
  value: { staff_id: string; staff_name: string } | null;
  onChange: (next: { staff_id: string; staff_name: string } | null) => void;
}

const PAGE_SIZE = 20;

/** 单选下拉：可搜索，选中即收起；无「全员」项（决策 12：单次定向发起对特定人员）。 */
export function StaffSingleSelect({ value, onChange }: StaffSingleSelectProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const wrapRef = useRef<HTMLDivElement>(null);

  const staffsQ = useStaffs(debouncedKeyword, 1, PAGE_SIZE);

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

  const pick = (s: { staff_id: string; staff_name: string }) => {
    onChange({ staff_id: s.staff_id, staff_name: s.staff_name });
    setOpen(false); // 选中即收起
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
          {t('create.singleTargetPlaceholder')}
          <ChevronDown className="size-4 opacity-50" />
        </Button>

        {value && (
          <Badge variant="secondary" className="h-7 gap-1 pr-1">
            {value.staff_name}
            <button
              type="button"
              aria-label={t('create.singleTargetClear', { name: value.staff_name })}
              className="hover:bg-accent rounded-sm"
              onClick={() => onChange(null)}
            >
              <X className="size-3" />
            </button>
          </Badge>
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
            {staffsQ.isLoading ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('create.targetLoading')}
              </div>
            ) : staffsQ.isError ? (
              // 错误态 + 重试入口限在下拉内，不置灰组件（§4.2.3 人员搜索）
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
            ) : list.length === 0 ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('create.targetEmpty')}
              </div>
            ) : (
              list.map((s) => {
                const checked = value?.staff_name === s.staff_name;
                return (
                  <button
                    type="button"
                    key={s.staff_name}
                    onClick={() => pick(s)}
                    className="hover:bg-accent flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm"
                  >
                    <span>{s.staff_name}</span>
                    <Check className={cn('size-4', checked ? 'opacity-100' : 'opacity-0')} />
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}
