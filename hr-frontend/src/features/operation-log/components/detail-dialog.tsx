// 操作详情弹窗（specs P4_LOG_001 §4.2）：零请求只读展示，行数据直出。
// 变更对比表与文本详情互斥渲染（§4.2.5），item 为 null 整体不渲染（父级由列表态控制开合）。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

import { moduleBadgeTone, moduleI18nKeys } from '../module-meta';
import type { OperationLogItem } from '../types';

/** 三类 Badge 追加语义色的 module（specs §4.1.5：login info / dimension warning / question_bank success）。 */
const MODULE_TEXT_TONE: Record<string, string> = {
  login: 'text-info',
  dimension: 'text-warning',
  question_bank: 'text-success',
};

/** 类型 Badge：variant 表底色 + 三类追加 text-*（列表与弹窗同配色，§4.2.2）。 */
export function ModuleBadge(props: { module: string }): JSX.Element {
  const { t } = useTranslation();
  const key = moduleI18nKeys[props.module];
  // 未知 module（数据异常）回退原值，不因查表 undefined 崩溃
  if (!key) return <Badge variant="outline">{props.module}</Badge>;
  return (
    <Badge variant={moduleBadgeTone[props.module]} className={MODULE_TEXT_TONE[props.module]}>
      {t(key)}
    </Badge>
  );
}

/** 操作详情弹窗：上部基本信息五行 + 下部互斥变更详情（对比表或文本段落）。 */
export function DetailDialog(props: {
  item: OperationLogItem | null;
  onClose: () => void;
}): JSX.Element | null {
  const { t } = useTranslation('operationLog');
  const item = props.item;
  if (item === null) return null;

  return (
    <Dialog open onOpenChange={(v) => !v && props.onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          {/* header 显操作时间（任务契约；§4.2.1 基本信息组仍保留该行） */}
          <DialogTitle>{item.created_at}</DialogTitle>
        </DialogHeader>

        {/* 基本信息：操作时间/操作结果/操作人/操作类型/操作对象（specs §4.2.1，无 IP 与终端） */}
        <dl className="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('list.colTime')}</dt>
          <dd>{item.created_at}</dd>
          <dt className="text-muted-foreground">{t('list.colResult')}</dt>
          <dd>
            {item.result === 'success' ? (
              <Badge variant="secondary" className="text-success">
                {t('list.resultSuccess')}
              </Badge>
            ) : (
              <Badge variant="destructive">{t('list.resultFail')}</Badge>
            )}
          </dd>
          <dt className="text-muted-foreground">{t('list.colOperator')}</dt>
          <dd>{item.operator}</dd>
          <dt className="text-muted-foreground">{t('list.colModule')}</dt>
          <dd>
            <ModuleBadge module={item.module} />
          </dd>
          <dt className="text-muted-foreground">{t('list.colTarget')}</dt>
          <dd>{item.target}</dd>
        </dl>

        {/* 变更详情互斥（specs §4.2.5）：有 changes 渲染对比表，否则文本详情，再兜底摘要 */}
        {item.changes !== null && item.changes.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('detail.colField')}</TableHead>
                <TableHead>{t('detail.colBefore')}</TableHead>
                <TableHead>{t('detail.colAfter')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {item.changes.map((c) => (
                <TableRow key={c.field}>
                  <TableCell>{c.field}</TableCell>
                  <TableCell className="text-destructive line-through">{c.before}</TableCell>
                  <TableCell className="font-semibold text-success">{c.after}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : item.detail ? (
          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground text-sm">{t('detail.textDetail')}</span>
            <p className="text-sm leading-relaxed">{item.detail}</p>
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">{item.summary}</p>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={props.onClose}>
            {t('detail.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
