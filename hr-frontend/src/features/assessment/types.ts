// assessment 批次域私有类型（跨域共享的列表/统计/计划契约在 lib/contracts.ts）。

/** GET /api/assessment/batches/targets 响应 data（03_api_interface §3.A4）。 */
export interface BatchTargets {
  batch_id: string;
  target_mode: 'all' | 'specified';
  /** 批次创建时点的全量名单快照（token_name 人名）。 */
  names: string[];
  total: number;
  period_start: string;
  period_end: string;
}

/** 失败人员明细项（03_api_interface §3.A5 list 项）。 */
export interface BatchFailureItem {
  token_name: string;
  error_summary: string;
}

/** GET /api/assessment/batches/failures 响应 data（03_api_interface §3.A5）。 */
export interface BatchFailures {
  batch_id: string;
  batch_no: string;
  failed_count: number;
  total_count: number;
  period_start: string;
  period_end: string;
  list: BatchFailureItem[];
}

/** POST /api/assessment/batches/create 请求体（03_api_interface §3.B1）。 */
export interface CreateBatchPayload {
  target_mode: 'all' | 'specified';
  /** specified 模式必填非空；staff_id 可选仅日志定位，staff_name 必填为去重键。 */
  staffs?: { staff_id?: string; staff_name: string }[];
  period_start: string;
  period_end: string;
}

/** 批次列表查询条件。空值表示全部，由页面重置逻辑清空。 */
export interface BatchFilter {
  trigger_type?: string;
  status?: string;
  page: number;
  page_size: number;
}
