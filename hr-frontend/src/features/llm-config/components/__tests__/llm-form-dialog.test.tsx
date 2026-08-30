// LLMFormDialog 弹窗交互测试（specs §4.3 / §4.2.4 规则6/7）
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

// 初始化 i18n（useTranslation 文案解析）
import i18n from '@/i18n/config';

// Mock httpClient + crypto + hooks 必须在被测模块 import 前完成
const encryptMock = vi.fn();
vi.mock('@/lib/crypto', () => ({
  encryptPasswordFresh: (...args: unknown[]) => encryptMock(...args),
  fetchPublicKey: vi.fn(),
}));

const createMock = vi.fn();
const updateMock = vi.fn();
vi.mock('@/features/llm-config/hooks', () => ({
  useCreateLLMConfig: () => ({
    mutate: createMock,
    isPending: false,
  }),
  useUpdateLLMConfig: () => ({
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

import { LLMFormDialog } from '@/features/llm-config/components/llm-form-dialog';
import type { LLMConfigItem } from '@/lib/contracts';

beforeEach(() => {
  void i18n.changeLanguage('zh');
});

const baseItem: LLMConfigItem = {
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

// 通过原生事件填充受控 Input（RHF register 监听 onChange）
function setInputValue(id: string, value: string) {
  const el = document.getElementById(id) as HTMLInputElement | null;
  if (!el) throw new Error(`input #${id} not found`);
  fireEvent.change(el, { target: { value } });
}

describe('LLMFormDialog（§4.3 / §4.2.4 规则6/7）', () => {
  beforeEach(() => {
    encryptMock.mockReset();
    createMock.mockReset();
    updateMock.mockReset();
    encryptMock.mockResolvedValue({
      passwordCipher: 'CIPHER',
      keyId: 'KEY_ID',
    });
  });

  it('create 模式：渲染所有字段为空，标题为「新增模型」', () => {
    render(
      <LLMFormDialog open mode="create" onOpenChange={() => {}} />,
    );
    expect(screen.getByText('新增模型')).toBeInTheDocument();
    expect((document.getElementById('llm-name') as HTMLInputElement).value).toBe('');
    expect((document.getElementById('llm-model-id') as HTMLInputElement).value).toBe('');
    expect((document.getElementById('llm-api-url') as HTMLInputElement).value).toBe('');
    expect((document.getElementById('llm-api-key') as HTMLInputElement).value).toBe('');
  });

  it('create 模式：提交空表单触发 api_key 必填校验（§4.2.4 规则6）', async () => {
    render(
      <LLMFormDialog open mode="create" onOpenChange={() => {}} />,
    );
    // 填写其他字段，仅 api_key 留空
    setInputValue('llm-name', '测试模型');
    setInputValue('llm-model-id', 'test-model');
    // provider 默认 deepseek
    fireEvent.click(screen.getByRole('button', { name: /提交|新增|确认/ }));
    // 提交按钮是 form 内 type=submit，用 click 触发
    const submitBtns = screen.getAllByRole('button');
    const submit = submitBtns.find((b) => b.getAttribute('type') === 'submit');
    fireEvent.click(submit!);

    await waitFor(() => {
      expect(screen.getByText('请输入 API Key')).toBeInTheDocument();
    });
    expect(createMock).not.toHaveBeenCalled();
  });

  it('edit 模式：传入 llm 回填 name/provider/model_id/api_url，api_key 为空', () => {
    render(
      <LLMFormDialog open mode="edit" onOpenChange={() => {}} llm={baseItem} />,
    );
    expect(screen.getByText('编辑模型')).toBeInTheDocument();
    expect((document.getElementById('llm-name') as HTMLInputElement).value).toBe('主力模型');
    expect((document.getElementById('llm-model-id') as HTMLInputElement).value).toBe('deepseek-chat');
    expect((document.getElementById('llm-api-url') as HTMLInputElement).value).toBe('https://api.deepseek.com/v1');
    expect((document.getElementById('llm-api-key') as HTMLInputElement).value).toBe('');
  });

  it('provider Select 展示四个选项（deepseek/openai/zhipu/anthropic）', async () => {
    // Radix Select 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，需补桩
    const origHasPointerCapture = Element.prototype.hasPointerCapture;
    const origReleasePointerCapture = Element.prototype.releasePointerCapture;
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.releasePointerCapture = () => {};
    const origScrollIntoView = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = () => {};
    const user = userEvent.setup();
    render(
      <LLMFormDialog open mode="create" onOpenChange={() => {}} />,
    );
    await user.click(screen.getByRole('combobox'));
    await waitFor(() => {
      expect(screen.getByRole('option', { name: 'DeepSeek' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: 'OpenAI' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: '智谱 GLM' })).toBeInTheDocument();
      expect(screen.getByRole('option', { name: 'Anthropic' })).toBeInTheDocument();
    });
    Element.prototype.hasPointerCapture = origHasPointerCapture;
    Element.prototype.releasePointerCapture = origReleasePointerCapture;
    Element.prototype.scrollIntoView = origScrollIntoView;
  });

  it('create 模式：提交合法值触发 encryptPasswordFresh 与 createMutation（§4.3.3）', async () => {
    const onOpenChange = vi.fn();
    createMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: '1' });
    });
    render(
      <LLMFormDialog open mode="create" onOpenChange={onOpenChange} />,
    );
    setInputValue('llm-name', '新模型');
    setInputValue('llm-model-id', 'new-model');
    setInputValue('llm-api-key', 'sk-test-key');

    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(encryptMock).toHaveBeenCalledWith('sk-test-key'));
    await waitFor(() => expect(createMock).toHaveBeenCalled());
    expect(createMock.mock.calls[0][0]).toMatchObject({
      name: '新模型',
      provider: 'deepseek',
      model_id: 'new-model',
      api_key: 'CIPHER',
      keyId: 'KEY_ID',
    });
    // 成功后关闭弹窗
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('edit 模式：api_key 留空时 payload 不含 api_key/keyId（§4.3.4 规则2）', async () => {
    const onOpenChange = vi.fn();
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: baseItem.id });
    });
    render(
      <LLMFormDialog open mode="edit" onOpenChange={onOpenChange} llm={baseItem} />,
    );
    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(updateMock).toHaveBeenCalled());
    const payload = updateMock.mock.calls[0][0];
    expect(payload.id).toBe(baseItem.id);
    expect(payload.name).toBe('主力模型');
    expect(payload.api_key).toBeUndefined();
    expect(payload.keyId).toBeUndefined();
    expect(encryptMock).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('edit 模式：填入新 api_key 触发加密并附 keyId', async () => {
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: baseItem.id });
    });
    render(
      <LLMFormDialog open mode="edit" onOpenChange={() => {}} llm={baseItem} />,
    );
    setInputValue('llm-api-key', 'sk-new-key');
    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(encryptMock).toHaveBeenCalledWith('sk-new-key'));
    await waitFor(() => expect(updateMock).toHaveBeenCalled());
    const payload = updateMock.mock.calls[0][0];
    expect(payload.api_key).toBe('CIPHER');
    expect(payload.keyId).toBe('KEY_ID');
  });

  it('api_url 填写非 URL 触发格式校验（§4.2.4 规则6）', async () => {
    render(
      <LLMFormDialog open mode="create" onOpenChange={() => {}} />,
    );
    setInputValue('llm-name', '模型');
    setInputValue('llm-model-id', 'm1');
    setInputValue('llm-api-key', 'sk-k');
    setInputValue('llm-api-url', 'not-a-url');

    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => {
      expect(screen.getByText('API 地址需为合法 URL')).toBeInTheDocument();
    });
    expect(createMock).not.toHaveBeenCalled();
  });

  it('取消按钮清空已填并关闭弹窗', () => {
    const onOpenChange = vi.fn();
    render(
      <LLMFormDialog open mode="create" onOpenChange={onOpenChange} />,
    );
    setInputValue('llm-name', '临时');
    setInputValue('llm-api-key', 'sk-x');
    fireEvent.click(screen.getByText('取消'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    // 重新打开应已清空（reset 在 onCancel 触发）
    expect((document.getElementById('llm-name') as HTMLInputElement).value).toBe('');
    expect((document.getElementById('llm-api-key') as HTMLInputElement).value).toBe('');
  });

  it('provider 提示与服务商协议说明文案可见（§4.2.4 规则7 / §4.3.5）', () => {
    render(
      <LLMFormDialog open mode="create" onOpenChange={() => {}} />,
    );
    expect(
      screen.getByText(
        /按所选服务商对应协议调用：DeepSeek、OpenAI、智谱 GLM 走 OpenAI 兼容协议，Anthropic 走 Anthropic 协议/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/留空使用服务商默认地址，填写则覆盖为代理地址/),
    ).toBeInTheDocument();
    // create 模式的 api_key 提示
    expect(screen.getByText(/保存后仅以掩码显示/)).toBeInTheDocument();
  });

  it('edit 模式的 api_key 提示为「留空保持原 Key 不变」', () => {
    render(
      <LLMFormDialog open mode="edit" onOpenChange={() => {}} llm={baseItem} />,
    );
    expect(screen.getByText(/留空保持原 Key 不变/)).toBeInTheDocument();
  });
});
