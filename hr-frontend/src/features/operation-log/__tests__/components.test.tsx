// operation-log 组件测试（验收锚点：六列数据表头/中文名 Badge/摘要 title 全文/弹窗互斥渲染/null 不渲染）。
// i18n 词条归 T3，此处 addResourceBundle 注入 operationLog ns 中文词条（batch-table 测试同款
// mock api + QueryClient 直挂形态）；Radix Select 在 jsdom 缺指针捕获 API，先补桩。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import { toast } from 'sonner';
import { useAuthStore } from '@/stores/auth';
// addResourceBundle 的键值须整体字面量（deep 默认 true 逐键合并），整段注入保持可读
void i18n.addResourceBundle('zh', 'operationLog', {
  list: {
    title: '操作日志',
    operatorLabel: '操作人',
    operatorPlaceholder: '按操作人搜索',
    filterModule: '操作类型',
    filterModuleAll: '全部类型',
    filterResult: '操作结果',
    filterResultAll: '全部',
    startDate: '开始日期',
    endDate: '结束日期',
    query: '查询',
    reset: '重置',
    export: '导出日志',
    exporting: '导出中…',
    exportFailed: '导出失败，请稍后重试',
    colTime: '操作时间',
    colOperator: '操作人',
    colModule: '操作类型',
    colTarget: '操作对象',
    colSummary: '详情摘要',
    colResult: '操作结果',
    colActions: '操作',
    actionDetail: '详情',
    resultSuccess: '成功',
    resultFail: '失败',
    loadError: '数据加载失败，请重试',
    retry: '重试',
    empty: '暂无日志记录',
    emptyFiltered: '未找到匹配日志，请尝试调整筛选条件',
    total: '共 {{total}} 条',
    prevPage: '上一页',
    nextPage: '下一页',
    pageSize: '{{n}} 条/页',
    pageSizeLabel: '每页条数',
  },
  validation: { range: '结束日期不能早于开始日期' },
  module: {
    login: '登录',
    account: '用户管理',
    dimension: '维度与权重',
    system_params: '系统参数',
    llm_config: '大模型配置',
    question_bank: '题库管理',
    assessment: '评估运营',
    system_job: '系统任务',
  },
  detail: {
    colField: '字段',
    colBefore: '变更前',
    colAfter: '变更后',
    textDetail: '文本详情',
    close: '关闭',
  },
});
await i18n.changeLanguage('zh');

vi.mock('../api', () => ({
  fetchOperationLogs: vi.fn(),
  exportOperationLogs: vi.fn(),
}));

import { fetchOperationLogs } from '../api';

import { OperationLogTable } from '../components/operation-log-table';
import { DetailDialog } from '../components/detail-dialog';
import type { OperationLogItem, OperationLogPage } from '../types';

// Radix Select 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，需补桩
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

afterEach(() => {
  useAuthStore.setState({ token: null });
  vi.clearAllMocks();
});

function makeItem(partial: Partial<OperationLogItem>): OperationLogItem {
  return {
    id: '1790000000000001001',
    created_at: '2026-10-07 14:23:05',
    operator: '李学涛',
    module: 'dimension',
    target: '维度「任务适配判断力」聚合权重',
    summary: '调整维度聚合权重 30 → 50',
    result: 'success',
    changes: null,
    detail: '',
    ...partial,
  };
}

/** 带 changes 的关键域行（specs §4.1.4 规则3 变更对比形态）。 */
const rowWithChanges = makeItem({
  changes: [
    { field: '聚合权重', before: '30', after: '50' },
    { field: '状态', before: '启用', after: '停用' },
  ],
});

/** 带 detail 的非关键域行（文本详情形态），字段值与 changes 行错开保证单匹配。 */
const rowWithDetail = makeItem({
  id: '1790000000000001002',
  created_at: '2026-10-07 03:00:12',
  operator: '系统',
  module: 'system_job',
  target: '批次 B20261001001（2026-09-22 ~ 2026-09-28）',
  summary: '区间执行完成，覆盖 36 人',
  changes: null,
  detail: '批次 B20261001001 区间 2026-09-22 ~ 2026-09-28 执行完成，覆盖 36 人',
});

const pageOf = (list: OperationLogItem[]): OperationLogPage => ({
  list,
  total: list.length,
  page: 1,
  page_size: 10,
});

function renderWithQuery(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

// ---------- OperationLogTable（验收锚点） ----------

describe('OperationLogTable（specs §4.1.1 / §4.1.5）', () => {
  beforeEach(() => {
    vi.mocked(fetchOperationLogs).mockResolvedValue(pageOf([rowWithChanges, rowWithDetail]));
  });

  it('TestTableSixColumns：渲染六列数据表头与操作列表头、行数据', async () => {
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);

    // 先等数据到位（列表条件渲染，表头随数据分支出现）
    expect(await screen.findByText('李学涛')).toBeInTheDocument();
    for (const label of ['操作时间', '操作人', '操作类型', '操作对象', '详情摘要', '操作结果', '操作']) {
      expect(screen.getByRole('columnheader', { name: label })).toBeInTheDocument();
    }
    expect(screen.getByText('2026-10-07 14:23:05')).toBeInTheDocument();
    expect(screen.getByText('维度「任务适配判断力」聚合权重')).toBeInTheDocument();
    // 两行各一个详情链接按钮（specs §4.1.3 行级详情）
    expect(screen.getAllByRole('button', { name: '详情' })).toHaveLength(2);
  });

  it('TestTableModuleBadgeChinese：module Badge 文案为 i18n zh 中文名', async () => {
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);

    expect(await screen.findByText('维度与权重')).toBeInTheDocument();
    expect(screen.getByText('系统任务')).toBeInTheDocument();
  });

  it('TestTableSummaryTitleFullText：摘要过长截断且 title 悬浮为全文', async () => {
    const longSummary =
      '调整维度「任务适配判断力」聚合权重自 30 至 50，同步联动兄弟维度归一化系数，操作经由维度配置页保存触发';
    vi.mocked(fetchOperationLogs).mockResolvedValue(
      pageOf([makeItem({ summary: longSummary })]),
    );
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);

    const cell = await screen.findByText(longSummary);
    expect(cell).toHaveAttribute('title', longSummary);
    expect(cell).toHaveClass('truncate');
  });

  it('TestTableResultBadgeTone：fail 结果 Badge destructive、success 语义色（specs §4.1.5）', async () => {
    vi.mocked(fetchOperationLogs).mockResolvedValue(
      pageOf([
        makeItem({ result: 'fail' }),
        makeItem({ id: '1790000000000001003', result: 'success' }),
      ]),
    );
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);

    const failBadge = (await screen.findByText('失败')).closest('[data-slot="badge"]');
    expect(failBadge).toHaveAttribute('data-variant', 'destructive');
    const successBadge = screen.getByText('成功').closest('[data-slot="badge"]');
    expect(successBadge).toHaveAttribute('data-variant', 'secondary');
    expect(successBadge).toHaveClass('text-success');
  });

  it('TestTableQuerySubmitsDraft：操作人草稿回车提交 trim 后回第 1 页', async () => {
    const user = userEvent.setup();
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);
    await screen.findByText('李学涛');

    await user.type(screen.getByRole('textbox', { name: '操作人' }), '  李  ');
    await user.keyboard('{Enter}');

    await waitFor(() => {
      expect(fetchOperationLogs).toHaveBeenLastCalledWith(
        expect.objectContaining({ operator: '李', page: 1 }),
      );
    });
  });

  it('TestTableRangeValidation：start > end 前端拦截 toast 提示不发请求', async () => {
    const toastSpy = vi.spyOn(toast, 'error').mockImplementation(() => 'mocked');
    const user = userEvent.setup();
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);
    await screen.findByText('李学涛');
    const callsBefore = vi.mocked(fetchOperationLogs).mock.calls.length;

    // 日期 input 无 textbox role（dom 角色映射），按 aria-label 直查；jsdom 无日期控件用 change 直填
    const startDate = document.querySelector('input[type="date"][aria-label="开始日期"]') as HTMLInputElement;
    const endDate = document.querySelector('input[type="date"][aria-label="结束日期"]') as HTMLInputElement;
    expect(startDate).toBeTruthy();
    expect(endDate).toBeTruthy();
    fireEvent.change(startDate, { target: { value: '2026-10-08' } });
    fireEvent.change(endDate, { target: { value: '2026-10-01' } });
    await user.click(screen.getByRole('button', { name: '查询' }));

    expect(toastSpy).toHaveBeenCalledWith('结束日期不能早于开始日期');
    expect(vi.mocked(fetchOperationLogs).mock.calls.length).toBe(callsBefore);
    toastSpy.mockRestore();
  });

  it('TestTableResetClearsAll：重置清空条件回无条件第 1 页', async () => {
    const user = userEvent.setup();
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);
    await screen.findByText('李学涛');

    await user.type(screen.getByRole('textbox', { name: '操作人' }), '李');
    await user.click(screen.getByRole('button', { name: '查询' }));
    await waitFor(() => {
      expect(fetchOperationLogs).toHaveBeenLastCalledWith(
        expect.objectContaining({ operator: '李' }),
      );
    });

    await user.click(screen.getByRole('button', { name: '重置' }));
    await waitFor(() => {
      expect(fetchOperationLogs).toHaveBeenLastCalledWith({
        operator: undefined,
        module: undefined,
        result: undefined,
        start_date: undefined,
        end_date: undefined,
        page: 1,
        page_size: 10,
      });
    });
  });

  it('TestTableEmptyDualCopy：无筛选与有筛选空态文案区分（specs §4.1.5 / 规范18）', async () => {
    vi.mocked(fetchOperationLogs).mockResolvedValue(pageOf([]));
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);
    expect(await screen.findByText('暂无日志记录')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.type(screen.getByRole('textbox', { name: '操作人' }), '不存在的人');
    await user.click(screen.getByRole('button', { name: '查询' }));
    expect(await screen.findByText('未找到匹配日志，请尝试调整筛选条件')).toBeInTheDocument();
  });

  it('TestTableDetailOpensDialog：点击行内详情打开弹窗展示该行变更对比', async () => {
    const user = userEvent.setup();
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);
    await screen.findByText('李学涛');

    const row = screen.getByText('李学涛').closest('tr')!;
    await user.click(within(row).getByRole('button', { name: '详情' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('columnheader', { name: '字段' })).toBeInTheDocument();
    expect(within(dialog).getByText('维度「任务适配判断力」聚合权重')).toBeInTheDocument();
  });

  it('TestTableErrorRetry：加载失败显示错误态与重试按钮', async () => {
    const user = userEvent.setup();
    let calls = 0;
    vi.mocked(fetchOperationLogs).mockImplementation(() => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new Error('network'))
        : Promise.resolve(pageOf([rowWithChanges]));
    });
    useAuthStore.setState({ token: 'test-token' });
    renderWithQuery(<OperationLogTable />);

    expect(await screen.findByText('数据加载失败，请重试')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('李学涛')).toBeInTheDocument();
  });
});

// ---------- DetailDialog（验收锚点：互斥渲染 + null 不渲染） ----------

describe('DetailDialog（specs §4.2）', () => {
  it('TestDetailDialogChangesRow：changes 行渲染三列表头且无 detail 段落文本', () => {
    render(<DetailDialog item={rowWithChanges} onClose={vi.fn()} />);

    for (const label of ['字段', '变更前', '变更后']) {
      expect(screen.getByRole('columnheader', { name: label })).toBeInTheDocument();
    }
    expect(screen.getByText('聚合权重')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('50')).toBeInTheDocument();
    expect(screen.queryByText(rowWithDetail.detail)).not.toBeInTheDocument();
    expect(screen.queryByText('文本详情')).not.toBeInTheDocument();
  });

  it('TestDetailDialogChangesStyling：变更前删除线红色、变更后加粗绿色（§4.1.4 规则3）', () => {
    render(<DetailDialog item={rowWithChanges} onClose={vi.fn()} />);

    expect(screen.getByText('30')).toHaveClass('line-through', 'text-destructive');
    expect(screen.getByText('50')).toHaveClass('font-semibold', 'text-success');
  });

  it('TestDetailDialogDetailRow：detail 行渲染文本段落且无三列表头', () => {
    render(<DetailDialog item={rowWithDetail} onClose={vi.fn()} />);

    expect(screen.getByText(rowWithDetail.detail)).toBeInTheDocument();
    for (const label of ['字段', '变更前', '变更后']) {
      expect(screen.queryByRole('columnheader', { name: label })).not.toBeInTheDocument();
    }
  });

  it('TestDetailDialogEmptyDetailFallback：detail 也为空时渲染摘要兜底段落', () => {
    render(<DetailDialog item={makeItem({ changes: null, detail: '' })} onClose={vi.fn()} />);

    expect(screen.getByText('调整维度聚合权重 30 → 50')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: '字段' })).not.toBeInTheDocument();
  });

  it('TestDetailDialogBasicInfoFiveRows：header 显操作时间、基本信息五行与类型 Badge 同列表配色（§4.2.1）', () => {
    render(<DetailDialog item={rowWithChanges} onClose={vi.fn()} />);

    // Dialog header 显操作时间（任务契约），基本信息组五行齐备
    expect(
      screen.getByRole('dialog').querySelector('[data-slot="dialog-title"]'),
    ).toHaveTextContent('2026-10-07 14:23:05');
    expect(screen.getByText('李学涛')).toBeInTheDocument();
    expect(screen.getByText('维度「任务适配判断力」聚合权重')).toBeInTheDocument();
    const moduleBadge = screen.getByText('维度与权重').closest('[data-slot="badge"]');
    expect(moduleBadge).toHaveAttribute('data-variant', 'secondary');
    const resultBadge = screen.getByText('成功').closest('[data-slot="badge"]');
    expect(resultBadge).toHaveAttribute('data-variant', 'secondary');
    expect(resultBadge).toHaveClass('text-success');
  });

  it('TestDetailDialogFooterClose：footer 仅关闭按钮且点击触发 onClose', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<DetailDialog item={rowWithChanges} onClose={onClose} />);

    // footer 关闭按钮 + 右上角关闭图标，无其他动作按钮（specs §8.3 偏离1）
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getAllByRole('button')).toHaveLength(2);
    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestDetailDialogNullItem：item 为 null 渲染 null（container 为空）', () => {
    const { container } = render(<DetailDialog item={null} onClose={vi.fn()} />);
    expect(container.innerHTML).toBe('');
  });
});
