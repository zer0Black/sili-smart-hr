// ReviewView 批量审核视图测试（specs §4.2.1-4.2.5 / §4.1.4 规则1/2/3/6 / §4A.2）
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchQuestionsResult, ReviewQuestionItem } from '@/lib/contracts';

await i18n.changeLanguage('zh');

const batchQ = vi.hoisted(() => ({
  data: undefined as BatchQuestionsResult | undefined,
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
}));
const confirmMutateMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/question-bank/batch-hooks', () => ({
  usePendingBatches: vi.fn(),
  useBatchQuestions: () => batchQ,
  useConfirmBatch: () => ({ mutate: confirmMutateMock, isPending: false }),
  useVoidBatch: vi.fn(),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { ReviewView } from '../review-view';
import { ApiError } from '@/lib/http-client';

function makeQuestion(overrides: Partial<ReviewQuestionItem> = {}): ReviewQuestionItem {
  return {
    id: '1785000000000000101',
    question_no: 'Q-AG-0101',
    dimension_id: '1780000000000000201',
    dimension_name: '授权与分工',
    answer_mode: 'CHAT',
    scenario: 'AI 给出了一份 12 人团队两周任务分解草案。',
    requirement: '请选择最符合你处理方式的选项。',
    focus_point: '识别高风险决策点、保留必要审批节点。',
    reject_reason: '',
    ...overrides,
  };
}

function makeResult(questions: ReviewQuestionItem[], source: 'AI' | 'SCALE' = 'AI'): BatchQuestionsResult {
  return {
    batch: {
      id: '1785000000000000001',
      batch_no: '#G0921',
      title: '授权与分工 ×2',
      source,
      batch_type: 'GENERATE',
      question_count: questions.length,
      created_at: '2026-09-21T22:15:00Z',
    },
    questions,
  };
}

beforeEach(() => {
  batchQ.data = undefined;
  batchQ.isLoading = false;
  batchQ.isError = false;
  batchQ.refetch.mockReset();
  confirmMutateMock.mockReset();
});

describe('ReviewView 批量审核视图（specs §4.2 / §4A.2）', () => {
  it('TestCardFlowFields：卡片流按编号展示全文与元信息，汇总条三数字初始为 N/0/N（§4.2.2 A / §4A.2）', () => {
    batchQ.data = makeResult([makeQuestion(), makeQuestion({
      id: '1785000000000000102',
      question_no: 'Q-AG-0102',
      dimension_name: '风险与担责',
    })]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    expect(screen.getByText('Q-AG-0101')).toBeInTheDocument();
    expect(screen.getByText('Q-AG-0102')).toBeInTheDocument();
    // 整卡全文（不截断，两题共用同文本命中两卡）
    expect(screen.getAllByText('AI 给出了一份 12 人团队两周任务分解草案。')).toHaveLength(2);
    expect(screen.getAllByText('识别高风险决策点、保留必要审批节点。')).toHaveLength(2);
    expect(screen.getAllByText('对话作答').length).toBeGreaterThanOrEqual(1);
    // 汇总条初始：共 2 / 驳回 0 / 入库 2
    expect(screen.getAllByText('（默认通过）')).toHaveLength(2);
    expect(screen.getByText('本批共 2 题')).toBeInTheDocument();
    expect(screen.getByText('已标记驳回 0 题')).toBeInTheDocument();
    expect(screen.getByText('将默认入库 2 题')).toBeInTheDocument();
  });

  it('TestAiHintBySource：AI 批次审核提示含情境合理性/命中度/偏见（§4.1.4 规则1 / BR4）', () => {
    batchQ.data = makeResult([makeQuestion()], 'AI');
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);
    const hint = screen.getByText(/情境合理性/);
    expect(hint.textContent).toContain('子能力命中度');
    expect(hint.textContent).toContain('潜在偏见');
  });

  it('TestScaleHintBySource：SCALE 批次提示换适用性/表述歧义且 AI 不重写文本（§4.1.4 规则1 / BR4）', () => {
    batchQ.data = makeResult([makeQuestion({
      answer_mode: 'LIKERT5',
      dimension_name: '第三型：成就者',
    })], 'SCALE');
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);
    const hint = screen.getByText(/本公司业务场景适用性/);
    expect(hint.textContent).toContain('表述歧义');
    expect(hint.textContent).toContain('不重写');
    expect(screen.getByText('Likert 5 级')).toBeInTheDocument();
  });

  it('TestMarkRejectEmptyReason：原因为空时点确认入库标记不生效，驳回数仍 0 且红字提示（§4.2.5 / BR3）', async () => {
    batchQ.data = makeResult([makeQuestion(), makeQuestion({ id: '102', question_no: 'Q-AG-0102' })]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    // 点「标记驳回」展开原因输入
    fireEvent.click(screen.getAllByRole('button', { name: '标记驳回' })[0]);
    const reasonInput = await screen.findByRole('textbox', { name: /驳回原因/ });
    expect(reasonInput).toBeInTheDocument();

    // 原因留空直接点「确认入库」：标记不生效（仍视为未标记）
    fireEvent.click(screen.getByRole('button', { name: '确认入库' }));
    await waitFor(() => expect(confirmMutateMock).toHaveBeenCalled());
    expect(confirmMutateMock.mock.calls[0][0].rejected).toEqual([]);
    expect(screen.getByText(/请填写驳回原因/)).toBeInTheDocument();
  });

  it('TestMarkRejectFilled：填原因后标记生效，汇总条「驳回 1 / 入库 N-1」（§4.2.4 规则2 / BR2）', () => {
    batchQ.data = makeResult([makeQuestion(), makeQuestion({ id: '102', question_no: 'Q-AG-0102' })]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    fireEvent.click(screen.getAllByRole('button', { name: '标记驳回' })[0]);
    const reasonInput = screen.getByRole('textbox', { name: /驳回原因/ });
    fireEvent.change(reasonInput, { target: { value: '情境仅绑定互联网场景，迁移性差' } });

    expect(screen.getByText('已标记驳回 1 题')).toBeInTheDocument();
    expect(screen.getByText('将默认入库 1 题')).toBeInTheDocument();
    expect(screen.getByText('（已标记驳回）')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '取消标记' })).toBeInTheDocument();
  });

  it('TestReasonTooLong：原因超 500 字被拦截提示（§4.2.2 B）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '标记驳回' }));
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '长'.repeat(501) },
    });
    expect(screen.getByText(/不超过 500 字/)).toBeInTheDocument();
    // 超长同样标记不生效
    expect(screen.getByText('已标记驳回 0 题')).toBeInTheDocument();
  });

  it('TestUnmarkClearsReason：取消标记清原因回默认通过（§4.2.3）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '标记驳回' }));
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '场景迁移性差' },
    });
    fireEvent.click(screen.getByRole('button', { name: '取消标记' }));

    expect(screen.getByText('（默认通过）')).toBeInTheDocument();
    expect(screen.getByText('已标记驳回 0 题')).toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: /驳回原因/ })).not.toBeInTheDocument();
  });

  it('TestClearAllMarks：「全部不标」一键清空，三数字复位（§4.2.3 / §4.1.4 规则2）', () => {
    batchQ.data = makeResult([makeQuestion(), makeQuestion({ id: '102', question_no: 'Q-AG-0102' })]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    const buttons = screen.getAllByRole('button', { name: '标记驳回' });
    fireEvent.click(buttons[0]);
    fireEvent.change(screen.getAllByRole('textbox', { name: /驳回原因/ })[0], {
      target: { value: '原因一' },
    });
    fireEvent.click(buttons[1]);
    fireEvent.change(screen.getAllByRole('textbox', { name: /驳回原因/ })[1], {
      target: { value: '原因二' },
    });
    expect(screen.getByText('已标记驳回 2 题')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /全部不标/ }));
    expect(screen.getByText('已标记驳回 0 题')).toBeInTheDocument();
    expect(screen.getByText('将默认入库 2 题')).toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: /驳回原因/ })).not.toBeInTheDocument();
  });

  it('TestConfirmSuccess：确认入库 toast 含入库与驳回数并调 onExit（§4.2.5 / BR6）', async () => {
    const { toast } = await import('sonner');
    batchQ.data = makeResult([makeQuestion(), makeQuestion({ id: '102', question_no: 'Q-AG-0102' })]);
    confirmMutateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({
        batch_id: '1785000000000000001',
        batch_status: 'CLOSED',
        admitted_count: 1,
        rejected_count: 1,
      });
    });
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getAllByRole('button', { name: '标记驳回' })[0]);
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '场景迁移性差' },
    });
    fireEvent.click(screen.getByRole('button', { name: '确认入库' }));

    await waitFor(() => expect(confirmMutateMock).toHaveBeenCalledWith(
      expect.objectContaining({
        batchId: '1785000000000000001',
        rejected: [{ question_id: '1785000000000000101', reason: '场景迁移性差' }],
      }),
      expect.anything(),
    ));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('入库 1 题，驳回 1 题'));
    await waitFor(() => expect(onExit).toHaveBeenCalledTimes(1));
  });

  it('TestConfirmClosed1706：批次已关闭（1706）toast、刷新列表并 onExit（§4.2.4 规则1）', async () => {
    const { toast } = await import('sonner');
    const { queryClient } = await import('@/lib/query-client');
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');
    batchQ.data = makeResult([makeQuestion()]);
    confirmMutateMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new ApiError(1706, 'closed'));
    });
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getByRole('button', { name: '确认入库' }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('批次已关闭或已作废，请刷新'));
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['question-bank'] }),
    ));
    await waitFor(() => expect(onExit).toHaveBeenCalledTimes(1));
    invalidateSpy.mockRestore();
  });

  it('TestConfirmGenericError：非 1706 错误通用 toast 且留在审核视图（§4.1.4 规则9）', async () => {
    const { toast } = await import('sonner');
    batchQ.data = makeResult([makeQuestion()]);
    confirmMutateMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new Error('network'));
    });
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getByRole('button', { name: '确认入库' }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('操作失败，请稍后重试'));
    expect(onExit).not.toHaveBeenCalled();
  });

  it('TestExitNoMarksDirect：无标记时「返回题库」直接 onExit（§4.2.3）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getByRole('button', { name: '返回题库' }));
    expect(onExit).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('离开审核确认')).not.toBeInTheDocument();
  });

  it('TestExitWithMarksRequiresConfirm：存在标记时「返回题库」出 ConfirmDialog，确认后离开（§4.2.3 / BR1）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getByRole('button', { name: '标记驳回' }));
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '场景迁移性差' },
    });
    fireEvent.click(screen.getByRole('button', { name: '返回题库' }));

    expect(onExit).not.toHaveBeenCalled();
    expect(screen.getByText('离开审核确认')).toBeInTheDocument();
    // 确认离开
    fireEvent.click(screen.getByRole('button', { name: '确认离开' }));
    expect(onExit).toHaveBeenCalledTimes(1);
  });

  it('TestExitConfirmCancel：取消离开留在审核视图，标记保留（BR1 中途离开无数据变更）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    const onExit = vi.fn();
    render(<ReviewView batchId="1785000000000000001" onExit={onExit} />);

    fireEvent.click(screen.getByRole('button', { name: '标记驳回' }));
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '场景迁移性差' },
    });
    fireEvent.click(screen.getByRole('button', { name: '返回题库' }));
    fireEvent.click(screen.getByRole('button', { name: '取消' }));

    expect(onExit).not.toHaveBeenCalled();
    expect(screen.getByText('已标记驳回 1 题')).toBeInTheDocument();
  });

  it('TestMarkingPurelyFrontend：标记/取消过程零请求（BR1 标记全程前端）', () => {
    batchQ.data = makeResult([makeQuestion()]);
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '标记驳回' }));
    fireEvent.change(screen.getByRole('textbox', { name: /驳回原因/ }), {
      target: { value: '原因' },
    });
    fireEvent.click(screen.getByRole('button', { name: '取消标记' }));
    fireEvent.click(screen.getByRole('button', { name: /全部不标/ }));
    expect(confirmMutateMock).not.toHaveBeenCalled();
  });

  it('TestLoadingState：明细加载中渲染占位不渲染卡片', () => {
    batchQ.data = undefined;
    batchQ.isLoading = true;
    const { container } = render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);
    expect(container.querySelector('.animate-pulse')).not.toBeNull();
    expect(screen.queryByRole('button', { name: '确认入库' })).not.toBeInTheDocument();
  });

  it('TestErrorState：加载失败渲染重试入口', () => {
    batchQ.data = undefined;
    batchQ.isError = true;
    render(<ReviewView batchId="1785000000000000001" onExit={vi.fn()} />);
    expect(screen.getByText('数据加载失败，请重试')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(batchQ.refetch).toHaveBeenCalled();
  });
});
