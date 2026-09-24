// QuestionTable 列表态组件测试（specs §4.1.3 / §4.1.4 规则4/5/9 / §4.1.5）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { QuestionListPage, QuestionListItem } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

// Radix Select 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，需补桩
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

const fetchQuestionsMock = vi.hoisted(() => vi.fn());
const fetchDetailMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/question-bank/api', () => ({
  fetchQuestions: fetchQuestionsMock,
  fetchQuestionDetail: fetchDetailMock,
  updateQuestion: vi.fn(),
  toggleQuestionStatus: vi.fn(),
  deleteQuestion: vi.fn(),
}));

const toggleMock = vi.fn();
const deleteMock = vi.fn();
vi.mock('@/features/question-bank/hooks', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/features/question-bank/hooks')>();
  return {
    ...orig,
    useToggleQuestionStatus: () => ({ mutate: toggleMock, isPending: false }),
    useDeleteQuestion: () => ({ mutate: deleteMock, isPending: false }),
  };
});

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { QuestionTable } from '../question-table';
import { ApiError } from '@/lib/http-client';

function makeItem(overrides: Partial<QuestionListItem> = {}): QuestionListItem {
  return {
    id: '1780000000000000101',
    question_no: 'Q-AG-0001',
    source: 'AI',
    dimension_id: '1780000000000000201',
    dimension_name: '授权与分工',
    answer_mode: 'CHAT',
    status: 'ACTIVE',
    summary: '下属小林第一次用 AI 工具辅助撰写季度规划……',
    updated_at: '2026-09-21 10:31:24',
    ...overrides,
  };
}

function makePage(list: QuestionListItem[], total = list.length): QuestionListPage {
  return { list, total, page: 1, page_size: 10 };
}

function renderTable(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

async function openSelect(trigger: HTMLElement, optionText: string) {
  const user = userEvent.setup();
  await user.click(trigger);
  await user.click(await screen.findByRole('option', { name: optionText }));
}

beforeEach(() => {
  fetchQuestionsMock.mockReset();
  fetchDetailMock.mockReset();
  fetchDetailMock.mockResolvedValue({
    id: '1780000000000000101',
    question_no: 'Q-AG-0001',
    source: 'AI',
    dimension_id: '1780000000000000201',
    dimension_name: '授权与分工',
    answer_mode: 'CHAT',
    status: 'ACTIVE',
    scenario: 's',
    requirement: 'r',
    focus_point: 'f',
    reject_reason: '',
    batch_id: '3001',
    batch_no: '#G0921',
    reference_count: 0,
    version: 5,
    created_at: '2026-09-21 10:31:24',
    updated_at: '2026-09-21 10:31:24',
  });
  toggleMock.mockReset();
  deleteMock.mockReset();
  useAuthStore.setState({ token: 'test-token' });
});

describe('QuestionTable 列表态（specs §4.1.3 / §4.1.5）', () => {
  it('TestScaleTabNoEdit：SCALE tab 行内无「编辑」与「重新提交」按钮（§4.1.4 规则5）', async () => {
    fetchQuestionsMock.mockResolvedValue(
      makePage([makeItem({ source: 'SCALE', question_no: 'Q-Scale-0001', answer_mode: 'LIKERT5' })]),
    );

    renderTable(
      <QuestionTable tab="SCALE" onEdit={vi.fn()} onView={vi.fn()} />,
    );

    await screen.findByText('Q-Scale-0001');
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重新提交' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查看' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '停用' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument();
    // 量表 tab 显示作答方式列（BR6 §4.1.2 B）
    expect(screen.getByRole('columnheader', { name: '作答方式' })).toBeInTheDocument();
    expect(screen.getByText('Likert 5 级')).toBeInTheDocument();
  });

  it('TestAiTabActiveRow：AI 题 ACTIVE 行渲染 查看/编辑/停用/删除，无作答方式列', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);

    await screen.findByText('Q-AG-0001');
    expect(screen.getByRole('button', { name: '查看' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '编辑' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '停用' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: '作答方式' })).not.toBeInTheDocument();
    // 状态标签
    expect(screen.getByText('启用')).toBeInTheDocument();
  });

  it('TestAiRejectedRow：REJECTED AI 行有「重新提交」且 disabled、无停用（SP2 前占位禁用）', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem({ status: 'REJECTED', question_no: 'Q-AG-0002' })]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);

    const resubmit = await screen.findByRole('button', { name: '重新提交' });
    expect(resubmit).toBeDisabled();
    expect(screen.queryByRole('button', { name: '停用' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '启用' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument();
    expect(screen.getByText('已驳回')).toBeInTheDocument();
  });

  it('TestDisabledRowToggle：DISABLED AI 行操作为 查看/编辑/启用/删除', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem({ status: 'DISABLED' })]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);

    await screen.findByText('Q-AG-0001');
    expect(screen.getByRole('button', { name: '启用' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '停用' })).not.toBeInTheDocument();
    expect(screen.getByText('已停用')).toBeInTheDocument();
  });

  it('TestViewEditCallback：点击查看与编辑回调父层携带行数据', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));
    const onEdit = vi.fn();
    const onView = vi.fn();

    renderTable(<QuestionTable tab="AI" onEdit={onEdit} onView={onView} />);

    await screen.findByText('Q-AG-0001');
    fireEvent.click(screen.getByRole('button', { name: '查看' }));
    expect(onView).toHaveBeenCalledWith(expect.objectContaining({ id: '1780000000000000101' }));
    fireEvent.click(screen.getByRole('button', { name: '编辑' }));
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ question_no: 'Q-AG-0001' }));
  });

  it('TestDraftFilterTwoPhase：改查询条件不发请求，点「查询」才携带条件发请求（§4.1.5）', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');
    fetchQuestionsMock.mockClear();

    // 修改草稿：状态选「已驳回」、关键词输入，均只改 draft 不触发请求
    await openSelect(screen.getByRole('combobox', { name: '状态' }), '已驳回');
    const keywordInput = screen.getByRole('textbox', { name: '关键词' });
    await userEvent.setup().type(keywordInput, '授权');
    expect(fetchQuestionsMock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '查询' }));
    await waitFor(() => {
      expect(fetchQuestionsMock).toHaveBeenCalledWith(
        expect.objectContaining({ source: 'AI', status: 'REJECTED', keyword: '授权', page: 1 }),
      );
    });
  });

  it('TestResetClears：重置清空当前 tab 全部查询条件（§4.1.3）', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');

    await openSelect(screen.getByRole('combobox', { name: '状态' }), '启用');
    await userEvent.setup().type(screen.getByRole('textbox', { name: '关键词' }), 'x');
    fireEvent.click(screen.getByRole('button', { name: '查询' }));
    await waitFor(() => {
      expect(fetchQuestionsMock).toHaveBeenCalledWith(expect.objectContaining({ status: 'ACTIVE' }));
    });

    fetchQuestionsMock.mockClear();
    fireEvent.click(screen.getByRole('button', { name: '重置' }));
    await waitFor(() => {
      expect(fetchQuestionsMock).toHaveBeenCalledWith(
        expect.objectContaining({ status: undefined, keyword: undefined, page: 1 }),
      );
    });
  });

  it('TestToggleMutate：停用按钮确认后直接 mutate 携带 id 与目标状态', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');

    fireEvent.click(screen.getByRole('button', { name: '停用' }));
    await waitFor(() => {
      expect(toggleMock).toHaveBeenCalledWith(
        expect.objectContaining({ id: '1780000000000000101', target_status: 'DISABLED', version: 5 }),
        expect.anything(),
      );
    });
  });

  it('TestDeleteReferenced：删除被引用（code 1705）toast 只可停用（§4.1.4 规则4）', async () => {
    const { toast } = await import('sonner');
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));
    deleteMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new ApiError(1705, 'referenced'));
    });

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');

    fireEvent.click(screen.getByRole('button', { name: '删除' }));
    const confirmBtn = await screen.findByRole('button', { name: '删除' });
    // ConfirmDialog（destructive）确认后 mutate；弹窗确认按钮与行内按钮同名，取最后一个（弹窗内）
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(deleteMock).toHaveBeenCalled());
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('该题已被测试引用，只可停用'));
  });

  it('TestDeleteConflict：并发冲突（code 1713）toast 提示并刷新列表（§4.1.4 规则9）', async () => {
    const { toast } = await import('sonner');
    const { queryClient } = await import('@/lib/query-client');
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem()]));
    deleteMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new ApiError(1713, 'conflict'));
    });

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');

    fireEvent.click(screen.getByRole('button', { name: '删除' }));
    fireEvent.click(await screen.findByRole('button', { name: '删除' }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('数据已变更，请刷新后重试'));
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalled());
    invalidateSpy.mockRestore();
  });

  it('TestEmptyState：AI tab 空数据渲染引导文案与禁用的「生成 AI 管理题」按钮（§4.1.5）', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);

    const gen = await screen.findByRole('button', { name: '生成 AI 管理题' });
    expect(gen).toBeDisabled();
    expect(screen.getByText(/题库为空/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '引入九型量表' })).not.toBeInTheDocument();
  });

  it('TestEmptyStateScale：SCALE tab 空数据渲染禁用的「引入九型量表」按钮（§4.1.5）', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([]));

    renderTable(<QuestionTable tab="SCALE" onEdit={vi.fn()} onView={vi.fn()} />);

    const imp = await screen.findByRole('button', { name: '引入九型量表' });
    expect(imp).toBeDisabled();
    expect(screen.queryByRole('button', { name: '生成 AI 管理题' })).not.toBeInTheDocument();
  });

  it('TestSummaryTruncate：摘要过长截断显示且 title 悬浮携带全文（§4.1.5 / 通用规范11）', async () => {
    const long = '长'.repeat(60);
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem({ summary: long })]));

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);

    const cell = await screen.findByTitle(long);
    expect(cell.textContent!.length).toBeLessThan(60);
    expect(cell.textContent!).toContain('…');
  });

  it('TestPagination：翻页携带 page=2 发请求（§4.1.3 分页）', async () => {
    fetchQuestionsMock.mockImplementation((p: { page: number }) =>
      Promise.resolve(makePage([makeItem()], 30 - p.page)),
    );

    renderTable(<QuestionTable tab="AI" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-AG-0001');

    fireEvent.click(screen.getByRole('button', { name: '下一页' }));
    await waitFor(() => {
      expect(fetchQuestionsMock).toHaveBeenCalledWith(
        expect.objectContaining({ page: 2, page_size: 10 }),
      );
    });
  });

  it('TestScaleSourceParam：SCALE tab 查询 source 参数为 SCALE', async () => {
    fetchQuestionsMock.mockResolvedValue(makePage([makeItem({ source: 'SCALE', question_no: 'Q-Scale-0001' })]));

    renderTable(<QuestionTable tab="SCALE" onEdit={vi.fn()} onView={vi.fn()} />);
    await screen.findByText('Q-Scale-0001');

    expect(fetchQuestionsMock).toHaveBeenCalledWith(
      expect.objectContaining({ source: 'SCALE' }),
    );
  });
});
