// 作答页三态骨架测试（specs §3.1/§3.3 线框 / §4.1.3 进入校验 / §4.2 失效态 / §4.3 成功态 / §5.1.5 异常处理）
// mock answer-api 模块：AnswerPage 状态机分支只依赖 fetchAnswerContext 的 resolve/reject 形态。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { ReactElement } from 'react';

import i18n from '@/i18n/config';
import { routeTree } from '@/routeTree.gen';
import { AnswerApiError } from '../answer-api';
import type { AnswerContextResult } from '../answer-types';

const fetchMock = vi.hoisted(() => vi.fn());
const replyMock = vi.hoisted(() => vi.fn());
vi.mock('../answer-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../answer-api')>();
  return { ...actual, fetchAnswerContext: fetchMock, sendAnswerReply: replyMock };
});

// 路由级用例经真实 routeTree 走 __root beforeLoad 的 setup 探针，mock 其数据源防重定向 /setup。
const setupStatusMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/system/api', () => ({ fetchSetupStatus: setupStatusMock }));

import { AnswerPage } from '../components/answer-page';
import { AnswerSuccessCard } from '../components/answer-success';
import { AnswerHero } from '../components/answer-hero';

const TOKEN = 'tk-test-token-aaaaaaaaaaaaaaaaaaaaaaaaa';

const CONTEXT: AnswerContextResult = {
  task_no: 'T202609290001',
  test_type: 'ai_mgmt',
  question_total: 5,
  answered_count: 0,
  finished: false,
  questions: [],
  replies: [],
};

function renderPage(node?: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node ?? <AnswerPage token={TOKEN} />}</QueryClientProvider>);
}

beforeEach(() => {
  // jsdom 的 window.close() 会真实销毁整个 jsdom 环境（document/body 置空，级联炸掉后续用例），
  // 组件按 specs §4.3.3 真实调用，测试侧统一 stub。
  vi.stubGlobal('close', vi.fn());
  // TanStack Router scroll-restoration 调 window.scrollTo，jsdom 未实现会 throw 使路由 match 报错。
  vi.stubGlobal('scrollTo', vi.fn());
  // ThemeProvider（__root 组件树）经 next-themes 读 matchMedia（含旧式 addListener），jsdom 无实现。
  if (!window.matchMedia) {
    const mq = {
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
    };
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue(mq));
  }
  void i18n.changeLanguage('zh');
  fetchMock.mockReset();
  replyMock.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('AnswerPage 三态编排（specs §6.1 / §5.1.5）', () => {
  it('TestInvalidState：AnswerApiError(1901) 渲染失效态文案且无发送按钮（§4.2.4 规则1 统一文案）', async () => {
    fetchMock.mockRejectedValue(new AnswerApiError(1901, 'answer token invalid'));
    renderPage();

    expect(await screen.findByText('作答链接不可用')).toBeInTheDocument();
    expect(screen.getByText(/请联系测评发起人重新获取链接/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '发送' })).not.toBeInTheDocument();
    // 失效态无操作入口（§4.2.3）：无重试按钮
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
  });

  it('TestErrorStateWithRetry：网络错误渲染加载失败与重试，点击重试重新调用 fetchAnswerContext（§5.1.5）', async () => {
    fetchMock.mockRejectedValue(new AnswerApiError(0, '网络异常，请稍后重试'));
    renderPage();

    expect(await screen.findByText('加载失败，请重试')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock).toHaveBeenCalledWith(TOKEN);
  });

  it('TestLoadingState：pending 期间整页 loading 且无令牌原文泄露（§4.1.3 加载状态）', () => {
    fetchMock.mockReturnValue(new Promise<AnswerContextResult>(() => {}));
    renderPage();

    expect(screen.getByText('加载中…')).toBeInTheDocument();
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument();
  });

  it('TestAnsweryStatePlaceholder：校验通过进作答态，hero 元信息与进度占位呈现（§4.1.1 页面结构）', async () => {
    fetchMock.mockResolvedValue(CONTEXT);
    renderPage();

    expect(await screen.findByText('AI 管理能力测评')).toBeInTheDocument();
    expect(screen.getByText('共 5 题')).toBeInTheDocument();
    expect(screen.getByText('作答须知')).toBeInTheDocument();
    expect(screen.getByText('已完成 0 / 5')).toBeInTheDocument();
  });

  it('TestReplyAdvancesProgress：reply 成功后进度计数即时推进（specs §4.1.2 B 每题确认后更新）', async () => {
    fetchMock.mockResolvedValue({
      ...CONTEXT,
      questions: [1, 2, 3, 4, 5].map((seq) => ({ seq, dimension_name: `维度${seq}`, scenario: `情境${seq}`, requirement: `要求${seq}` })),
    });
    replyMock.mockResolvedValue({ question_seq: 1, answered_count: 1, question_total: 5, action: 'next', next_question: { seq: 2, dimension_name: '维度2', scenario: '情境2', requirement: '要求2' } });
    renderPage();

    const box = (await screen.findByRole('textbox')) as HTMLTextAreaElement;
    expect(screen.getByText('已完成 0 / 5')).toBeInTheDocument();
    fireEvent.change(box, { target: { value: '选 C，因为……' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));

    expect(await screen.findByText('已完成 1 / 5')).toBeInTheDocument();
    expect(screen.queryByText('已完成 0 / 5')).not.toBeInTheDocument();
  });

  it('TestReplyTokenInvalidToInvalidState：作答中 reply 收到 1901 页面转失效态（specs §4.1.4 规则5 / §5.2.5）', async () => {
    fetchMock.mockResolvedValue(CONTEXT);
    replyMock.mockRejectedValue(new AnswerApiError(1901, 'answer token invalid'));
    renderPage();

    const box = (await screen.findByRole('textbox')) as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: '选 C，因为……' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));

    expect(await screen.findByText('作答链接不可用')).toBeInTheDocument();
    expect(screen.getByText(/请联系测评发起人重新获取链接/)).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });
});

describe('AnswerSuccessCard（specs §4.3）', () => {
  it('TestSuccessCard：任务号 font-mono 呈现，点击关闭按钮调用 onClose（§4.3.3/§4.3.2）', () => {
    const onClose = vi.fn();
    renderPage(<AnswerSuccessCard testType="ai_mgmt" taskNo="T202609290001" onClose={onClose} />);

    expect(screen.getByText(/T202609290001/)).toHaveClass('font-mono');
    expect(screen.getByText('作答已提交')).toBeInTheDocument();
    // i18next 插值在 {{title}} 前后留空格，断言用宽松匹配
    expect(screen.getByText(/感谢您的参与，AI 管理能力测评 ?已成功提交/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '关闭窗口' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestSuccessCardCloseFallback：300ms 后窗口未关呈现手动关闭提示（§4.1.5 closeFallback）', async () => {
    renderPage(<AnswerSuccessCard testType="enneagram" taskNo="E202609290001" onClose={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: '关闭窗口' }));
    expect(window.close).toHaveBeenCalled();

    // 不用 fake timers：cleanup 与 i18next 语言缓存均依赖真实定时器语义
    expect(screen.queryByText(/请手动关闭浏览器标签页/)).not.toBeInTheDocument();
    expect(await screen.findByText(/请手动关闭浏览器标签页/, undefined, { timeout: 800 })).toBeInTheDocument();
  });

  it('TestSuccessCardEnneagram：enneagram 呈现九型标题与任务号', () => {
    renderPage(<AnswerSuccessCard testType="enneagram" taskNo="E202609290001" onClose={vi.fn()} />);
    expect(screen.getByText(/九型人格测评 ?已成功提交/)).toBeInTheDocument();
    expect(screen.getByText(/E202609290001/)).toBeInTheDocument();
  });
});

describe('AnswerHero（specs §4.1.2 元信息）', () => {
  it('TestHeroMeta：enneagram 取九型标签/标题/用途/用时与题量（§4.1.2 系统预设）', () => {
    renderPage(<AnswerHero testType="enneagram" questionTotal={12} />);
    expect(screen.getByText('标准量表')).toBeInTheDocument();
    expect(screen.getByText('九型人格测评')).toBeInTheDocument();
    expect(screen.getByText(/约 10-15 分钟/)).toBeInTheDocument();
    expect(screen.getByText('共 12 题')).toBeInTheDocument();
  });
});

describe('路由入口（$token.tsx 薄入口透传）', () => {
  it('TestRouteEntry：URL token 透传给 AnswerPage（fetchMock 收到路径参数）', async () => {
    setupStatusMock.mockResolvedValue({ initialized: true });
    fetchMock.mockResolvedValue(CONTEXT);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createRouter({
      routeTree,
      context: { queryClient: qc },
      defaultPreload: 'intent',
    });
    render(
      <QueryClientProvider client={qc}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    await router.navigate({ to: '/answer/$token', params: { token: 'URLTOKEN123' } });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('URLTOKEN123'));
    expect(await screen.findByText('AI 管理能力测评')).toBeInTheDocument();
  });
});
