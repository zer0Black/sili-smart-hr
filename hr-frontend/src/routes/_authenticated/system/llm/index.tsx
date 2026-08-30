import { createFileRoute } from '@tanstack/react-router';
import { Eye, EyeOff, Pencil, Plus, Trash2 } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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
import { LLMDeleteDialog } from '@/features/llm-config/components/llm-delete-dialog';
import { LLMFormDialog } from '@/features/llm-config/components/llm-form-dialog';
import { LLMViewDialog } from '@/features/llm-config/components/llm-view-dialog';
import { IntegrationSecretCard } from '@/features/integration-secret/components/integration-secret-card';
import {
  useEnableLLMConfig,
  useLLMConfigs,
} from '@/features/llm-config/hooks';
import { fetchLLMDetail } from '@/features/llm-config/api';
import type { LLMConfigDetail, LLMConfigItem } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { useDebouncedValue } from '@/lib/use-debounced';

export const Route = createFileRoute('/_authenticated/system/llm/')({
  component: LLMConfigPage,
});

/**
 * 大模型配置页（specs §3.1 / §4.2.1 / §4.2.4 / §4.2.5）。
 * 上区：模型列表 + 搜索 + 新增 + 排他启用开关 + API Key 掩码临时查看 + 弹窗编排。
 * 下区：IntegrationSecretCard（BR6 §4.2.1，本子计划占位，子计划 04 填充）。
 * 列表不分页（§4.2.5 / §8.3 偏离），全量返回，底部统计前端计算。
 */
function LLMConfigPage() {
  const { t } = useTranslation('llmConfig');
  const [keyword, setKeyword] = useState('');
  // 搜索关键字防抖，避免逐字符打后端（全量列表场景）。
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const listQ = useLLMConfigs(debouncedKeyword);
  const enableMut = useEnableLLMConfig();

  // 弹窗编排（§4.2.5）：父组件持有 create/edit/view/delete 四弹窗 open state 与选中 llm。
  const [formOpen, setFormOpen] = useState(false);
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create');
  const [formLlm, setFormLlm] = useState<LLMConfigItem | null>(null);
  const [viewOpen, setViewOpen] = useState(false);
  const [viewLlmId, setViewLlmId] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteLlm, setDeleteLlm] = useState<LLMConfigItem | null>(null);

  // API Key 临时明文（BR3 §4.2.4 规则4）：按 id 存明文，刷新/重查恢复掩码。
  const [revealedKeys, setRevealedKeys] = useState<Record<string, string>>({});
  const [revealingId, setRevealingId] = useState<string | null>(null);

  const list = listQ.data ?? [];
  // BR4：底部统计由前端据列表计算。
  const enabledItem = useMemo(() => list.find((it) => it.enabled) ?? null, [list]);

  // 列表变化（删除/重查）时把 revealedKeys 收敛到当前列表存在的 id，
  // 删除模型后旧明文自动清掉，避免残留失效密钥。
  useEffect(() => {
    if (!list.length) {
      // 函数式更新：prev 已空时返回同引用触发 React bailout，避免 loading/error 态下
      // list（listQ.data ?? []）每次渲染产新 [] 引用 → effect 重跑 → 死循环。
      setRevealedKeys((prev) => (Object.keys(prev).length === 0 ? prev : {}));
      return;
    }
    setRevealedKeys((prev) => {
      const ids = new Set(list.map((it) => it.id));
      let changed = false;
      const next: Record<string, string> = {};
      for (const [k, v] of Object.entries(prev)) {
        if (ids.has(k)) next[k] = v;
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [list]);

  const openCreate = () => {
    setFormMode('create');
    setFormLlm(null);
    setFormOpen(true);
  };
  const openEdit = (llm: LLMConfigItem) => {
    setFormMode('edit');
    setFormLlm(llm);
    setFormOpen(true);
  };
  const openView = (llm: LLMConfigItem) => {
    setViewLlmId(llm.id);
    setViewOpen(true);
  };
  const openDelete = (llm: LLMConfigItem) => {
    setDeleteLlm(llm);
    setDeleteOpen(true);
  };

  // view → edit：onEdit 回调拿 detail（LLMConfigDetail），从 LLMViewDialog 切到 LLMFormDialog。
  // LLMFormDialog 的 llm prop 是 LLMConfigItem，edit 模式只消费 name/provider/model_id/api_url，
  // 不读 api_key_masked；detail 经映射补回掩码占位以满足类型契约。
  const onViewEdit = (detail: LLMConfigDetail) => {
    setFormMode('edit');
    setFormLlm({
      id: detail.id,
      name: detail.name,
      provider: detail.provider,
      model_id: detail.model_id,
      api_url: detail.api_url,
      api_key_masked: '',
      enabled: detail.enabled,
      version: detail.version,
      created_at: detail.created_at,
      updated_at: detail.updated_at,
    });
    setFormOpen(true);
  };

  // BR1 §4.2.4 规则1：排他开关，点击非启用项直接调 enableLLMConfig(id)。
  // 后端 EnableExclusive 保证其余自动停用，前端无 disable 接口。
  // BR2 §4.2.4 规则2：当前启用项的开关禁用关闭方向，停用唯一路径是启用另一个。
  const onToggleEnable = (llm: LLMConfigItem) => {
    if (llm.enabled) return; // 启用项不可关
    enableMut.mutate(llm.id, {
      onSuccess: () => {
        toast.success(t('toast.enableSuccess'));
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        if (code === ErrCode.LastLLMConfig) {
          toast.error(t('toast.errLastModel'));
          return;
        }
        toast.error(t('toast.errGeneric'));
      },
    });
  };

  // BR3 §4.2.4 规则4：眼睛图标就地切换明文，调 fetchLLMDetail 拿明文存局部 state。
  // 明文以警示色（text-warning）呈现，刷新/重查恢复掩码。
  const onRevealKey = async (llm: LLMConfigItem) => {
    if (revealedKeys[llm.id]) {
      // 已有明文：就地隐藏。
      setRevealedKeys((prev) => {
        const next = { ...prev };
        delete next[llm.id];
        return next;
      });
      return;
    }
    setRevealingId(llm.id);
    try {
      const detail = await fetchLLMDetail(llm.id);
      setRevealedKeys((prev) => ({ ...prev, [llm.id]: detail.api_key }));
    } catch {
      toast.error(t('toast.detailError'));
    } finally {
      setRevealingId(null);
    }
  };

  const providerLabel = (provider: LLMConfigItem['provider']) =>
    t(`form.provider${provider.charAt(0).toUpperCase()}${provider.slice(1)}`);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      {/* 上区：模型列表（§4.2.1） */}
      <Card>
        <CardHeader>
          <CardTitle>{t('page.title')}</CardTitle>
          <p className="text-muted-foreground text-xs">{t('page.exclusiveHint')}</p>
          <div className="mt-2 flex items-center justify-between gap-2">
            <Input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder={t('list.searchPlaceholder')}
              className="max-w-xs"
            />
            <Button onClick={openCreate}>
              <Plus className="size-4" />
              {t('list.addAction')}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('list.colName')}</TableHead>
                  <TableHead>{t('list.colProvider')}</TableHead>
                  <TableHead>{t('list.colApiUrl')}</TableHead>
                  <TableHead>{t('list.colApiKey')}</TableHead>
                  <TableHead>{t('list.colStatus')}</TableHead>
                  <TableHead className="text-right">{t('list.colActions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {listQ.isLoading ? (
                  <TableRow>
                    <TableCell
                      colSpan={6}
                      className="text-muted-foreground py-6 text-center text-sm"
                    >
                      {t('list.loading')}
                    </TableCell>
                  </TableRow>
                ) : list.length === 0 ? (
                  // BR4 §4.2.5：空状态文案
                  <TableRow>
                    <TableCell
                      colSpan={6}
                      className="text-muted-foreground py-6 text-center text-sm"
                    >
                      <div className="flex flex-col items-center gap-1">
                        <span>{t('list.empty')}</span>
                        <span className="text-xs">{t('list.emptyHint')}</span>
                      </div>
                    </TableCell>
                  </TableRow>
                ) : (
                  list.map((row) => {
                    const revealed = revealedKeys[row.id];
                    return (
                      <TableRow key={row.id}>
                        {/* BR5 §4.2.5：模型名称下方等宽 model_id 副标题 */}
                        <TableCell>
                          <div className="flex flex-col">
                            <span className="font-medium">{row.name}</span>
                            <span className="font-mono text-muted-foreground text-xs">
                              {row.model_id}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">{providerLabel(row.provider)}</Badge>
                        </TableCell>
                        {/* BR5 §4.2.5：API 地址等宽 */}
                        <TableCell className="font-mono text-xs break-all">
                          {row.api_url || t('view.empty')}
                        </TableCell>
                        {/* BR3/BR5 §4.2.4 规则4 / §4.2.5：API Key 掩码 + 眼睛图标并排，临时明文警示色 */}
                        <TableCell>
                          <div className="flex items-center gap-1.5">
                            {revealed ? (
                              <span className="font-mono text-xs text-warning break-all">
                                {revealed}
                              </span>
                            ) : (
                              <span className="font-mono text-muted-foreground text-xs">
                                {row.api_key_masked}
                              </span>
                            )}
                            <button
                              type="button"
                              aria-label={
                                revealed ? t('list.hideLabel') : t('list.revealLabel')
                              }
                              onClick={() => onRevealKey(row)}
                              disabled={revealingId === row.id}
                              className="text-muted-foreground hover:text-foreground disabled:opacity-50"
                            >
                              {revealed ? (
                                <EyeOff className="size-4" />
                              ) : (
                                <Eye className="size-4" />
                              )}
                            </button>
                          </div>
                        </TableCell>
                        {/* BR1/BR2 §4.2.4 规则1/2：排他开关，启用项禁用关闭方向 */}
                        <TableCell>
                          <Switch
                            checked={row.enabled}
                            disabled={row.enabled || enableMut.isPending}
                            onCheckedChange={() => onToggleEnable(row)}
                          />
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            variant="link"
                            size="sm"
                            onClick={() => openView(row)}
                          >
                            {t('list.viewAction')}
                          </Button>
                          <Button
                            variant="link"
                            size="sm"
                            onClick={() => openEdit(row)}
                          >
                            <Pencil className="size-3" />
                            {t('list.editAction')}
                          </Button>
                          <Button
                            variant="link"
                            size="sm"
                            onClick={() => openDelete(row)}
                          >
                            <Trash2 className="size-3" />
                            {t('list.deleteAction')}
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })
                )}
              </TableBody>
            </Table>
          </div>

          {/* BR4 §4.2.5：底部统计，空列表隐藏 */}
          {list.length > 0 && (
            <div className="text-muted-foreground mt-3 flex flex-col gap-1 text-sm">
              <span>
                {t('list.total', { total: list.length })}
                {enabledItem
                  ? `，${t('list.enabledCurrent', { name: enabledItem.name })}`
                  : ''}
              </span>
              <span className="text-xs">{t('list.footHint')}</span>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 下区：集成密钥卡片（BR6 §4.2.1，子计划 03 占位，子计划 04 填充） */}
      <IntegrationSecretCard />

      {/* 弹窗编排 */}
      <LLMFormDialog
        open={formOpen}
        mode={formMode}
        onOpenChange={setFormOpen}
        llm={formLlm}
        onUpdateSuccess={() => setRevealedKeys({})}
      />
      <LLMViewDialog
        open={viewOpen}
        llmId={viewLlmId}
        onOpenChange={setViewOpen}
        onEdit={onViewEdit}
      />
      <LLMDeleteDialog
        open={deleteOpen}
        llm={deleteLlm}
        onOpenChange={setDeleteOpen}
      />
    </div>
  );
}
