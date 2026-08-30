// 评估对象多选下拉（specs §4.1.4 规则4 / §4.1.5）
//
// shadcn 暂未引入 popover/command，这里用 button 触发 + 纯 Tailwind dropdown 面板组合实现：
// - 折叠标签：已选人员以 Badge 形式平铺，每个 Badge 带 X 可单独移除
// - 过滤：输入框按人名 keyword 触发 useStaffs refetch
// - 失败降级：useStaffs isError 时 toast + 自动切全员 + 置灰本组件（BR6）
// - 无工号：选项只展示 staff_name（系统无工号约束）
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Check, ChevronDown, X } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useStaffs } from '@/features/system-params/hooks';
import type { StaffItem } from '@/lib/contracts';
import { useDebouncedValue } from '@/lib/use-debounced';
import { cn } from '@/lib/utils';

export interface MemberMultiSelectProps {
  /** 受控已选值。 */
  value: StaffItem[];
  /** 受控变更。 */
  onChange: (next: StaffItem[]) => void;
  /** 置灰开关：父组件据全员模式或人员数据不可用控制（BR6 降级）。 */
  disabled?: boolean;
  /**
   * 人员数据加载失败降级回调（§4.1.4 规则4）。
   * 父组件据此把 target_mode 切回 'all' 并清空 specified_members，
   * 一次失败会话只触发一次（与 toast 共用 degradedFiredRef）。
   */
  onDegraded?: () => void;
}

const PAGE_SIZE = 20;

/**
 * 多选下拉：折叠标签展示已选，按人名 keyword 过滤、分页拉取。
 * 内部 ref + 外部点击关闭面板。useStaffs 失败时一次性 toast 降级提示。
 */
export function MemberMultiSelect({ value, onChange, disabled, onDegraded }: MemberMultiSelectProps) {
  const { t } = useTranslation('systemParams');
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  // 关键字防抖，避免逐字符打后端；变化时回到第 1 页。
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const [page, setPage] = useState(1);
  useEffect(() => {
    setPage(1);
  }, [debouncedKeyword]);
  const [degraded, setDegraded] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const degradedFiredRef = useRef(false);

  const staffsQ = useStaffs(debouncedKeyword, page, PAGE_SIZE);
  const total = staffsQ.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // 失败降级（§4.1.4 规则4）：首次失败时一次性 toast + 通知父组件切全员并清空指定人员。
  // 用 ref 保证一次会话只提示一次，避免重复 toast 与重复 setValue；父组件传回 disabled 后组件置灰。
  useEffect(() => {
    if (staffsQ.isError && !degradedFiredRef.current) {
      degradedFiredRef.current = true;
      setDegraded(true);
      toast.error(t('staffUnavailable'));
      // 通知父组件：自动选中全员（target_mode='all'）并清空 specified_members
      onDegraded?.();
    }
    if (!staffsQ.isError && degradedFiredRef.current) {
      // 重试成功：解除降级，允许下次失败再次提示
      degradedFiredRef.current = false;
      setDegraded(false);
    }
  }, [staffsQ.isError, t, onDegraded]);

  // 外部点击关闭面板
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

  // 渲染态：合并已选 + 当页，避免翻页时已选项消失；以 staff_id 去重
  const merged = useMemo(() => {
    const map = new Map<string, StaffItem>();
    for (const s of value) map.set(s.staff_id, s);
    for (const s of list) if (!map.has(s.staff_id)) map.set(s.staff_id, s);
    return Array.from(map.values());
  }, [value, list]);

  const selectedIds = useMemo(() => new Set(value.map((s) => s.staff_id)), [value]);

  const toggle = (s: StaffItem) => {
    if (selectedIds.has(s.staff_id)) {
      onChange(value.filter((x) => x.staff_id !== s.staff_id));
    } else {
      onChange([...value, s]);
    }
  };

  const remove = (staffId: string) => {
    onChange(value.filter((x) => x.staff_id !== staffId));
  };

  const clearAll = () => onChange([]);

  const isDisabled = disabled || degraded;

  return (
    <div ref={wrapRef} className="relative">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={isDisabled}
          onClick={() => setOpen((v) => !v)}
          className="h-8"
        >
          {t('membersPlaceholder')}
          <ChevronDown className="size-4 opacity-50" />
        </Button>

        {value.length > 0 && (
          <>
            {value.map((s) => (
              <Badge
                key={s.staff_id}
                variant="secondary"
                className="h-7 gap-1 pr-1"
              >
                {s.staff_name}
                {!isDisabled && (
                  <button
                    type="button"
                    aria-label={t('membersRemove', { name: s.staff_name })}
                    className="hover:bg-accent rounded-sm"
                    onClick={() => remove(s.staff_id)}
                  >
                    <X className="size-3" />
                  </button>
                )}
              </Badge>
            ))}
            <span className="text-muted-foreground text-xs">
              {t('membersSelected', { count: value.length })}
            </span>
            {!isDisabled && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
                onClick={clearAll}
              >
                {t('membersClearAll')}
              </Button>
            )}
          </>
        )}
      </div>

      {open && !isDisabled && (
        <div className="absolute top-full left-0 z-50 mt-1 flex max-h-80 w-72 flex-col rounded-md border bg-popover p-1 shadow-md">
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder={t('membersSearchPlaceholder')}
            className="h-8"
          />
          <div className="mt-1 flex-1 overflow-y-auto">
            {staffsQ.isLoading ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('membersLoading')}
              </div>
            ) : merged.length === 0 ? (
              <div className="text-muted-foreground px-2 py-3 text-center text-sm">
                {t('membersEmpty')}
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
                    <Check
                      className={cn('size-4', checked ? 'opacity-100' : 'opacity-0')}
                    />
                  </button>
                );
              })
            )}
          </div>
          {/* 分页：total 大于一页时提供上一/下一，避免只能看前 PAGE_SIZE 条。 */}
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
                {t('membersPrev')}
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
                {t('membersNext')}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
