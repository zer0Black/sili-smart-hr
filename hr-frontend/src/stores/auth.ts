import { create } from 'zustand';
import { persist } from 'zustand/middleware';

import type { Account } from '@/lib/contracts';

interface AuthState {
  token: string | null;
  account: Account | null;
  setAuth: (token: string, account: Account) => void;
  logout: () => void;
}

/** auth store 持久化 token 与脱敏 account（localStorage）。 */
export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      account: null,
      setAuth: (token, account) => set({ token, account }),
      logout: () => set({ token: null, account: null }),
    }),
    {
      name: 'sili-smart-hr-auth',
      partialize: (state) => ({ token: state.token, account: state.account }),
    }
  )
);
