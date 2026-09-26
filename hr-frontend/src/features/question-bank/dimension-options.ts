// 题库域共用的维度下拉数据源 hook：从维度树取指定模块叶子维度。
import { useDimensionTree } from '@/features/dimension/hooks';
import type { DimensionBrief } from '@/lib/contracts';

/** 取指定模块的叶子维度（specs §4.1.2 A）：enabledOnly 为 true 时限当前启用集合。 */
export function useModuleDimensions(moduleCode: string, enabledOnly: boolean): DimensionBrief[] {
  const treeQ = useDimensionTree();
  const mod = treeQ.data?.modules.find((m) => m.module_code === moduleCode);
  if (!mod) return [];
  const leaves = mod.groups ? mod.groups.flatMap((g) => g.dimensions) : (mod.dimensions ?? []);
  return enabledOnly ? leaves.filter((d) => d.enabled) : leaves;
}
