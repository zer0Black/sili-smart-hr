import { createFileRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useMe } from '@/features/account/hooks';

export const Route = createFileRoute('/_authenticated/')({
  component: HomePage,
});

function HomePage() {
  const { t } = useTranslation('account');
  const { data, isLoading } = useMe();

  return (
    <Card className="max-w-md">
      <CardHeader>
        <CardTitle>{t('homeTitle')}</CardTitle>
      </CardHeader>
      <CardContent className="text-muted-foreground space-y-2 text-sm">
        <p>{t('homeDesc')}</p>
        {isLoading && <p>{t('loading', { ns: 'common' })}</p>}
        {data?.account && (
          <p>
            {t('currentUser')}: {data.account.name}（{data.account.username}）
          </p>
        )}
      </CardContent>
    </Card>
  );
}
