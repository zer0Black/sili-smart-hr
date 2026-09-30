// 员工作答域契约类型（03_api_interface §3 A1/A2/A3 响应字段一一对应）。

/** 测试类型（specs §4.1.2，驱动前端预设文案与输入校验分支）。 */
export type AnswerTestType = 'ai_mgmt' | 'enneagram';

/** 题目条目（A1 questions 与 A2 next_question 同构）。enneagram 时 dimension_name 恒空串不渲染。 */
export interface AnswerQuestionItem {
  seq: number;
  dimension_name: string;
  scenario: string;
  requirement: string;
}

/** 已有作答对话记录条目（按 seq 升序，重进恢复消息流用）。 */
export interface AnswerReplyItem {
  seq: number;
  content: string;
}

/** POST /api/answer/context 响应 data（03 §3 A1）。 */
export interface AnswerContextResult {
  task_no: string;
  test_type: AnswerTestType;
  question_total: number;
  answered_count: number;
  /** 全部作答完成（answered_count ≥ question_total），true 时直接进完成待提交态。 */
  finished: boolean;
  questions: AnswerQuestionItem[];
  replies: AnswerReplyItem[];
}

/** A2 推进指令：next 携带下一题全文，finished 转完成待提交态。 */
export type AnswerReplyAction = 'next' | 'finished';

/** POST /api/answer/reply 响应 data（03 §3 A2）。question_seq 为服务端推算的实际落库题号。 */
export interface AnswerReplyResult {
  question_seq: number;
  answered_count: number;
  question_total: number;
  action: AnswerReplyAction;
  next_question: AnswerQuestionItem | null;
}

/** POST /api/answer/submit 响应 data（03 §3 A3）。task_no 为成功态参考编号。 */
export interface AnswerSubmitResult {
  task_no: string;
}
