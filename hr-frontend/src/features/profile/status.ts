// 画像域展示口径：等级映射与分数取整（specs P2_PRF_001 §4.2.4 规则3）。
// 四组枚举键映射唯一定义在 types.ts，此处 re-export 统一消费入口，避免双份维护。

export {
  activityLevelKey,
  dataStatusKey,
  confidenceKey,
  enneagramTypeKey,
  moduleLabelKey,
  gradeLabelKey,
} from './types';

/** 分数取整展示（模块分浮点，specs §4.2.4 规则3）。 */
export function roundScore(score: number): number {
  // Math.round 四舍五入（.5 进位），与后端取整口径一致。
  return Math.round(score);
}

/** 等级映射：≥85 优秀 / 70-84 良好 / 60-69 中等 / <60 待提升；返回 i18n 键。
 *  先取整再判级（specs §4.2.4 规则3：模块浮点分 84.5 → 85 → excellent）。 */
export function scoreGrade(score: number): 'excellent' | 'good' | 'medium' | 'poor' {
  const rounded = roundScore(score);
  if (rounded >= 85) return 'excellent';
  if (rounded >= 70) return 'good';
  if (rounded >= 60) return 'medium';
  return 'poor';
}
