// 维度树遍历 helper：叶子拍平与选中兜底，供配置页与树组件共用。
import type { DimensionBrief, DimensionModuleNode, DimensionTreeNode } from '@/lib/contracts';
import { MODULE_ORDER } from './types';

/** 模块叶子列表：AI_USAGE 走 groups 拍平，其余直接 dimensions。 */
export function moduleLeaves(m: DimensionModuleNode): DimensionBrief[] {
  return m.groups ? m.groups.flatMap((g) => g.dimensions) : m.dimensions ?? [];
}

/**
 * 找树里首个 enabled 叶子维度 id（specs §3.2 流程说明 / BR1）。
 * 遍历顺序：MODULE_ORDER → 模块内按树原序（AI_USAGE 取各分组的第一个命中）。
 * 全部停用或空树返回 null，调用方据此回到空状态。
 */
export function findFirstEnabledLeafId(tree: DimensionTreeNode): string | null {
  for (const code of MODULE_ORDER) {
    const m = tree.modules.find((x) => x.module_code === code);
    if (!m) continue;
    const hit = moduleLeaves(m).find((d) => d.enabled);
    if (hit) return hit.id;
  }
  return null;
}

/** 全树叶子 id 集合（BR2 删除后的选中兜底用）。 */
export function allLeafIds(tree: DimensionTreeNode): Set<string> {
  const ids = new Set<string>();
  for (const m of tree.modules) {
    for (const d of moduleLeaves(m)) ids.add(d.id);
  }
  return ids;
}
