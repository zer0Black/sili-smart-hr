// 题目查看弹窗（specs §4.1.2 D / §4.1.3 查看题目 / §4A.4）。只读展示全量字段，
// 驳回题含驳回原因区块；底部「编辑」仅 source=AI 且状态启用/已停用显示（§4.1.4 规则5）。
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useQuestionDetail } from '@/features/question-bank/hooks';
import type { QuestionDetail } from '@/lib/contracts';

export interface QuestionViewDialogProps {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  questionId: string | null;
  /** 跳转编辑：查看弹窗先关闭，父层再打开编辑弹窗。 */
  onEdit: (q: QuestionDetail) => void;
}

function statusVariant(status: QuestionDetail['status']): 'default' | 'secondary' | 'destructive' {
  if (status === 'DISABLED') return 'secondary';
  if (status === 'REJECTED' || status === 'PENDING') return 'destructive';
  return 'default';
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[100px_1fr] gap-x-3 text-sm">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="min-w-0 whitespace-pre-wrap break-words">{children}</span>
    </div>
  );
}

// RFC3339 入库时间按 yyyy-MM-dd HH:mm:ss 本地格式化（通用规范 9），与 status 页
// formatStartedAt 同款 dayjs 写法（保留非法输入兜底）。
function formatTime(raw: string): string {
  return dayjs(raw).isValid() ? dayjs(raw).format('YYYY-MM-DD HH:mm:ss') : raw;
}

export function QuestionViewDialog({ open, onOpenChange, questionId, onEdit }: QuestionViewDialogProps) {
  const { t } = useTranslation('questionBank');
  const detailQ = useQuestionDetail(open ? questionId : null);
  const detail = detailQ.data;
  const showEdit = !!detail && detail.source === 'AI' && (detail.status === 'ACTIVE' || detail.status === 'DISABLED');

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[640px]">
        <DialogHeader>
          <DialogTitle>{t('view.title')}</DialogTitle>
        </DialogHeader>

        {detailQ.isLoading && (
          <p className="text-muted-foreground text-sm">{t('loading', { ns: 'common' })}</p>
        )}

        {detail && (
          <div className="flex flex-col gap-4">
            {/* 元信息标签组 + 时间与引用（§4.1.2 D / §4A.4） */}
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-sm font-medium">{detail.question_no}</span>
              <Badge variant="outline">{t(`view.source.${detail.source}`)}</Badge>
              <Badge variant="outline">{detail.dimension_name}</Badge>
              <Badge variant="outline">{t(`view.answerMode.${detail.answer_mode}`)}</Badge>
              <Badge variant={statusVariant(detail.status)}>{t(`view.status.${detail.status}`)}</Badge>
            </div>
            <div className="text-muted-foreground flex items-center gap-4 text-sm">
              <span>{t('view.createdAt', { time: formatTime(detail.created_at) })}</span>
              <span>{t('view.referenceCount', { count: detail.reference_count })}</span>
            </div>

            <div className="flex flex-col gap-3 rounded-md border p-3">
              <Row label={t('view.scenario')}>{detail.scenario}</Row>
              <Row label={t('view.requirement')}>{detail.requirement}</Row>
              <Row label={t('view.focusPoint')}>{detail.focus_point}</Row>
              {detail.status === 'REJECTED' && detail.reject_reason !== '' && (
                <Row label={t('view.rejectReason')}>
                  <span className="text-destructive">{detail.reject_reason}</span>
                </Row>
              )}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t('view.close')}
          </Button>
          {showEdit && (
            <Button
              type="button"
              onClick={() => {
                if (!detail) return;
                onOpenChange(false);
                onEdit(detail);
              }}
            >
              {t('view.edit')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
