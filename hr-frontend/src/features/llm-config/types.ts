// 大模型配置域私有类型。跨域共享契约见 @/lib/contracts。

/** 服务商枚举值，与后端 provider 字段对齐。 */
export type LLMProvider = 'deepseek' | 'openai' | 'zhipu' | 'anthropic';

/**服务商下拉选项，供 T5 弹窗 Select 消费。label 为展示名。 */
export const PROVIDER_OPTIONS: { value: LLMProvider; label: string }[] = [
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'zhipu', label: '智谱' },
  { value: 'anthropic', label: 'Anthropic' },
];

/**
 * 表单值类型。api_url 空串表示走服务商默认地址；
 * api_key 为明文，提交前由组件加密为密文并附 keyId。
 */
export interface LLMFormValues {
  name: string;
  provider: LLMProvider;
  model_id: string;
  api_url: string;
  api_key: string;
}
