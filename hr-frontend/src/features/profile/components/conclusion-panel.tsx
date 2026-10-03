// 核心结论区（specs P2_PRF_001 §4.2.2 C / §4.2.4 规则2）：规则拼装不调 LLM。
// 结论计算在 conclusion.ts 纯函数（T3），此处只做 {key, params} JSON 串解析 + t() 渲染。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DimensionTreeNode, ProfileDetail } from '@/lib/contracts';

import { buildConclusion, type ConclusionFinding } from '../conclusion';

interface FindingPayload {
  key: string;
  params: Record<string, string | number>;
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

function renderFinding(t: (key: string, opts?: Record<string, unknown>) => string, text: string): string {
  const parsed = parseFinding(text);
  if (!parsed) return text;
  return t(parsed.key, parsed.params);
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
        <p className="text-sm leading-relaxed">{t(headlineKey, headlineParams)}</p>
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
