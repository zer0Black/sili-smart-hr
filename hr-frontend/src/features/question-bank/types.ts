// question-bank 域私有类型（跨域共享的契约在 lib/contracts.ts）。

/** 来源 tab：AI 管理题库 / 九型人格量表，与 source 参数一一映射（specs §4.1.2 A）。 */
export type QuestionTab = 'AI' | 'SCALE';

/**
 * 列表查询条件（03 §3.1）。source 必填对应当前 tab；维度/状态/关键词三条件
 * 空值表示全部，由页面重置逻辑清空（specs §4.1.2 A）。
 */
export interface QuestionQuery {
  source: QuestionTab;
  dimension_id?: string;
  status?: string;
  keyword?: string;
  page: number;
  page_size: number;
}
