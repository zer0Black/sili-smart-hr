// 本期画像速览区（specs P2_WRK_001 §4.1.2 C / §4.1.3 短板标签与看板入口 / §4.1.4 规则5）：
// 双模块雷达（复用 module-radar-card 形态：overall_avg 虚线参考线 + null 断轴）+
// 九型柱状图（主导型 primary 高亮恒 9 项）+ 共性短板标签（跳 F9）+ 研判摘要 + 完整看板入口。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import type { JSX } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
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

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { WorkspaceModule, WorkspaceProfile } from '@/lib/contracts';

const DIMENSION_COLOR = 'var(--chart-1)';
const REFERENCE_COLOR = 'var(--chart-2)';
const MAIN_BAR_COLOR = 'var(--primary)';
const BAR_COLOR = 'var(--muted)';

export function ProfileBrief(props: { data: WorkspaceProfile }): JSX.Element {
  const { t } = useTranslation('workspace');
  const { t: td } = useTranslation('dashboard');
  const { t: tp } = useTranslation('profile');
  const navigate = useNavigate();
  const { data } = props;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('profile.title')}</CardTitle>
        {/* 查看完整团队看板跳 /dashboard（specs §4.1.3 / BR7） */}
        <button
          type="button"
          onClick={() => {
            void navigate({ to: '/dashboard' });
          }}
          className="text-primary inline-flex cursor-pointer items-center gap-0.5 text-xs hover:underline"
        >
          {t('profile.viewDashboard')}
          <ChevronRight className="size-3.5" aria-hidden />
        </button>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
          {data.modules.map((m) => (
            <ModuleRadar
              key={m.module}
              data={m}
              moduleLabel={td(`module.${m.module}`)}
              dimensionAvgLabel={td('module.dimensionAvg')}
              referenceAvgLabel={td('module.referenceAvg')}
            />
          ))}
        </div>
        <EnneagramBrief
          data={data.enneagram}
          typeName={(type) => (type >= '1' && type <= '9' ? tp(`enneagram.type${type}`) : type)}
        />
        {/* 共性短板标签行：warning 底色，点击携维度 code 跳 F9（specs §4.1.3 / BR6，口径同 F10） */}
        {data.weaknesses.length > 0 ? (
          <div className="flex flex-col gap-1.5">
            <span className="text-muted-foreground text-xs">{t('profile.weaknessTitle')}</span>
            <div className="flex flex-wrap gap-2">
              {data.weaknesses.map((w) => (
                <button
                  key={`${w.module}-${w.dimension_code}`}
                  type="button"
                  onClick={() => {
                    void navigate({
                      to: '/profile',
                      search: { dimension_code: w.dimension_code },
                    });
                  }}
                  className="cursor-pointer rounded-md border border-warning/40 bg-warning/10 px-2.5 py-1 text-xs transition-colors hover:bg-warning/20"
                >
                  <span className="font-medium">{w.dimension_name}</span>
                  <span className="text-muted-foreground ml-1.5 font-mono">
                    {w.low_ratio.toFixed(1)}%
                  </span>
                </button>
              ))}
            </div>
          </div>
        ) : null}
        {/* 研判摘要：status=generated 显示 summary，其余态暂无数据；短板标签照常展示（规则5） */}
        <div className="flex flex-col gap-1.5">
          <span className="text-muted-foreground text-xs">{t('profile.suggestionTitle')}</span>
          <p className="text-sm leading-relaxed">
            {data.suggestion.status === 'generated' && data.suggestion.summary !== ''
              ? data.suggestion.summary
              : t('profile.suggestionEmpty')}
          </p>
        </div>
      </CardContent>
    </Card>
  );
}

/** 双模块雷达（module-radar-card 同形态）：null 轴断开 + overall_avg 虚线参考线（null 隐藏）。 */
function ModuleRadar(props: {
  data: WorkspaceModule;
  dimensionAvgLabel: string;
  referenceAvgLabel: string;
  moduleLabel: string;
}): JSX.Element {
  const { data, dimensionAvgLabel, referenceAvgLabel, moduleLabel } = props;
  const radarData = data.dimensions.map((d) => ({
    dimension: d.dimension_name,
    avg: d.avg_score ?? undefined,
    overall: data.overall_avg ?? undefined,
  }));

  return (
    <div className="flex flex-col gap-2">
      <h4 className="text-sm font-semibold">{moduleLabel}</h4>
      <div className="h-56 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <RadarChart data={radarData} margin={{ top: 16, right: 40, bottom: 16, left: 40 }}>
            <PolarGrid />
            <PolarAngleAxis dataKey="dimension" tick={{ fontSize: 12 }} />
            <PolarRadiusAxis domain={[0, 100]} tick={{ fontSize: 10 }} angle={90} />
            {data.overall_avg !== null ? (
              <Radar
                name={referenceAvgLabel}
                dataKey="overall"
                stroke={REFERENCE_COLOR}
                fill={REFERENCE_COLOR}
                fillOpacity={0}
                strokeDasharray="4 3"
                isAnimationActive={false}
              />
            ) : null}
            <Radar
              name={dimensionAvgLabel}
              dataKey="avg"
              stroke={DIMENSION_COLOR}
              fill={DIMENSION_COLOR}
              fillOpacity={0.15}
              isAnimationActive={false}
            />
            {/* 悬浮分值明细同 F10：number 直出、null 显示 -（specs §4.1.5） */}
            <Tooltip formatter={(value) => (typeof value === 'number' ? value : '-')} />
          </RadarChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

/** 九型柱状图：恒 9 项，主导型 primary 高亮、其余 muted（enneagram-panel 同形态，仅主导一型）。 */
function EnneagramBrief(props: {
  data: WorkspaceProfile['enneagram'];
  typeName: (type: string) => string;
}): JSX.Element {
  const { t } = useTranslation('workspace');
  const { data, typeName } = props;

  if (data === null) {
    return (
      <div className="flex h-24 items-center justify-center">
        <p className="text-muted-foreground text-sm">{t('profile.enneagramEmpty')}</p>
      </div>
    );
  }

  const byType = new Map(data.distribution.map((d) => [d.type, d]));
  const items = ['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((k) => ({
    type: k,
    value: byType.get(k)?.ratio ?? 0,
  }));

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline gap-3 text-sm">
        <span className="text-muted-foreground text-xs">{t('profile.enneagramTitle')}</span>
        <span className="font-medium">
          {t('profile.dominantType')}：
          <span>{typeName(data.dominant_type)}</span>
          <span className="text-muted-foreground ml-1.5 font-mono text-xs">
            {data.dominant_ratio.toFixed(1)}%
          </span>
        </span>
      </div>
      <div className="h-40 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={items} margin={{ top: 4, right: 4, left: -18, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="type" tickLine={false} axisLine={true} tickMargin={8} />
            <YAxis tickLine={false} axisLine={false} tickMargin={4} unit="%" domain={[0, 100]} />
            <Bar dataKey="value" radius={2}>
              {items.map((d) => (
                <Cell
                  key={d.type}
                  fill={d.type === data.dominant_type ? MAIN_BAR_COLOR : BAR_COLOR}
                />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}
