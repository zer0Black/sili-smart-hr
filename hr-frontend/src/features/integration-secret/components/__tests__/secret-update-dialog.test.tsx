// SecretUpdateDialog 弹窗交互测试（specs §4.5）
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

// 初始化 i18n（useTranslation 文案解析）
import i18n from '@/i18n/config';

// Mock crypto + hooks 必须在被测模块 import 前完成（hoist 规则）
const encryptMock = vi.fn();
vi.mock('@/lib/crypto', () => ({
  encryptPasswordFresh: (...args: unknown[]) => encryptMock(...args),
  fetchPublicKey: vi.fn(),
}));

const updateMock = vi.fn();
vi.mock('@/features/integration-secret/hooks', () => ({
  useUpdateIntegrationSecret: () => ({
    mutate: updateMock,
    isPending: false,
  }),
}));

vi.mock('@/features/account/hooks', () => ({
  usePublicKey: () => ({
    isError: false,
    refetch: vi.fn(),
  }),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { SecretUpdateDialog } from '@/features/integration-secret/components/secret-update-dialog';

beforeEach(() => {
  void i18n.changeLanguage('zh');
});

// 通过原生事件填充受控 Input（RHF register 监听 onChange）
function setInputValue(id: string, value: string) {
  const el = document.getElementById(id) as HTMLInputElement | null;
  if (!el) throw new Error(`input #${id} not found`);
  fireEvent.change(el, { target: { value } });
}

function getSubmitButton(): HTMLElement {
  const btn = screen
    .getAllByRole('button')
    .find((b) => b.getAttribute('type') === 'submit');
  if (!btn) throw new Error('submit button not found');
  return btn;
}

describe('SecretUpdateDialog（§4.5）', () => {
  beforeEach(() => {
    encryptMock.mockReset();
    updateMock.mockReset();
    encryptMock.mockResolvedValue({
      passwordCipher: 'CIPHER',
      keyId: 'KEY_ID',
    });
  });

  it('configured=false：标题「配置集成密钥」，按钮「确认配置」', () => {
    render(
      <SecretUpdateDialog open configured={false} version={0} onOpenChange={() => {}} />,
    );
    expect(screen.getByText('配置集成密钥')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认配置' })).toBeInTheDocument();
  });

  it('configured=true：标题「集成密钥更新」，按钮「确认更新」', () => {
    render(
      <SecretUpdateDialog open configured version={0} onOpenChange={() => {}} />,
    );
    expect(screen.getByText('集成密钥更新')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认更新' })).toBeInTheDocument();
  });

  it('提交空 secret 触发必填校验（§4.5.2）', async () => {
    render(
      <SecretUpdateDialog open configured version={0} onOpenChange={() => {}} />,
    );
    fireEvent.click(getSubmitButton());
    await waitFor(() => {
      expect(screen.getByText('请输入新集成密钥')).toBeInTheDocument();
    });
    expect(updateMock).not.toHaveBeenCalled();
    expect(encryptMock).not.toHaveBeenCalled();
  });

  it('提交合法值触发 encryptPasswordFresh 与 updateMutation（§4.5.3）', async () => {
    const onOpenChange = vi.fn();
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: '1', configured: true });
    });
    render(
      <SecretUpdateDialog open configured version={0} onOpenChange={onOpenChange} />,
    );
    setInputValue('integration-secret-input', 'new-secret-value');
    fireEvent.click(getSubmitButton());

    await waitFor(() => expect(encryptMock).toHaveBeenCalledWith('new-secret-value'));
    await waitFor(() => expect(updateMock).toHaveBeenCalled());
    expect(updateMock.mock.calls[0][0]).toMatchObject({
      secret: 'CIPHER',
      keyId: 'KEY_ID',
    });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('首次配置态 configured=false 成功后 toast「集成密钥已配置」（§4.5.3）', async () => {
    const { toast } = await import('sonner');
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: '1', configured: true });
    });
    render(
      <SecretUpdateDialog open configured={false} version={0} onOpenChange={() => {}} />,
    );
    setInputValue('integration-secret-input', 'fresh-secret');
    fireEvent.click(getSubmitButton());
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('集成密钥已配置'));
  });

  it('取消按钮清空已填并关闭弹窗（§4.5.3 取消）', () => {
    const onOpenChange = vi.fn();
    render(
      <SecretUpdateDialog open configured version={0} onOpenChange={onOpenChange} />,
    );
    setInputValue('integration-secret-input', 'temp-secret');
    fireEvent.click(screen.getByText('取消'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(
      (document.getElementById('integration-secret-input') as HTMLInputElement).value,
    ).toBe('');
  });
});
