import { httpClient } from '@/lib/http-client';
import type {
  DeleteQuestionPayload,
  QuestionDetail,
  QuestionListPage,
  QuestionMutationResult,
  ToggleQuestionStatusPayload,
  UpdateQuestionPayload,
} from '@/lib/contracts';

import type { QuestionQuery } from './types';

/** GET /api/questions：题库列表分页查询（source 必选对应 tab，updated_at 倒序）。 */
export async function fetchQuestions(params: QuestionQuery): Promise<QuestionListPage> {
  const { data } = await httpClient.get<QuestionListPage>('/questions', {
    params: {
      source: params.source,
      dimension_id: params.dimension_id || undefined,
      status: params.status || undefined,
      keyword: params.keyword || undefined,
      page: params.page,
      page_size: params.page_size,
    },
  });
  return data;
}

/** GET /api/questions/:id：题目详情（查看弹窗，PENDING 态可查）。 */
export async function fetchQuestionDetail(id: string): Promise<QuestionDetail> {
  const { data } = await httpClient.get<QuestionDetail>(`/questions/${id}`);
  return data;
}

/** POST /api/questions/update：题目编辑（仅 AI 题 ACTIVE/DISABLED 可编辑）。 */
export async function updateQuestion(payload: UpdateQuestionPayload): Promise<QuestionMutationResult> {
  const { data } = await httpClient.post<QuestionMutationResult>('/questions/update', payload);
  return data;
}

/** POST /api/questions/toggle-status：题目启停互切。 */
export async function toggleQuestionStatus(
  payload: ToggleQuestionStatusPayload,
): Promise<QuestionMutationResult> {
  const { data } = await httpClient.post<QuestionMutationResult>('/questions/toggle-status', payload);
  return data;
}

/** POST /api/questions/delete：题目删除（被引用拒删，成功无载荷）。 */
export async function deleteQuestion(payload: DeleteQuestionPayload): Promise<void> {
  await httpClient.post('/questions/delete', payload);
}
