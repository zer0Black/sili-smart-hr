// 单 tab 测试任务列表（specs P2_TST_001 §4.1.2 A/B / §4.1.3 / §4.1.4 / §4.1.5）。
// 两 tab 各持一份实例常驻挂载，查询条件（draft→filter 两段式）与分页内部自治，
// 切 tab 不互相污染（§4.1.5）。
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';
import { Loader2 } from 'lucide-react';

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
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
import { useRefetchOnVisible } from '@/features/assessment/hooks';
import { useCancelTestTask, useTestTasks } from '@/features/assessment/test-task-hooks';
import {
  linkStatusVariant,
  type TestTaskListItem,
  type TestTaskStatus,
  type TestType,
} from '@/features/assessment/test-task-types';
import { usePageClamp, useResetSignal } from '@/lib/use-table-state';
import { cn } from '@/lib/utils';

export interface TestTaskTableProps {
  /** 当前 tab 对应的测试类型（specs §4.1.1：tab 即类型）。 */
  testType: TestType;
  /** 打开发起评测弹窗（三 tab 共用入口，specs §4.1.3）。 */
  onCreateOpen: () => void;
  /** 查看作答链接弹窗（specs §4.1.3）。 */
  onLinkOpen: (taskId: string) => void;
  /** 重发作答链接（specs §4.1.3：仅已逾期可见）。 */
  onResend: (taskId: string) => void;
  /** 行级重发中任务：该行重发按钮 loading 至接口响应（specs §4.1.3 加载状态），其余行可点。 */
  resendingId?: string | null;
  /** 外部重置信号：值变化时清空筛选并回第一页（发起成功重置入口，specs §4.2.3）。 */
  resetKey?: number;
  /** 轮询开关：当前类型存在未终态任务时由父级 poll-counts 探针驱动（specs §4.1.3）。 */
  polling?: boolean;
  /** 深链预填状态筛选（specs §4.1.3 逾期卡跳转）：同时初始化草稿与生效两层，
   *  仅首挂载生效；resetKey 重置仍回全部。 */
  initialStatusFilter?: string;
}

export const DEFAULT_PAGE_SIZE = 10;
const PAGE_SIZE_OPTIONS = [10, 20, 50];
const ALL = '__all__';

/** 取消可见状态：待作答/进行中/已逾期（specs §4.1.3 / §4.1.4 规则2）。 */
const CANCELABLE_STATUSES: readonly TestTaskStatus[] = ['pending', 'in_progress', 'expired'];

/** 任务状态标签变体：pending 中性、in_progress 主色、expired/canceled 警示、completed 成功。 */
function statusVariant(status: TestTaskListItem['status']): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'completed') return 'secondary';
  if (status === 'expired' || status === 'canceled') return 'destructive';
  if (status === 'pending') return 'outline';
  return 'default';
}

/** 状态枚举守卫：深链入参白名单收敛，未知值按未预填处理。 */
function isTestTaskStatus(v: string): v is TestTaskStatus {
  return (
    v === 'pending' || v === 'in_progress' || v === 'completed' || v === 'expired' || v === 'canceled'
  );
}

/** 阅卷状态标签变体：scored 成功、degraded 警示、waiting/grading 中性。 */
function gradingVariant(status: TestTaskListItem['grading_status']): 'default' | 'secondary' | 'destructive' {
  if (status === 'scored') return 'secondary';
  if (status === 'degraded') return 'destructive';
  return 'default';
}

/** 测试任务列表：筛选 + 七列 + 行操作 + 分页。行操作可见性见 specs §4.1.3。 */
export function TestTaskTable({
  testType,
  onCreateOpen,
  onLinkOpen,
  onResend,
  resendingId,
  resetKey,
  polling,
  initialStatusFilter,
}: TestTaskTableProps): JSX.Element {
  const { t } = useTranslation('assessment');
  // 九型 tab 阅卷状态列呈现为判型状态、已评分呈现为已判定（specs §4.1.2 B）
  const isEnneagram = testType === 'enneagram';

  // 深链预填：草稿与生效两层同初始化，Select 显示与查询生效同步（specs §4.1.3）。
  // 入参先过枚举守卫，未知值（含页面白名单外的 status）不进查询条件。
  const presetStatus =
    initialStatusFilter !== undefined && isTestTaskStatus(initialStatusFilter)
      ? initialStatusFilter
      : undefined;
  const [draftStatus, setDraftStatus] = useState<string>(presetStatus ?? ALL);
  const [draftKeyword, setDraftKeyword] = useState('');
  const [filter, setFilter] = useState<{
    status?: TestTaskStatus;
    keyword?: string;
    page: number;
    page_size: number;
  }>({
    status: presetStatus,
    page: 1,
    page_size: DEFAULT_PAGE_SIZE,
  });
  // 取消二次确认（specs §4.1.3 / §8.3 偏离4）：null 为关、非 null 为待取消行
  const [cancelTarget, setCancelTarget] = useState<TestTaskListItem | null>(null);

  const query = useTestTasks(
    { test_type: testType, status: filter.status, keyword: filter.keyword, page: filter.page, page_size: filter.page_size },
    { polling },
  );
  const cancelMut = useCancelTestTask();

  // 恢复即拉覆盖列表（specs §4.1.3 后半句）：探针注册在页面级，列表在此补齐。
  useRefetchOnVisible([query]);

  // 外部 resetKey 变化（跳过首挂载）时把筛选与页码重置为默认（batch-table 先例）
  useResetSignal(resetKey, () => {
    setDraftStatus(ALL);
    setDraftKeyword('');
    setFilter({ page: 1, page_size: DEFAULT_PAGE_SIZE });
  });

  const list = query.data?.list ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / filter.page_size));

  // 页码钳位：total 收缩让当前页落空时回落末页（batch-table 先例）
  usePageClamp(filter.page, totalPages, (page) => setFilter((f) => ({ ...f, page })));

  function applyDraft() {
    setFilter({
      status: draftStatus === ALL ? undefined : (draftStatus as TestTaskStatus),
      keyword: draftKeyword.trim() === '' ? undefined : draftKeyword.trim(),
      page: 1,
      page_size: filter.page_size,
    });
    if (query.isError) void query.refetch();
  }

  function onReset() {
    setDraftStatus(ALL);
    setDraftKeyword('');
    setFilter({ page: 1, page_size: filter.page_size });
  }

  function onPageSizeChange(v: string) {
    // 函数式展开保留已应用的筛选条件，只重置页码
    setFilter((f) => ({ ...f, page: 1, page_size: Number(v) }));
  }

  function onConfirmCancel() {
    const item = cancelTarget;
    if (!item) return;
    cancelMut.mutate(item.id, {
      onSuccess: () => setCancelTarget(null),
      onError: () => setCancelTarget(null),
    });
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('testTask.table.title')}</CardTitle>
        <Button onClick={onCreateOpen}>{t('testTask.table.createAction')}</Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <Select value={draftStatus} onValueChange={setDraftStatus}>
            <SelectTrigger className="w-36" aria-label={t('testTask.table.filterStatus')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('testTask.table.filterStatusAll')}</SelectItem>
              <SelectItem value="pending">{t('testTask.table.status.pending')}</SelectItem>
              <SelectItem value="in_progress">{t('testTask.table.status.in_progress')}</SelectItem>
              <SelectItem value="completed">{t('testTask.table.status.completed')}</SelectItem>
              <SelectItem value="expired">{t('testTask.table.status.expired')}</SelectItem>
              <SelectItem value="canceled">{t('testTask.table.status.canceled')}</SelectItem>
            </SelectContent>
          </Select>
          <Input
            value={draftKeyword}
            onChange={(e) => setDraftKeyword(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') applyDraft();
            }}
            placeholder={t('testTask.table.keywordPlaceholder')}
            aria-label={t('testTask.table.keywordLabel')}
            className="w-56"
          />
          <Button variant="outline" onClick={applyDraft}>
            {t('testTask.table.query')}
          </Button>
          <Button variant="ghost" onClick={onReset}>
            {t('testTask.table.reset')}
          </Button>
        </div>

        {query.isLoading ? (
          <div className="bg-muted h-40 w-full animate-pulse rounded-md" />
        ) : query.isError ? (
          // 加载失败态：查询按钮兼作重试入口（specs §4.1.3 任务记录查询）
          <div className="flex flex-col items-center gap-2 py-8">
            <span className="text-muted-foreground text-sm">{t('testTask.table.loadError')}</span>
            <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
              {t('stats.refresh')}
            </Button>
          </div>
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center text-sm">{t('testTask.table.empty')}</p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('testTask.table.colTaskNo')}</TableHead>
                  <TableHead>{t('testTask.table.colStaff')}</TableHead>
                  <TableHead>{t('testTask.table.colStatus')}</TableHead>
                  <TableHead>{t('testTask.table.colLink')}</TableHead>
                  {/* 阅卷状态列：九型 tab 显示为判型状态（specs §4.1.2 B，取值同源） */}
                  <TableHead>
                    {isEnneagram ? t('testTask.table.colGradingEnneagram') : t('testTask.table.colGrading')}
                  </TableHead>
                  <TableHead>{t('testTask.table.colCreatedAt')}</TableHead>
                  <TableHead>{t('testTask.table.colCompletedAt')}</TableHead>
                  <TableHead className="text-right">{t('testTask.table.colActions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((item) => (
                  <TableRow
                    key={item.id}
                    // 逾期整行警示底色（specs §4.1.5 / §4.1.2 B）
                    className={cn(item.status === 'expired' && 'bg-destructive/5')}
                  >
                    <TableCell className="font-mono">{item.task_no}</TableCell>
                    <TableCell>{item.staff_name}</TableCell>
                    <TableCell>
                      <Badge variant={statusVariant(item.status)}>
                        {t(`testTask.table.status.${item.status}`)}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {/* link_status 空串兜底：任务创建必落链接行，空串仅在异常数据
                          出现，渲染无链接占位防 i18n 键名直出 */}
                      {item.link_status ? (
                        <Badge variant={linkStatusVariant(item.link_status)}>
                          {t(`testTask.table.linkStatus.${item.link_status}`)}
                        </Badge>
                      ) : (
                        <span className="text-muted-foreground text-sm">—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge variant={gradingVariant(item.grading_status)}>
                        {isEnneagram
                          ? t(`testTask.table.gradingEnneagram.${item.grading_status}`)
                          : t(`testTask.table.grading.${item.grading_status}`)}
                      </Badge>
                    </TableCell>
                    <TableCell>{item.created_at}</TableCell>
                    <TableCell>{item.completed_at ?? '—'}</TableCell>
                    <TableCell className="text-right">
                      {/* 行操作可见性见 specs §4.1.3：链接恒可见、重发仅已逾期、取消三态可见、结果已完成且已评分 */}
                      <Button variant="link" size="sm" onClick={() => onLinkOpen(item.id)}>
                        {t('testTask.table.actionLink')}
                      </Button>
                      {item.status === 'expired' && (
                        <Button
                          variant="link"
                          size="sm"
                          disabled={resendingId === item.id}
                          onClick={() => onResend(item.id)}
                        >
                          {resendingId === item.id && (
                            <Loader2 className="size-3.5 animate-spin" aria-hidden />
                          )}
                          {resendingId === item.id
                            ? t('testTask.table.resendLoading')
                            : t('testTask.table.actionResend')}
                        </Button>
                      )}
                      {CANCELABLE_STATUSES.includes(item.status) && (
                        <Button variant="link" size="sm" onClick={() => setCancelTarget(item)}>
                          {t('testTask.table.actionCancel')}
                        </Button>
                      )}
                      {item.status === 'completed' && item.grading_status === 'scored' && (
                        <Button
                          variant="link"
                          size="sm"
                          disabled
                          title={t('testTask.table.resultDisabled')}
                        >
                          {t('testTask.table.actionResult')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="flex items-center justify-between">
              <span className="text-muted-foreground text-sm">
                {t('testTask.table.total', { total })}
              </span>
              <div className="flex items-center gap-2">
                <Select value={String(filter.page_size)} onValueChange={onPageSizeChange}>
                  <SelectTrigger size="sm" aria-label={t('testTask.table.pageSizeLabel')} title={t('testTask.table.pageSizeLabel')}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PAGE_SIZE_OPTIONS.map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {t(`testTask.table.pageSize${n}`)}
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
                  {t('testTask.table.prevPage')}
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
                  {t('testTask.table.nextPage')}
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>

      {/* 取消二次确认（specs §4.1.3 / §8.3 偏离4）：文案含任务号与对象姓名 */}
      <AlertDialog open={cancelTarget !== null} onOpenChange={(v) => { if (!v) setCancelTarget(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('testTask.table.cancelTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {cancelTarget
                ? t('testTask.table.cancelDesc', {
                    taskNo: cancelTarget.task_no,
                    staffName: cancelTarget.staff_name,
                  })
                : ''}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={cancelMut.isPending}>
              {t('cancel', { ns: 'common' })}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={cancelMut.isPending}
              onClick={(e) => {
                e.preventDefault();
                onConfirmCancel();
              }}
            >
              {cancelMut.isPending ? t('testTask.table.cancelSubmitting') : t('testTask.table.cancelConfirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
