// 题目生成页路由（specs §4.3.1）：独立页面，从题库管理页「生成 AI 管理题」进入。
import { createFileRoute } from '@tanstack/react-router';

import { GenerateForm } from '@/features/question-bank/components/generate-form';

export const Route = createFileRoute('/_authenticated/question-bank/generate')({
  component: GeneratePage,
});

function GeneratePage() {
  return <GenerateForm />;
}
