// 作答页 hero 元信息区（specs §4.1.2 A / §3.3 线框）：类型标签、标题、用途、用时/题量元信息行。
// 品牌头与居中容器在 AnswerPage 顶层，不放本组件。类型色渐变底以 primary token 表达（DESIGN.md token 约定）。
import { useTranslation } from 'react-i18next';
import { Bot, Clock, ListChecks, type LucideIcon } from 'lucide-react';
import type { JSX } from 'react';

import type { AnswerTestType } from '../answer-types';

type HeroMeta = {
  tag: string;
  title: string;
  purpose: string;
  duration: string;
  icon: LucideIcon;
};

/** 按 testType 取 i18n 预设文案与类型图标（specs §4.1.2：系统预设按 test_type 分支）。 */
function heroMeta(
  testType: AnswerTestType,
  t: (key: string, opts?: Record<string, unknown>) => string,
): HeroMeta {
  if (testType === 'enneagram') {
    return {
      tag: t('meta.enne.tag'),
      title: t('meta.enne.title'),
      purpose: t('meta.enne.purpose'),
      duration: t('meta.enne.duration'),
      icon: ListChecks,
    };
  }
  return {
    tag: t('meta.ai.tag'),
    title: t('meta.ai.title'),
    purpose: t('meta.ai.purpose'),
    duration: t('meta.ai.duration'),
    icon: Bot,
  };
}

export function AnswerHero({ testType, questionTotal }: { testType: AnswerTestType; questionTotal: number }): JSX.Element {
  const { t } = useTranslation('answer');
  const meta = heroMeta(testType, t);
  const Icon = meta.icon;

  return (
    <section className="bg-linear-135 from-primary/85 via-primary to-primary/90 relative overflow-hidden rounded-t-xl px-6 py-7 text-primary-foreground sm:px-8">
      <div
        aria-hidden
        className="absolute -right-16 -top-16 size-[200px] rounded-full bg-primary-foreground/10"
      />
      <div className="relative z-10 flex items-start justify-between gap-4">
        <div>
          <span className="inline-block rounded-full border border-primary-foreground/30 bg-primary-foreground/15 px-3 py-0.5 text-xs">
            {meta.tag}
          </span>
          <h1 className="mt-3 text-2xl font-semibold tracking-wide sm:text-[24px]">{meta.title}</h1>
          <p className="mt-2 max-w-[560px] text-[13px] leading-relaxed text-primary-foreground/90">{meta.purpose}</p>
        </div>
        <div
          aria-hidden
          className="hidden size-12 shrink-0 items-center justify-center rounded-xl bg-primary-foreground/15 md:flex"
        >
          <Icon className="size-[26px]" />
        </div>
      </div>
      <dl className="relative z-10 mt-5 flex flex-wrap gap-x-6 gap-y-2 rounded-lg bg-primary-foreground/10 px-4 py-3 text-[13px]">
        <div className="flex items-center gap-1.5">
          <Clock className="size-4 opacity-90" aria-hidden />
          <dt className="sr-only">{t('meta.durationLabel')}</dt>
          <dd>
            <span className="mr-0.5 text-primary-foreground/75">{t('meta.durationLabel')}</span>
            {meta.duration}
          </dd>
        </div>
        <div className="flex items-center gap-1.5">
          <ListChecks className="size-4 opacity-90" aria-hidden />
          <dt className="sr-only">{t('meta.countLabel')}</dt>
          <dd>
            <span className="mr-0.5 text-primary-foreground/75">{t('meta.countLabel')}</span>
            {t('meta.questionCount', { count: questionTotal })}
          </dd>
        </div>
      </dl>
    </section>
  );
}
