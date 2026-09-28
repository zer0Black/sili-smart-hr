// 主动测试任务 API（03_api_interface §3 A1/A2/B1/B2/C1/C2/C3）。
import { httpClient } from '@/lib/http-client';
import type { Page } from '@/lib/contracts';

import type {
  CreateTestTaskPayload,
  TestScaleStatus,
  TestTaskFilter,
  TestTaskLinkInfo,
  TestTaskListItem,
  TestTaskPollCounts,
} from './test-task-types';

/** B1 响应 data：创建后任务快照。status 恒为 pending。 */
export interface CreateTestTaskResult {
  id: string;
  task_no: string;
  test_type: string;
  staff_name: string;
  status: string;
  answer_url: string;
  created_at: string;
}

/** GET /api/assessment/test-tasks：单类型任务分页查询（按发起时间倒序，03 §3 A1）。 */
export async function fetchTestTasks(params: TestTaskFilter): Promise<Page<TestTaskListItem>> {
  const { data } = await httpClient.get<Page<TestTaskListItem>>('/assessment/test-tasks', {
    params: {
      test_type: params.test_type,
      status: params.status || undefined,
      keyword: params.keyword || undefined,
      page: params.page,
      page_size: params.page_size,
    },
  });
  return data;
}

/** GET /api/assessment/test-tasks/poll-counts：轮询探针计数（03 §3 A2）。 */
export async function fetchTestTaskPollCounts(): Promise<TestTaskPollCounts> {
  const { data } = await httpClient.get<TestTaskPollCounts>(
    '/assessment/test-tasks/poll-counts',
  );
  return data;
}

/** GET /api/assessment/test-tasks/scale-status：九型量表就绪查询（03 §3 B2）。 */
export async function fetchScaleStatus(): Promise<TestScaleStatus> {
  const { data } = await httpClient.get<TestScaleStatus>(
    '/assessment/test-tasks/scale-status',
  );
  return data;
}

/** GET /api/assessment/test-tasks/link?task_id=：作答链接弹窗数据源（03 §3 C1）。 */
export async function fetchTestTaskLink(taskId: string): Promise<TestTaskLinkInfo> {
  const { data } = await httpClient.get<TestTaskLinkInfo>('/assessment/test-tasks/link', {
    params: { task_id: taskId },
  });
  return data;
}

/** POST /api/assessment/test-tasks/resend：重发作答链接（03 §3 C2），响应为新链接。 */
export async function resendTestTaskLink(taskId: string): Promise<TestTaskLinkInfo> {
  const { data } = await httpClient.post<TestTaskLinkInfo>('/assessment/test-tasks/resend', {
    task_id: taskId,
  });
  return data;
}

/** POST /api/assessment/test-tasks/cancel：取消任务（03 §3 C3），status 恒为 canceled。 */
export async function cancelTestTask(taskId: string): Promise<{ task_id: string; status: string }> {
  const { data } = await httpClient.post<{ task_id: string; status: string }>(
    '/assessment/test-tasks/cancel',
    { task_id: taskId },
  );
  return data;
}

/** POST /api/assessment/test-tasks/create：发起主动测试（03 §3 B1）。 */
export async function createTestTask(payload: CreateTestTaskPayload): Promise<CreateTestTaskResult> {
  const { data } = await httpClient.post<CreateTestTaskResult>(
    '/assessment/test-tasks/create',
    payload,
  );
  return data;
}
