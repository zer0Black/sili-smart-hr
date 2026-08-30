// 顶部导航菜单：一级平铺、二级 hover 下拉。纯 Tailwind 骨架，零额外依赖。
//
// 设计口径：导航挂顶栏，不以原型侧栏为准。工作台（/）当前唯一真实路由；
// 其余业务域为「待激活」态弱化显示并以 tooltip 标注归属 Feature，随各 Feature 落地逐个接通。
import { useLocation, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

interface Leaf {
  label: string;
  feature?: string;
  // 存在 to 即接通为可点 leaf；不存在则保留「待激活」灰显。
  to?: string;
}
interface Entry {
  label: string;
  feature?: string;
  children?: Leaf[];
}

const itemCls =
  'inline-flex h-9 items-center rounded-md px-3 text-sm font-medium transition-colors';
const leafCls =
  'inline-flex w-full items-center rounded-sm px-2 py-1.5 text-sm transition-colors';

export function NavMenu() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();

  const pending = (feature?: string) =>
    feature ? `${t('nav.pendingHint')} · ${feature}` : t('nav.pendingHint');
  const isHome = location.pathname === '/';
  // 已接通 leaf 以 to 为前缀判定命中，覆盖其下子路径。
  const isPathActive = (to: string) => location.pathname.startsWith(to);

  const entries: Entry[] = [
    {
      label: t('nav.dashboard'),
      children: [
        { label: t('nav.dashboardOverview'), feature: 'F10' },
        { label: t('nav.dashboardTrend'), feature: 'F10' },
      ],
    },
    { label: t('nav.profile'), feature: 'F9' },
    {
      label: t('nav.assessment'),
      children: [
        { label: t('nav.assessmentConversation'), feature: 'F6' },
        { label: t('nav.assessmentTest'), feature: 'F7' },
        { label: t('nav.questionbank'), feature: 'F5' },
      ],
    },
    {
      label: t('nav.config'),
      children: [
        { label: t('nav.account'), to: '/system/users' },
        { label: t('nav.systemStatus'), to: '/system/status' },
        { label: t('nav.systemParam'), to: '/system/params' },
        { label: t('nav.configLlm'), to: '/system/llm' },
        { label: t('nav.dimension'), to: '/system/dimension' },
        { label: t('nav.operationLog'), feature: 'F12' },
      ],
    },
  ];

  return (
    <nav className="flex items-center gap-1">
      <button
        type="button"
        onClick={() => navigate({ to: '/' })}
        className={cn(
          itemCls,
          isHome ? 'bg-accent text-accent-foreground' : 'hover:bg-accent'
        )}
      >
        {t('nav.workspace')}
      </button>

      {entries.map((entry) =>
        entry.children ? (
          <div key={entry.label} className="group relative">
            <span className={cn(itemCls, 'hover:bg-accent')}>{entry.label}</span>
            {/* 透明桥接区：填满 span 底边到面板顶边的 mt-1 视觉缝。
                absolute 面板不撑开 .group，外 margin 会留出 hover 命中真空带，
                鼠标下滑经过缝时 group-hover 失效、菜单消失。真实桥接 div 作为
                .group 后代常驻透明覆盖缝，鼠标其上 group-hover 持续成立。 */}
            <div aria-hidden className="absolute left-0 top-full h-1 w-40" />
            <div className="invisible absolute left-0 top-full z-50 mt-1 flex min-w-40 flex-col rounded-md border bg-popover p-1 opacity-0 shadow-md transition group-hover:visible group-hover:opacity-100">
              {entry.children.map((leaf) =>
                leaf.to ? (
                  <button
                    key={leaf.label}
                    type="button"
                    onClick={() => navigate({ to: leaf.to })}
                    className={cn(
                      leafCls,
                      isPathActive(leaf.to)
                        ? 'bg-accent text-accent-foreground'
                        : 'hover:bg-accent',
                    )}
                  >
                    {leaf.label}
                  </button>
                ) : (
                  <span
                    key={leaf.label}
                    title={pending(leaf.feature)}
                    className={cn(leafCls, 'cursor-not-allowed text-muted-foreground/60')}
                  >
                    {leaf.label}
                  </span>
                ),
              )}
            </div>
          </div>
        ) : (
          <span
            key={entry.label}
            title={pending(entry.feature)}
            className={cn(itemCls, 'cursor-not-allowed text-muted-foreground/60')}
          >
            {entry.label}
          </span>
        )
      )}
    </nav>
  );
}
