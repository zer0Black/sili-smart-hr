// 纯前端 JWT 工具：仅做本地过期判断，不做签名校验（校验在后端）。
// 用于路由守卫前置拦截过期 token，避免携带过期 token 进入受保护页面后
// 再被 401 踢回，造成登录态闪烁。

/** 解析 JWT 的 exp 声明判断是否过期。
 *  无法解析（非标准 JWT、缺 exp）时返回 false，交由后端 401 兜底。 */
export function isTokenExpired(token: string): boolean {
  const parts = token.split('.');
  if (parts.length !== 3) return false;
  try {
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/'))) as { exp?: number };
    if (typeof payload.exp !== 'number') return false;
    return payload.exp * 1000 < Date.now();
  } catch {
    return false;
  }
}
