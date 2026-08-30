// Package conversationlog 封装 sili-smart-api 会话日志的只读消费客户端，
// 是 AI 使用能力评估全部对话数据的唯一入口（specs §2.1 能力清单）：
//
//   - ListSessions：会话列表拉取，支持 username/时间窗过滤、分页钳制、
//     UserID 内存过滤（specs §2.4 能力1）
//   - GetSessionDetail：单会话详情拉取，turns/messages 全量归一化
//     （specs §2.4 能力2）
//   - Ping：连通探活（specs §2.4 能力3，单次语义不经重试）
//
// 统一承载 Bearer 集成密钥鉴权、分档超时（httpClient 不设全局 Timeout，
// 探活 5s / 列表详情 30s 经 context.WithTimeout 派生子 ctx 注入，specs §2.2）、
// 网络层重试退避 doWithRetry/sleepContext（指数退避 10s→20s→40s，仅覆盖
// 列表与详情，specs §2.4 能力4）与错误分类（specs §2.3）：7 个 sentinel
// 经 errors.Is 精确分类，ErrUpstreamBusiness 载体 *UpstreamError 提供
// IsNotFound 白名单识别记录不存在。
package conversationlog
