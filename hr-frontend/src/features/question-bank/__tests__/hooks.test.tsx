import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

import { useAuthStore } from '@/stores/auth';

vi.mock('@/features/question-bank/api', () => ({
  fetchQuestions: vi.fn(),
  fetchQuestionDetail: vi.fn(),
  updateQuestion: vi.fn(),
  toggleQuestionStatus: vi.fn(),
  deleteQuestion: vi.fn(),
}));

import {
  deleteQuestion,
  fetchQuestionDetail,
  fetchQuestions,
  toggleQuestionStatus,
  updateQuestion,
} from '../api';
import {
  useDeleteQuestion,
  useQuestionDetail,
  useQuestions,
  useToggleQuestionStatus,
  useUpdateQuestion,
} from '../hooks';
import type { QuestionQuery } from '../types';

afterEach(() => {
  useAuthStore.setState({ token: null });
  vi.clearAllMocks();
});

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function renderWithClient<T>(callback: () => T, qc: QueryClient) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(callback, { wrapper });
}

describe('question-bank hooks', () => {
  it('TestUseQuestionsSnakeCase：初次渲染触发 fetchQuestions 且查询参数 snake_case 透传（page_size 非 pageSize）', async () => {
    vi.mocked(fetchQuestions).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });
    useAuthStore.setState({ token: 'test-token' });

    const query: QuestionQuery = {
      source: 'AI',
      dimension_id: '1780000000000000100',
      status: 'ACTIVE',
      keyword: '授权',
      page: 1,
      page_size: 10,
    };
    const qc = makeClient();
    const { result } = renderWithClient(() => useQuestions(query), qc);

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(fetchQuestions).toHaveBeenCalledTimes(1);
    expect(fetchQuestions).toHaveBeenCalledWith(
      expect.objectContaining({ source: 'AI', page: 1, page_size: 10 }),
    );
    const arg = vi.mocked(fetchQuestions).mock.calls[0][0] as unknown as Record<string, unknown>;
    expect(arg).not.toHaveProperty('pageSize');
    expect(result.current.data?.total).toBe(0);
  });

  it('TestUseQuestionsDisabledWithoutToken：无 token 时查询保持 idle 不发起请求', () => {
    vi.mocked(fetchQuestions).mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });

    const qc = makeClient();
    const { result } = renderWithClient(
      () => useQuestions({ source: 'SCALE', page: 1, page_size: 20 }),
      qc,
    );

    expect(result.current.fetchStatus).toBe('idle');
    expect(fetchQuestions).not.toHaveBeenCalled();
  });

  it('TestUseQuestionDetailFetch：id 非 null 时按 id 拉详情', async () => {
    vi.mocked(fetchQuestionDetail).mockResolvedValue({
      id: '1001',
      question_no: 'Q-AG-0001',
      source: 'AI',
      dimension_id: '2001',
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
      version: 1,
      created_at: '2026-09-23T10:31:24Z',
      updated_at: '2026-09-23T10:31:24Z',
    });
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(() => useQuestionDetail('1001'), qc);

    await waitFor(() => expect(result.current.data?.question_no).toBe('Q-AG-0001'));
    expect(fetchQuestionDetail).toHaveBeenCalledWith('1001');
  });

  it('TestUseQuestionDetailNullId：id 为 null 时不发起详情请求', () => {
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(() => useQuestionDetail(null), qc);

    expect(result.current.fetchStatus).toBe('idle');
    expect(fetchQuestionDetail).not.toHaveBeenCalled();
  });

  it('TestUseDeleteQuestionInvalidates：删除成功后废弃 question-bank 缓存并透传 payload', async () => {
    vi.mocked(deleteQuestion).mockResolvedValue(undefined);
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderWithClient(() => useDeleteQuestion(), qc);

    await act(async () => {
      await result.current.mutateAsync({ id: '1001', version: 3 });
    });

    // TanStack Query v5 mutationFn 被调时附带第二参数 context，断言只锁定 payload
    expect(deleteQuestion).toHaveBeenCalledWith(
      expect.objectContaining({ id: '1001', version: 3 }),
      expect.anything(),
    );
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['question-bank'] }),
    );
  });

  it('TestUseToggleQuestionStatusInvalidates：启停成功后废弃 question-bank 缓存', async () => {
    vi.mocked(toggleQuestionStatus).mockResolvedValue({
      id: '1001',
      status: 'DISABLED',
      version: 4,
      updated_at: '2026-09-23T11:05:00Z',
    });
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderWithClient(() => useToggleQuestionStatus(), qc);

    await act(async () => {
      await result.current.mutateAsync({ id: '1001', target_status: 'DISABLED', version: 3 });
    });

    expect(toggleQuestionStatus).toHaveBeenCalledWith(
      expect.objectContaining({ id: '1001', target_status: 'DISABLED', version: 3 }),
      expect.anything(),
    );
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['question-bank'] }),
    );
  });

  it('TestUseUpdateQuestionInvalidates：编辑成功后废弃 question-bank 缓存', async () => {
    vi.mocked(updateQuestion).mockResolvedValue({
      id: '1001',
      version: 3,
      updated_at: '2026-09-23T11:00:00Z',
    });
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderWithClient(() => useUpdateQuestion(), qc);

    await act(async () => {
      await result.current.mutateAsync({
        id: '1001',
        dimension_id: '2001',
        scenario: 's',
        requirement: 'r',
        focus_point: 'f',
        version: 2,
      });
    });

    expect(updateQuestion).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['question-bank'] }),
    );
  });

  it('TestUseDeleteQuestionErrorNoInvalidate：删除失败时不废弃缓存（异常上抛交消费方分支）', async () => {
    vi.mocked(deleteQuestion).mockRejectedValue(new Error('network'));
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderWithClient(() => useDeleteQuestion(), qc);

    await act(async () => {
      await result.current.mutateAsync({ id: '1001', version: 3 }).catch(() => undefined);
    });

    // 状态更新在 act 后的下一拍落地（mutation 状态机异步推进）
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(spy).not.toHaveBeenCalled();
  });
});
