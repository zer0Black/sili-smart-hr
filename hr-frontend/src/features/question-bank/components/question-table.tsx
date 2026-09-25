// 单 tab 题库列表组件（specs §4.1.2 A/B / §4.1.3 / §4.1.5）。双 tab 各持一份实例，
// 查询条件（draft→filter 两段式）与分页在组件内部自治，切 tab 不互相清空。
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { toast } from 'sonner';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/components/confirm-dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useDimensionTree } from '@/features/dimension/hooks';
import { fetchQuestionDetail } from '@/features/question-bank/api';
import {
  useDeleteQuestion,
  useQuestions,
  useToggleQuestionStatus,
} from '@/features/question-bank/hooks';
import type { QuestionTab } from '@/features/question-bank/types';
import { ErrCode } from '@/lib/contracts';
import type { QuestionListItem } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export interface QuestionTableProps {
  tab: QuestionTab;
  /** 打开编辑弹窗（仅 AI 题）。 */
  onEdit: (q: QuestionListItem) => void;
  /** 查看弹窗。 */
  onView: (q: QuestionListItem) => void;
  /** 打开重新提交弹窗（仅 REJECTED AI 题，specs §4.1.4 规则6）。 */
  onResubmit: (q: QuestionListItem) => void;
  /** 列表 total 上报，供父页渲染 tab 头「共 N 题」（specs §4.1.5）。 */
  onTotalChange?: (tab: QuestionTab, total: number) => void;
  /** 打开量表引入弹窗（仅 SCALE tab 空态内嵌按钮，specs §4.1.5）。 */
  onImportScale?: () => void;
}

const DEFAULT_PAGE_SIZE = 10;
const PAGE_SIZE_OPTIONS = [10, 20, 50];
const ALL = '__all__';
/** 摘要展示截断长度（specs §4.1.2 B：过长截断悬浮展示全文）。 */
const SUMMARY_MAX = 30;

function statusVariant(status: QuestionListItem['status']): 'default' | 'secondary' | 'destructive' {
  if (status === 'DISABLED') return 'secondary';
  if (status === 'REJECTED') return 'destructive';
  return 'default';
}

/** tab 维度下拉数据源：AI 取启用 AI_MGMT 子能力，SCALE 型别维度 SP3 前为空（仅「全部维度」）。 */
function useTabDimensions(tab: QuestionTab) {
  const treeQ = useDimensionTree();
  if (tab !== 'AI') return [];
  const mod = treeQ.data?.modules.find((m) => m.module_code === 'AI_MGMT');
  if (!mod) return [];
  const leaves = mod.groups ? mod.groups.flatMap((g) => g.dimensions) : (mod.dimensions ?? []);
  return leaves.filter((d) => d.enabled).map((d) => ({ id: d.id, name: d.name }));
}

export function QuestionTable({ tab, onEdit, onView, onResubmit, onTotalChange, onImportScale }: QuestionTableProps): JSX.Element {
  const { t } = useTranslation('questionBank');
  const navigate = useNavigate();
  const dimensions = useTabDimensions(tab);

  // draft→filter 两段式：改条件只动 draft，点「查询」才落 filter 触发请求（specs §4.1.5）
  const [draftDimension, setDraftDimension] = useState<string>(ALL);
  const [draftStatus, setDraftStatus] = useState<string>(ALL);
  const [draftKeyword, setDraftKeyword] = useState('');
  const [filter, setFilter] = useState<{ dimension_id?: string; status?: string; keyword?: string; page: number; page_size: number }>({
    page: 1,
    page_size: DEFAULT_PAGE_SIZE,
  });
  // 删除二次确认（specs §4.1.3 删除题目 / 通用规范 14）：null 为关、非 null 为待删行
  const [deleteTarget, setDeleteTarget] = useState<QuestionListItem | null>(null);

  const query = useQuestions({
    source: tab,
    dimension_id: filter.dimension_id,
    status: filter.status,
    keyword: filter.keyword,
    page: filter.page,
    page_size: filter.page_size,
  });
  const toggleMut = useToggleQuestionStatus();
  const deleteMut = useDeleteQuestion();

  const list = query.data?.list ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / filter.page_size));

  // total 上报：tab 头「共 N 题」，仅在拿到数据后变化时上报（specs §4.1.5）
  useEffect(() => {
    if (query.data !== undefined) onTotalChange?.(tab, total);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, query.data]);

  // 页码钳位：total 收缩让当前页落空时回落末页（batch-table 先例）
  useEffect(() => {
    if (filter.page > totalPages) {
      setFilter((f) => ({ ...f, page: totalPages }));
    }
  }, [totalPages]); // eslint-disable-line react-hooks/exhaustive-deps

  function onQuery() {
    setFilter({
      dimension_id: draftDimension === ALL ? undefined : draftDimension,
      status: draftStatus === ALL ? undefined : draftStatus,
      keyword: draftKeyword.trim() === '' ? undefined : draftKeyword.trim(),
      page: 1,
      page_size: filter.page_size,
    });
    if (query.isError) void query.refetch();
  }

  function onReset() {
    setDraftDimension(ALL);
    setDraftStatus(ALL);
    setDraftKeyword('');
    setFilter({ page: 1, page_size: filter.page_size });
  }

  /** 写操作异常统一处理：1713 冲突刷新列表，其余通用失败（specs §4.1.4 规则9）。 */
  function onWriteError(err: unknown) {
    const code = err instanceof ApiError ? err.code : undefined;
    if (code === ErrCode.QuestionVersionConflict) {
      toast.error(t('toastConflict'));
      void queryClient.invalidateQueries({ queryKey: ['question-bank'] });
      return;
    }
    toast.error(t('toastGeneric'));
  }

  // 乐观锁 version 不在列表项里，启停/删除前先取详情拿当前 version（03 §3.3/§3.4/§3.5）
  async function fetchVersion(item: QuestionListItem): Promise<number | null> {
    try {
      const detail = await fetchQuestionDetail(item.id);
      return detail.version;
    } catch {
      toast.error(t('toastGeneric'));
      return null;
    }
  }

  async function onToggle(item: QuestionListItem) {
    const version = await fetchVersion(item);
    if (version === null) return;
    toggleMut.mutate(
      {
        id: item.id,
        target_status: item.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE',
        version,
      },
      {
        onSuccess: () =>
          toast.success(item.status === 'ACTIVE' ? t('table.toastDisabled') : t('table.toastEnabled')),
        onError: onWriteError,
      },
    );
  }

  function onDelete() {
    const item = deleteTarget;
    if (!item) return;
    void (async () => {
      const version = await fetchVersion(item);
      if (version === null) {
        setDeleteTarget(null);
        return;
      }
      deleteMut.mutate(
        { id: item.id, version },
        {
          onSuccess: () => {
            toast.success(t('table.toastDeleted'));
            setDeleteTarget(null);
          },
          onError: (err) => {
            const code = err instanceof ApiError ? err.code : undefined;
            // 被引用拒删（1705）：只可停用（specs §4.1.4 规则4）
            if (code === ErrCode.QuestionReferenced) {
              toast.error(t('table.toastReferenced'));
              setDeleteTarget(null);
              return;
            }
            onWriteError(err);
          },
        },
      );
    })();
  }

  const summaryText = (s: string) => (s.length > SUMMARY_MAX ? `${s.slice(0, SUMMARY_MAX)}…` : s);

  return (
    <div className="flex flex-col gap-4">
      {/* 查询区（specs §4.1.2 A）：维度/状态/关键词 + 查询/重置 */}
      <div className="flex flex-wrap items-center gap-2">
        <Select value={draftDimension} onValueChange={setDraftDimension}>
          <SelectTrigger className="w-40" aria-label={t('table.filterDimension')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>{t('table.filterDimensionAll')}</SelectItem>
            {dimensions.map((d) => (
              <SelectItem key={d.id} value={d.id}>
                {d.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={draftStatus} onValueChange={setDraftStatus}>
          <SelectTrigger className="w-32" aria-label={t('table.filterStatus')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>{t('table.filterStatusAll')}</SelectItem>
            <SelectItem value="ACTIVE">{t('table.status.ACTIVE')}</SelectItem>
            <SelectItem value="DISABLED">{t('table.status.DISABLED')}</SelectItem>
            {tab === 'AI' && <SelectItem value="REJECTED">{t('table.status.REJECTED')}</SelectItem>}
          </SelectContent>
        </Select>
        <input
          value={draftKeyword}
          onChange={(e) => setDraftKeyword(e.target.value)}
          placeholder={t('table.keywordPlaceholder')}
          aria-label={t('table.keywordLabel')}
          className="border-input bg-background h-9 w-56 rounded-md border px-3 text-sm shadow-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
        />
        <Button variant="outline" onClick={onQuery} disabled={query.isFetching}>
          {t('table.query')}
        </Button>
        <Button variant="ghost" onClick={onReset}>
          {t('table.reset')}
        </Button>
      </div>

      {query.isLoading ? (
        <div className="bg-muted h-40 w-full animate-pulse rounded-md" />
      ) : query.isError ? (
        <div className="flex flex-col items-center gap-2 py-8">
          <span className="text-muted-foreground text-sm">{t('table.loadError')}</span>
          <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
            {t('table.retry')}
          </Button>
        </div>
      ) : list.length === 0 ? (
        // 空状态：引导文案 + 内嵌按钮（specs §4.1.5）：AI 空态跳题目生成页，量表空态开引入弹窗
        <div className="flex flex-col items-center gap-3 rounded-md border border-dashed py-12 text-center">
          <p className="text-base font-medium">{t(`table.emptyTitle.${tab}`)}</p>
          <p className="text-muted-foreground text-sm">{t(`table.emptyDesc.${tab}`)}</p>
          {tab === 'AI' ? (
            <Button onClick={() => void navigate({ to: '/question-bank/generate' })}>{t('table.generateAction')}</Button>
          ) : (
            <Button onClick={onImportScale}>{t('table.importAction')}</Button>
          )}
        </div>
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('table.colQuestionNo')}</TableHead>
                <TableHead>{t('table.colSummary')}</TableHead>
                <TableHead>{t('table.colDimension')}</TableHead>
                {/* 作答方式列仅 SCALE tab 显示（specs §4.1.2 B） */}
                {tab === 'SCALE' && <TableHead>{t('table.colAnswerMode')}</TableHead>}
                <TableHead>{t('table.colStatus')}</TableHead>
                <TableHead className="text-right">{t('table.colActions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-mono">{item.question_no}</TableCell>
                  <TableCell className="max-w-[320px]">
                    <span title={item.summary} className="block truncate">
                      {summaryText(item.summary)}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{item.dimension_name}</Badge>
                  </TableCell>
                  {tab === 'SCALE' && (
                    <TableCell>{t(`table.answerMode.${item.answer_mode}`)}</TableCell>
                  )}
                  <TableCell>
                    <Badge variant={statusVariant(item.status)}>{t(`table.status.${item.status}`)}</Badge>
                  </TableCell>
                  <TableCell className="text-right">
                    {/* 行操作按状态渲染（specs §4.1.3 / §4.1.4 规则5） */}
                    <Button variant="link" size="sm" onClick={() => onView(item)}>
                      {t('table.actionView')}
                    </Button>
                    {tab === 'AI' && item.status !== 'REJECTED' && (
                      <Button variant="link" size="sm" onClick={() => onEdit(item)}>
                        {t('table.actionEdit')}
                      </Button>
                    )}
                    {tab === 'AI' && item.status === 'REJECTED' && (
                      /* 驳回题修正并入重新提交（specs §4.1.4 规则6）：打开编辑弹窗 resubmit 复用 */
                      <Button variant="link" size="sm" onClick={() => onResubmit(item)}>
                        {t('table.actionResubmit')}
                      </Button>
                    )}
                    {item.status !== 'REJECTED' && (
                      <Button variant="link" size="sm" disabled={toggleMut.isPending} onClick={() => void onToggle(item)}>
                        {item.status === 'ACTIVE' ? t('table.actionDisable') : t('table.actionEnable')}
                      </Button>
                    )}
                    <Button
                      variant="link"
                      size="sm"
                      disabled={deleteMut.isPending}
                      onClick={() => setDeleteTarget(item)}
                    >
                      {t('table.actionDelete')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="flex items-center justify-between">
            <span className="text-muted-foreground text-sm">{t('table.total', { total })}</span>
            <div className="flex items-center gap-2">
              <Select
                value={String(filter.page_size)}
                onValueChange={(v) => setFilter((f) => ({ ...f, page: 1, page_size: Number(v) }))}
              >
                <SelectTrigger size="sm" aria-label={t('table.pageSizeLabel')} title={t('table.pageSizeLabel')}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PAGE_SIZE_OPTIONS.map((n) => (
                    <SelectItem key={n} value={String(n)}>
                      {t(`table.pageSize${n}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                variant="outline"
                size="sm"
                disabled={filter.page <= 1}
                onClick={() => setFilter((f) => ({ ...f, page: f.page - 1 }))}
              >
                {t('table.prevPage')}
              </Button>
              <span className="text-muted-foreground text-sm">
                {filter.page} / {totalPages}
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={filter.page >= totalPages}
                onClick={() => setFilter((f) => ({ ...f, page: f.page + 1 }))}
              >
                {t('table.nextPage')}
              </Button>
            </div>
          </div>
        </>
      )}

      {/* 删除二次确认（specs §4.1.3 / 通用规范 14）：destructive，成功或被引用拒删后关闭 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(v) => {
          if (!v && !deleteMut.isPending) setDeleteTarget(null);
        }}
        title={t('table.deleteTitle')}
        desc={t('table.deleteDesc', { no: deleteTarget?.question_no ?? '' })}
        confirmText={t('table.actionDelete')}
        cancelText={t('table.cancel', { ns: 'common' })}
        submittingText={t('table.deleteSubmitting')}
        submitting={deleteMut.isPending}
        destructive
        onConfirm={onDelete}
      />
    </div>
  );
}
