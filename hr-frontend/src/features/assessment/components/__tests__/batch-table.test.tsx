import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchListItem } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

vi.mock('@/features/assessment/api', () => ({
  fetchBatches: vi.fn(),
}));

import { fetchBatches } from '@/features/assessment/api';

import { BatchTable } from '../batch-table';

// Radix Select 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，需补桩
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

function makeItem(overrides: Partial<BatchListItem> = {}): BatchListItem {
  return {
    id: '1001',
    batch_no: 'B20260913001',
    trigger_type: 'scheduled',
    target_mode: 'specified',
    target_brief: ['张敏', '李芳'],
    target_names: ['张敏', '李芳', '王强'],
    period_start: '2026-09-01',
    period_end: '2026-09-07',
    status: 'running',
    stalled: false,
    evaluated_count: 3,
    total_count: 10,
    progress_percent: 30,
    covered_session_count: 42,
    failed_count: 0,
    triggered_at: '2026-09-13 23:00',
    ...overrides,
  };
}

function makeProps() {
  return {
    onCreateOpen: vi.fn(),
    onFailuresOpen: vi.fn(),
    onReSubmit: vi.fn(),
  };
}

function renderTable(node?: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>{node ?? <BatchTable {...makeProps()} />}</QueryClientProvider>,
  );
}

async function openSelect(trigger: HTMLElement, optionText: string) {
  const user = userEvent.setup();
  await user.click(trigger);
  await user.click(await screen.findByRole('option', { name: optionText }));
}

describe('BatchTable 批次列表', () => {
  it('TestBatchTableColumns：单行渲染批次号等宽与九列文本', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [makeItem()],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    const no = await screen.findByText('B20260913001');
    expect(no).toHaveClass('font-mono');
    expect(screen.getByText('定时评估')).toBeInTheDocument();
    expect(screen.getByText('张敏、李芳 等 3 人')).toBeInTheDocument();
    expect(screen.getByText('2026-09-01 ~ 2026-09-07')).toBeInTheDocument();
    expect(screen.getByText('进行中')).toBeInTheDocument();
    expect(screen.getByText(/30%/)).toBeInTheDocument();
    expect(screen.getByText(/3\/10/)).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
    expect(screen.getByText('2026-09-13 23:00')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '批次号' })).toBeInTheDocument();
  });

  it('TestBatchTableRowActions：failed_count=3 可见失败明细、隐藏重新发起', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [makeItem({ status: 'partial_failed', failed_count: 3 })],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<BatchTable {...props} />);

    const link = await screen.findByRole('button', { name: '失败明细' });
    fireEvent.click(link);
    expect(props.onFailuresOpen).toHaveBeenCalledWith('1001');
    expect(screen.queryByRole('button', { name: '重新发起' })).not.toBeInTheDocument();
    expect(screen.getByText('3/10')).toHaveClass('text-destructive');
  });

  it('TestBatchTableRowActions：停滞且失败 0 可见重新发起、隐藏失败明细，点击回调父层', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [makeItem({ stalled: true, failed_count: 0 })],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<BatchTable {...props} />);

    const link = await screen.findByRole('button', { name: '重新发起' });
    fireEvent.click(link);
    expect(props.onReSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ id: '1001', stalled: true }),
    );
    expect(screen.queryByRole('button', { name: '失败明细' })).not.toBeInTheDocument();
  });

  it('TestBatchTableRowActions：成功行两链接皆隐、查看结果置灰且带悬浮提示', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [makeItem({ status: 'success', failed_count: 0, progress_percent: 100 })],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    const view = await screen.findByRole('button', { name: '查看结果' });
    expect(view).toBeDisabled();
    expect(view).toHaveAttribute('title', '个人画像功能建设中');
    expect(screen.queryByRole('button', { name: '失败明细' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重新发起' })).not.toBeInTheDocument();
    // 完成后进度隐藏（specs §4.1.2D）
    expect(screen.queryByText('100%')).not.toBeInTheDocument();
  });

  it('TestBatchTableStalledBadge：停滞进行中行带已停滞标识且进度仍显示', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [makeItem({ stalled: true })],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    await screen.findByText('B20260913001');
    expect(screen.getByText('已停滞')).toBeInTheDocument();
    expect(screen.getByText(/30%/)).toBeInTheDocument();
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('TestBatchTableEmpty：空列表显示引导文案', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    expect(
      await screen.findByText('暂无评测记录，点击右上角发起评测或等待周期自动跑批'),
    ).toBeInTheDocument();
  });

  it('TestBatchTableFilter：选择触发方式与状态后查询回第一页并携带条件', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();
    await screen.findByText('暂无评测记录，点击右上角发起评测或等待周期自动跑批');

    await openSelect(screen.getByRole('combobox', { name: '触发方式' }), '定时评估');
    await openSelect(screen.getByRole('combobox', { name: '状态' }), '部分失败');
    fireEvent.click(screen.getByRole('button', { name: '查询' }));
    await screen.findByRole('button', { name: '重置' });

    await waitFor(() => {
      expect(fetchBatches).toHaveBeenCalledWith(
        expect.objectContaining({ trigger_type: 'scheduled', status: 'partial_failed', page: 1 }),
      );
    });
  });

  it('TestBatchTableReset：重置清空筛选条件并刷新', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();
    await screen.findByText('暂无评测记录，点击右上角发起评测或等待周期自动跑批');

    await openSelect(screen.getByRole('combobox', { name: '触发方式' }), '手动发起');
    fireEvent.click(screen.getByRole('button', { name: '查询' }));
    await screen.findByRole('button', { name: '重置' });
    await waitFor(() => {
      expect(fetchBatches).toHaveBeenCalledWith(
        expect.objectContaining({ trigger_type: 'manual' }),
      );
    });

    fireEvent.click(screen.getByRole('button', { name: '重置' }));
    await waitFor(() => {
      expect(fetchBatches).toHaveBeenCalledWith({
        trigger_type: undefined,
        status: undefined,
        page: 1,
        page_size: 10,
      });
    });
  });

  it('TestBatchTableRetry：加载失败态查询按钮兼作重试入口', async () => {
    let calls = 0;
    vi.mocked(fetchBatches).mockImplementation(() => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new Error('network'))
        : Promise.resolve({ list: [makeItem()], total: 1, page: 1, page_size: 10 });
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    // 初次失败渲染空态
    const queryBtn = await screen.findByRole('button', { name: '查询' });
    await screen.findByText('暂无评测记录，点击右上角发起评测或等待周期自动跑批');
    expect(screen.queryByText('B20260913001')).not.toBeInTheDocument();

    fireEvent.click(queryBtn);

    // 重试拿第二响应并渲染数据
    await screen.findByText('B20260913001', {}, { timeout: 3000 });
    expect(calls).toBeGreaterThanOrEqual(2);
  });

  it('TestBatchTableCreateEntry：点击发起评测触发 onCreateOpen', async () => {
    vi.mocked(fetchBatches).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<BatchTable {...props} />);

    fireEvent.click(screen.getByRole('button', { name: '发起评测' }));
    expect(props.onCreateOpen).toHaveBeenCalledTimes(1);
  });

  it('TestBatchTablePagination：翻页与每页条数切换', async () => {
    vi.mocked(fetchBatches).mockImplementation((p) =>
      Promise.resolve({ list: [makeItem()], total: 30, page: p.page, page_size: p.page_size }),
    );
    useAuthStore.setState({ token: 'test-token' });

    renderTable();
    await screen.findByText('B20260913001');

    const pageSizeTrigger = screen.getAllByRole('combobox')[2];
    expect(pageSizeTrigger).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '下一页' }));
    await waitFor(() => {
      expect(fetchBatches).toHaveBeenCalledWith(expect.objectContaining({ page: 2 }));
    });
    await screen.findByText('B20260913001');

    // 切每页条数：触发第三个 combobox 并点击 20 条/页
    const user2 = userEvent.setup();
    await user2.click(screen.getAllByRole('combobox')[2]);
    const opt = await screen.findByText('20 条/页');
    await user2.click(opt);
    await waitFor(() => {
      expect(fetchBatches).toHaveBeenCalledWith(
        expect.objectContaining({ page: 1, page_size: 20 }),
      );
    });
  });
});
