import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AccountFormDialog } from '@/features/account/components/account-form-dialog';
import { DeleteConfirmDialog } from '@/features/account/components/delete-confirm-dialog';
import { ResetPasswordDialog } from '@/features/account/components/reset-password-dialog';
import { useAccounts, useToggleEnabled } from '@/features/account/hooks';
import type { AccountListItem } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export const Route = createFileRoute('/_authenticated/system/users/')({
  component: UsersPage,
});

const PAGE_SIZE = 10;

/**
 * 用户管理列表页。刻意无工号列（系统无工号约束）。搜索框 keyword 透传后端模糊匹配，
 * Switch 切换走 toggleEnabled，最后一个启用账号被拦截时 refetch 回滚。
 */
function UsersPage() {
  const { t } = useTranslation('account');
  const [keyword, setKeyword] = useState('');
  const [page, setPage] = useState(1);

  const [formOpen, setFormOpen] = useState(false);
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create');
  const [formAccount, setFormAccount] = useState<AccountListItem | undefined>(undefined);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetAccount, setResetAccount] = useState<AccountListItem | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteAccount, setDeleteAccount] = useState<AccountListItem | null>(null);

  const accountsQ = useAccounts({
    page,
    pageSize: PAGE_SIZE,
    keyword: keyword || undefined,
  });
  const toggleMut = useToggleEnabled();

  const openCreate = () => {
    setFormMode('create');
    setFormAccount(undefined);
    setFormOpen(true);
  };
  const openEdit = (acc: AccountListItem) => {
    setFormMode('edit');
    setFormAccount(acc);
    setFormOpen(true);
  };
  const openReset = (acc: AccountListItem) => {
    setResetAccount(acc);
    setResetOpen(true);
  };
  const openDelete = (acc: AccountListItem) => {
    setDeleteAccount(acc);
    setDeleteOpen(true);
  };

  // 关键词变化重置回第一页。
  const onSearchChange = (v: string) => {
    setKeyword(v);
    setPage(1);
  };

  // Switch 传目标状态 checked；失败时 refetch 回滚。
  const onToggleEnabled = (acc: AccountListItem, checked: boolean) => {
    toggleMut.mutate(
      { id: acc.id, enabled: checked },
      {
        onError: (err) => {
          const code = err instanceof ApiError ? err.code : undefined;
          toast.error(
            code === ErrCode.LastEnabledAccount
              ? t('users.errLastEnabled')
              : t('users.errGeneric'),
          );
          void accountsQ.refetch();
        },
      },
    );
  };

  const total = accountsQ.data?.total ?? 0;
  const list = accountsQ.data?.list ?? [];
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <Input
          value={keyword}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder={t('users.searchPlaceholder')}
          className="max-w-xs"
        />
        <Button onClick={openCreate}>{t('users.createAction')}</Button>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('users.colAccount')}</TableHead>
              <TableHead>{t('users.colPassword')}</TableHead>
              <TableHead>{t('users.colEnabled')}</TableHead>
              <TableHead>{t('users.colLastLogin')}</TableHead>
              <TableHead className="text-right">{t('users.colActions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {accountsQ.isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="text-muted-foreground py-6 text-center text-sm"
                >
                  {t('users.loading')}
                </TableCell>
              </TableRow>
            ) : list.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="text-muted-foreground py-6 text-center text-sm"
                >
                  {t('users.empty')}
                </TableCell>
              </TableRow>
            ) : (
              list.map((row) => (
                <TableRow key={row.id}>
                  <TableCell>
                    <div className="flex flex-col">
                      <span className="font-medium">{row.username}</span>
                      <span className="text-muted-foreground text-sm">{row.name}</span>
                    </div>
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono">
                    {t('users.passwordMask')}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Switch
                        checked={row.enabled}
                        disabled={toggleMut.isPending}
                        onCheckedChange={(checked) => onToggleEnabled(row, checked)}
                      />
                      <Badge variant={row.enabled ? 'default' : 'secondary'}>
                        {row.enabled ? t('users.enabled') : t('users.disabled')}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell className="text-sm">
                    {row.last_login_at
                      ? dayjs(row.last_login_at).format('YYYY-MM-DD HH:mm')
                      : t('users.neverLogin')}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button variant="link" size="sm" onClick={() => openEdit(row)}>
                      {t('users.editAction')}
                    </Button>
                    <Button variant="link" size="sm" onClick={() => openReset(row)}>
                      {t('users.resetAction')}
                    </Button>
                    <Button variant="link" size="sm" onClick={() => openDelete(row)}>
                      {t('users.deleteAction')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <div className="flex items-center justify-between">
        <span className="text-muted-foreground text-sm">
          {t('users.total', { total })}
        </span>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={page <= 1}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            {t('users.prevPage')}
          </Button>
          <span className="text-muted-foreground text-sm">
            {page} / {totalPages}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={page * PAGE_SIZE >= total}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('users.nextPage')}
          </Button>
        </div>
      </div>

      <AccountFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        mode={formMode}
        account={formAccount}
      />
      <ResetPasswordDialog
        open={resetOpen}
        onOpenChange={setResetOpen}
        account={resetAccount}
      />
      <DeleteConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        account={deleteAccount}
      />
    </div>
  );
}
