// 核心结论规则拼装纯函数（specs P2_PRF_001 §4.2.4 规则2）：不调 LLM，只读 B1 data 与维度树。
// 参与聚合判据三条件（03 §1.9，与后端 A1 第 7 条同口径）：
//   树中该维度 include_overview && 所属模块非 ENNEAGRAM && B1 该维度行 status === 'normal'。

import type { DimensionBrief, DimensionTreeNode, ProfileDetail, ProfileDimensionRow } from '@/lib/contracts';

import { scoreGrade } from './status';

export interface ConclusionFinding {
  kind: 'strength' | 'weakness' | 'risk' | 'trend';
  /** {key, params} 的 JSON 串（i18n 键与插值参数），由消费组件解析后 t() 渲染，保证纯函数可测。 */
  text: string;
}

/** 严重不足判定：使用能力无聚合行，或使用模块降权+缺失（含 failed 行，规则8 口径）计数之和 ≥ 4（specs §4.2.4 规则2，阈值 4 为前端常量）。 */
export const CONCLUSION_LIMITED_THRESHOLD = 4;
/** 优势阈值 75 / 短板阈值 60（specs §4.2.4 规则2 前端常量）。 */
export const STRENGTH_THRESHOLD = 75;
export const WEAKNESS_THRESHOLD = 60;

export interface ConclusionResult {
  headlineKey: string; // 正常主文键或谨慎参考提示键
  headlineParams: Record<string, string | number>;
  findings: ConclusionFinding[];
}

interface FindingPayload {
  key: string;
  params: Record<string, string | number>;
}

function encodeFinding(payload: FindingPayload): string {
  return JSON.stringify(payload);
}

/** 展开模块节点（AI_USAGE 的 groups 与各模块直挂 dimensions），取全部启用维度。 */
function moduleDimensions(mod: {
  groups: DimensionTreeNode['modules'][number]['groups'];
  dimensions: DimensionTreeNode['modules'][number]['dimensions'];
}): DimensionBrief[] {
  const out: DimensionBrief[] = [];
  if (mod.dimensions) out.push(...mod.dimensions);
  if (mod.groups) for (const g of mod.groups) out.push(...g.dimensions);
  return out;
}

/** 参与聚合维度键集合：树中 include_overview 且模块非 ENNEAGRAM（03 §1.9 前两条）。 */
function overviewCodes(tree: DimensionTreeNode): Set<string> {
  const codes = new Set<string>();
  for (const mod of tree.modules) {
    if (mod.module_code === 'ENNEAGRAM') continue;
    for (const d of moduleDimensions(mod)) {
      if (d.include_overview) codes.add(d.code);
    }
  }
  return codes;
}

/** 数据严重不足判定（specs §4.2.4 规则2；缺失口径同规则8 含 failed 行）。 */
function isLimited(detail: ProfileDetail): boolean {
  const usage = detail.modules.find((m) => m.module === 'AI_USAGE');
  if (!usage || usage.score === null) return true;
  return usage.insufficient_count + usage.missing_count + usage.failed_count >= CONCLUSION_LIMITED_THRESHOLD;
}

function moduleGrade(score: number | null): string {
  return score === null ? 'pending' : scoreGrade(score);
}

/** 主文：概括两模块等级与最高分维度；严重不足时替换为谨慎参考提示。 */
function buildHeadline(
  detail: ProfileDetail,
  eligible: ProfileDimensionRow[],
): Pick<ConclusionResult, 'headlineKey' | 'headlineParams'> {
  if (isLimited(detail)) {
    return { headlineKey: 'profile:conclusion.limited', headlineParams: {} };
  }
  const usage = detail.modules.find((m) => m.module === 'AI_USAGE');
  const mgmt = detail.modules.find((m) => m.module === 'AI_MGMT');
  const top = eligible.reduce<ProfileDimensionRow | null>((acc, r) => {
    if (!acc || (r.score as number) > (acc.score as number)) return r;
    return acc;
  }, null);
  return {
    headlineKey: 'profile:conclusion.headline',
    headlineParams: {
      usageGrade: usage ? moduleGrade(usage.score) : 'pending',
      mgmtGrade: mgmt ? moduleGrade(mgmt.score) : 'pending',
      topDimensionName: top?.dimension_name ?? '',
    },
  };
}

/** 参与聚合维度行：B1 行 status=normal 有分（03 §1.9 第三条）且维度在 overview 集合内。 */
function eligibleRows(
  rows: ProfileDimensionRow[],
  overview: Set<string>,
): ProfileDimensionRow[] {
  return rows.filter((r) => r.status === 'normal' && r.score !== null && overview.has(r.dimension_code));
}

/** 优势：参与聚合维度中分 ≥ 75 的最高两项（两模块合并排序；降权/缺失维度已被排除）。 */
function buildStrengths(eligible: ProfileDimensionRow[]): ConclusionFinding[] {
  const sorted = [...eligible]
    .sort((a, b) => (b.score as number) - (a.score as number))
    .filter((r) => (r.score as number) >= STRENGTH_THRESHOLD);
  return sorted.slice(0, 2).map((r) => ({
    kind: 'strength' as const,
    text: encodeFinding({
      key: 'profile:conclusion.strength',
      params: { code: r.dimension_code, name: r.dimension_name, score: r.score as number },
    }),
  }));
}

/** 短板：短板集合（参与聚合维度中最低分，并列全选）内分数 < 60 的维度全部列出。 */
function buildWeaknesses(eligible: ProfileDimensionRow[]): ConclusionFinding[] {
  if (eligible.length === 0) return [];
  let min = eligible[0].score as number;
  for (const r of eligible) min = Math.min(min, r.score as number);
  if (min >= WEAKNESS_THRESHOLD) return [];
  return eligible
    .filter((r) => r.score === min)
    .map((r) => ({
      kind: 'weakness' as const,
      text: encodeFinding({
        key: 'profile:conclusion.weakness',
        params: { code: r.dimension_code, name: r.dimension_name, score: r.score as number },
      }),
    }));
}

/** 风险：按模块各拼一条「N 个维度降权、M 个维度缺失」计数摘要（M 含 failed 行，specs §4.2.4 规则8 数据缺失口径）；管理模块 pending 拼待评估。 */
function buildRisks(detail: ProfileDetail): ConclusionFinding[] {
  const findings: ConclusionFinding[] = [];
  for (const mod of detail.modules) {
    if (mod.module === 'AI_MGMT' && mod.data_status === 'pending') {
      findings.push({
        kind: 'risk',
        text: encodeFinding({ key: 'profile:conclusion.riskMgmtPending', params: {} }),
      });
      continue;
    }
    const missing = mod.missing_count + mod.failed_count;
    if (mod.insufficient_count > 0 || missing > 0) {
      findings.push({
        kind: 'risk',
        text: encodeFinding({
          key: 'profile:conclusion.riskCounts',
          params: {
            module: mod.module,
            degraded: mod.insufficient_count,
            missing,
          },
        }),
      });
    }
  }
  return findings;
}

/** 趋势：两模块 change_vs_prev 升/降条目（后端已按取整分差计算，前端直接消费）。 */
function buildTrends(detail: ProfileDetail): ConclusionFinding[] {
  const findings: ConclusionFinding[] = [];
  for (const mod of detail.modules) {
    const change = mod.change_vs_prev;
    if (change === null || change === 0) continue;
    findings.push({
      kind: 'trend',
      text: encodeFinding({
        key: change > 0 ? 'profile:conclusion.trendUp' : 'profile:conclusion.trendDown',
        params: { module: mod.module, change: Math.abs(change) },
      }),
    });
  }
  return findings;
}

/** buildConclusion：按 B1 响应 + 维度树（include_overview 判据，03 §1.9）拼装主文与四类发现。
 *  detail 为 B1 data；dimensionTree 为 /dimensions/tree 数据（取启用维度与 include_overview）。
 *  返回 { headlineKey, headlineParams, findings }；findings 条目 text 承载 i18n 键与插值参数，
 *  由消费组件 t() 渲染，保证纯函数可测。空类不产出条目。 */
export function buildConclusion(detail: ProfileDetail, dimensionTree: DimensionTreeNode): ConclusionResult {
  const overview = overviewCodes(dimensionTree);
  // B1 dimensions[] 仅含两业务模块（03 B1 字段表），九型不回传；参与聚合判据统一走 overview + normal 行。
  const eligible = eligibleRows(detail.dimensions, overview);
  const findings: ConclusionFinding[] = [
    ...buildStrengths(eligible),
    ...buildWeaknesses(eligible),
    ...buildRisks(detail),
    ...buildTrends(detail),
  ];
  return {
    ...buildHeadline(detail, eligible),
    findings,
  };
}
