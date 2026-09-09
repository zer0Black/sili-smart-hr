// 维度配置页左侧树。按 MODULE_ORDER 固定顺序渲染四模块，AI_USAGE 展开分组。
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';

import type {
  DimensionBrief,
  DimensionGroupNode,
  DimensionModuleNode,
  DimensionTreeNode,
} from '@/lib/contracts';
import type {
  GroupCode,
  ModuleCode,
  ModuleMeta,
  SelectedNode,
} from '@/features/dimension/types';
import { MODULE_META } from '@/features/dimension/types';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

export interface DimensionTreeProps {
  tree: DimensionTreeNode;
  selected: SelectedNode;
  onSelect: (node: SelectedNode) => void;
  searchKeyword: string;
}

/**
 * 按 name 做本地模糊过滤（specs §4.1.5）。keyword 为空返回原数组副本，
 * 否则返回 name 含 keyword 的项（case-insensitive，便于命中英文大小写）。
 */
export function filterDimensionsByKeyword<T extends { name: string }>(
  list: readonly T[],
  keyword: string,
): T[] {
  if (!keyword) return [...list];
  const lower = keyword.trim().toLowerCase();
  if (!lower) return [...list];
  return list.filter((d) => d.name.toLowerCase().includes(lower));
}

// 后端树接口按 moduleOrder 固定顺序返回模块（dimension service），此处直接消费不再重排。

/** 判断模块节点当前是否命中选中态。 */
function isModuleSelected(selected: SelectedNode, moduleCode: string): boolean {
  return selected.kind === 'module' && selected.moduleCode === moduleCode;
}

/** 判断分组节点当前是否命中选中态。 */
function isGroupSelected(
  selected: SelectedNode,
  moduleCode: string,
  groupCode: string,
): boolean {
  return (
    selected.kind === 'group' &&
    selected.moduleCode === moduleCode &&
    selected.groupCode === groupCode
  );
}

/** 判断维度节点当前是否命中选中态。 */
function isLeafSelected(selected: SelectedNode, dimensionId: string): boolean {
  return selected.kind === 'leaf' && selected.dimensionId === dimensionId;
}

/**
 * 是否展示权重徽标（specs §4.1.2 A）：include_overview=true 且非 ACTIVITY 模块。
 * ACTIVITY 不参与总览（weightDisabled），ENNEAGRAM 的维度 include_overview 恒为 false。
 */
function shouldShowWeightBadge(
  dim: DimensionBrief,
  moduleCode: ModuleCode,
): boolean {
  return dim.include_overview && moduleCode !== 'ACTIVITY';
}

/** 维度行：左侧名称，右侧徽标。点击 onSelect 传 leaf。 */
function DimensionLeafRow({
  dim,
  moduleCode,
  selected,
  onSelect,
  t,
}: {
  dim: DimensionBrief;
  moduleCode: ModuleCode;
  selected: SelectedNode;
  onSelect: (node: SelectedNode) => void;
  t: TFunction<'dimension'>;
}) {
  const active = isLeafSelected(selected, dim.id);
  return (
    <button
      type="button"
      onClick={() => onSelect({ kind: 'leaf', dimensionId: dim.id })}
      className={cn(
        'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
        'hover:bg-accent hover:text-accent-foreground',
        active && 'bg-accent text-accent-foreground',
      )}
    >
      <span
        className={cn(
          'h-1.5 w-1.5 shrink-0 rounded-full',
          dim.enabled ? 'bg-primary' : 'bg-muted-foreground',
        )}
        aria-hidden
      />
      <span
        className={cn(
          'flex-1 truncate',
          !dim.enabled && 'text-muted-foreground line-through',
        )}
      >
        {dim.name}
      </span>
      {!dim.enabled && (
        <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
          {t('badge.disabled')}
        </Badge>
      )}
      {shouldShowWeightBadge(dim, moduleCode) && (
        <Badge variant="outline" className="px-1.5 py-0 text-[10px] font-mono">
          {dim.weight}%
        </Badge>
      )}
    </button>
  );
}

/** 分组节点（仅 AI_USAGE）：标题行 + 维度列表。 */
function GroupBranch({
  group,
  moduleCode,
  selected,
  onSelect,
  keyword,
  t,
}: {
  group: DimensionGroupNode;
  moduleCode: ModuleCode;
  selected: SelectedNode;
  onSelect: (node: SelectedNode) => void;
  keyword: string;
  t: TFunction<'dimension'>;
}) {
  const groupCode = group.group_code as GroupCode;
  const filtered = filterDimensionsByKeyword(group.dimensions, keyword);
  if (keyword && filtered.length === 0) return null;

  const groupActive = isGroupSelected(selected, moduleCode, group.group_code);

  return (
    <div className="space-y-0.5">
      <button
        type="button"
        onClick={() => onSelect({ kind: 'group', moduleCode, groupCode })}
        className={cn(
          'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
          'hover:bg-accent hover:text-accent-foreground',
          groupActive && 'bg-accent text-accent-foreground',
        )}
      >
        <span
          className="h-1.5 w-1.5 shrink-0 rounded-xs bg-muted-foreground"
          aria-hidden
        />
        <span className="flex-1 truncate font-medium">{group.name}</span>
        <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
          {t('badge.group')}
        </Badge>
      </button>
      <div className="ml-3 space-y-0.5 border-l border-border pl-2">
        {filtered.map((dim) => (
          <DimensionLeafRow
            key={dim.id}
            dim={dim}
            moduleCode={moduleCode}
            selected={selected}
            onSelect={onSelect}
            t={t}
          />
        ))}
      </div>
    </div>
  );
}

/** 模块节点：标题行；AI_USAGE 下挂分组，其余直接列维度。 */
function ModuleBranch({
  moduleNode,
  selected,
  onSelect,
  keyword,
  t,
}: {
  moduleNode: DimensionModuleNode;
  selected: SelectedNode;
  onSelect: (node: SelectedNode) => void;
  keyword: string;
  t: TFunction<'dimension'>;
}) {
  const moduleCode = moduleNode.module_code as ModuleCode;
  const meta: ModuleMeta = MODULE_META[moduleCode];
  const moduleActive = isModuleSelected(selected, moduleCode);

  let body: ReactNode = null;
  let hasHits = true;
  if (moduleNode.groups) {
    body = (
      <div className="ml-3 space-y-1 border-l border-border pl-2">
        {moduleNode.groups.map((g) => (
          <GroupBranch
            key={g.group_code}
            group={g}
            moduleCode={moduleCode}
            selected={selected}
            onSelect={onSelect}
            keyword={keyword}
            t={t}
          />
        ))}
      </div>
    );
    if (keyword) {
      hasHits = moduleNode.groups.some(
        (g) => filterDimensionsByKeyword(g.dimensions, keyword).length > 0,
      );
    }
  } else if (moduleNode.dimensions) {
    const filtered = filterDimensionsByKeyword(moduleNode.dimensions, keyword);
    hasHits = filtered.length > 0;
    body = (
      <div className="ml-3 space-y-0.5 border-l border-border pl-2">
        {filtered.map((dim) => (
          <DimensionLeafRow
            key={dim.id}
            dim={dim}
            moduleCode={moduleCode}
            selected={selected}
            onSelect={onSelect}
            t={t}
          />
        ))}
      </div>
    );
  }

  if (keyword && !hasHits) return null;

  return (
    <div className="space-y-1">
      <button
        type="button"
        onClick={() => onSelect({ kind: 'module', moduleCode })}
        className={cn(
          'flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm transition-colors',
          'hover:bg-accent hover:text-accent-foreground',
          moduleActive && 'bg-accent text-accent-foreground',
        )}
      >
        <span className="flex-1 truncate font-semibold">{t(`module.${moduleCode}`)}</span>
        {meta.isReference && (
          <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
            {t('badge.reference')}
          </Badge>
        )}
        <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
          {t('badge.module')}
        </Badge>
      </button>
      {body}
    </div>
  );
}

/**
 * 左侧维度树。仅做展示与选择，所有 mutation 在父组件。
 * 搜索关键词对维度名称做本地模糊过滤，命中维度所属的模块/分组节点保留以承载命中项。
 */
export function DimensionTree({
  tree,
  selected,
  onSelect,
  searchKeyword,
}: DimensionTreeProps) {
  const { t } = useTranslation('dimension');
  const modules = tree.modules;
  if (modules.length === 0) {
    return (
      <div className="text-muted-foreground px-2 py-6 text-center text-sm">
        {t('tree.empty')}
      </div>
    );
  }

  return (
    <nav className="space-y-1">
      {modules.map((m) => (
        <ModuleBranch
          key={m.module_code}
          moduleNode={m}
          selected={selected}
          onSelect={onSelect}
          keyword={searchKeyword}
          t={t}
        />
      ))}
    </nav>
  );
}
