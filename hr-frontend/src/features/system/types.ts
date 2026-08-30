/**
 * 向导表单值。承载 password 明文，提交时由 encryptPasswordFresh 现场加密为
 * passwordCipher/keyId 组装 InitializePayload。confirmPassword 仅前端 zod 校验，不入请求。
 */
export interface SetupFormValues {
  username: string;
  name: string;
  password: string;
  confirmPassword: string;
}
