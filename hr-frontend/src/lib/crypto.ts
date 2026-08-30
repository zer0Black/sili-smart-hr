import forge from 'node-forge';

import { httpClient } from '@/lib/http-client';
import type { PublicKeyResult } from '@/lib/contracts';

/** GET /api/auth/public-key：取 RSA 公钥与 keyId。 */
export async function fetchPublicKey(): Promise<PublicKeyResult> {
  const { data } = await httpClient.get<PublicKeyResult>('/auth/public-key');
  return data;
}

/**
 * 用后端 RSA 公钥以 RSA-OAEP（SHA-256 / MGF1-SHA-256）加密密码明文，返回 base64 密文。
 * 填充方案与后端解密严格对齐，不可偏移。
 */
export async function encryptPassword(publicKeyPEM: string, plaintext: string): Promise<string> {
  const pub = forge.pki.publicKeyFromPem(publicKeyPEM);
  const raw = pub.encrypt(plaintext, 'RSA-OAEP', {
    md: forge.md.sha256.create(),
    mgf1: { md: forge.md.sha256.create() },
  });
  return forge.util.encode64(raw);
}

/**
 * 实时拉取一次性 RSA 公钥并加密密码明文，返回密文与对应 keyId。
 *
 * 后端 keyId 严格一次性（GETDEL 取出即删），不能复用缓存里的旧 keyId，否则必然解密失败。
 * 每次提交前实时拉新公钥，确保 keyId 未被消耗。
 */
export async function encryptPasswordFresh(
  plaintext: string,
): Promise<{ passwordCipher: string; keyId: string }> {
  const pk = await fetchPublicKey();
  const passwordCipher = await encryptPassword(pk.publicKey, plaintext);
  return { passwordCipher, keyId: pk.keyId };
}
