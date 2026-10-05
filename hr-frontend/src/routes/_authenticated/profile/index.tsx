import { createFileRoute, useNavigate, useSearch } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ProfileTable } from '@/features/profile/components/profile-table';

export const Route = createFileRoute('/_authenticated/profile/')({
  // search 参数 dimension_code/unused_only：看板短板标签与未使用卡携条件深链进入（specs §4.1.3 / §7.2）
  validateSearch: (search: Record<string, unknown>): { dimension_code?: string; unused_only?: string } => ({
    dimension_code: typeof search.dimension_code === 'string' && search.dimension_code !== '' ? search.dimension_code : undefined,
    unused_only: typeof search.unused_only === 'string' && search.unused_only === 'true' ? 'true' : undefined,
  }),
  component: ProfileListPage,
});

/** 人员画像列表页（specs §4.1.1）：页面级组装标题 + ProfileTable，筛选与分页在表格内部自治。 */
export function ProfileListPage() {
  const { t } = useTranslation('profile');
  const navigate = useNavigate();
  const search = useSearch({ from: '/_authenticated/profile/' });

  // 深链预填只在首挂载读一次 search（惰性初始化），后续清参重渲染不重置筛选
  const [initialFilter] = useState(() => ({
    dimension_code: search.dimension_code,
    unused_only: search.unused_only === 'true',
  }));

  // 消费后清参：一次性深链语义，防刷新重入（question-bank 先例）
  useEffect(() => {
    if (search.dimension_code || search.unused_only) {
      void navigate({ to: '/profile', search: {}, replace: true });
    }
  }, [search.dimension_code, search.unused_only, navigate]);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>
      <ProfileTable initialFilter={initialFilter} />
    </div>
  );
}
