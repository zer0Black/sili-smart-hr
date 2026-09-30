// 员工作答页 hooks（specs P2_TST_002 §4.1.3/§5.1.5）。上下文查询与 reply/submit 两 mutation。
import { useMutation, useQuery } from '@tanstack/react-query';
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';

import { fetchAnswerContext, sendAnswerReply, submitAnswer } from './answer-api';
import type { AnswerApiError } from './answer-api';
import type { AnswerContextResult, AnswerReplyResult, AnswerSubmitResult } from './answer-types';

/**
 * useAnswerContext：作答上下文查询。staleTime 0 + retry 0 只发一次，
 * 失败经页面重试按钮显式 refetch（specs §5.1.5：重试重新走校验与上报）。
 */
export function useAnswerContext(token: string): UseQueryResult<AnswerContextResult, AnswerApiError> {
  return useQuery({
    queryKey: ['answer', 'context', token],
    queryFn: () => fetchAnswerContext(token),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/** useAnswerReply：逐题作答回复。retry 0 防越限重发（落库幂等语义归服务端题号权威），失败由页面保留输入重试。 */
export function useAnswerReply(
  token: string,
): UseMutationResult<AnswerReplyResult, AnswerApiError, { content: string; questionSeq: number }> {
  return useMutation({
    mutationFn: ({ content, questionSeq }: { content: string; questionSeq: number }) =>
      sendAnswerReply(token, content, questionSeq),
    retry: false,
  });
}

/** useAnswerSubmit：提交作答（specs §4.1.3 提交并结束）。失败 toast 后留在作答态由页面重试。 */
export function useAnswerSubmit(token: string): UseMutationResult<AnswerSubmitResult, AnswerApiError, void> {
  return useMutation({
    mutationFn: () => submitAnswer(token),
    retry: false,
  });
}
