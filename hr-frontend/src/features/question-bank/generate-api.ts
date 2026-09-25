// 题目生成域 API（03_api_interface §3.13/§3.14）：发起生成、进度轮询、取消。
import { httpClient } from '@/lib/http-client';

import type { CreateGenerationResult, GenerationProgress } from '@/lib/contracts';

/** POST /api/question-generations/create：校验通过即建 QUEUED 行并投递任务，即时返回。 */
export async function createGeneration(payload: {
  dimension_ids: string[];
  count: number;
}): Promise<CreateGenerationResult> {
  const { data } = await httpClient.post<CreateGenerationResult>(
    '/question-generations/create',
    payload,
  );
  return data;
}

/** GET /api/question-generations/:id：进度轮询（建议间隔 2s，直至终态）。 */
export async function fetchGenerationProgress(id: string): Promise<GenerationProgress> {
  const { data } = await httpClient.get<GenerationProgress>(`/question-generations/${id}`);
  return data;
}

/** POST /api/question-generations/:id/cancel：协作式取消（放弃本批），终态调用幂等成功。 */
export async function cancelGeneration(id: string): Promise<void> {
  await httpClient.post(`/question-generations/${id}/cancel`);
}
