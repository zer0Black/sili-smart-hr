import { createFileRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { OperationLogTable } from '@/features/operation-log/components/operation-log-table';

export const Route = createFileRoute('/_authenticated/system/operation-log')({
  component: OperationLogPage,
});

/** 操作日志页（specs §4.1）：页面级组装标题 + OperationLogTable，筛选与分页在表格内部自治。 */
export function OperationLogPage() {
  const { t } = useTranslation('operationLog');

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>
      <OperationLogTable />
    </div>
  );
}
