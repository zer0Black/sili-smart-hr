# AGENTS.md

本文件登记项目级 agent 工作规则与约束文件位置，供 Claude Code 及各类自动化 agent 加载。

## 项目级规则文件

| 规则文件 | 路径 | 用途 |
|---------|------|------|
| AGENTS_DATABASE_API_RULE | ./AGENTS_DATABASE_API_RULE.md | 数据库与 API 接口设计规则（主键、公共字段、命名、类型约束、响应结构、错误码、传输安全） |

## 工作目录指引

进入子目录工作前先读对应子文档：

- 后端（Go）见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)
- 前端（React）见 [hr-frontend/CLAUDE.md](hr-frontend/CLAUDE.md)
- 跨前后端整体规约见 [CLAUDE.md](CLAUDE.md)
- 规格权威源见 [context/03_architecture/architecture.md](context/03_architecture/architecture.md)
