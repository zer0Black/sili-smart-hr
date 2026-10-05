// 顶栏导航「团队看板」leaf 接通测试（specs P2_TMD_001 §3.1）：
// 两 leaf 挂 /dashboard 与 /dashboard/trend、至多一个 leaf 高亮（分层前缀匹配）、
// 趋势页一级「团队看板」高亮保持（下钻页语义）、点击导航。
// 文件级 mock router hooks + 可变 pathname：NavMenu 用 useLocation/useNavigate，
// 不需要整棵 routeTree。
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const navigateSpy = vi.fn();
let mockPathname = '/';

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>();
  return {
    ...actual,
    useLocation: () => ({ pathname: mockPathname, search: {}, href: mockPathname }),
    useNavigate: () => navigateSpy,
  };
});

import { NavMenu } from '@/components/nav-menu';
import i18n from '@/i18n/config';

// 消费真实 zh.json nav 命名空间（与 dashboard 路由测试同形态）
beforeAll(async () => {
  await i18n.changeLanguage('zh');
});

function renderNavMenu() {
  render(<NavMenu />);
  return {
    overview: screen.getByText('整体看板'),
    trend: screen.getByText('能力趋势'),
    group: screen.getByText('团队看板'),
  };
}

// 下拉由 group-hover 纯 CSS 控制，jsdom 无 :hover；leaf 按钮常驻 DOM（面板
// invisible 不卸载），直接断言 class。classList.contains 精确 token 匹配，
// 避免命中 hover:bg-accent 悬停类。
const isHighlighted = (el: HTMLElement) => el.classList.contains('bg-accent');

beforeEach(() => {
  navigateSpy.mockClear();
});

describe('顶栏导航「团队看板」两 leaf（specs §3.1）', () => {
  it('TestNavMenu_DashboardLeavesActive：路径 /dashboard 时仅整体看板 leaf 高亮；路径 /dashboard/trend 时仅能力趋势 leaf 高亮且一级「团队看板」保持高亮（§3.1 BR1/BR2）', () => {
    mockPathname = '/dashboard';
    const at = renderNavMenu();
    expect(isHighlighted(at.overview)).toBe(true);
    expect(isHighlighted(at.trend)).toBe(false);
    expect(isHighlighted(at.group)).toBe(true);
  });

  it('TestNavMenu_TrendLeafActive：路径 /dashboard/trend 时仅能力趋势 leaf 高亮、整体看板不高亮、一级「团队看板」保持高亮（§3.1 BR2）', () => {
    mockPathname = '/dashboard/trend';
    const at = renderNavMenu();
    expect(isHighlighted(at.overview)).toBe(false);
    expect(isHighlighted(at.trend)).toBe(true);
    // 趋势页为看板下钻页：一级菜单高亮保持团队看板
    expect(isHighlighted(at.group)).toBe(true);
  });

  it('TestNavMenu_DashboardLeavesNavigable：两 leaf 可点击且分别导航到 /dashboard 与 /dashboard/trend（§3.1 BR1）', () => {
    mockPathname = '/';
    const at = renderNavMenu();
    expect(at.overview.tagName).toBe('BUTTON');
    expect(at.trend.tagName).toBe('BUTTON');

    fireEvent.click(at.overview);
    expect(navigateSpy).toHaveBeenCalledWith({ to: '/dashboard' });

    navigateSpy.mockClear();
    fireEvent.click(at.trend);
    expect(navigateSpy).toHaveBeenCalledWith({ to: '/dashboard/trend' });
  });

  it('TestNavMenu_OtherPathNoHighlight：无关路径两 leaf 与一级菜单均不高亮（既有前缀匹配回归）', () => {
    mockPathname = '/profile';
    const at = renderNavMenu();
    expect(isHighlighted(at.overview)).toBe(false);
    expect(isHighlighted(at.trend)).toBe(false);
    expect(isHighlighted(at.group)).toBe(false);
  });
});
