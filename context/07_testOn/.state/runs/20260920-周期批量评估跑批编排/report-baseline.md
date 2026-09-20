# 测试报告

[查看本任务测试用例](<E:/Agent-zone/sili-smart-hr/context/07_testOn/测试用例/周期批量评估跑批编排-v1.md>)

**部分结果需要你判断**

测试范围：评测运营中心页（批次列表/统计卡/计划卡）、发起评测弹窗（手动定向分析）、失败明细弹窗与补跑、周期跑批调度（batch-tick）、跑批编排（batch-run 全流程）、失败兜底（重试/告警/停滞判定）
被测版本：main@56d3a6b（后端 go run + SMART_API_BASE_URL=10.10.10.36:3000 + SQLite）+ 前端 rsbuild dev 3000；环境：本机开发环境

通过 8 项；失败 1 项；阻塞 0 项；待判断 3 项；未执行 0 项；执行中 0 项；待继续 0 项

**接下来：** 请先看下面的待判断事项，其他可执行场景继续处理。

## 本任务暂不覆盖

- F7 主动测试评估运营（AI 管理能力/九型 tab，未开发）
- F9 个人画像与 F10 看板（查看结果按钮置灰属预期）
- F11 工作台告警消费界面（本功能只产信号）
- 告警阈值与重试次数的在线配置（本期为代码常量）
- 批次取消/作废/删除（specs 明确不提供）

## 需要你判断

### UI-FAILURE-RERUN 失败明细弹窗与补跑、停滞批次重新发起

实际观察：失败明细弹窗：标题带批次号、姓名/失败原因摘要两列、无重试删除等操作（只读快照）、条数与失败人数一致（API 侧 2=2 已验）；按失败对象重新发起后预填已选 N 人+原时段（截图 ui-rerun-prefill）；有已选人员取消弹二次确认。停滞批次「重新发起」入口未实测：库内无停滞批次（运行批均快速终态），依赖单测 TestListDerivesStalled 系列与 A4 接口 Targets 完整名单实测作部分证据，页面侧留待人工构造停滞批次后复核

预期：弹窗列出失败人员姓名与原因摘要；无重试/删除等操作按钮；条数与批次失败人数一致；发起弹窗打开时评估对象为失败人员、时段为原批次时段；停滞批次行显示已停滞标识；失败人数为零时显示重新发起、非零时只显示失败明细；预填名单为完整名单（非摘要）；查看结果按钮置灰不可点，悬浮提示个人画像功能建设中

依据：specs §4.3.2/§4.3.4：姓名与失败原因摘要只读快照，无分页筛选；specs §4.3.3：带入本批次评估时段与全部失败人员；specs §4.1.3：停滞且失败人数为零行内显示重新发起，预填完整名单与时段，止日钳位昨天；specs §4.1.3：F9 建成前查看结果置灰，悬浮提示建设中

证据：[UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt)

### UI-POLLING 进行中批次轮询与页面可见性

实际观察：POLL-10S：存在 running 批次时 25s 内 stats 3 次、batches 3 次，两轮间隔 20015ms 即 10s 一轮，符合；POLL-STOP：无 running 批次时 stats 仅首拉 1 次（轮询停止）；POLL-PROBE：探针口径经 hooks 源码与 stats 驱动逻辑核验（useBatchStats 自驱动 + 页面级探针传 BatchTable），未做筛选挤出页面的浏览器实测；POLL-VISIBILITY：headless 无法真实切换页面可见性，标记待人工复核

预期：存在 running 批次时每约 10 秒发出 stats 与 batches 请求；隐藏期间无轮询请求；切回后立即触发一次 stats 与列表请求；批次终态后轮询停止（不再发出周期请求）；筛选 success 无进行中行时，若统计卡仍报进行中批次则轮询继续

依据：specs §4.1.3 后台自动流程：10 秒间隔轮询列表与统计卡；specs §4.1.3：页面隐藏暂停，恢复可见立即拉一次；specs §4.1.3：所有非停滞批次离开进行中后停止轮询；specs §4.1.3：轮询开关以统计卡 running 计数为探针，不受翻页筛选影响

证据：[UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt)

### UI-EXPLORE 页面体验探索

实际观察：探索截图 2 张（整页布局、人员搜索空态）：布局与信息层级清晰，分页控件正常，人员搜索无结果空态文案正确。空批次空态文案与卡片失败刷新入口经源码与 i18n key 核验存在，未做破坏性清库实测；整体体验无疑点需改进，主观观感交人工判断

预期：空列表显示「暂无评测记录，点击右上角发起评测或等待周期自动跑批」；加载中有骨架占位；停后端后卡片展示失败态与刷新按钮；记录布局、反馈、文案与交互困难点，附截图，交测试人员判断

依据：specs §4.1.5：空状态引导文案；specs §4.1.5：加载完成前显示占位骨架；卡片失败展示刷新入口；探索章程：体验疑点交人判断

证据：[UI-EXPLORE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-EXPLORE-%E7%BB%93%E6%9E%9C.txt)


## 发现的问题

### FLOW-SCHEDULED 定时批次周期触发

实际观察：触发链路本身符合预期：22:41 改配置 daily/22:45，22:45:00.591 tick 命中创建 B202609202245001（scheduled/all/时段 2026-09-20~2026-09-20 正确）；宽限窗内后续 tick 未重复建批；进行中手动批次不受影响；骨架 total_count 秒级回填。同时发现产品口径缺陷：all 展开名单取 users 接口 username（admin/肖文宇），真实会话 token_name 是 48 个人名，两口径不同源，全员批次评估对象恒为零会话者（skipped 空转 success），spec §1.6 同源假定被真实上游证伪

预期：命中触发点后自动创建 scheduled 批次且仅一条；宽限窗内剩余 tick 不会重复建批；daily 批次评估时段为当日；定时批次评估时段与周期窗口推算口径一致（以 daily 实测，weekly/monthly 依赖单测证据）；修改周期配置后，下一次触发按新配置执行，进行中批次不受影响

依据：specs §5.1.4 规则4 + 03 §4.3：触发点 2 分钟宽限窗 + 本周期已建批判定防重复；specs §5.1.4 规则1：日→当日、周→本周一至本周日、月→本月1日至月末；specs §4.1.4 规则4：配置变更下次触发生效

证据：[FLOW-SCHEDULED-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-SCHEDULED-%E8%A7%82%E5%AF%9F.md)


## 全部测试结果

| 用例 | 场景 | 当前结果 | 说明 | 证据 |
| --- | --- | --- | --- | --- |
| API-LIST | 批次列表查询与筛选 | 通过 |  | [API-LIST-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-LIST-%E7%BB%93%E6%9E%9C.txt) |
| API-STATS-PLAN | 跑批态势统计与计划卡 | 通过 | 历史结果有差异，需复核原因 | [API-STATS-PLAN-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-STATS-PLAN-%E7%BB%93%E6%9E%9C.txt)；[API-STATS-PLAN-补验.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-STATS-PLAN-%E8%A1%A5%E9%AA%8C.txt) |
| API-TARGETS-FAILURES | 批次名单与失败明细查询 | 通过 | 执行前曾受阻，条件恢复后通过 | [API-TARGETS-FAILURES-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-TARGETS-FAILURES-%E7%BB%93%E6%9E%9C.txt) |
| API-CREATE | 发起手动定向分析（B1） | 通过 | 历史结果有差异，需复核原因 | [API-CREATE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-CREATE-%E7%BB%93%E6%9E%9C.txt)；[FLOW-SCHEDULED-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-SCHEDULED-%E8%A7%82%E5%AF%9F.md) |
| FLOW-BATCH-RUN | 手动批次端到端跑批闭环 | 通过 | 历史结果有差异，需复核原因 | [FLOW-BATCH-RUN-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-BATCH-RUN-%E8%A7%82%E5%AF%9F.md)；[FLOW-UPSTREAM-FAIL-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-UPSTREAM-FAIL-%E8%A7%82%E5%AF%9F.md) |
| FLOW-SCHEDULED | 定时批次周期触发 | 失败 |  | [FLOW-SCHEDULED-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-SCHEDULED-%E8%A7%82%E5%AF%9F.md) |
| FLOW-UPSTREAM-FAIL | 上游不可用时整批失败兜底 | 通过 |  | [FLOW-UPSTREAM-FAIL-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-UPSTREAM-FAIL-%E8%A7%82%E5%AF%9F.md) |
| UI-CENTER | 评测运营中心页面功能 | 通过 |  | [UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt) |
| UI-FAILURE-RERUN | 失败明细弹窗与补跑、停滞批次重新发起 | 待判断 |  | [UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt) |
| UI-POLLING | 进行中批次轮询与页面可见性 | 待判断 |  | [UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt) |
| UNIT-REGRESSION | 既有自动化资产回归确认 | 通过 |  | [UNIT-REGRESSION-go-test.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-go-test.txt)；[UNIT-REGRESSION-fe-typecheck.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-fe-typecheck.txt)；[UNIT-REGRESSION-fe-test.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-fe-test.txt) |
| UI-EXPLORE | 页面体验探索 | 待判断 |  | [UI-EXPLORE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-EXPLORE-%E7%BB%93%E6%9E%9C.txt) |

<details>
<summary>查看执行与恢复记录</summary>

## 逐次执行记录

- API-LIST，第 1 次：通过；16 组参数全部实测：默认查询 code=0 空列表结构 {list,total,page,page_size} 正确；trigger_type/status 非法值返回 1400，合法枚举正常；page_size=101 钳位 100、page_size=0 兜底 10、page=0/abc 兜底 1；无 token HTTP 401 + code 1003。库内暂无批次，倒序断言空集通过（倒序在后继用例有数据时复验）；证据：[API-LIST-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-LIST-%E7%BB%93%E6%9E%9C.txt)
- API-STATS-PLAN，第 1 次：待判断；stats：eval_count=3（本期周日 23:00 推算间隔内 3 个手动批次，与库一致）、evaluated_person_count=0（尚无成功侧终态）、running_batch_count=1（剔除 2 个 failed 终态）。plan：next_trigger_at=2026-09-20 23:00（周日当日推算正确）、period=weekly、target_mode=all、target_count=2、dimension 4+4 一致。PLAN-ALL-DEGRADE（密钥不可用降级返 0）暂未复验：改坏密钥场景在 FLOW-UPSTREAM-FAIL 执行时顺带补证；证据：[API-STATS-PLAN-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-STATS-PLAN-%E7%BB%93%E6%9E%9C.txt)
- API-STATS-PLAN，第 2 次：通过；stats：eval_count 与库内批次一致、running_batch_count 剔除终态正确；plan：next_trigger_at 周日推算正确、dimension 4+4、target_count=2。PLAN-ALL-DEGRADE 补验：密钥无效时 plan 仍 code=0 且 target_count 降级 0，恢复后回到 2，降级分支符合 03 §3.A3；证据：[API-STATS-PLAN-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-STATS-PLAN-%E7%BB%93%E6%9E%9C.txt)；[API-STATS-PLAN-补验.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-STATS-PLAN-%E8%A1%A5%E9%AA%8C.txt)
- API-TARGETS-FAILURES，执行前检查：阻塞；上游用例 API-CREATE 当前为 blocked（CREATE-ALL 检查点未独立执行）。实际观察已完成：7 组请求结果均在证据文件（targets/failures 对存在与不存在批次、缺参与非数字参数），结论待 API-CREATE 的 CREATE-ALL 补验后随整批复核确认；证据：[API-TARGETS-FAILURES-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-TARGETS-FAILURES-%E7%BB%93%E6%9E%9C.txt)
- API-TARGETS-FAILURES，第 1 次：通过；7 组实测：targets 返回完整名单（李雪涛/王莹）与创建时段一致（2026-09-13~09-19）；failures 对 failed 批次返回 2 条失败人员（error_summary 为批次级原因，failed_count=2 与 list 条数一致）、对进行中批次返回 failed_count=0 空列表；不存在批次 id 两接口均返 1601；缺 batch_id 与非数字 batch_id 均返 1400；证据：[API-TARGETS-FAILURES-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-TARGETS-FAILURES-%E7%BB%93%E6%9E%9C.txt)
- API-CREATE，第 1 次：阻塞；12 组参数实测完成 11 项检查全过：合法 specified 建批成功（batch_no B+yyyyMMddHHmm+3位序号、status=running、total=2）；end 为今天/未来/早于 start 均返 1602；日期格式非法 1602；空 staffs、缺 staff_name、纯空白名返 1603；target_mode=xyz 与缺 period_end 返 1400；同名去重 total_count=2；单日时段建批成功。CREATE-ALL 单独阻塞：all 建批会触发 48 人全量展开与 8.8 万会话真实抽取（成本过高），骨架口径改在 FLOW 用例以受限窗口验证后本条改回。周窗口批次因上游单周 8.8 万会话击穿 listMaxPages=100×100 上限整批落 failed，属上游量级与设计上限的边界发现；证据：[API-CREATE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-CREATE-%E7%BB%93%E6%9E%9C.txt)
- API-CREATE，第 2 次：通过；12 组参数实测全过：合法 specified 建批成功（batch_no 格式正确、running、total=2）；end 今天/未来/早于 start 返 1602；日期格式非法 1602；空 staffs/缺 staff_name/纯空白名返 1603；target_mode=xyz 与缺 period_end 返 1400；同名去重 total=2；单日时段建批成功。CREATE-ALL 补验通过：定时 all 批次 B202609202245001 同链路实测，骨架 total_count 秒级从 0 展开回填 2（观察记录见 FLOW-SCHEDULED-观察.md）；密钥缺失建批拦截路径未单独触发（其余密钥口径已在 FLOW-UPSTREAM-FAIL 覆盖）。周窗口批次因上游 8.8 万会话击穿 listMaxPages 上限整批 failed 属边界发现；证据：[API-CREATE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/API-CREATE-%E7%BB%93%E6%9E%9C.txt)；[FLOW-SCHEDULED-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-SCHEDULED-%E8%A7%82%E5%AF%9F.md)
- FLOW-BATCH-RUN，第 1 次：待判断；B202609202233003 全程实测：建批→batch-run 消费→全量拉取（298 条去重 237，跨页重复 61 条去重生效）→逐会话投递抽取→等待落库→逐人评估（李雪涛 140 会话 degraded、王莹 97 会话 success）→终态 success（2/2，失败 0，covered=237，session_fail_ratio=29.54 落库）。终态判定、进度单调、覆盖会话口径、告警抑制（0%≤10%）全部符合预期。RUN-IDEMPOTENT（同人同区间 reused）未实测：重跑需再抽 237 会话耗时 40+ 分钟，待人工决策是否追加；证据：[FLOW-BATCH-RUN-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-BATCH-RUN-%E8%A7%82%E5%AF%9F.md)
- FLOW-BATCH-RUN，第 2 次：通过；B202609202233003 全程实测：建批→batch-run 消费→全量拉取（298 条去重 237，跨页重复 61 条去重）→逐会话投递抽取→等待落库→逐人评估（李雪涛 140 会话 degraded、王莹 97 会话 success）→终态 success（2/2，covered=237，session_fail_ratio=29.54 落库）。RUN-IDEMPOTENT 补验：补跑批次同人同区间王莹 reused（档案幂等未调 LLM），无重复计分。全部检查点闭环；证据：[FLOW-BATCH-RUN-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-BATCH-RUN-%E8%A7%82%E5%AF%9F.md)；[FLOW-UPSTREAM-FAIL-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-UPSTREAM-FAIL-%E8%A7%82%E5%AF%9F.md)
- FLOW-SCHEDULED，第 1 次：失败；触发链路本身符合预期：22:41 改配置 daily/22:45，22:45:00.591 tick 命中创建 B202609202245001（scheduled/all/时段 2026-09-20~2026-09-20 正确）；宽限窗内后续 tick 未重复建批；进行中手动批次不受影响；骨架 total_count 秒级回填。同时发现产品口径缺陷：all 展开名单取 users 接口 username（admin/肖文宇），真实会话 token_name 是 48 个人名，两口径不同源，全员批次评估对象恒为零会话者（skipped 空转 success），spec §1.6 同源假定被真实上游证伪；证据：[FLOW-SCHEDULED-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-SCHEDULED-%E8%A7%82%E5%AF%9F.md)
- FLOW-UPSTREAM-FAIL，第 1 次：通过；改坏密钥后发起 B202609202321001：15 秒内整批落 failed，failed_count=2/2（N/N 全员计入），失败明细 error_summary 为批次级原因（会话列表拉取失败: 密钥无效 HTTP 401），新增一条告警（100% 超阈）。恢复密钥后按失败对象补跑 B202609202321002：王莹 reused（档案幂等未调 LLM）、李雪涛 degraded，终态 success 2/2 covered=237；原失败批次保持 failed（终态不可逆）。附带拿到 RUN-IDEMPOTENT 的 reused 直接证据与 B1 仅 staff_name 补跑口径验证；证据：[FLOW-UPSTREAM-FAIL-观察.md](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/FLOW-UPSTREAM-FAIL-%E8%A7%82%E5%AF%9F.md)
- UI-CENTER，第 1 次：通过；6 条 Playwright 用例全过：页面加载（tab/统计卡三字段/计划卡/列表列头/批次号等宽）；筛选与重置；发起评测弹窗（三类型卡片、另两态 disabled、时段默认回填上一周期 2026-09-07~09-13、空对象提交字段级校验、无对象取消直接关）；全员互斥（选全员后选肖文宇自动移除全员，已选 1 人）；提交流程（提交后弹窗关闭列表顶部出现进行中批次）；查看结果置灰（disabled+title=个人画像功能建设中）。6 张截图在 shots/；证据：[UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt)
- UI-FAILURE-RERUN，第 1 次：待判断；失败明细弹窗：标题带批次号、姓名/失败原因摘要两列、无重试删除等操作（只读快照）、条数与失败人数一致（API 侧 2=2 已验）；按失败对象重新发起后预填已选 N 人+原时段（截图 ui-rerun-prefill）；有已选人员取消弹二次确认。停滞批次「重新发起」入口未实测：库内无停滞批次（运行批均快速终态），依赖单测 TestListDerivesStalled 系列与 A4 接口 Targets 完整名单实测作部分证据，页面侧留待人工构造停滞批次后复核；证据：[UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt)
- UI-POLLING，第 1 次：待判断；POLL-10S：存在 running 批次时 25s 内 stats 3 次、batches 3 次，两轮间隔 20015ms 即 10s 一轮，符合；POLL-STOP：无 running 批次时 stats 仅首拉 1 次（轮询停止）；POLL-PROBE：探针口径经 hooks 源码与 stats 驱动逻辑核验（useBatchStats 自驱动 + 页面级探针传 BatchTable），未做筛选挤出页面的浏览器实测；POLL-VISIBILITY：headless 无法真实切换页面可见性，标记待人工复核；证据：[UI-e2e-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-e2e-%E7%BB%93%E6%9E%9C.txt)
- UNIT-REGRESSION，第 1 次：通过；后端 go test ./... 全部通过（25 个包 ok，exit=0，pipeline/fallback/handler/service/repository/worker 相关包全绿）；前端 pnpm type-check 零错误（exit=0）、vitest 17 个测试文件 153 条测试全部通过（exit=0）；证据：[UNIT-REGRESSION-go-test.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-go-test.txt)；[UNIT-REGRESSION-fe-typecheck.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-fe-typecheck.txt)；[UNIT-REGRESSION-fe-test.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UNIT-REGRESSION-fe-test.txt)
- UI-EXPLORE，第 1 次：待判断；探索截图 2 张（整页布局、人员搜索空态）：布局与信息层级清晰，分页控件正常，人员搜索无结果空态文案正确。空批次空态文案与卡片失败刷新入口经源码与 i18n key 核验存在，未做破坏性清库实测；整体体验无疑点需改进，主观观感交人工判断；证据：[UI-EXPLORE-结果.txt](%E8%AF%81%E6%8D%AE/%E6%89%A7%E8%A1%8C001-%E9%A6%96%E6%AC%A1%E6%89%A7%E8%A1%8C/UI-EXPLORE-%E7%BB%93%E6%9E%9C.txt)

</details>

本报告保留本任务实际结果。未执行和阻塞项仍属于覆盖缺口，后续回归单独记录。

## 测试脚本

- [api_list_test.py](<E:/Agent-zone/sili-smart-hr/context/07_testOn/测试脚本/api/api_list_test.py>)
- [api_create_test.py](<E:/Agent-zone/sili-smart-hr/context/07_testOn/测试脚本/api/api_create_test.py>)

## 本任务执行记录

- 首次执行：[证据目录](<E:/Agent-zone/sili-smart-hr/context/07_testOn/测试记录/20260920-周期批量评估跑批编排/证据/执行001-首次执行>)
