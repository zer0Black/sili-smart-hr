// TestTaskTable 测试（specs §4.1.2 / §4.1.3 / §4.1.4 / §4.1.5 / §8.3）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

vi.mock('@/features/assessment/test-task-api', () => ({
  fetchTestTasks: vi.fn(),
  fetchTestTaskPollCounts: vi.fn(),
  fetchScaleStatus: vi.fn(),
  fetchTestTaskLink: vi.fn(),
  resendTestTaskLink: vi.fn(),
  cancelTestTask: vi.fn(() => Promise.resolve({ task_id: '', status: 'canceled' })),
  createTestTask: vi.fn(),
}));

import { fetchTestTasks } from '@/features/assessment/test-task-api';

import { TestTaskTable } from '../test-task-table';
import type { TestTaskListItem } from '@/features/assessment/test-task-types';

// Radix Select/AlertDialog 在 jsdom 缺指针捕获与 scrollIntoView，补桩
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

function makeItem(overrides: Partial<TestTaskListItem> = {}): TestTaskListItem {
  return {
    id: '1790000000000000001',
    task_no: 'T202609280001',
    test_type: 'ai_mgmt',
    staff_name: '张敏',
    status: 'pending',
    link_status: 'valid',
    grading_status: 'waiting',
    created_at: '2026-09-28 08:30',
    completed_at: null,
    ...overrides,
  };
}

function makeProps() {
  return {
    testType: 'ai_mgmt' as const,
    onCreateOpen: vi.fn(),
    onLinkOpen: vi.fn(),
    onResend: vi.fn(),
    onCancel: vi.fn(),
  };
}

function renderTable(node?: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      {node ?? <TestTaskTable {...makeProps()} />}
    </QueryClientProvider>,
  );
}

/** 行定位：由任务号文本向上找所属 table row。 */
function rowOf(taskNo: string): HTMLElement {
  const cell = screen.getByText(taskNo).closest('td');
  const row = cell?.closest('tr');
  if (!row) throw new Error(`row of ${taskNo} not found`);
  return row;
}

describe('TestTaskTable 两 tab 任务列表（specs §4.1.2/§4.1.3）', () => {
  it('TestTaskTableRowRendering：mock 三行断言逾期警示、行操作可见性、completed_at 占位与等宽任务号', async () => {
    const pending = makeItem({ id: '1', task_no: 'T202609280001', staff_name: '张敏', status: 'pending' });
    const expired = makeItem({
      id: '2',
      task_no: 'T202609280002',
      staff_name: '李芳',
      status: 'expired',
      link_status: 'invalid',
    });
    const completed = makeItem({
      id: '3',
      task_no: 'T202609280003',
      staff_name: '王强',
      status: 'completed',
      link_status: 'used',
      grading_status: 'scored',
      completed_at: '2026-09-29 10:00',
    });
    vi.mocked(fetchTestTasks).mockResolvedValue({
      list: [completed, expired, pending],
      total: 3,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    // 任务号等宽字体（specs §4.1.2 B）
    expect(await screen.findByText('T202609280001')).toHaveClass('font-mono');

    // 逾期整行警示底色（specs §4.1.5 / 计划警示 token bg-destructive/5）
    const expiredRow = rowOf('T202609280002');
    expect(expiredRow.className).toContain('bg-destructive/5');
    const pendingRow = rowOf('T202609280001');
    expect(pendingRow.className).not.toContain('bg-destructive/5');

    // 重发仅 expired 行出现（specs §4.1.3 重发可见条件）
    expect(screen.getAllByRole('button', { name: '重发' })).toHaveLength(1);
    expect(expiredRow.querySelector('button')?.textContent !== null).toBe(true);

    // 取消按钮 completed 行不出现（specs §4.1.3：待作答/进行中/已逾期可见）
    expect(pendingRow.textContent).toContain('取消');
    expect(expiredRow.textContent).toContain('取消');
    const completedRow = rowOf('T202609280003');
    expect(completedRow.textContent).not.toContain('取消');

    // completed+scored 行渲染结果按钮（disabled + 悬浮提示，specs §4.1.3 查看结果）
    const resultBtn = screen.getByRole('button', { name: '结果' });
    expect(completedRow.contains(resultBtn)).toBe(true);
    expect(resultBtn).toBeDisabled();
    expect(resultBtn).toHaveAttribute('title', '个人画像功能建设中');

    // completed_at 为 null 渲染 —（specs §4.1.2 B）
    expect(pendingRow.textContent).toContain('—');
    expect(completedRow.textContent).toContain('2026-09-29 10:00');

    // 作答链接行操作恒可见（三行各一）
    expect(screen.getAllByRole('button', { name: '作答链接' })).toHaveLength(3);
  });

  it('TestTaskTableEnneagramGrading：九型 filter 时 grading 列文案为「已判定」且表头为判型状态', async () => {
    vi.mocked(fetchTestTasks).mockResolvedValue({
      list: [
        makeItem({
          task_no: 'E202609280001',
          test_type: 'enneagram',
          status: 'completed',
          grading_status: 'scored',
          completed_at: '2026-09-29 12:00',
        }),
      ],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable(<TestTaskTable {...makeProps()} testType="enneagram" />);

    expect(await screen.findByText('E202609280001')).toBeInTheDocument();
    // 九型 tab 阅卷状态列呈现为判型状态，已评分呈现为已判定（specs §4.1.2 B / §8.1）
    expect(screen.getByRole('columnheader', { name: '判型状态' })).toBeInTheDocument();
    expect(screen.getByText('已判定')).toBeInTheDocument();
    expect(screen.queryByText('已评分')).not.toBeInTheDocument();
  });

  it('TestTaskTableCancelConfirm：取消二次确认文案含任务号与姓名，确认后回调 onCancel', async () => {
    vi.mocked(fetchTestTasks).mockResolvedValue({
      list: [makeItem()],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<TestTaskTable {...props} />);

    fireEvent.click(await screen.findByRole('button', { name: '取消' }));
    // 确认弹窗文案含任务号与对象姓名（specs §4.1.3 取消任务）
    const dialog = await screen.findByRole('alertdialog');
    expect(dialog.textContent).toContain('T202609280001');
    expect(dialog.textContent).toContain('张敏');
    fireEvent.click(screen.getByRole('button', { name: '确认取消' }));
    await waitFor(() => expect(props.onCancel).toHaveBeenCalledWith('1790000000000000001'));
  });

  it('TestTaskTableLinkAndResend：行内链接与重发回调父层', async () => {
    vi.mocked(fetchTestTasks).mockResolvedValue({
      list: [makeItem({ status: 'expired', link_status: 'invalid' })],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<TestTaskTable {...props} />);

    fireEvent.click(await screen.findByRole('button', { name: '作答链接' }));
    expect(props.onLinkOpen).toHaveBeenCalledWith('1790000000000000001');

    fireEvent.click(screen.getByRole('button', { name: '重发' }));
    expect(props.onResend).toHaveBeenCalledWith('1790000000000000001');
  });

  it('TestTaskTableFilterTwoPhase：状态下拉 + 关键字回车等效查询，重置清空', async () => {
    vi.mocked(fetchTestTasks).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();
    await screen.findByText('暂无测试任务，点击右上角发起评测');

    // 状态下拉五值 + 全部（specs §4.1.2 A）
    const statusTrigger = screen.getByRole('combobox', { name: '状态' });
    const user = userEvent.setup();
    await user.click(statusTrigger);
    expect(await screen.findByRole('option', { name: '待作答' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '进行中' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '已完成' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '已逾期' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '已取消' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '全部' })).toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: '已逾期' }));

    // 关键字输入回车等效查询（specs §4.1.3 任务记录查询）
    const keywordInput = screen.getByRole('textbox', { name: '对象' });
    await user.type(keywordInput, '张敏{Enter}');
    await waitFor(() => {
      expect(fetchTestTasks).toHaveBeenCalledWith(
        expect.objectContaining({
          test_type: 'ai_mgmt',
          status: 'expired',
          keyword: '张敏',
          page: 1,
        }),
      );
    });

    // 重置清空筛选（specs §4.1.3 筛选重置）
    fireEvent.click(screen.getByRole('button', { name: '重置' }));
    await waitFor(() => {
      expect(fetchTestTasks).toHaveBeenCalledWith({
        test_type: 'ai_mgmt',
        status: undefined,
        keyword: undefined,
        page: 1,
        page_size: 10,
      });
    });
  });

  it('TestTaskTablePagination：翻页与每页条数 10/20/50 切换', async () => {
    vi.mocked(fetchTestTasks).mockImplementation((p) =>
      Promise.resolve({ list: [makeItem()], total: 30, page: p.page, page_size: p.page_size }),
    );
    useAuthStore.setState({ token: 'test-token' });

    renderTable();
    await screen.findByText('T202609280001');

    fireEvent.click(screen.getByRole('button', { name: '下一页' }));
    await waitFor(() => {
      expect(fetchTestTasks).toHaveBeenCalledWith(expect.objectContaining({ page: 2 }));
    });

    // 每页条数三档 10/20/50（specs §8.3 偏离1）
    const user = userEvent.setup();
    await user.click(screen.getAllByRole('combobox')[1]);
    expect(await screen.findByText('20 条/页')).toBeInTheDocument();
    expect(screen.getByText('50 条/页')).toBeInTheDocument();
    await user.click(screen.getByText('20 条/页'));
    await waitFor(() => {
      expect(fetchTestTasks).toHaveBeenCalledWith(
        expect.objectContaining({ page: 1, page_size: 20 }),
      );
    });
  });

  it('TestTaskTableRetry：加载失败态查询按钮兼作重试入口（specs §4.1.3）', async () => {
    let calls = 0;
    vi.mocked(fetchTestTasks).mockImplementation(() => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new Error('network'))
        : Promise.resolve({ list: [makeItem()], total: 1, page: 1, page_size: 10 });
    });
    useAuthStore.setState({ token: 'test-token' });

    renderTable();

    const queryBtn = await screen.findByRole('button', { name: '查询' });
    await screen.findByText('数据加载失败，请重试');
    fireEvent.click(queryBtn);
    await screen.findByText('T202609280001', {}, { timeout: 3000 });
    expect(calls).toBeGreaterThanOrEqual(2);
  });

  it('TestTaskTableCreateEntry：点击发起评测触发 onCreateOpen，空态文案引导', async () => {
    vi.mocked(fetchTestTasks).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });
    const props = makeProps();

    renderTable(<TestTaskTable {...props} />);

    expect(
      await screen.findByText('暂无测试任务，点击右上角发起评测'),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '发起评测' }));
    expect(props.onCreateOpen).toHaveBeenCalledTimes(1);
  });
});
