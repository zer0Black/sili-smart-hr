// BatchCardStrip 批次卡区测试（specs §4.1.2 C / §4.1.3 / §4.1.5）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

import { BatchCardStrip } from '../batch-card-strip';
import { ApiError } from '@/lib/http-client';
import type { QuestionBatchCard } from '@/lib/contracts';

const pendingQ = vi.hoisted(() => ({
  data: undefined as { list: QuestionBatchCard[]; total: number } | undefined,
}));
const voidMutateMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/question-bank/batch-hooks', () => ({
  usePendingBatches: () => pendingQ,
  useBatchQuestions: vi.fn(),
  useConfirmBatch: vi.fn(),
  useVoidBatch: () => ({ mutate: voidMutateMock, isPending: false }),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

function makeCard(overrides: Record<string, unknown> = {}): QuestionBatchCard {
  return {
    id: '1785000000000000001',
    batch_no: '#G0921',
    title: '授权与分工 ×2',
    source: 'AI',
    batch_type: 'GENERATE',
    question_count: 12,
    created_at: '2026-09-21T22:15:00Z',
    ...overrides,
  };
}

function renderStrip(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

beforeEach(() => {
  pendingQ.data = undefined;
  voidMutateMock.mockReset();
  useAuthStore.setState({ token: 'test-token' });
});

describe('BatchCardStrip 批次卡区（specs §4.1.2 C / §4.1.5）', () => {
  it('TestEmptyRendersNull：空列表渲染 null，卡区整体隐藏（§4.1.5 / BR1）', () => {
    pendingQ.data = { list: [], total: 0 };
    const { container } = renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);
    expect(container.innerHTML).toBe('');
  });

  it('TestLoadingRendersNull：查询未返回数据时同样不渲染（加载期隐藏）', () => {
    pendingQ.data = undefined;
    const { container } = renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);
    expect(container.innerHTML).toBe('');
  });

  it('TestCardFields：批次卡显示标题/批次号/来源标签/题数/生成时间（§4.1.2 C）', () => {
    pendingQ.data = {
      list: [makeCard(), makeCard({
        id: '1785000000000000002',
        batch_no: '#S0921',
        title: 'Riso-Hudson 标准量表',
        source: 'SCALE',
        batch_type: 'IMPORT',
        question_count: 144,
        created_at: '2026-09-21T22:30:00Z',
      })],
      total: 2,
    };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);

    expect(screen.getByText('#G0921')).toBeInTheDocument();
    expect(screen.getByText('授权与分工 ×2')).toBeInTheDocument();
    expect(screen.getByText('AI 管理题')).toBeInTheDocument();
    expect(screen.getByText('共 12 题')).toBeInTheDocument();
    // 生成时间本地时区无关断言：格式化为 yyyy-MM-dd HH:mm:ss 形态
    expect(screen.getByText(/2026-09-\d{2} \d{2}:15:00/)).toBeInTheDocument();
    expect(screen.getByText('#S0921')).toBeInTheDocument();
    expect(screen.getByText('Riso-Hudson 标准量表')).toBeInTheDocument();
    expect(screen.getByText('共 144 题')).toBeInTheDocument();
    expect(screen.getByText(/2026-09-\d{2} \d{2}:30:00/)).toBeInTheDocument();
  });

  it('TestInvalidTimeFallback：非法生成时间原样透传（view-dialog formatTime 同款兜底）', () => {
    pendingQ.data = { list: [makeCard({ created_at: 'not-a-time' })], total: 1 };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);
    expect(screen.getByText(/not-a-time/)).toBeInTheDocument();
  });

  it('TestStartReviewCallback：卡上「开始审核」点击回调携带 batchId（§4.1.3）', () => {
    pendingQ.data = { list: [makeCard()], total: 1 };
    const onStartReview = vi.fn();
    renderStrip(<BatchCardStrip onStartReview={onStartReview} />);

    fireEvent.click(screen.getByRole('button', { name: '开始审核' }));
    expect(onStartReview).toHaveBeenCalledWith('1785000000000000001');
  });

  it('TestVoidRequiresConfirm：点「作废」先出确认弹窗，未确认前不发请求（§4.1.3）', async () => {
    pendingQ.data = { list: [makeCard()], total: 1 };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '作废' }));
    // ConfirmDialog 已弹出（标题可见），但 mutate 尚未调用
    expect(await screen.findByText('作废批次确认')).toBeInTheDocument();
    expect(voidMutateMock).not.toHaveBeenCalled();
  });

  it('TestVoidConfirmMutates：确认弹窗点「作废」后才携带 batchId 发请求（BR2）', async () => {
    pendingQ.data = { list: [makeCard()], total: 1 };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '作废' }));
    // 弹窗确认按钮与卡上按钮同名，取弹出后的最后一个匹配
    const confirmBtn = (await screen.findAllByRole('button', { name: '作废' })).pop()!;
    fireEvent.click(confirmBtn);

    await waitFor(() => {
      expect(voidMutateMock).toHaveBeenCalledWith(
        '1785000000000000001',
        expect.anything(),
      );
    });
  });

  it('TestVoidCancelNoMutate：确认弹窗取消不发请求', async () => {
    pendingQ.data = { list: [makeCard()], total: 1 };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '作废' }));
    fireEvent.click(await screen.findByRole('button', { name: '取消' }));

    await waitFor(() => {
      expect(screen.queryByText('作废批次确认')).not.toBeInTheDocument();
    });
    expect(voidMutateMock).not.toHaveBeenCalled();
  });

  it('TestVoidClosed1706：作废收 1706 时 toast 批次已关闭并刷新卡区与列表（§4.1.3）', async () => {
    const { toast } = await import('sonner');
    const { queryClient } = await import('@/lib/query-client');
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');
    voidMutateMock.mockImplementation((_id: string, cb?: { onError?: (e: unknown) => void }) => {
      cb?.onError?.(new ApiError(1706, 'closed'));
    });
    pendingQ.data = { list: [makeCard()], total: 1 };

    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '作废' }));
    fireEvent.click((await screen.findAllByRole('button', { name: '作废' })).pop()!);

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('批次已关闭或已作废，请刷新'));
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['question-bank'] }),
    ));
    invalidateSpy.mockRestore();
  });

  it('TestVoidGenericError：非 1706 错误走通用失败 toast', async () => {
    const { toast } = await import('sonner');
    voidMutateMock.mockImplementation((_id: string, cb?: { onError?: (e: unknown) => void }) => {
      cb?.onError?.(new Error('network'));
    });
    pendingQ.data = { list: [makeCard()], total: 1 };

    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '作废' }));
    fireEvent.click((await screen.findAllByRole('button', { name: '作废' })).pop()!);

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('操作失败，请稍后重试'));
  });

  it('TestStripHorizontal：卡区容器为横排可横向滚动（§4.1.5 批次卡横排）', () => {
    pendingQ.data = { list: [makeCard(), makeCard({ id: '1785000000000000002', batch_no: '#S0921' })], total: 2 };
    renderStrip(<BatchCardStrip onStartReview={vi.fn()} />);

    const strip = screen.getByText('#G0921').closest('.overflow-x-auto');
    expect(strip).not.toBeNull();
    expect(strip!.className).toContain('flex');
  });
});
