import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

/**
 * 登录/初始化共享的公开页骨架。
 * 左侧 hero 用 block-* 粉彩拼"能力矩阵"叙事装置，右侧白色表单卡。
 * 只承载布局与品牌语言，具体表单由各路由自己渲染。
 */
export function AuthSplitLayout({
  heroEyebrow,
  heroTitle,
  heroTagline,
  heroMeta,
  formEyebrow,
  formTitle,
  formSubtitle,
  children,
  footer,
}: {
  /** 左侧 hero 顶部 eyebrow（mono 大写） */
  heroEyebrow: string;
  /** 左侧 hero 大标题 */
  heroTitle: string;
  /** 左侧 hero tagline */
  heroTagline: string;
  /** 左侧 hero 底部辅助信息（如版本号、环境提示） */
  heroMeta?: string;
  /** 右侧表单 eyebrow（mono 大写，标识当前操作类型） */
  formEyebrow: string;
  formTitle: string;
  formSubtitle: string;
  children: ReactNode;
  /** 表单下方辅助区（忘记密码链接、环境提示等） */
  footer?: ReactNode;
}) {
  const { t } = useTranslation('common');

  return (
    <div className="grid min-h-svh lg:grid-cols-[1.1fr_1fr]">
      {/* 左栏：粉彩 hero + 能力矩阵装置 */}
      <div className="relative flex flex-col justify-between overflow-hidden bg-block-lilac p-8 lg:p-12">
        {/* 顶部品牌行 */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="size-2 rounded-full bg-foreground" aria-hidden />
            <span className="font-mono text-xs uppercase tracking-[0.18em] text-foreground/80">
              {t('appTitle')}
            </span>
          </div>
          <span className="font-mono text-xs uppercase tracking-[0.18em] text-foreground/60">
            {heroEyebrow}
          </span>
        </div>

        {/* 中部标题区 */}
        <div className="max-w-md py-10 lg:py-0">
          <h1 className="text-4xl font-semibold leading-[1.08] tracking-tight text-foreground md:text-5xl">
            {heroTitle}
          </h1>
          <p className="mt-4 text-base leading-relaxed text-foreground/70 md:text-lg">
            {heroTagline}
          </p>
        </div>

        {/* 底部：能力矩阵装置 + meta */}
        <div className="flex flex-col gap-6">
          <CompetencyMatrix />
          <div className="flex items-center justify-between border-t border-foreground/10 pt-4">
            <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-foreground/60">
              {t('appTagline')}
            </span>
            {heroMeta && (
              <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-foreground/50">
                {heroMeta}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 右栏：表单 */}
      <div className="relative flex items-center justify-center bg-background p-6 lg:p-12">
        {/* 顶部 hairline 锚点（仅桌面端可见，给右栏一个上沿） */}
        <div
          aria-hidden
          className="absolute inset-x-0 top-0 hidden h-px bg-border lg:block"
        />
        <div className="w-full max-w-[380px]">
          <div className="mb-8">
            <p className="font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
              {formEyebrow}
            </p>
            <h2 className="mt-3 text-2xl font-semibold tracking-tight">{formTitle}</h2>
            <p className="mt-1.5 text-sm text-muted-foreground">{formSubtitle}</p>
          </div>
          {children}
          {footer && <div className="mt-6">{footer}</div>}
        </div>
      </div>
    </div>
  );
}

/**
 * 能力矩阵装置：用 block-* 色块拼一个抽象的人才画像矩阵。
 * 是装饰性的品牌叙事，不承担数据含义，仅呼应"能力测评"产品定位。
 * 维度缩写 PRO/COM/LEA/INN/EXE 是图表符号非自然语言，刻意双语一致不进语言包。
 */
function CompetencyMatrix() {
  const { t } = useTranslation('common');
  // 五维能力，每维给一个 0-1 的饱和度权重，用色块深浅表达
  const dimensions = [
    { label: 'PRO', value: 0.92, block: 'bg-block-navy', tone: 'text-block-lilac' },
    { label: 'COM', value: 0.78, block: 'bg-block-coral', tone: 'text-foreground/80' },
    { label: 'LEA', value: 0.65, block: 'bg-block-mint', tone: 'text-foreground/80' },
    { label: 'INN', value: 0.84, block: 'bg-block-cream', tone: 'text-foreground/80' },
    { label: 'EXE', value: 0.71, block: 'bg-block-pink', tone: 'text-foreground/80' },
  ];

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-foreground/70">
          {t('authMatrixTitle')}
        </span>
        <span className="font-mono text-[11px] tracking-[0.18em] text-foreground/50">
          /01
        </span>
      </div>
      <div className="grid grid-cols-5 gap-2">
        {dimensions.map((d) => (
          <MatrixCell key={d.label} {...d} />
        ))}
      </div>
    </div>
  );
}

function MatrixCell({
  label,
  value,
  block,
  tone,
}: {
  label: string;
  value: number;
  block: string;
  tone: string;
}) {
  // 用栅格行数表达数值强弱，行数越多值越高
  const rows = 6;
  const filledRows = Math.round(value * rows);

  return (
    <div className="flex flex-col gap-1.5">
      <div
        className={cn(
          'flex aspect-[3/4] flex-col-reverse gap-[3px] rounded-sm p-1.5',
          block
        )}
      >
        {Array.from({ length: rows }).map((_, i) => (
          <div
            key={i}
            className={cn(
              'h-[3px] rounded-full transition-opacity',
              i < filledRows ? 'bg-foreground/85' : 'bg-foreground/15'
            )}
          />
        ))}
      </div>
      <div className="flex items-baseline justify-between">
        <span className={cn('font-mono text-[10px] uppercase tracking-[0.14em]', tone)}>
          {label}
        </span>
        <span className="font-mono text-[10px] tracking-tight text-foreground/60">
          {(value * 100).toFixed(0)}
        </span>
      </div>
    </div>
  );
}
