// LLMConfigPage 页面测试（specs §4.2.1 / §4.2.4 / §4.2.5 / BR1-BR7 核心断言）
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import i18n from '@/i18n/config';

// Switch 依赖 Radix，jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，补桩
const origHasPointerCapture = Element.prototype.hasPointerCapture;
const origReleasePointerCapture = Element.prototype.releasePointerCapture;
const origScrollIntoView = Element.prototype.scrollIntoView;

const listMock = vi.fn();
const enableMock = vi.fn();
vi.mock('@/features/llm-config/hooks', () => ({
  useLLMConfigs: (...args: unknown[]) => {
    const data = listMock(...args);
    return {
      data,
      isLoading: false,
      isError: false,
    };
  },
  useEnableLLMConfig: () => ({
    mutate: (...args: unknown[]) => enableMock(...args),
    isPending: false,
  }),
}));

const fetchDetailMock = vi.fn();
vi.mock('@/features/llm-config/api', () => ({
  fetchLLMDetail: (...args: unknown[]) => fetchDetailMock(...args),
}));

// 弹窗组件占位：避免触发 RSA / 公钥 / clipboard 等副作用，仅渲染占位以证明挂载点存在
vi.mock('@/features/llm-config/components/llm-form-dialog', () => ({
  LLMFormDialog: () => <div data-testid="form-dialog-stub" />,
}));
vi.mock('@/features/llm-config/components/llm-view-dialog', () => ({
  LLMViewDialog: () => <div data-testid="view-dialog-stub" />,
}));
vi.mock('@/features/llm-config/components/llm-delete-dialog', () => ({
  LLMDeleteDialog: () => <div data-testid="delete-dialog-stub" />,
}));
// 集成密钥卡片（子计划 04）：页面测试只校验挂载点存在，stub 出真实组件避免 QueryClient 依赖。
vi.mock('@/features/integration-secret/components/integration-secret-card', () => ({
  IntegrationSecretCard: () => (
    <div data-testid="integration-secret-card-stub">sili-smart-ap日志集成密钥</div>
  ),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { Route } from '@/routes/_authenticated/system/llm/index';
import type { LLMConfigItem } from '@/lib/contracts';

beforeEach(() => {
  void i18n.changeLanguage('zh');
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
});

afterEach(() => {
  Element.prototype.hasPointerCapture = origHasPointerCapture;
  Element.prototype.releasePointerCapture = origReleasePointerCapture;
  Element.prototype.scrollIntoView = origScrollIntoView;
});

const itemA: LLMConfigItem = {
  id: '1780000000000000101',
  name: '主力模型',
  provider: 'deepseek',
  model_id: 'deepseek-chat',
  api_url: 'https://api.deepseek.com/v1',
  api_key_masked: 'sk-1***ab12',
  enabled: true,
  version: 1,
  created_at: '2026-08-12T10:00:00Z',
  updated_at: '2026-08-12T10:00:00Z',
};
const itemB: LLMConfigItem = {
  id: '1780000000000000102',
  name: '备用模型',
  provider: 'openai',
  model_id: 'gpt-4o',
  api_url: '',
  api_key_masked: 'sk-9***xyz',
  enabled: false,
  version: 2,
  created_at: '2026-08-12T10:00:00Z',
  updated_at: '2026-08-12T10:00:00Z',
};

const LLMConfigPage = Route.options.component as React.ComponentType;

describe('LLMConfigPage（§4.2.1 / §4.2.4 / §4.2.5 / BR1-BR7）', () => {
  beforeEach(() => {
    listMock.mockReset();
    enableMock.mockReset();
    fetchDetailMock.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it('BR4：mock 返 2 条，列表渲染 2 行模型名称 + 底部统计「共 2 个模型，当前启用：主力模型」', () => {
    listMock.mockReturnValue([itemA, itemB]);
    render(<LLMConfigPage />);
    expect(screen.getByText('主力模型')).toBeInTheDocument();
    expect(screen.getByText('备用模型')).toBeInTheDocument();
    // 底部统计
    expect(screen.getByText(/共 2 个模型/)).toBeInTheDocument();
    expect(screen.getByText(/当前启用：主力模型/)).toBeInTheDocument();
  });

  it('BR4：空列表显示空状态文案，隐藏底部统计', () => {
    listMock.mockReturnValue([]);
    render(<LLMConfigPage />);
    expect(screen.getByText('暂无模型')).toBeInTheDocument();
    expect(screen.queryByText(/共/)).not.toBeInTheDocument();
  });

  it('BR1：点击非启用项的开关触发 enableLLMConfig(id)', async () => {
    listMock.mockReturnValue([itemA, itemB]);
    render(<LLMConfigPage />);
    // itemB（备用模型）非启用，其 Switch 的 button 应可点
    const switches = document.querySelectorAll('button[data-slot="switch"]');
    // 第一行 itemA 启用（disabled），第二行 itemB 未启用（可点）
    expect(switches.length).toBeGreaterThanOrEqual(2);
    const target = switches[1] as HTMLElement;
    expect((target as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(target);
    // enableMock 是 useEnableLLMConfig().mutate，page 以 mutate(id, callbacks) 调用，
    // 故断言首参为 itemB.id。
    await waitFor(() => expect(enableMock).toHaveBeenCalled());
    expect(enableMock.mock.calls[0][0]).toBe(itemB.id);
  });

  it('BR2：当前启用项的开关 disabled，关闭方向不可点', () => {
    listMock.mockReturnValue([itemA, itemB]);
    render(<LLMConfigPage />);
    const switches = document.querySelectorAll('button[data-slot="switch"]');
    // 第一行 itemA 启用，Switch 应 disabled
    expect(switches[0]).toBeDisabled();
    // 点击不触发 enable
    fireEvent.click(switches[0]);
    expect(enableMock).not.toHaveBeenCalled();
  });

  it('BR5：模型名称下方渲染等宽 model_id 副标题，API 地址等宽，API Key 掩码呈现', () => {
    listMock.mockReturnValue([itemA]);
    render(<LLMConfigPage />);
    expect(screen.getByText('deepseek-chat')).toBeInTheDocument();
    expect(screen.getByText('https://api.deepseek.com/v1')).toBeInTheDocument();
    expect(screen.getByText('sk-1***ab12')).toBeInTheDocument();
  });

  it('BR3：点击眼睛图标调 fetchLLMDetail 取明文，警示色呈现，再次点击隐藏', async () => {
    listMock.mockReturnValue([itemA]);
    fetchDetailMock.mockResolvedValue({
      ...itemA,
      api_key: 'sk-plaintext-secret',
    });
    render(<LLMConfigPage />);
    const revealBtn = screen.getByRole('button', { name: '查看 API Key 明文' });
    fireEvent.click(revealBtn);
    await waitFor(() => expect(fetchDetailMock).toHaveBeenCalledWith(itemA.id));
    await waitFor(() =>
      expect(screen.getByText('sk-plaintext-secret')).toBeInTheDocument(),
    );
    // 警示色：明文 span 含 text-warning class
    const plain = screen.getByText('sk-plaintext-secret');
    expect(plain.className).toContain('text-warning');
    // 再次点击隐藏
    const hideBtn = screen.getByRole('button', { name: '隐藏 API Key 明文' });
    fireEvent.click(hideBtn);
    expect(screen.queryByText('sk-plaintext-secret')).not.toBeInTheDocument();
  });

  it('BR6：下区渲染 IntegrationSecretCard 占位（标题与占位文案）', () => {
    listMock.mockReturnValue([itemA]);
    render(<LLMConfigPage />);
    expect(screen.getByText('sili-smart-ap日志集成密钥')).toBeInTheDocument();
  });

  it('BR1 排他启用成功 toast「已切换启用模型」', async () => {
    listMock.mockReturnValue([itemA, itemB]);
    enableMock.mockImplementation((_id: string, cb: any) => {
      cb?.onSuccess?.();
    });
    render(<LLMConfigPage />);
    const switches = document.querySelectorAll('button[data-slot="switch"]');
    await userEvent.click(switches[1] as HTMLElement);
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('已切换启用模型'),
    );
  });
});
