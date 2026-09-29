// 主动测试任务域私有类型（specs P2_TST_001 §4.1 / 03_api_interface §3）。
// 跨域共享的分页结构 Page<T> 在 lib/contracts.ts。

/** 测试类型：ai_mgmt=AI 管理能力 tab、enneagram=九型人格 tab（03 §2 A1 test_type）。 */
export type TestType = 'ai_mgmt' | 'enneagram';

/** 任务状态五态（specs §6.1）。 */
export type TestTaskStatus = 'pending' | 'in_progress' | 'completed' | 'expired' | 'canceled';

/** 链接状态三态（specs §6.1）。 */
export type TestTaskLinkStatus = 'valid' | 'used' | 'invalid';

/** 阅卷状态四态（specs §6.1；九型 tab 已评分呈现为已判定，取值同源）。 */
export type TestTaskGradingStatus = 'waiting' | 'grading' | 'scored' | 'degraded';

/** GET /api/assessment/test-tasks 响应 list 项（03 §3 A1）。id 雪花 string 化，时间直出原样渲染。 */
export interface TestTaskListItem {
  id: string;
  /** 任务号：T（ai_mgmt）/ E（enneagram）前缀 + yyyyMMdd + 4 位序号。 */
  task_no: string;
  test_type: TestType;
  /** 被测评人姓名快照（人名即标识，系统无工号）。 */
  staff_name: string;
  status: TestTaskStatus;
  link_status: TestTaskLinkStatus;
  grading_status: TestTaskGradingStatus;
  created_at: string;
  completed_at: string | null;
}

/** 任务列表查询条件。test_type 必填对应当前 tab；status/keyword 空值表示全部。 */
export interface TestTaskFilter {
  test_type: TestType;
  status?: TestTaskStatus;
  keyword?: string;
  page: number;
  page_size: number;
}

/** GET /api/assessment/test-tasks/poll-counts 响应（03 §3 A2）：两类未终态任务计数。 */
export interface TestTaskPollCounts {
  ai_mgmt_active: number;
  enneagram_active: number;
}

/** POST /api/assessment/test-tasks/create 请求体（03 §3 B1）。dimension_ids 仅 ai_mgmt 必填。 */
export interface CreateTestTaskPayload {
  test_type: TestType;
  staff_id: string;
  staff_name: string;
  dimension_ids?: string[];
}

/** POST /api/assessment/test-tasks/scale-status 响应（03 §3 B2）。未就绪时空串零值。 */
export interface TestScaleStatus {
  ready: boolean;
  scale_key: string;
  scale_name: string;
  active_question_count: number;
}

/** 作答链接信息（03 §3 C1/C2 同构响应）。answer_url 含一次性令牌原文。 */
export interface TestTaskLinkInfo {
  task_id: string;
  task_no: string;
  test_type: TestType;
  staff_name: string;
  answer_url: string;
  link_status: TestTaskLinkStatus;
  generated_at: string;
  expires_at: string;
}
