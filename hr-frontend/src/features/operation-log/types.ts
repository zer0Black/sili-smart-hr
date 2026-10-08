// operation-log 域页面侧私有类型（specs P4_LOG_001 §4.1.2/§4.2.2，03 §3 A1）。
// 行数据内嵌详情弹窗全量字段（03 §1.9 弹窗零请求），无独立详情契约。

/** 字段级变更对比项（03 §1.7：field 为业务字段中文名，before/after 字符串化）。 */
export interface ChangeItem {
  field: string;
  before: string;
  after: string;
}

/** 操作日志行（A1 list 元素，雪花 ID string 化）。 */
export interface OperationLogItem {
  id: string;
  created_at: string;
  operator: string;
  module: string;
  target: string;
  summary: string;
  result: 'success' | 'fail';
  /** null 表示无对比数据，弹窗整区不渲染改走文本详情（specs §4.2.5 互斥）。 */
  changes: ChangeItem[] | null;
  detail: string;
}

/** 列表筛选条件（03 §3 A1 查询参数；start_date/end_date 成对出现）。 */
export interface OperationLogFilter {
  operator?: string;
  module?: string;
  result?: string;
  start_date?: string;
  end_date?: string;
  page: number;
  page_size: number;
}

/** 分页页体（统一分页结构作 data 透传）。 */
export interface OperationLogPage {
  list: OperationLogItem[];
  total: number;
  page: number;
  page_size: number;
}
