// 跨域共享的表单校验原子：与后端口径对齐，供 setup 向导与 account 表单复用。

/** username 字符集：字母、数字、下划线（长度由各 schema 的 min/max 约束）。 */
export const USERNAME_PATTERN = /^[A-Za-z0-9_]+$/;

/** password 强度：≥8 位且含字母与数字（数字限 ASCII，避免 \d 误纳 Unicode 数字）。 */
export const isStrongPassword = (v: string): boolean =>
  v.length >= 8 && /[A-Za-z]/.test(v) && /[0-9]/.test(v);

/** 按 Unicode 码点计数（对齐后端 utf8.RuneCountInString，规避 JS code unit 对多字节字符的偏差）。 */
export const countRunes = (s: string): number => Array.from(s).length;
