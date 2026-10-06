// 能力面板（specs P2_PRF_001 §4.2.2 D / §4.2.3 / §4.2.5）：双 tab + 雷达 + 维度明细行。
// 维度集合与分组动态取 dimensionTree（启用维度）；展开集合由页面持有（key=dimension_code），
// 区间切换期间面板随数据区卸载重挂，展开状态在页面存活（specs §4.2.3）；管理 tab 首次切入才挂载雷达（mgmtMounted 门控）。
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import type { JSX } from 'react';
import {
  CartesianGrid,
  Line,
  LineChart,
  PolarAngleAxis,
  PolarGrid,
  PolarRadiusAxis,
  Radar,
  RadarChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import type {
  DimensionTreeNode,
  ProfileDetail,
  ProfileDimensionRow,
  ProfileEvidence,
} from '@/lib/contracts';
import { cn } from '@/lib/utils';

import type { ProfileTabKey } from '../types';
import { confidenceKey, dimensionStatusKey, evidenceSourceKey } from '../types';
import { roundScore } from '../status';

export interface DimensionPanelProps {
  detail: ProfileDetail;
  dimensionTree: DimensionTreeNode;
  activeTab: ProfileTabKey;
  onTabChange: (tab: ProfileTabKey) => void;
  /** 管理雷达是否已挂载过（首次切入后置 true 并保持，实现按需加载，specs §4.2.3）。 */
  mgmtMounted: boolean;
  onMgmtFirstActivated: () => void;
  /** 展开集合由页面持有：区间切换面板卸载重挂后展开状态不丢（specs §4.2.3）。 */
  expandedCodes: Set<string>;
  onToggleExpanded: (code: string) => void;
}

/** 单个分组渲染单元：分组标题 + 该组维度行。 */
interface DimensionGroupView {
  key: string;
  titleKey: string;
  codes: string[];
}

const MODULE_OF_TAB: Record<ProfileTabKey, 'AI_USAGE' | 'AI_MGMT'> = {
  aiUsage: 'AI_USAGE',
  aiMgmt: 'AI_MGMT',
};

const PERSONAL_COLOR = 'var(--chart-2)';
const COMPANY_COLOR = 'var(--chart-1)';

export function DimensionPanel(props: DimensionPanelProps): JSX.Element {
  const { t } = useTranslation('profile');
  const {
    detail,
    dimensionTree,
    activeTab,
    onTabChange,
    mgmtMounted,
    onMgmtFirstActivated,
    expandedCodes,
    onToggleExpanded,
  } = props;
  const moduleCode = MODULE_OF_TAB[activeTab];

  const groups = buildGroups(dimensionTree, moduleCode);
  const rowsByCode = new Map<string, ProfileDimensionRow>();
  for (const row of detail.dimensions) rowsByCode.set(row.dimension_code, row);

  const selectTab = (tab: ProfileTabKey) => {
    onTabChange(tab);
    if (tab === 'aiMgmt') onMgmtFirstActivated();
  };

  const tabCount = (tab: ProfileTabKey) =>
    countModuleDimensions(dimensionTree, MODULE_OF_TAB[tab]);

  const tabs: { key: ProfileTabKey; label: string }[] = [
    { key: 'aiUsage', label: `${t('module.aiUsage')} (${tabCount('aiUsage')})` },
    { key: 'aiMgmt', label: `${t('module.aiMgmt')} (${tabCount('aiMgmt')})` },
  ];

  return (
    <Card>
      <CardHeader>
        <div role="tablist" className="flex items-center gap-1 border-b" aria-label={t('panel.tabsLabel')}>
          {tabs.map((tab) => (
            <button
              key={tab.key}
              type="button"
              role="tab"
              aria-selected={activeTab === tab.key}
              onClick={() => selectTab(tab.key)}
              className={cn(
                '-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors',
                activeTab === tab.key
                  ? 'border-primary text-primary'
                  : 'text-muted-foreground hover:text-foreground border-transparent',
              )}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="h-72 w-full">
          {activeTab === 'aiUsage' || mgmtMounted ? (
            <DimensionRadar rows={groups.flatMap((g) => g.codes.map((c) => rowsByCode.get(c)).filter((r): r is ProfileDimensionRow => !!r))} />
          ) : null}
        </div>
        <div className="space-y-6">
          {groups.map((g) => (
            <section key={g.key} className="space-y-1">
              <h3 className="text-muted-foreground text-xs font-medium uppercase tracking-wide">
                {t(g.titleKey)}
              </h3>
              <div className="divide-y rounded-lg border">
                {g.codes.map((code) => {
                  const row = rowsByCode.get(code);
                  return row ? (
                    <DimensionRowItem
                      key={code}
                      row={row}
                      expanded={expandedCodes.has(code)}
                      onToggle={() => {
                        if (row.status === 'missing') return; // 数据缺失行不可展开（specs §4.2.3）
                        onToggleExpanded(code);
                      }}
                    />
                  ) : null;
                })}
              </div>
            </section>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

/** 模块内启用维度按 specs 分组：AI_USAGE 分 BASE/UPPER，AI_MGMT 单组。 */
function buildGroups(tree: DimensionTreeNode, moduleCode: 'AI_USAGE' | 'AI_MGMT'): DimensionGroupView[] {
  const mod = tree.modules.find((m) => m.module_code === moduleCode);
  if (!mod) return [];
  if (moduleCode === 'AI_USAGE') {
    const out: DimensionGroupView[] = [];
    for (const g of mod.groups ?? []) {
      out.push({
        key: g.group_code,
        titleKey: g.group_code === 'BASE' ? 'panel.group.base' : 'panel.group.upper',
        codes: g.dimensions.filter((d) => d.enabled).map((d) => d.code),
      });
    }
    return out;
  }
  return [
    {
      key: 'AI_MGMT',
      titleKey: 'panel.group.mgmt',
      codes: (mod.dimensions ?? []).filter((d) => d.enabled).map((d) => d.code),
    },
  ];
}

function countModuleDimensions(tree: DimensionTreeNode, moduleCode: 'AI_USAGE' | 'AI_MGMT'): number {
  return buildGroups(tree, moduleCode).reduce((n, g) => n + g.codes.length, 0);
}

function DimensionRadar(props: { rows: ProfileDimensionRow[] }): JSX.Element {
  const { t } = useTranslation('profile');
  const data = props.rows.map((r) => ({
    dimension: r.dimension_name,
    personal: r.score === null ? undefined : r.score,
    // company_avg null（<3 人）对照值为 undefined，recharts 对 nullish 取 radius=0 塌陷圆心（非断线，specs §4.2.4 规则4）
    company: r.company_avg === null ? undefined : r.company_avg,
  }));
  const hasCompany = data.some((d) => d.company !== undefined);
  return (
    <ResponsiveContainer width="100%" height="100%">
      <RadarChart data={data} margin={{ top: 16, right: 40, bottom: 16, left: 40 }}>
        <PolarGrid />
        <PolarAngleAxis dataKey="dimension" tick={{ fontSize: 12 }} />
        <PolarRadiusAxis domain={[0, 100]} tick={{ fontSize: 10 }} angle={90} />
        {hasCompany ? (
          <Radar
            name={t('panel.companyAvg')}
            dataKey="company"
            stroke={COMPANY_COLOR}
            fill={COMPANY_COLOR}
            fillOpacity={0.08}
            strokeDasharray="4 3"
            isAnimationActive={false}
          />
        ) : null}
        <Radar
          name={t('panel.personal')}
          dataKey="personal"
          stroke={PERSONAL_COLOR}
          fill={PERSONAL_COLOR}
          fillOpacity={0.25}
          isAnimationActive={false}
        />
        <Tooltip
          formatter={(value, name) => [
            typeof value === 'number' ? roundScore(value) : value,
            name,
          ]}
        />
      </RadarChart>
    </ResponsiveContainer>
  );
}

function DimensionRowItem(props: {
  row: ProfileDimensionRow;
  expanded: boolean;
  onToggle: () => void;
}): JSX.Element {
  const { t } = useTranslation('profile');
  const { row, expanded, onToggle } = props;
  const missing = row.status === 'missing';
  return (
    <div className={cn('text-sm', missing && 'text-muted-foreground')}>
      <button
        type="button"
        className={cn(
          'flex w-full items-center justify-between gap-4 px-4 py-3 text-left',
          missing ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:bg-accent/50',
        )}
        aria-expanded={missing ? undefined : expanded}
        disabled={missing}
        onClick={onToggle}
      >
        <span className="flex items-center gap-2 truncate font-medium">
          {!missing && (
            <ChevronDown
              className={cn('text-muted-foreground size-4 shrink-0 transition-transform', expanded && 'rotate-180')}
              aria-hidden
            />
          )}
          {row.dimension_name}
          {row.status === 'insufficient' && (
            <Badge variant="outline">{t(dimensionStatusKey.insufficient)}</Badge>
          )}
          {missing && <Badge variant="outline">{t(dimensionStatusKey.missing)}</Badge>}
        </span>
        <span className="flex items-center gap-2">
          {row.company_avg !== null && (
            <span className="text-muted-foreground text-xs">
              {t('panel.companyAvg')} {roundScore(row.company_avg)}
            </span>
          )}
          <span className="font-mono text-base font-semibold">
            {row.score === null ? '-' : roundScore(row.score)}
          </span>
        </span>
      </button>
      {expanded && !missing && (
        <div className="space-y-4 border-t px-4 py-4">
          {row.rationale ? (
            <p className="leading-relaxed">{row.rationale}</p>
          ) : null}
          <TrendChart trend={row.trend} />
          <EvidenceList evidences={row.evidences} />
        </div>
      )}
    </div>
  );
}

/** 走势：近 4 期折线，仅展开时 mount、收起即 unmount（外层条件渲染天然承载，specs §4.2.3）。 */
function TrendChart(props: { trend: { period_start: string; period_end: string; score: number }[] }): JSX.Element {
  const { t } = useTranslation('profile');
  if (props.trend.length === 0) {
    return <p className="text-muted-foreground text-xs">{t('panel.noTrend')}</p>;
  }
  const labelOf = (p: { period_start: string; period_end: string }) =>
    `${p.period_start} ~ ${p.period_end}`;
  const data = props.trend.map((p) => ({ label: labelOf(p), score: p.score }));
  return (
    <div className="h-40 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ top: 8, right: 16, bottom: 0, left: -16 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={true} tickMargin={8} tick={{ fontSize: 10 }} />
          <YAxis domain={[0, 100]} tickLine={false} axisLine={false} tick={{ fontSize: 10 }} width={36} />
          <Tooltip
            formatter={(value) => [
              typeof value === 'number' ? roundScore(value) : value,
              t('panel.trendScore'),
            ]}
          />
          <Line
            type="monotone"
            dataKey="score"
            stroke={PERSONAL_COLOR}
            strokeWidth={2}
            dot
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

function EvidenceList(props: { evidences: ProfileEvidence[] }): JSX.Element {
  const { t } = useTranslation('profile');
  if (props.evidences.length === 0) {
    return <p className="text-muted-foreground text-xs">{t('panel.noEvidence')}</p>;
  }
  return (
    <ul className="space-y-2">
      {props.evidences.map((ev, i) => (
        <li key={i} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <span className="font-medium">{t(evidenceSourceKey[ev.source] ?? 'evidenceSource.unknown')}</span>
          <span className="text-muted-foreground">{ev.time}</span>
          <Badge variant="secondary">{t(confidenceKey[ev.confidence])}</Badge>
          <span className="text-muted-foreground">{t('panel.sessionCount', { count: ev.session_count })}</span>
          {Object.entries(ev.summary).length > 0 ? (
            <span className="text-muted-foreground font-mono">
              {Object.entries(ev.summary)
                .map(([k, v]) => `${k}=${v}`)
                .join(' ')}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}
