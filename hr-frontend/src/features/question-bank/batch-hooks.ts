// 题库批次域 hooks：批次卡区与审核视图的数据通道。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';

import type {
  BatchQuestionsResult,
  ConfirmBatchResult,
  QuestionBatchCard,
} from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { confirmBatch, fetchBatchQuestions, fetchPendingBatches, voidBatch } from './batch-api';

/** usePendingBatches：待审核批次卡列表（卡区数据源）。 */
export function usePendingBatches(): UseQueryResult<{ list: QuestionBatchCard[]; total: number }> {
  const token = useAuthStore((s) => s.token);
  return useQuery<{ list: QuestionBatchCard[]; total: number }>({
    queryKey: ['question-bank', 'pending-batches'],
    queryFn: fetchPendingBatches,
    enabled: !!token,
  });
}

/** useBatchQuestions：批次明细，未选中批次（null）或无 token 时不发请求。 */
export function useBatchQuestions(batchId: string | null): UseQueryResult<BatchQuestionsResult> {
  const token = useAuthStore((s) => s.token);
  return useQuery<BatchQuestionsResult>({
    queryKey: ['question-bank', 'batch-questions', batchId],
    queryFn: () => fetchBatchQuestions(batchId as string),
    enabled: !!batchId && !!token,
  });
}

// 批次写操作 mutation：成功后统一废弃 ['question-bank']（批次卡区与题目列表一并刷新）。

export function useConfirmBatch(): UseMutationResult<
  ConfirmBatchResult,
  Error,
  { batchId: string; rejected: { question_id: string; reason: string }[] }
> {
  const qc = useQueryClient();
  return useMutation<
    ConfirmBatchResult,
    Error,
    { batchId: string; rejected: { question_id: string; reason: string }[] }
  >({
    mutationFn: ({ batchId, rejected }) => confirmBatch(batchId, rejected),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}

export function useVoidBatch(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: voidBatch,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}
