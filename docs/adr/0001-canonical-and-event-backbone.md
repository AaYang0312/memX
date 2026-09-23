# ADR 0001：Canonical PostgreSQL、Transactional Outbox 与 Kafka 事件骨干

**状态：** Proposed（Task 0 文档切片草案，待 owner 评审；本 ADR 批准及评测门禁关闭前不得启动 Task 1，见 §14）
**日期：** 2026-09-22
**范围（In scope）：** canonical 持久化与事务边界、transactional outbox、Kafka 事件分发契约、消费者幂等与 inbox pending lease、连续 watermark 与 DLQ、删除 fence/epoch 与防复活、RLS 与租户隔离、读路径 canonical 复核。
**明确排除（Out of scope）：** Elasticsearch/Qdrant/Redis 投影的内部实现（ADR 0002）、confirmed fact 准入与冲突状态机策略（ADR 0003）、Go 服务栈与精确依赖版本（ADR 0004）、任何业务代码实现。本 ADR 只记录骨干决策，不引入任何业务行为。
**权威输入（本 ADR 与其冲突时以上游为准，须修订本 ADR）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码与基础设施；本 ADR 属于实施计划 §6（Task 0）文件清单的一部分，先于 Task 1 创建。本 ADR 的创建不构成对任何待批准项（§14）的批准。

---

## 1. 背景与问题

memX 是独立、可部署、可重放、可审计的多租户 AI 记忆服务。写路径既要持久化用户可见的会话事件（turn、显式确认命令），又要驱动大量异步派生（事实提取、摘要、embedding、搜索与向量索引、working-memory 缓存）。若让应用层在多个存储间直接双写，会产生部分失败、不可重放、缓存被误当事实源、删除后数据复活等问题。

必须在写代码前回答：

1. 事实（canonical state）放在哪里，谁有权改变它；
2. 来源事件写入与事件发布如何保证原子且不阻塞主对话路径；
3. 多个异步消费者如何在不承诺跨存储 exactly-once 的前提下做到不重复、不乱序覆盖、不复活已删数据；
4. 如何证明"投影是陈旧的还是最新的"，以及"删除已传播"；
5. 多租户隔离在存储层如何 fail closed。

## 2. 决策（已确认基线）

以下决策来自设计规格 §1"已确认决策"与 README 核心不变量，属于已确认基线，本 ADR 予以固化为架构约束：

- **D1 — PostgreSQL 是唯一 canonical source of truth。** 会话、turn、摘要、confirmed facts、revisions、episodes、procedures、outbox、inbox、watermark、幂等账本、删除 fence 全部持久化在 PostgreSQL；禁止应用层双写任何投影（设计规格 §1.1、README 不变量 1、实施计划 §2.3）。
- **D2 — 原始 turn append 与 transactional outbox 同一 PostgreSQL 事务提交；派生记忆全部异步。** 同步写路径不等待事实抽取、embedding 或索引完成（设计规格 §1.2、§7.1、README 不变量 2）。
- **D3 — Kafka 是唯一的事件分发、重放与多投影日志。** 不同时引入 RabbitMQ（设计规格 §1.3）。Kafka 不是事实源：事件在保留期外仍必须能从 PostgreSQL 当前状态重建（实施计划 §20.4）。
- **D4 — 消费语义为 at-least-once，通过 event id、aggregate seq、revision、deletion epoch 实现 effectively-once。** 不承诺跨存储 exactly-once，不使用跨存储分布式事务（设计规格 §5.9、§3、实施计划 §2.6）。
- **D5 — 删除使用 tombstone + 持久 deletion fence/epoch。** 迟到事件、重放和重建不得复活已删除数据（设计规格 §17.4、README 不变量 6）。
- **D6 — PostgreSQL 使用最小权限 service role 与 RLS。** 每笔事务以 `SET LOCAL` 注入已验证租户/主体上下文，缺失上下文拒绝访问（设计规格 §17.1）。
- **D7 — 任何检索命中进入 MemoryPack 前必须按 `memory_ref + canonical_version` 回查 PostgreSQL 复核权限、版本、状态、TTL 与删除 fence。** 陈旧、过期、撤销、未授权命中一律丢弃（设计规格 §10.2、README 不变量 4）。
- **D8 — 模型抽取只能产生 proposal（初始状态为 `pending`），不能直接成为 `confirmed` 事实。** 在事件骨干层面，extractor 只是普通消费者：它产出 proposal；白名单确定性规则或可信服务端事件经准入校验后才可产生 confirmed fact。其写入同样走"canonical 变更 + outbox 同事务"的边界（设计规格 §1.5、§8.5、§12.2，README 不变量 3）。准入白名单与冲突状态机细节归 ADR 0003。

## 3. 理由

- **单一事实源**消除"哪个存储是对的"这类问题：投影全部可从 PostgreSQL 重建，均不是事实源；任何补充存储（Redis、ES、Qdrant、可选图库）失效时，系统仍能用当前请求与 PG confirmed facts 工作（设计规格 §5.8）。
- **Transactional outbox** 把"状态已变"和"事件可见"绑定在一个本地事务里，避免应用层双写的部分失败窗口；Kafka 短暂不可用时 outbox 累积，canonical 写入继续（设计规格 §18.2），以 backlog 告警兜底。
- **异步派生**保证主对话延迟不包含抽取/embedding/索引；同步写只做身份验证、形状校验、aggregate sequence 推进与 outbox 落库（设计规格 §7.1）。
- **at-least-once + 幂等**是跨 PG/Kafka/外部投影唯一可诚实承诺的组合；watermark 与删除回执把"最终一致"变成可度量的状态，而不是口号。
- **持久删除 fence** 补齐事件顺序模型无法解决的问题：Kafka 只保证同一聚合根分区内有序，主体删除与其 turn/fact 事件可能跨分区乱序，不能依赖事件顺序阻止复活（设计规格 §13.1）。

## 4. 备选方案与拒绝理由

| 备选 | 拒绝理由 |
|---|---|
| 应用层直接双写 PG + Kafka/投影 | 违反 D1/不变量"禁止双写"；进程崩溃在两写之间产生不可判定状态；无法重放修复。 |
| Kafka 作为事实源（event sourcing，PG 只是投影） | 违反 D1；Kafka 保留期有限、不支持按主体点查与强一致 CAS；canonical 审计、revision 链与幂等账本需要关系约束。 |
| 跨存储分布式事务（2PC/XA）实现原子发布 | 设计规格 §3 非目标："不使用跨存储分布式事务或宣称端到端 exactly-once"；可用性与运维成本不可接受。 |
| CDC/逻辑复制（Debezium 类）替代应用层 outbox | CDC 从物理变更推导事件，事件契约、schema 版本与删除语义将受制于表结构差异；WAL 变更缺少本设计要求的 envelope（deletion_epoch、trace_ref 等）。v1 不采用；若未来引入须单独评审，且不得放松 D1/D2。 |
| 引入第二个消息队列（RabbitMQ）分流 | 已确认决策：Kafka 唯一，不同时引入 RabbitMQ（设计规格 §1.3）。 |
| 不用 Kafka，消费者直连 PG（轮询/LISTEN-NOTIFY） | 无重放、无 consumer group、无多投影独立消费进度；LISTEN-NOTIFY 不耐久。与已确认的 Kafka 基线冲突。 |
| 消费者宣称 exactly-once | 不诚实：PG inbox 与外部 Redis/ES/Qdrant 写入不是同一事务（设计规格 §8.9）；改为 at-least-once + 幂等 + 条件写 + watermark。 |

## 5. 事务与事件边界

### 5.1 同步写事务边界（唯一写入口）

每个可重试写命令（append turn、confirm/revoke、delete 请求等）在一个 PostgreSQL 事务内完成：

1. 校验已验证身份导出的 tenant/subject 与资源所有权；幂等账本（设计规格 §8.11）优先判定：命中同 key 同 digest 返回原安全结果（仍重验当前授权与删除 fence），不同 digest 返回 409；
2. 锁定或 CAS 更新 aggregate（如 conversation head）；
3. 插入 canonical 变更（turn、fact revision、fence 等）；
4. 插入 outbox 事件与幂等账本的安全结果记录；
5. 同事务提交；事务回滚不留任何已完成记录或被占用的幂等 key；
6. 提交后允许 best-effort 更新 Redis，但不得依赖该更新保证耐久性（设计规格 §7.1、§12.1）。

### 5.2 异步边界

以下派生与投影处理全部异步，且不得出现在同步写延迟中：事实提取与 proposal 生成、派生候选的冲突检测、摘要与 episodic memory、embedding、ES/向量/图投影、TTL 到期处理及撤销/删除向投影的传播（设计规格 §7.1）；显式 revoke 和删除 fence 的 canonical 命令本身仍同步提交。派生结果写入 canonical（如 extractor 写 proposal/fact）时，同样遵守 §5.1 的"变更 + outbox 同事务"边界（设计规格 §12.2）。

### 5.3 事件契约

- Envelope 遵循设计规格 §13.1：`event_id`、`event_type`、`schema_version`、`tenant_ref`、`aggregate_type`、`aggregate_ref`、`aggregate_seq`、`deletion_epoch`、`occurred_at`、`trace_ref`、payload。主体级事件必须携带写入事务观察到的 `deletion_epoch`（非主体范围事件携带适用 scope 版本）。
- 必需事件集合为设计规格 §13.2 列出的 14 个 `memory.*.v1` 事件（turn committed/redacted、summary committed、fact confirmed/superseded/revoked/deleted、proposal created/resolved、episode committed、procedure approved/revoked、subject deletion requested/completed）。事件 schema 只向后兼容演进；破坏性变化使用新 event type/version（实施计划 §22）。
- **Kafka 不承载正文：** outbox 优先只携带 opaque ref、版本与安全元数据；需要正文的受信消费者按 ref 从 PG 读取（设计规格 §8.8、实施计划 §14.4）。日志、指标、trace、Kafka payload、Redis key 中不得出现原始用户标识或敏感值（实施计划 §2.9）。
- **Kafka key 草案：** `(tenant_ref, aggregate_ref)`（设计规格 §13.1 已定语义）。Topic 命名、分区数、保留期为**保守草案，待 Task 5 评审冻结**：草案为单事件 topic（如 `memx.events.v1`）+ 按 key 哈希分区、分区数由容量基线复算推导；未冻结前不创建任何生产 topic。

### 5.4 顺序保证与限制

- 仅同一聚合根（同 Kafka key 分区）内有序；跨聚合根/分区不假设有序（实施计划 §11.3）。主体删除与该主体的 turn/fact 事件可能跨分区乱序——这正是 §8 删除 fence 存在的原因，任何"靠事件顺序防复活"的设计都被排除。
- canonical 事件内容与 fact revision 只追加，不允许 UPDATE/DELETE；outbox 的投递状态（如 attempts、published_at）可按 relay 协议更新，不得改写已提交的事件内容（实施计划 §9.4、§11.3）。

## 6. 消费幂等与 inbox pending lease

每个消费者维护 PG inbox（设计规格 §8.9）：`consumer_name`、`event_id`、aggregate ref/seq、`pending | applied | quarantined`、attempt/lease expiry、processed_at、projection version；唯一键 `(consumer_name, event_id)` 去重。

- **PG 内副作用：** 业务变更 + inbox `applied` 同一事务提交后才提交 Kafka offset。
- **外部副作用（Redis/ES/Qdrant）：** 先持久占用 pending lease，再执行带 epoch/revision 条件的幂等外部写，确认外部成功后记 `applied`，最后提交 offset。**禁止先标 applied 再执行外部写。**
- 崩溃恢复：处理前崩溃、事务中崩溃、外部写成功但 applied 未落库、applied 后 offset 未提交——全部可安全重试；重复投递由 inbox 去重 + 条件写吸收。
- 消费时以 PG 当前主体 fence 拒绝旧 epoch 事件；schema version 不支持的事件进入隔离队列（quarantined），不静默丢弃（实施计划 §11.3）。
- 可重试与永久错误分类分离；DLQ 不包含敏感 payload。

## 7. 连续 watermark 与 DLQ

- 每个投影、每个 partition/shard 独立记录**连续成功处理前缀**的 processed offset；不得用"已见最大 offset"冒充进度（设计规格 §8.10、§18.3）。
- watermark 同时记录 canonical barrier/version/time、缺口、DLQ 与重试中 offset、last success、lag、health status。
- 隔离/DLQ 缺口保持可见；修复并重放前，缺口之后的进度不得作为 fresh 证明、删除完成回执或越过依据（实施计划 §11.3、§18.3）。
- ACK、TTL、watermark、tombstone 各自职责不同，互不替代：消费者 ACK 不能作为投影清理或删除完成依据（设计规格 §9.4、§16）。

## 8. 删除 fence、epoch 与防复活

- 删除命令在同一事务内：建立持久 `tenant_ref + subject_ref + deletion_epoch` fence、将主体置为不可读/不可写、递增 epoch、写入 `memory.subject.deletion_requested.v1` 删除 outbox（设计规格 §17.4）。
- 任何读取、消费者、重放和全量重建都以 PG 当前 fence 过滤旧 epoch；消费者应用事件时检查 deletion epoch、aggregate seq、canonical revision、event id、content hash，旧 revision/旧 epoch/重复事件不得覆盖新状态（设计规格 §18.3）。
- 写前的 fence 检查不能替代写后竞态处理：若 fence 检查与外部投影写之间发生删除，投影须通过持久 epoch guard/串行化或写后复核补偿删除，并定期核对直至该主体无残留（设计规格 §15.3）。
- fence 保留期必须覆盖事件重放、索引重建和备份恢复窗口；恢复旧备份时先恢复/合并 fence 才能暴露流量（设计规格 §17.4）。
- 每个投影对删除事件记录持久、可审计的处理回执（含确认无记录的 no-op）及该主体索引/缓存清空核对结果；回执失败、DLQ 有缺口或重建仍可导入旧数据时，删除作业不得标记在线完成；连续 watermark 追过事件只是必要条件（设计规格 §16）。
- Tombstone 至少保留到所有投影确认删除并满足审计窗口（设计规格 §16）。在线删除完成与备份物理清除分别报告，不得互相冒充。

## 9. RLS 与租户隔离

- PostgreSQL 使用最小权限 service role；application role 无 DDL、无越权表权限（实施计划 §9.4/§9.5）。
- 每笔事务用 `SET LOCAL` 注入已验证租户/主体上下文并以 RLS 约束；**缺失上下文一律拒绝**；连接池复用不得残留上一笔事务的身份（设计规格 §17.1、实施计划 §10.3）。
- tenant/subject 只从已验证身份派生，客户端 header/body 与路径参数不得扩大授权；所有 canonical 查询同时约束 tenant 与 subject/scope（设计规格 §17.1）。身份签发者与 JWT/JWKS 契约本身是待批准门禁（§14 G4）。
- Kafka topic/ACL、Redis keyspace、ES alias、向量 namespace 均按租户策略隔离（设计规格 §17.1，具体投影机制归 ADR 0002）。

## 10. 读路径 canonical 校验

- 同步读按设计规格 §10.1 顺序组装；任何 ES/向量/缓存候选在进入 MemoryPack 前按 `memory_ref + canonical_version` 批量回查 PG，过滤过期、撤销、未授权、版本不兼容、被 fence 屏蔽的命中（§10.2）。
- 一致性偏好 `fresh | bounded_stale | cache_preferred` 只影响补充投影的使用方式，**都不能跳过 PG 复核**；`fresh` 要求投影达到请求开始时取得的 PG barrier，否则超时或按显式允许的降级排除来源；输出逐来源实际 watermark、lag 与降级状态（设计规格 §14.1）。
- PG 不可用时：写入失败、不虚假确认；身份无法验证即拒绝读取；默认不使用任何无法复核的缓存历史；允许已验证身份按部署策略返回仅含当前请求的 request-only MemoryPack 并标 degraded（设计规格 §18.2）。
- 两条 confirmed facts 冲突视为数据不变量被破坏：读取 fail closed 并报警（设计规格 §10.3）。

## 11. 风险与缓解

| 风险 | 缓解 |
|---|---|
| outbox 积压（Kafka 不可用/消费者过慢） | canonical 写入不受阻；backlog 阈值告警与 readiness 联动（实施计划 §11.5）；runbook `docs/runbooks/outbox-backlog.md`（Task 5 交付）。 |
| 重复发布/重复消费 | outbox 标记允许重复发布；inbox 去重 + 条件写 + 幂等账本；重放不产生重复 canonical side effect（Task 5 验收）。 |
| 跨分区乱序导致删除后复活 | 持久 fence + envelope epoch + 消费前 fence 检查 + 写后补偿复核 + 回执核对（§8）；自动化测试要求"删除复活 = 0"（设计规格 §19.2）。 |
| watermark 假新鲜（跳过缺口） | 连续前缀语义 + DLQ 缺口可见 + 缺口阻断 fresh/删除完成证明（§7）。 |
| RLS 上下文泄露/缺失 | `SET LOCAL` 事务级注入 + 缺失拒绝 + 连接复用回归测试（Task 3/4 门禁）。 |
| 事件 payload 泄露正文或敏感值 | payload 仅 opaque ref/版本/安全元数据；正文按 ref 回读 PG；契约测试断言禁止字段。 |
| 单点 PG 容量/可用性 | 首轮单区域试点基线（峰值 50 QPS、10 万主体、100 万 turn/日、月可用性 99.9%），上线前必须真实负载复算；复算结论与延迟/错误预算为待批准门禁（§14 G8）。 |
| 本 ADR 被当作全部架构批准 | 本 ADR 仅覆盖骨干；ADR 0002/0003/0004 与评测门禁未关闭前，对应能力保持禁用（§14）。 |

## 12. Task 3/4/5 门禁（本 ADR 的落地验收）

**Task 3 — PostgreSQL canonical schema 与 repository（实施计划 §9）：**

- 迁移实现 conversations/turns/summaries/facts/revisions/proposals/episodes/procedures/outbox/inbox/watermark/idempotency/fence/回执 表，顺序迁移与允许的重放通过；
- outbox、canonical 变更与幂等结果三者原子：并发同 key 只有一次副作用，回滚不占用 key；
- 并发 append 只有合法 seq 成功；并发 fact 更新只有一个 CAS 成功；事件/revision 不可更新删除；
- 删除 fence 拒绝旧 epoch；跨租户读写拒绝；RLS 上下文缺失拒绝；application role 无 DDL；
- 失败事务不留下孤立 outbox；repository 不泄露 SQL/DSN/原始错误。

**Task 4 — Canonical API（实施计划 §10）：**

- 开放任何业务 API 前完成最小 JWT/JWKS 或受信服务身份验证与 RLS 上下文注入（未验收不得对非测试流量开放）；
- 持久 Idempotency-Key 全语义验收：同 key 同 digest 返回原结果（含 commit 成功但响应丢失后的重试）、不同 payload/target 409、同 key 并发串行化、账本过期策略；
- 事务提交 turn + conversation head + outbox + 幂等结果；proposal 永不出现在 facts read；
- 删除请求仅原子建立 fence/不可读状态并发出 tombstone、返回 pending job，不报告在线删除完成；
- API 在 Kafka/Redis/ES/Qdrant 全部关闭时可工作。

**Task 5 — Outbox relay 与 Kafka 消费框架（实施计划 §11）：**

- outbox 批量锁取避免多 relay 重复占用；Kafka key = `tenant_ref + aggregate_ref`；producer ack 满足 durable；publish 后标记 published，标记失败允许重复发布；
- 消费者 inbox 去重 + pending lease 全生命周期测试（含外部投影写前/写后 applied 前/applied 后 offset 前崩溃回放）；
- 每分区 watermark 只推进连续成功前缀；DLQ 缺口下 watermark 不前进；
- schema version 不支持进隔离队列；DLQ 无敏感 payload；backpressure 与优雅停机；
- 验收：重放不重复产生 canonical side effect、同聚合根顺序可验证、Kafka 不可用时 canonical API 继续提交且 backlog 指标/告警按本 ADR §11 执行。

以上任一门禁未通过，不得进入依赖该能力的后续 Task（实施计划 §2.1）。

## 13. 与后续 ADR 的关系

- **ADR 0002（search/vector projections）**：ES/Qdrant 投影、embedding 版本、索引重建切换——继承本 ADR 的 inbox/watermark/fence 约束；
- **ADR 0003（confirmed fact policy）**：namespace 白名单、自动确认准入、冲突状态机——继承本 ADR 的事务边界与 D8；
- **ADR 0004（Go service stack）**：精确版本与库选型——不得引入违反 D1–D8 的依赖。

## 14. 待 owner 批准项与 fail-closed 默认

以下事项**尚未获得 owner 批准**，本 ADR 不为其编造决定；仅登记 owner、最晚关闭点与未关闭时的 fail-closed 默认（与实施计划 §23.2 一致）：

| # | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 |
|---|---|---|---|---|
| G1 | GitHub owner、Go module path、CI 权限模型 | 项目 Owner | Task 1 前 | 不初始化 `go.mod`，不创建远端 workflow |
| G2 | Go 与 PostgreSQL/Kafka/Redis/ES/Qdrant 精确版本、许可证 | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像 |
| G3 | 部署/数据驻留区域、KMS/secret manager | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 |
| G4 | tenant/subject identity 签发者、JWT/JWKS 与服务代理授权契约 | Security + API Owner | Task 2 前 | 不开放业务 API；Task 4 前必须实现并验收身份验证、授权及 RLS 上下文 |
| G5 | confirmed namespace/key 白名单 | Product + Security | Task 7 前 | proposal 不得转 confirmed |
| G6 | embedding provider/model/dimension/DPA 与降级 | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量 |
| G7 | 法务保留、诉讼保全、备份物理清除期限 | Legal + Security | Task 12 前 | 不接真实主体数据，不声称物理清除完成 |
| G8 | 真实负载 latency/error budget、成本与 token budget 复算 | Product + SRE | Task 13 前 | 使用设计初值，不进入发布门禁 |
| G9 | 评测门禁数值（gold set 分层/样本/一致性；precision@5、recall@10、nDCG@10 绝对下限与相对词法基线改善及置信区间；错误记忆率上限；成对任务差异；删除传播 p95/p99） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；阈值必须为批准的数字，禁止 `TBD` 或"优于 baseline" |
| G10 | Kafka topic 命名/分区数/保留期（本 ADR §5.3 的保守草案） | Tech Lead + SRE | Task 5 评审 | 不创建生产 topic，仅本地合成配置 |
| G11 | 平均/峰值消息长度、租户分布、配额与增长余量 | Product + SRE | Task 13 前 | 使用试点基线（50 QPS/10 万主体/100 万 turn/日），禁止生产容量声明 |
| G12 | 首个真实客户端与 shadow 数据集 | Product | Task 14 前 | 不发布 GA |

已确认、无需再批准的基线（本 ADR §2 的依据）：私有 GitHub + GitHub Actions；v1 单区域试点；容量试点基线与 99.9% 月可用性；embedding 仅脱敏低敏文本可外发；raw turns 90 天、审计 365 天；通用 API 显式 `confirm`/`revoke`。

## 15. 后果

**正面：**

- 事实与审计单一化；投影全部可重建，任何补充存储故障都可降级而非丢数据；
- 主对话延迟与派生解耦；事件可重放，新投影可从 PG 全量重建后追增量；
- "删除已传播"与"投影是否新鲜"成为可度量的持久状态（fence、回执、连续 watermark）。

**负面/代价：**

- 所有读路径多一跳 PG 复核（延迟与 PG 负载上升）——这是防复活与防越权的必要成本；
- outbox/relay/inbox 带来额外表、迁移与运维面（backlog 告警、DLQ runbook）；
- at-least-once 意味着消费者必须全部实现幂等，无法靠"恰好一次"偷懒；
- 删除 fence 的持久性要求备份/恢复流程感知 fence，增加演练义务。

**修订规则：** 本 ADR 的任何修改不得违反设计规格不可变原则（§5）与 README 核心不变量；冲突时先修订上游文档并走 Task 0 评审，再同步本 ADR。
