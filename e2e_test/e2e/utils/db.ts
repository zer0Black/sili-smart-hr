// /e2e_test/e2e/utils/db.ts
//
// 数据库直连兜底清理工具。
//
// 存在原因：大模型配置页有「至少保留一个模型」业务约束（后端 Delete 在 count<=1 时拒绝），
// API/UI 路径都无法把模型表清空到 0。而「首个模型自动启用」业务规则（spec §4.3）只在全表为空时
// 新建触发，要隔离验证该规则必须制造空表窗口。本工具用 sqlite3 CLI 物理清空，绕过业务层删除约束，
// 仅用于测试数据隔离，不触碰业务逻辑。
//
// 前提（已实测）：SQLite journal_mode=delete，后端在线时外部 CLI 写提交后立即可见，无锁冲突、无快照滞后。
// 仅适用于默认 SQLite 库；切 PostgreSQL 时该工具不可用（throw 提示），此时需另行准备空表。
import { execSync } from 'child_process';
import * as path from 'path';

const SQLITE3_BIN = process.env.SQLITE3_BIN || 'sqlite3';
const DB_PATH = process.env.DB_PATH || path.resolve(__dirname, '..', '..', '..', 'hr-backend', 'data', 'sili-smart-hr.db');

function sqlite(sql: string): string {
  try {
    return execSync(`"${SQLITE3_BIN}" "${DB_PATH}" "${sql.replace(/"/g, '\\"')}"`, {
      encoding: 'utf-8',
      timeout: 15_000,
    }).trim();
  } catch (e) {
    throw new Error(`sqlite 执行失败: ${sql} | ${(e as Error).message}`);
  }
}

// 物理清空 llm_configs 表，绕过「至少保留一个」业务约束。仅 SQLite。
export function resetLlmConfigs(): void {
  sqlite('DELETE FROM llm_configs;');
}

// 把集成密钥置回未配置态。集成密钥是单例（migrateDB seed 保证恒有 1 行，repo.Get 用 First 读首行，
// 无记录会 ErrRecordNotFound → API 500 破坏卡片加载），故不能 DELETE 整行。未配置态的正确表示是
// 单例行存在但 secret_cipher 为空（configured 由 cipher 是否非空推导）。
// 若之前的清理误删了单例行（COUNT=0），先 INSERT 一行空密钥恢复不变量，再 UPDATE 清空。
export function resetIntegrationSecret(): void {
  const count = Number(sqlite('SELECT count(*) FROM integration_secrets;') || '0');
  if (count === 0) {
    sqlite("INSERT INTO integration_secrets (id, secret_cipher, secret_masked, version, created_at, updated_at) VALUES (1, '', '', 1, datetime('now'), datetime('now'));");
  }
  sqlite("UPDATE integration_secrets SET secret_cipher='', secret_masked='' WHERE rowid IN (SELECT rowid FROM integration_secrets LIMIT 1);");
}
