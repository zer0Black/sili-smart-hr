// 时间展示格式化单一来源：RFC3339/ISO 字符串按 YYYY-MM-DD HH:mm:ss 本地格式化
// （通用规范 9），非法输入原样透传兜底。status/users/question-bank 各页共用。
import dayjs from 'dayjs';

export function formatDateTime(raw: string): string {
  return dayjs(raw).isValid() ? dayjs(raw).format('YYYY-MM-DD HH:mm:ss') : raw;
}
