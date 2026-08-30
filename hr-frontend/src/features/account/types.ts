export interface LoginPayload {
  username: string;
  /** 密码明文经 RSA-OAEP 公钥加密后的 base64 密文（见 lib/crypto.ts encryptPassword）。 */
  passwordCipher: string;
  /** 公钥接口返回的 keyId，后端据此取私钥解密。 */
  keyId: string;
}
