import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type {
  DeleteQuestionPayload,
  QuestionDetail,
  QuestionListPage,
  QuestionMutationResult,
  ResubmitQuestionPayload,
  ResubmitQuestionResult,
  ToggleQuestionStatusPayload,
  UpdateQuestionPayload,
} from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import {
  deleteQuestion,
  fetchQuestionDetail,
  fetchQuestions,
  resubmitQuestion,
  toggleQuestionStatus,
  updateQuestion,
} from './api';
import type { QuestionQuery } from './types';

/** useQuestions：题库列表分页查询。query 对象引用稳定性由调用方 useMemo 保证（batch-table 先例）。 */
export function useQuestions(query: QuestionQuery) {
  const token = useAuthStore((s) => s.token);
  return useQuery<QuestionListPage>({
    queryKey: ['question-bank', 'questions', query],
    queryFn: () => fetchQuestions(query),
    enabled: !!token,
  });
}

/** useQuestionDetail：题目详情，未选中（id 为 null）或无 token 时不发请求。 */
export function useQuestionDetail(id: string | null) {
  const token = useAuthStore((s) => s.token);
  return useQuery<QuestionDetail>({
    queryKey: ['question-bank', 'detail', id],
    queryFn: () => fetchQuestionDetail(id as string),
    enabled: !!id && !!token,
  });
}

// 题库写操作 mutation：成功后统一废弃 ['question-bank']（列表与详情一并刷新）。

export function useUpdateQuestion() {
  const qc = useQueryClient();
  return useMutation<QuestionMutationResult, Error, UpdateQuestionPayload>({
    mutationFn: updateQuestion,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}

export function useToggleQuestionStatus() {
  const qc = useQueryClient();
  return useMutation<QuestionMutationResult, Error, ToggleQuestionStatusPayload>({
    mutationFn: toggleQuestionStatus,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}

export function useDeleteQuestion() {
  const qc = useQueryClient();
  return useMutation<void, Error, DeleteQuestionPayload>({
    mutationFn: deleteQuestion,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}

/** useResubmitQuestion：驳回 AI 题修正后重新送审，成功废弃 ['question-bank']（列表与批次卡区一并刷新）。 */
export function useResubmitQuestion() {
  const qc = useQueryClient();
  return useMutation<ResubmitQuestionResult, Error, ResubmitQuestionPayload>({
    mutationFn: resubmitQuestion,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}
