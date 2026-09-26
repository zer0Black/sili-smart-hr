import { useEffect, useState } from 'react';

// visibilitychange 模块级单例订阅：所有消费点共享一份监听器，
// 避免每个轮询 hook 各挂一个 document listener。
const visibilityListeners = new Set<() => void>();
let visibilityListenerInstalled = false;

function ensureVisibilityListener() {
  if (visibilityListenerInstalled || typeof document === 'undefined') return;
  visibilityListenerInstalled = true;
  document.addEventListener('visibilitychange', () => {
    for (const fn of visibilityListeners) fn();
  });
}

/** usePageVisible：页面可见性状态，隐藏暂停轮询、恢复续轮。 */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(
    () => typeof document === 'undefined' || document.visibilityState === 'visible',
  );
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState === 'visible');
    visibilityListeners.add(onChange);
    ensureVisibilityListener();
    return () => {
      visibilityListeners.delete(onChange);
    };
  }, []);
  return visible;
}

/** subscribeVisibility：把回调挂进共享监听器（恢复即拉等非状态式消费点用）。 */
export function subscribeVisibility(fn: () => void): () => void {
  visibilityListeners.add(fn);
  ensureVisibilityListener();
  return () => {
    visibilityListeners.delete(fn);
  };
}
