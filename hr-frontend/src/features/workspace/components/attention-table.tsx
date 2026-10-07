// 需要关注的人表格（specs P2_WRK_001 §4.1.2 D / §4.1.3 查看画像跳转 / §8.3 偏离1、偏离3）：
// 六列表格（姓名+首字母头像、活跃度三态 Badge、两模块总分、关注原因、操作），至多 10 行无分页；
// 总分 null 待评估、< 60 destructive 红色。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import i18n from '@/i18n/config';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { buildAttentionReason, type AttentionReasonLabels, type WorkspaceModuleCode } from '@/features/workspace/types';
import type { WorkspaceAttentionRow } from '@/lib/contracts';
import { cn } from '@/lib/utils';

/** 关注原因句式模板（i18n，specs §4.1.2 D）：weak 条目与 unused 天数两态。 */
const reasonLabels: AttentionReasonLabels = {
  daysSinceActive: (n) => i18n.t('workspace:attention.reasonDays', { n }),
  weakModule: (module, score, dims) =>
    i18n.t('workspace:attention.reasonWeak', { module, score, dims: dims === '' ? '' : i18n.t('workspace:attention.reasonWeakDims', { dims }) }),
};

/** 首字母头像占位：取姓名首个字符。 */
function AvatarInitial(props: { name: string }): JSX.Element {
  const { name } = props;
  return (
    <span
      aria-hidden
      className="bg-muted text-muted-foreground inline-flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-medium"
    >
      {name.charAt(0)}
    </span>
  );
}

/** 总分单元格：null 待评估（§8.3 偏离3）；< 60 destructive 红色（specs §4.1.2 D）。 */
function ScoreCell(props: { score: number | null }): JSX.Element {
  const { t } = useTranslation('workspace');
  const { score } = props;
  if (score === null) {
    return <span className="text-muted-foreground text-sm">{t('attention.pendingScore')}</span>;
  }
  return (
    <span className={cn('font-mono text-sm', score < 60 && 'text-destructive')}>{score}</span>
  );
}

export function AttentionTable(props: {
  rows: WorkspaceAttentionRow[];
  formatModule: (m: WorkspaceModuleCode) => string;
  formatDimension: (code: string) => string;
}): JSX.Element {
  const { t } = useTranslation('workspace');
  const navigate = useNavigate();
  const { rows, formatModule, formatDimension } = props;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('attention.title')}</CardTitle>
        {/* 查看全员画像跳 F9 列表（specs §4.1.3 / BR8） */}
        <button
          type="button"
          onClick={() => {
            void navigate({ to: '/profile' });
          }}
          className="text-primary inline-flex cursor-pointer items-center gap-0.5 text-xs hover:underline"
        >
          {t('attention.viewAll')}
        </button>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          // 空态且无分页控件（§8.3 偏离1：固定速览不分页）
          <p className="text-muted-foreground py-8 text-center text-sm">{t('attention.empty')}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('attention.colName')}</TableHead>
                <TableHead>{t('attention.colActivity')}</TableHead>
                <TableHead>{t('attention.colAiUsage')}</TableHead>
                <TableHead>{t('attention.colAiMgmt')}</TableHead>
                <TableHead>{t('attention.colReason')}</TableHead>
                <TableHead className="text-right">{t('attention.colActions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={`${row.category}-${row.staff_name}`}>
                  <TableCell>
                    <span className="flex items-center gap-2 font-medium">
                      <AvatarInitial name={row.staff_name} />
                      {row.staff_name}
                    </span>
                  </TableCell>
                  {/* 活跃度三态 Badge：unused 中性弱化，活跃/低频同基础样式（specs §4.1.5） */}
                  <TableCell>
                    <Badge variant={row.activity_level === 'unused' ? 'secondary' : 'default'}>
                      {t(`activityLevel.${row.activity_level}`)}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <ScoreCell score={row.ai_usage_score} />
                  </TableCell>
                  <TableCell>
                    <ScoreCell score={row.ai_mgmt_score} />
                  </TableCell>
                  <TableCell className="max-w-72 text-sm">
                    {buildAttentionReason(row, reasonLabels, formatModule, formatDimension) || '-'}
                  </TableCell>
                  <TableCell className="text-right">
                    {/* 查看画像跳个人画像详情（specs §4.1.3 / BR8） */}
                    <Button
                      variant="link"
                      size="sm"
                      onClick={() => {
                        void navigate({
                          to: '/profile/$staffName',
                          params: { staffName: row.staff_name },
                        });
                      }}
                    >
                      {t('attention.actionView')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
