# P2_PRF_001_FEAT_个人画像 开发实施计划 建议性质量扫描报告

mode-3（开发实施计划阶段）| 扫描日期 2026-10-03 | 全程只读，未修改任何文件
扫描范围：00_plan.yaml、01_后端画像域.md、02_前端画像页.md；上游依据 01/03/04；代码库事实核对 hr-backend 与 hr-frontend。

## 总体结论

**有条件通过**。计划结构完整、规格覆盖全面、代码库引用绝大多数属实，依赖图无环、验收锚点可执行。存在 1 项 major（前端导出失败判别漏 HTTP 200 主路径，与项目错误映射约定直接冲突，会导致用户下载到内含 JSON 错误体的坏 xlsx 且无提示），建议修订后再推进实施。

---

## 一、按检查维度的发现

### 维度 1：结构完整性（无 blocker）

00_plan.yaml 的 design_inputs 与上游文档实际结论一致：api applicable 对应 03 文档 3 个新接口（A1/A2/B1）加 §5.3 预留契约转记不占任务；model not_applicable 对应 04 文档 §1「不新建表、不改动既有表」的明文结论，04 承载消费表清单与索引核对作为仓储任务取数依据的定位也一致。sub_plans 两个文件均存在，input_files 三条相对路径（../01、../03、../04）有效；前端子计划不引 04 与其上游设计来源表「不作为本子计划输入」自洽。execution_order 01→02 无问题。

### 维度 2：规格覆盖（无 blocker）

specs 第 4/5 章功能点全部落入任务：4.1.3 筛选/跳转/导出/自动查询（前端 T2/T5/T6），4.2.3 返回/切区间/展开/tab 切换（前端 T4/T6），5.1/5.2 聚合接口（后端 T3/T4），5.3 预留不占任务（与 specs §5.3.2 第 4 条一致）。接口契约要点逐项有承载：错误码 2001（后端 T1 定义、T4 触发、T6 映射、前端 T6 回落分支），区间换算含止日（后端 T4 第 1 条、T7 边界用例），导出流旁路（后端 T6 + 前端 T1），判型过滤 scored 且 main_type 非空（后端 T2），每模块最新周期（后端 T1/T3，BR2 索引到 03 §1.9），计算常量 4/3/10/2（后端 T4 常量块）。specs §8.3 三条偏离记录（姓名排序、待评估口径、原型裁剪）也分别落在后端 T3 第 9 条与前端 T5。无遗漏功能点。

### 维度 3：代码库依据真实性（1 major，2 minor）

**[major] 前端导出失败判别漏掉 HTTP 200 + JSON blob 的主路径。**
位置：02_前端画像页.md T1（api.ts 关键逻辑）与 T5（export.test.ts）。
问题：计划只在 axios catch 分支判别 `error.response.data instanceof Blob 且 type 含 json`。但按项目约定（hr-backend/CLAUDE.md 错误映射），后端业务错误经 handleServiceError 返回 HTTP 200 带 code（仅 1003 是 401）。核对 hr-frontend/src/lib/http-client.ts 第 54-57 行，拦截器 success 分支 `typeof body.code !== 'number'` 对 blob 直接放行且 promise resolve：HTTP 200 + application/json 的失败体不会被 axios 抛错，永远走不到 catch。结果是指定场景（1305 名单失败、1400 参数错）下 exportProfiles 把 JSON 错误 blob 当成功文件返回，浏览器下载一个坏 xlsx，无 toast，直接违反 specs §4.1.3「导出请求失败提示通用错误文案，不产出文件」。catch 分支仅覆盖 401/5xx 等真 HTTP 错误。
证据链：response.go 与 handler/account.go 的 httpStatusFor（code 非 1003 一律 200）；http-client.ts 拦截器两分支行为。
建议：exportProfiles 成功分支先判 `resp.data.type` 含 application/json，是则 text() 解析抛 ApiError，再走文件名解析；T5 测试用例须显式 mock HTTP 200 + json blob。

**[minor] 前端 T3 参与聚合判据缺 status 条件。**
位置：02 计划 T3 conclusion.ts 关键逻辑「参与聚合判据 = dimensionTree 中该维度 include_overview 且非 ENNEAGRAM 模块」。
03 §1.9 原文是三条件：status=success 且 insufficient=false 且 include_overview=true。B1 dimensions[].status 已回传（normal/insufficient/missing），insufficient 行照常出分，仅按计划的判据会让降权维度混入优势（≥75）与短板（<60）判定，与 specs §4.2.4 规则2 短板口径（含排除被剔除维度）不符。后端 T3 第 7 条写对了（「仅 status=success 行计分」+ included_json 判据），前端同口径补 status 即可。

**[minor] aggregate overview 行的 IncludedJSON 形态差异未提示。**
位置：01_后端画像域.md「代码库依据」与 T2 契约。scorer.go 实测：模块行 included_json 是数组形态（marshalList，`[{"code":...,"weight":...}]`），overview 行是 map 形态（marshalMap，键为模块名）。T2 的 ListByToken 含 overview 行（供 B1 并集），计划只描述了 `{code, weight}` 单元。实际短板消费面（ListLatestModuleRowsByTokens，已排除 overview 行）避开了此坑，但 B1 若复用该解析路径时无防御提示，建议计划补一句形态差异说明。

### 维度 4：任务内聚与依赖（1 minor）

后端 T1/T2 并行、T3 汇聚、T4/T5 依赖 T3、T6 收口、T7/T8 回归，无环无断链；前端 T1→T2/T3→T4/T5→T6→T7 同样无环。8 任务与 7 任务超 6 任务阈值的拆分理由均已给出（同两接口组装链路、共享契约类型与 api 层），成立。验收锚点具体（如 ListLatestByTokens 的 6 行期望、change_vs_prev 82-79=3、company_avg 3 人/2 人分支），可执行。

**[minor] T6 改 NewRouter 形参后 router_test.go 即刻编译断裂，T6 验收命令发现不了。**
位置：01 计划 T6 验收锚点。router_test.go 第 87 行直呼 router.NewRouter 传全部 17 个实参，追加 profileHandler 形参后该文件编译失败。T6 的验证命令是 `go test ./internal/api/handler && go build ./...`：go build 不编译测试文件，handler 包测试不含 router 包，断裂要等到 T8 的 `go test ./...` 才暴露。与计划「router.go/wire_gen.go/errcode.go 为顺序修改，各自完成时可编译可测」的自述不符。另外 T8 写的「若其断言接口计数则同步 +3」与事实不符：router_test.go 不断言接口计数，需要的是补一个 nil 实参。建议把 router_test.go 的参数同步挪进 T6 文件清单（一行改动）。

### 维度 5：风险与遗漏（3 info）

**[info] evaluated_at 的时区转换细节未明示。** 后端 T4 第 4 条「PeriodEndAt 前一日 Format」。落库行 PeriodEndAt 是 UTC 存储、本地语义（03 §1.4 已交代秒值不变），正确实现须先 `.In(time.Local)` 再减一日格式化；直接对 UTC 值减日会在负时区部署下差一天。当前 UTC+8 语境两种写法同结果，建议计划补明。同理 periods/selected_period 的 yyyy-MM-dd 展示也依赖 Local 转换。

**[info] 前端路由参数编码指引有双重编码风险。** 02 计划 T5 写「staffName 需 encodeURIComponent 由 router params 承载」。项目 router 版本 ^1.170.24，TanStack Router 新版对 path params 默认自动编码，再手动 encodeURIComponent 中文名会双编码导致详情页 staff_name 与后端不匹配。建议改为「依赖 router 默认编码，实现时以中文名跳转做一次实测验证」。

**[info] i18n 键对齐无自动校验。** 已核对测试目录仅有 contracts.test.ts 的 ErrCode 键守护，无 zh/en 键树对齐用例。T7 已预案「无则人工比对并在提交说明记录」，可接受；若想机械化，可在 T7 顺手补一个键集合一致性用例（成本极低）。

---

## 二、与代码库事实核对结果（逐项）

| 计划引用 | 核对结论 |
|---|---|
| WalkStaffPages(ctx, p, secret, keyword, onPage)，page_size 100、上限 100 页 | 确认，paginate.go 签名与 StaffPageSize/StaffMaxPages 常量一致 |
| FindStaff keyword 精确查人、ListStaffPager 窄接口 | 确认存在（FindStaff 实为 staff_id+staff_name 双匹配，语义兼容） |
| resolveSecret 为 assessment_config.go 私有方法，复用需同款实现或提取 | 确认，private 方法挂 struct；decryptSecretCipher 在 integration_secret.go 包内私有，计划已给出两条路径 |
| handler 的 Staffs/handleServiceError 样板 | 确认，handleServiceError 在 handler/account.go 包内可直接用 |
| 四表 domain 字段（TokenName/ModuleScore *float64/ActiveLevel/MainType 等） | 全部确认，与 domain 四文件逐字段一致 |
| evidenceJSON 结构 session_keys/summary/dimension_specs；active_test 行只含 dimension_specs | 确认，evaluate.go 与 grading.go buildEvidence 实测一致 |
| IncludedJSON 单元 {code, weight} | 确认（模块行数组形态）；overview 行 map 形态未提示，见 minor |
| dimension ListAll 可作基准（内过滤 enabled+module） | 确认，ListAll 返回未软删含 enabled/include_overview 投影 |
| aggregate_score_test.go 的 :memory: SQLite + 雪花回调范式 | 确认 |
| errcode 20xx 段空闲（当前最大 1903） | 确认 |
| router auth 组可挂三路径、NewRouter 加形参可行 | 可行，但 router_test.go 需同步（见 minor） |
| wire 追加两行即可、providers.go 无需修改 | 确认成立：userapiClient 复用既有 ProvideUserapiClient 绑定，encKey 复用 NewLLMEncKey（[]byte 唯一无冲突），五仓储 provider 均在 wire.Build |
| excelize 为新增后端依赖 | 确认，go.mod 当前无 excelize |
| http-client 拦截器对无 code 的 blob 放行 | 确认第 54-57 行；但派生出 major 项（放行后 resolve 而非 reject） |
| contracts.ts DimensionBrief 含 include_overview/group_code；fetchDimensionTree 存在 | 确认 |
| nav-menu F9 灰显形态 `{ label: t('nav.profile'), feature: 'F9' }` | 确认（nav 键在 common 命名空间下，T7 表述为「nav 增键」不矛盾） |
| useResetSignal/usePageClamp、staff-multi-select.tsx 存在 | 确认 |
| i18n 现有 14 命名空间，profile 为第 15 个 | 确认，实测顶层键恰 14 个 |
| Recharts ^3.10.1 已在前端 package.json、当前零 import 首次启用 | 确认 |
| tasks 表索引 idx_staff_name / idx_type_status_created、results uk_result_task 支撑 T2 JOIN 路径 | 确认 |

结论：引用真实性整体良好，无虚构样板或签名；唯一实质性偏差是 http-client 放行行为与导出失败判别设计的组合后果（major）。

---

## 三、遗留建议清单

1. 修订 02 计划 T1：exportProfiles 增加成功分支的 blob.type 判别（HTTP 200 + application/json 即业务错误，text() 解析抛 ApiError），T5 用例显式覆盖该形态。这是推进实施前应完成的一项。
2. 修订 02 计划 T3：buildConclusion 参与聚合判据补 `status === 'normal'`（即 success 且非 insufficient），与 03 §1.9 三条件对齐。
3. 修订 01 计划 T6：文件清单加入 router_test.go（补一个 nil 实参），并修正 T8 中「断言接口计数 +3」的不实表述。
4. 01 计划 T4 补一句 evaluated_at 及区间展示的 `.In(time.Local)` 转换约定；02 计划 T5 的 encodeURIComponent 指引改为依赖 router 默认编码并附验证要求。
5. 可选增强：01 计划提示 aggregate overview 行 included_json 为 map 形态与模块行数组形态的差异；02 计划 T7 顺手补 zh/en 键集合一致性 vitest 用例，替代人工比对。

以上第 1 项为放行前置条件，第 2、3 项建议随计划定稿一并修订，第 4、5 项可由实现者在对应任务内消化。
