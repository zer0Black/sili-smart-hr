// 核心结论区（specs P2_PRF_001 §4.2.2 C / §4.2.4 规则2）：规则拼装不调 LLM。
// 结论计算在 conclusion.ts 纯函数（T3），此处只做 {key, params} JSON 串解析 + t() 渲染。
// params 承载的等级枚举与模块码原值（保纯函数可测）在渲染前映射为本地化文案（T7 BR2）。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DimensionTreeNode, ProfileDetail } from '@/lib/contracts';

import { buildConclusion, type ConclusionFinding, type ConclusionParams, type LocalizableParamKey } from '../conclusion';
import { gradeLabelKey, moduleLabelKey } from '../status';

interface FindingPayload {
  key: string;
  params: ConclusionParams;
}

/** finding text {key, params} JSON 串安全解析：非法串回退原文案键路径不渲染 params。 */
function parseFinding(text: string): FindingPayload | null {
  try {
    const parsed = JSON.parse(text) as FindingPayload;
    if (typeof parsed.key === 'string') return parsed;
  } catch {
    // 非法串走回退
  }
  return null;
}

/** 插值参数本地化：等级枚举（usageGrade/mgmtGrade）与模块码（module）换成本地化文案再插值。 */
function localizeParams(t: (key: string, opts?: Record<string, unknown>) => string, params: ConclusionParams): Record<string, string | number> {
  const out: Record<string, string | number> = {};
  const gradeKeys: Record<string, Record<string, string>> = { usageGrade: gradeLabelKey, mgmtGrade: gradeLabelKey };
  for (const [k, v] of Object.entries(params)) {
    if (typeof v !== 'string') {
      out[k] = v;
    } else if (gradeKeys[k]) {
      const map = gradeKeys[k];
      out[k] = map[v] ? t(map[v]) : v;
    } else if ((k as LocalizableParamKey) === 'module') {
      out[k] = moduleLabelKey[v] ? t(moduleLabelKey[v]) : v;
    } else {
      out[k] = v;
    }
  }
  return out;
}

function renderFinding(t: (key: string, opts?: Record<string, unknown>) => string, text: string): string {
  const parsed = parseFinding(text);
  if (!parsed) return text;
  return t(parsed.key, localizeParams(t, parsed.params));
}

const FINDING_ICON: Record<ConclusionFinding['kind'], string> = {
  strength: '◆',
  weakness: '◇',
  risk: '▲',
  trend: '↗',
};

export function ConclusionPanel(props: {
  detail: ProfileDetail;
  dimensionTree: DimensionTreeNode;
}): JSX.Element {
  const { t } = useTranslation('profile');
  const { headlineKey, headlineParams, findings } = buildConclusion(props.detail, props.dimensionTree);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('conclusion.title')}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm leading-relaxed">{t(headlineKey, localizeParams(t, headlineParams))}</p>
        {findings.length > 0 ? (
          <ul className="space-y-1.5 text-sm">
            {findings.map((f, i) => (
              <li key={i} className="flex items-start gap-2">
                <span className="text-muted-foreground select-none" aria-hidden>
                  {FINDING_ICON[f.kind]}
                </span>
                <span>
                  {renderFinding(t, f.text)}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </CardContent>
    </Card>
  );
}
