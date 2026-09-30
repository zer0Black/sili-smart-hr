// 对话区组件集成测试（specs §4.1.3 发送回复加载状态 / 完成待提交态输入区原位替换 / §4.1.5 非法提示保留输入）。
// mock answer-api：AnswerChat 经 useAnswerReply/useAnswerSubmit 走真实 TanStack Query mutation。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { ReactElement } from 'react';

import i18n from '@/i18n/config';
import type { AnswerContextResult, AnswerQuestionItem } from '../answer-types';

const replyMock = vi.hoisted(() => vi.fn());
const submitMock = vi.hoisted(() => vi.fn());
vi.mock('../answer-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../answer-api')>();
  return { ...actual, sendAnswerReply: replyMock, submitAnswer: submitMock };
});

import { AnswerChat } from '../components/answer-chat';

const TOKEN = 'tk-test-token-aaaaaaaaaaaaaaaaaaaaaaaaa';

function q(seq: number): AnswerQuestionItem {
  return { seq, dimension_name: `维度${seq}`, scenario: `情境${seq}`, requirement: `要求${seq}` };
}

function context(partial: Partial<AnswerContextResult>): AnswerContextResult {
  return {
    task_no: 'T202609290001',
    test_type: 'ai_mgmt',
    question_total: 5,
    answered_count: 0,
    finished: false,
    questions: [q(1), q(2), q(3), q(4), q(5)],
    replies: [],
    ...partial,
  };
}

function renderChat(node?: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node ?? <AnswerChat token={TOKEN} ctx={context({})} onSubmitSuccess={vi.fn()} />}</QueryClientProvider>);
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  replyMock.mockReset();
  submitMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AnswerChat（specs §4.1.2 B / §4.1.3 / §4.1.5）', () => {
  it('TestChatFinishedPhase：phase=finished 渲染提交按钮与完成提示，无输入框', () => {
    renderChat(
      <AnswerChat
        token={TOKEN}
        ctx={context({ answered_count: 5, finished: true, replies: [1, 2, 3, 4, 5].map((seq) => ({ seq, content: `答${seq}` })) })}
        onSubmitSuccess={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: '提交并结束' })).toBeInTheDocument();
    expect(screen.getByText('本次作答已完成，可提交结束。')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '发送' })).not.toBeInTheDocument();
  });

  it('TestChatSendingDisabled：reply 请求在途时 typing 气泡呈现且输入框禁用（§4.1.3 加载状态）', async () => {
    replyMock.mockReturnValue(new Promise(() => {}));
    renderChat();

    const box = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: 'C，因为……' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));

    await waitFor(() => expect(replyMock).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(box).toBeDisabled();
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled();
  });

  it('TestChatReplyInvalidKeepsInput：1902/本地非法时 AI 剧本提示重答且输入框保留员工输入（§4.1.5）', () => {
    renderChat(
      <AnswerChat
        token={TOKEN}
        ctx={context({ test_type: 'enneagram', questions: [q(1)] })}
        onSubmitSuccess={vi.fn()}
      />,
    );

    const box = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: 'abc' } });
    // 本地校验先拦：非法输入根本不发请求，AI 剧本提示、输入保留（specs §5.2.5 提交前拦截）。
    fireEvent.click(screen.getByRole('button', { name: '发送' }));
    expect(replyMock.mock.calls).toHaveLength(0);
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('abc');
    expect(screen.getByText('请回复 1 到 5 之间的数字。')).toBeInTheDocument();
  });

  it('TestChatSubmitSuccess：提交成功回调携带响应 task_no（specs §4.1.3 提交并结束）', async () => {
    submitMock.mockResolvedValue({ task_no: 'T202609290009' });
    const onSuccess = vi.fn();
    renderChat(
      <AnswerChat
        token={TOKEN}
        ctx={context({ answered_count: 5, finished: true, replies: [1, 2, 3, 4, 5].map((seq) => ({ seq, content: `答${seq}` })) })}
        onSubmitSuccess={onSuccess}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: '提交并结束' }));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith('T202609290009'));
  });
});
