// 模块能力雷达卡（specs P2_TMD_001 §4.1.2 C / §4.1.4 规则3/规则4 / §4.1.3 短板与趋势跳转）：
// 雷达（avg_score null 轴断开）+ overall_avg 虚线参考线（null 隐藏）+ 共性短板标签区 + 查看逐期趋势。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import type { JSX } from 'react';
import {
  PolarAngleAxis,
  PolarGrid,
  PolarRadiusAxis,
  Radar,
  RadarChart,
  ResponsiveContainer,
  Tooltip,
} from 'recharts';

import { Card, CardContent, CardHeader } from '@/components/ui/card';
import type { DashboardModule } from '@/lib/contracts';

import type { DashboardAbilityType } from '../types';

const DIMENSION_COLOR = 'var(--chart-1)';
const REFERENCE_COLOR = 'var(--chart-2)';

/** 模块 → 趋势页 type 参数（AI_USAGE→use、AI_MGMT→manage，03 A2）。 */
const ABILITY_OF_MODULE: Record<DashboardModule['module'], DashboardAbilityType> = {
  AI_USAGE: 'use',
  AI_MGMT: 'manage',
};

export function ModuleRadarCard(props: { data: DashboardModule }): JSX.Element {
  const { t } = useTranslation('dashboard');
  const navigate = useNavigate();
  const { data } = props;

  const type = ABILITY_OF_MODULE[data.module];
  const weaknesses = data.dimensions.filter((d) => d.is_weakness);

  // avg_score null 的轴数据点为 undefined，Radar 不画该点（断轴，数据层 null 传导）
  const radarData = data.dimensions.map((d) => ({
    dimension: d.dimension_name,
    avg: d.avg_score ?? undefined,
    overall: data.overall_avg ?? undefined,
  }));

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-2">
        <div className="flex flex-col gap-0.5">
          <h3 className="text-sm font-semibold leading-none">
            {t(`module.${data.module}`)}
          </h3>
          <p className="text-muted-foreground text-xs">
            {t(type === 'use' ? 'module.sourceUsage' : 'module.sourceMgmt')}
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            // 查看逐期趋势（specs §4.1.3）：携能力类型参数跳趋势页。
            // to 走类型断言：/dashboard/trend 路由在 T3 建（routeTree.gen 届时收录），
            // 组件先行交付时路径字面量暂未进路由联合类型。
            void navigate({ to: '/dashboard/trend' as never, search: { type } as never });
          }}
          className="text-primary inline-flex cursor-pointer items-center gap-0.5 text-xs hover:underline"
        >
          {t('module.viewTrend')}
          <ChevronRight className="size-3.5" aria-hidden />
        </button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="h-64 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <RadarChart data={radarData} margin={{ top: 16, right: 40, bottom: 16, left: 40 }}>
              <PolarGrid />
              <PolarAngleAxis dataKey="dimension" tick={{ fontSize: 12 }} />
              <PolarRadiusAxis domain={[0, 100]} tick={{ fontSize: 10 }} angle={90} />
              {/* 全维度均值虚线参考线：任一维度置空（overall_avg=null）时隐藏（specs §4.1.4 规则3） */}
              {data.overall_avg !== null ? (
                <Radar
                  name={t('module.referenceAvg')}
                  dataKey="overall"
                  stroke={REFERENCE_COLOR}
                  fill={REFERENCE_COLOR}
                  fillOpacity={0}
                  strokeDasharray="4 3"
                  isAnimationActive={false}
                />
              ) : null}
              <Radar
                name={t('module.dimensionAvg')}
                dataKey="avg"
                stroke={DIMENSION_COLOR}
                fill={DIMENSION_COLOR}
                fillOpacity={0.15}
                isAnimationActive={false}
              />
              <Tooltip formatter={(value) => (typeof value === 'number' ? value : value)} />
            </RadarChart>
          </ResponsiveContainer>
        </div>
        {weaknesses.length > 0 ? (
          <div className="flex flex-col gap-1.5">
            <span className="text-muted-foreground text-xs">{t('module.weaknessTitle')}</span>
            <div className="flex flex-wrap gap-2">
              {weaknesses.map((d) => (
                <button
                  key={d.dimension_code}
                  type="button"
                  aria-label={`${d.dimension_name} ${d.avg_score ?? ''}`}
                  onClick={() => {
                    // 共性短板人群跳转（03 §4.5）：携维度 code 跳 F9 短板维度筛选
                    void navigate({
                      to: '/profile',
                      search: { dimension_code: d.dimension_code },
                    });
                  }}
                  className="cursor-pointer rounded-md border border-warning/40 bg-warning/10 px-2.5 py-1 text-xs transition-colors hover:bg-warning/20"
                >
                  <span className="font-medium">{d.dimension_name}</span>
                  <span className="text-muted-foreground ml-1.5 font-mono">{d.avg_score}</span>
                  <span className="text-muted-foreground ml-1.5 font-mono">
                    {(d.low_ratio ?? 0).toFixed(1)}%
                  </span>
                </button>
              ))}
            </div>
          </div>
        ) : (
          <p className="text-muted-foreground text-xs">{t('module.noWeakness')}</p>
        )}
      </CardContent>
    </Card>
  );
}
