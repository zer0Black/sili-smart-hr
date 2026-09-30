import { createFileRoute } from '@tanstack/react-router';

import { AnswerPage } from '@/features/answer/components/answer-page';

export const Route = createFileRoute('/answer/$token')({
  component: AnswerRoute,
});

// 薄入口：令牌校验与三态编排全部在 AnswerPage（specs §4.1.1 一次性令牌鉴权）。
function AnswerRoute() {
  const { token } = Route.useParams();
  return <AnswerPage token={token} />;
}
