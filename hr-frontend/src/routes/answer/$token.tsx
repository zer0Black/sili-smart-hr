import { createFileRoute } from '@tanstack/react-router';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

export const Route = createFileRoute('/answer/$token')({
  component: AnswerPage,
});

function AnswerPage() {
  const { token } = Route.useParams();

  return (
    <div className="bg-muted/40 flex min-h-svh items-center justify-center p-4">
      <Card className="max-w-md">
        <CardHeader>
          <CardTitle>员工作答页</CardTitle>
        </CardHeader>
        <CardContent className="text-muted-foreground space-y-2 text-sm">
          <p>B 档占位结构：一次性令牌鉴权，与主平台 JWT 体系隔离。</p>
          <p>token: {token}</p>
        </CardContent>
      </Card>
    </div>
  );
}
