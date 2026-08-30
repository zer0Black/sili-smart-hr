import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ActivityRulePanel } from '@/features/dimension/components/activity-rule-panel';
import { CreateDimensionDialog } from '@/features/dimension/components/create-dimension-dialog';
import { DimensionTree } from '@/features/dimension/components/dimension-tree';
import { LeafConfigForm } from '@/features/dimension/components/leaf-config-form';
import { ModuleSummary } from '@/features/dimension/components/module-summary';
import {
  useActivityRule,
  useDimensionDetail,
  useDimensionTree,
} from '@/features/dimension/hooks';
import type { ModuleCode, SelectedNode } from '@/features/dimension/types';
import { MODULE_ORDER } from '@/features/dimension/types';
import type {
  DimensionBrief,
  DimensionModuleNode,
  DimensionTreeNode,
} from '@/lib/contracts';

export const Route = createFileRoute('/_authenticated/system/dimension/')({
  component: DimensionConfigPage,
});

/**
 * 找树里首个 enabled 叶子维度 id（specs §3.2 流程说明 / BR1）。
 * 遍历顺序：MODULE_ORDER → 模块内按树原序（AI_USAGE 取各分组的第一个命中）。
 * 全部停用或空树返回 null，调用方据此回到空状态。
 */
function findFirstEnabledLeafId(tree: DimensionTreeNode): string | null {
  const byCode = new Map<string, DimensionModuleNode>();
  for (const m of tree.modules) byCode.set(m.module_code, m);
  for (const code of MODULE_ORDER) {
    const m = byCode.get(code);
    if (!m) continue;
    // AI_USAGE 有 groups，其余直接挂 dimensions。
    const list: DimensionBrief[] = m.groups
      ? m.groups.flatMap((g) => g.dimensions)
      : m.dimensions ?? [];
    const hit = list.find((d) => d.enabled);
    if (hit) return hit.id;
  }
  return null;
}

/** 树是否首次拿到数据（用于 BR1 effect 的单次触发判定）。 */
const treeJustLoaded = (prev: boolean | undefined, cur: boolean | undefined): boolean =>
  prev !== true && cur === true;

/**
 * 维度与权重配置页（specs §3.1 页面 1）。
 * 左侧维度树 + 右侧形态切换（ACTIVITY 模块优先判为规则面板，specs §4.1.5）。
 * 默认选中首个 enabled 叶子（BR1）；新增成功选中新维度；删除成功切到首个可用叶子（BR2）。
 */
function DimensionConfigPage() {
  const { t } = useTranslation('dimension');
  const [selected, setSelected] = useState<SelectedNode>({ kind: 'empty' });
  const [searchKeyword, setSearchKeyword] = useState('');
  const [createOpen, setCreateOpen] = useState(false);

  const treeQ = useDimensionTree();
  const ruleQ = useActivityRule();
  const detailQ = useDimensionDetail(
    selected.kind === 'leaf' ? selected.dimensionId : null,
  );

  const treeHasData = treeQ.data !== undefined;

  // 树首次拿到数据时选首个 enabled 叶子（BR1）。仅依赖「数据是否到位」这一次跃迁，
  // 避免后续 refetch 抖动覆盖用户已选节点。
  const [prevTreeHasData, setPrevTreeHasData] = useState<boolean | undefined>(undefined);
  useEffect(() => {
    if (treeJustLoaded(prevTreeHasData, treeHasData) && treeQ.data) {
      const firstId = findFirstEnabledLeafId(treeQ.data);
      setSelected(firstId ? { kind: 'leaf', dimensionId: firstId } : { kind: 'empty' });
    }
    setPrevTreeHasData(treeHasData);
  }, [treeHasData, prevTreeHasData, treeQ.data]);

  // 新增维度弹窗 defaultModuleCode：当前 selected 所属模块或默认 AI_USAGE（specs §4.1.3 / §4.2.2）。
  const defaultModuleCode: ModuleCode =
    selected.kind === 'module' || selected.kind === 'group'
      ? selected.moduleCode
      : selected.kind === 'leaf' && detailQ.data
        ? (detailQ.data.module_code as ModuleCode)
        : 'AI_USAGE';

  // 删除/新增后的选中兜底（BR2）：
  // - 当前选中叶子已不在新树里（被删除）→ 切到首个可用 enabled 叶子，无则回空态。
  // effect 仅在 treeQ.data 引用变化时触发，不影响正常切换选中。
  useEffect(() => {
    if (selected.kind !== 'leaf' || !treeQ.data) return;
    const allIds = new Set<string>();
    for (const m of treeQ.data.modules) {
      if (m.groups) {
        for (const g of m.groups) {
          for (const d of g.dimensions) allIds.add(d.id);
        }
      } else if (m.dimensions) {
        for (const d of m.dimensions) allIds.add(d.id);
      }
    }
    if (!allIds.has(selected.dimensionId)) {
      const next = findFirstEnabledLeafId(treeQ.data);
      setSelected(next ? { kind: 'leaf', dimensionId: next } : { kind: 'empty' });
    }
  }, [treeQ.data, selected]);

  // 新增成功后选中新维度（specs §3.2 流程说明 3）。
  const onDimensionCreated = (newId: string) => {
    setSelected({ kind: 'leaf', dimensionId: newId });
  };

  // 右侧面板渲染（specs §4.1.5，ACTIVITY 优先于通用 module 判定）。
  const rightPanel = useMemo(() => {
    if (selected.kind === 'module' && selected.moduleCode === 'ACTIVITY') {
      return ruleQ.data ? (
        <ActivityRulePanel rule={ruleQ.data} onSaved={() => ruleQ.refetch()} />
      ) : (
        <div className="text-muted-foreground text-sm">{t('common:loading')}</div>
      );
    }
    if (selected.kind === 'module' || selected.kind === 'group') {
      const moduleCode = selected.moduleCode;
      const mod = treeQ.data?.modules.find((m) => m.module_code === moduleCode);
      let dims: DimensionBrief[] = [];
      if (mod) {
        if (mod.groups) {
          if (selected.kind === 'group') {
            const g = mod.groups.find((x) => x.group_code === selected.groupCode);
            dims = g ? g.dimensions : [];
          } else {
            // module 级别（仅 AI_USAGE 有 groups）：聚合所有分组维度。
            dims = mod.groups.flatMap((g) => g.dimensions);
          }
        } else {
          dims = mod.dimensions ?? [];
        }
      }
      return <ModuleSummary dimensions={dims} moduleCode={moduleCode} />;
    }
    if (selected.kind === 'leaf') {
      if (detailQ.isLoading || !detailQ.data) {
        return (
          <div className="text-muted-foreground text-sm">{t('common:loading')}</div>
        );
      }
      // onSaved：保存与删除成功都会回调（T3 实现）。保存场景刷新详情拿新 version，
      // 删除场景由上方 effect 兜底切选中。两侧都经 invalidateQueries 自动刷新树。
      return (
        <LeafConfigForm detail={detailQ.data} onSaved={() => detailQ.refetch()} />
      );
    }
    // kind === 'empty'：空状态引导 + 新增维度入口（BR3 第二处）。
    return (
      <div className="flex min-h-[320px] flex-col items-center justify-center gap-3 rounded-md border border-dashed p-8 text-center">
        <p className="text-base font-medium">{t('empty.title')}</p>
        <p className="text-muted-foreground text-sm">{t('empty.desc')}</p>
        <Button onClick={() => setCreateOpen(true)}>{t('empty.createAction')}</Button>
      </div>
    );
  }, [selected, ruleQ.data, ruleQ.refetch, detailQ.data, detailQ.isLoading, detailQ.refetch, treeQ.data, t]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('title')}</h1>
        <p className="text-muted-foreground text-sm">{t('subtitle')}</p>
      </div>

      {/* 顶部信息条（specs §4.1.1 + §4.1.4 规则1 / BR4）：评分下次评估生效 */}
      <div className="rounded-md border bg-muted/30 px-4 py-2.5 text-sm text-muted-foreground">
        {t('infoBar')}
      </div>

      <div className="grid grid-cols-[320px_1fr] gap-4">
        {/* 左侧维度树面板 */}
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-2">
            <Input
              value={searchKeyword}
              onChange={(e) => setSearchKeyword(e.target.value)}
              placeholder={t('tree.searchPlaceholder')}
              className="h-9"
            />
            {/* 新增维度入口第一处：树面板顶部（specs §4.1.3 / BR3） */}
            <Button size="sm" onClick={() => setCreateOpen(true)} className="shrink-0">
              {t('tree.createAction')}
            </Button>
          </div>
          <div className="rounded-md border p-2">
            {treeQ.isLoading || !treeQ.data ? (
              <div className="text-muted-foreground px-2 py-6 text-center text-sm">
                {t('common:loading')}
              </div>
            ) : (
              <DimensionTree
                tree={treeQ.data}
                selected={selected}
                onSelect={setSelected}
                searchKeyword={searchKeyword}
              />
            )}
          </div>
        </div>

        {/* 右侧形态切换面板 */}
        <div className="min-w-0">{rightPanel}</div>
      </div>

      <CreateDimensionDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        defaultModuleCode={defaultModuleCode}
        onCreated={onDimensionCreated}
      />
    </div>
  );
}
