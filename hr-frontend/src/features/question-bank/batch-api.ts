// 题库批次域 API（03_api_interface §3.7-§3.10）：待审核卡列表、批次明细、确认入库与作废。
import { httpClient } from '@/lib/http-client';

import type {
  BatchQuestionsResult,
  ConfirmBatchResult,
  QuestionBatchCard,
} from '@/lib/contracts';

/** GET /api/question-batches：待审核批次卡（仅 PENDING，created_at DESC，不分页）。 */
export async function fetchPendingBatches(): Promise<{ list: QuestionBatchCard[]; total: number }> {
  const { data } = await httpClient.get<{ list: QuestionBatchCard[]; total: number }>(
    '/question-batches',
  );
  return data;
}

/** GET /api/question-batches/:id/questions：批次明细（批次卡 + 全量题目）。 */
export async function fetchBatchQuestions(batchId: string): Promise<BatchQuestionsResult> {
  const { data } = await httpClient.get<BatchQuestionsResult>(
    `/question-batches/${batchId}/questions`,
  );
  return data;
}

/** POST /api/question-batches/:id/confirm：确认入库，rejected 为被标记驳回的题目与原因。 */
export async function confirmBatch(
  batchId: string,
  rejected: { question_id: string; reason: string }[],
): Promise<ConfirmBatchResult> {
  const { data } = await httpClient.post<ConfirmBatchResult>(
    `/question-batches/${batchId}/confirm`,
    { rejected },
  );
  return data;
}

/** POST /api/question-batches/:id/void：作废批次，成功无载荷。 */
export async function voidBatch(batchId: string): Promise<void> {
  await httpClient.post(`/question-batches/${batchId}/void`);
}
