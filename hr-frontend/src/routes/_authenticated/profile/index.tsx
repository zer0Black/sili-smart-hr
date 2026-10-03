import { createFileRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { ProfileTable } from '@/features/profile/components/profile-table';

export const Route = createFileRoute('/_authenticated/profile/')({
  component: ProfileListPage,
});

/** 人员画像列表页（specs §4.1.1）：页面级组装标题 + ProfileTable，筛选与分页在表格内部自治。 */
export function ProfileListPage() {
  const { t } = useTranslation('profile');
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>
      <ProfileTable />
    </div>
  );
}
