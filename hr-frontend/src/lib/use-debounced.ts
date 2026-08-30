import { useEffect, useState } from 'react';

/**
 * useDebouncedValue 延迟透传快速变化的值（如搜索输入）。
 * 每次 value 变化重置定时器，delayMs 内稳定后才输出，避免逐字符打后端。
 */
export function useDebouncedValue<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState<T>(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}
