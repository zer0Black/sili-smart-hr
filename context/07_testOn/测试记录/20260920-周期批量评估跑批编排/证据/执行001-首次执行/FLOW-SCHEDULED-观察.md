# FLOW-SCHEDULED 观察记录

## 配置变更
- 22:41:31 经 POST /api/assessment-config/save 改为 daily / 22:45 / all（version 1→2）
- 变更时进行中批次 B202609202233003（manual，running）不受影响（继续跑）

## 触发观察
- 22:45:00.591 日志：`batch tick triggered batch_no=B202609202245001 period=daily`
- 定时批次 B202609202245001 创建：scheduled / all / 评估时段 2026-09-20 ~ 2026-09-20（daily → 当日，符合 §5.1.4 规则1）
- 宽限窗验证：22:46 与 22:47 分钟的后续 tick 未再建批（scheduled total 恒为 1），符合本周期已建批判定
- 配置变更生效边界：22:41 改配置，22:45 即按新配置触发（下次触发生效）

## all 模式骨架展开
- all 批次经 staffs 接口展开名单为 [admin, 肖文宇]（上游 users 接口的 username 口径，total=2）
- 展开后 total_count=2（骨架期短暂 0 已被覆盖，秒级完成）
- 两人均零会话 → skipped 终态（成功侧），批次 success 2/2，无告警

## 重要发现（口径差异）
- 03 接口设计 §1.6 假定 staff_name（上游 username）与 token_name（会话归属人名）同源
- 本环境实测：会话 token_name 是 48 个人名（肖文钰/叶海军/…），而 users 接口 username 只有 admin/肖文宇 2 个
- 两口径不一致：all 模式定时批次实际评估对象是 admin/肖文宇（近零会话，全员批次恒 skipped 空转），而真实有会话的 48 人不在名单内
- 该差异使 all 模式的全员评估语义失效，属产品口径问题，需人工判断（spec 假定被真实上游证伪）

## 告警信号
- 两个周窗口失败批次（100% 失败）各产生一条告警，failed_ratio=100.0，每批次至多一条符合 §5.2.4 规则4

## 恢复
- 观察完成后改回 weekly/23:00（version 3）
