// 画像域页面侧私有类型与展示口径常量（specs P2_PRF_001 §4.1/§4.2）。
// 跨域共享的接口契约在 lib/contracts.ts，等级映射与取整在 status.ts（T3）。

/** 详情页能力 tab 键（specs §4.2.3 能力 tab 切换）。 */
export type ProfileTabKey = 'aiUsage' | 'aiMgmt';

/** 活跃度枚举 → profile ns i18n 键（specs §4.1.2 B；无 activity_stats 行后端已判 unused）。 */
export const activityLevelKey: Record<string, string> = {
  active: 'profile:activityLevel.active',
  low_freq: 'profile:activityLevel.low_freq',
  unused: 'profile:activityLevel.unused',
};

/** 数据状态 → profile ns i18n 键（specs §4.2.2 B 四态）。 */
export const dataStatusKey: Record<string, string> = {
  complete: 'profile:dataStatus.complete',
  degraded: 'profile:dataStatus.degraded',
  missing: 'profile:dataStatus.missing',
  pending: 'profile:dataStatus.pending',
};

/** 置信度 → profile ns i18n 键（specs §4.2.4 规则5，后端规则映射前端只做标签）。 */
export const confidenceKey: Record<string, string> = {
  high: 'profile:confidence.high',
  medium: 'profile:confidence.medium',
  low: 'profile:confidence.low',
};

/** 九型型名数字串 → profile ns i18n 键，措辞与后端导出中文名常量对齐（03 A2）。 */
export const enneagramTypeKey: Record<string, string> = {
  '1': 'profile:enneagram.type1',
  '2': 'profile:enneagram.type2',
  '3': 'profile:enneagram.type3',
  '4': 'profile:enneagram.type4',
  '5': 'profile:enneagram.type5',
  '6': 'profile:enneagram.type6',
  '7': 'profile:enneagram.type7',
  '8': 'profile:enneagram.type8',
  '9': 'profile:enneagram.type9',
};

/** 证据来源类型 → profile ns i18n 键（specs §4.2.2 D）。 */
export const evidenceSourceKey: Record<string, string> = {
  conversation: 'profile:evidenceSource.conversation',
  active_test: 'profile:evidenceSource.active_test',
};

/** 维度行状态 → profile ns i18n 键（specs 第 6 章状态映射）。 */
export const dimensionStatusKey: Record<string, string> = {
  normal: 'profile:dimensionStatus.normal',
  insufficient: 'profile:dimensionStatus.insufficient',
  missing: 'profile:dimensionStatus.missing',
};
