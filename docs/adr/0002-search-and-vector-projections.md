# ADR 0002：Elasticsearch 词法投影与 Qdrant 向量投影

**状态：** Proposed（Task 0 文档切片草案，待 owner 评审；本 ADR 的批准不构成 §17 任何待批准项的批准，也不解除实施计划 §6.4 的 Task 1 阻塞条件）
**日期：** 2026-09-22
**范围（In scope）：** Elasticsearch 词法投影与 Qdrant 向量投影的职责边界、可索引数据与禁止字段、必需索引元数据、embedding 外发合规与版本隔离、乱序/重复/epoch/revision/content hash 条件幂等、删除 fence 写前检查与删除竞态写后补偿、持久删除回执与清空核对、PG 快照 + outbox barrier + 增量追赶 + 切换 barrier 的全量重建与 alias/collection 原子切换、检索硬上限/超时/融合与最终 PG canonical revalidation、ES/Qdrant 单独故障降级、租户隔离。
**明确排除（Out of scope）：** Redis working-memory 投影（实施计划 Task 6，跟随 ADR 0001 的骨干约束，不设独立 ADR）；confirmed fact 准入与冲突状态机（ADR 0003）；Go 服务栈与精确依赖版本（ADR 0004）；融合打分与 rerank 的具体公式、MemoryPack token budget 数值（实施计划 Task 11 实现）；任何代码、依赖、镜像或基础设施变更。
**权威输入（本 ADR 与其冲突时以上游为准，须修订本 ADR）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"）
- `docs/adr/0001-canonical-and-event-backbone.md`（下称"ADR 0001"，只读引用，本 ADR 继承其全部骨干约束）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码、依赖与基础设施；本 ADR 属于实施计划 §6.2（Task 0）文件清单的一部分，先于 Task 1 创建。本 ADR 的创建不构成对任何待批准项（§17）的批准；涉及本文档的验证以文件与引用一致性检查为准，不使用 `git diff`。

---

## 1. 背景与问题

memX 的同步读路径需要词法与语义两类补充召回（设计规格 §10.2）：Elasticsearch 提供 BM25、字段过滤、短语/前缀词法候选，Qdrant 提供脱敏文本 embedding 的语义候选。两者都是可从 PostgreSQL 重建的投影，均不是事实源（设计规格 §1.6、README 架构基线与不变量 1）。

在写任何索引代码前必须回答：

1. 哪些 canonical 数据允许进入索引，哪些字段绝对禁止；
2. 索引文档必须携带哪些元数据，才能支撑读取侧过滤与"不信任投影"的读法；
3. at-least-once 消费与跨分区乱序下，重复、迟到、旧版本事件如何做到条件幂等且不复活已删数据；
4. fence 写前检查为何不够，删除竞态窗口如何补偿；
5. 全量重建与切换期间如何保证删除不丢、新旧投影一致；
6. 第三方 embedding 的外发边界与版本隔离如何设计；
7. 检索的硬上限、超时、降级与最终 canonical revalidation 如何落地。

## 2. 决策（已确认基线）

以下决策来自设计规格 §1"已确认决策"、§15、§17、§18 与 README 核心不变量，本 ADR 予以固化为架构约束。本 ADR 的 D1–D13 独立编号，全文同时继承 ADR 0001 §2 的骨干决策。

- **D1 — ES 词法投影与 Qdrant 向量投影均为可重建投影，均非事实源。** 词法索引用 Elasticsearch，语义索引用独立 Qdrant（设计规格 §1.6）；任一投影失效时系统仍能用当前请求与 PG confirmed facts 工作（设计规格 §5.8、不变量 8）。禁止应用层直接双写 PG 与 ES/Qdrant（实施计划 §2.3）；投影内容只能由事件驱动且可从 PG 重建（实施计划 §2.5）。
- **D2 — 两个投影消费者继承 ADR 0001 §6 的 inbox pending lease 协议。** 先持久占用 pending lease，再执行带 epoch/revision 条件的幂等外部写，确认外部成功后记 `applied`，最后提交 Kafka offset；禁止先标 applied 再执行外部写。ES indexer 与 Qdrant indexer 是独立 consumer group，各自维护独立 inbox 与连续 watermark（ADR 0001 §6/§7）；单投影失败不阻塞其他 consumer group（设计规格 §18.2）。
- **D3 — 可索引数据白名单（设计规格 §15.1）。** 只允许：已脱敏的 episode 摘要（PG `memory_episodes` 已持久化的脱敏摘要，§8.6）、approved procedure 模板（§8.7）、可公开索引的 confirmed fact 文本投影、注册知识源的安全片段。working memory 最近回合、raw turns、任何状态的 proposal、未注册外部内容不进入 ES/Qdrant（Redis 才消费 turn/summary 事件，设计规格 §15.3）。
- **D4 — 禁止字段与内容（设计规格 §15.1、§17.2、实施计划 §2.9）。** 索引中禁止：密钥、凭据、DSN；原始内部主键（索引/向量点 id 只能由 opaque ref 派生）；未脱敏完整聊天；模型隐藏推理、原始 SQL、未脱敏结果行；已拒绝或 pending proposal；无 authorization metadata 的内容。**高敏 namespace 内容禁止进入 ES 和 Qdrant**（§17.2）；原始用户标识（邮箱、手机号、原始 ID）不得出现在索引 id、字段名、日志、指标、trace（实施计划 §2.9、§8.4）。
- **D5 — 每个索引文档/向量 payload 必含元数据（设计规格 §15.2、实施计划 §16.3）。** memory ref；tenant/subject scope；authorization labels；canonical version；status；valid/expires（TTL）；sensitivity；content hash；extractor/embedding model version；index schema version。缺元数据的文档视为非法：读取侧必须丢弃，投影侧必须可识别并清理，不得静默保留。
- **D6 — 第三方 embedding 外发合规（设计规格 §1.11、§17.2）。** 只允许处理经过确定性脱敏的低敏文本；embedding 前执行 PII/secret policy；中敏 namespace 禁止外发第三方 embedding；高敏 namespace 同时禁止进入 ES 和 Qdrant；provider 必须满足批准的数据驻留与不训练条款。**在 G6（provider/model/dimension/处理区域/DPA/降级，§17）批准前，生产 embedding 外发保持关闭**：向量投影只允许确定性测试向量，不对真实数据开放语义召回（实施计划 §23.2）。embedding 请求不得记录原文（实施计划 §16.3）。
- **D7 — 条件幂等（设计规格 §15.3、§18.3）。** 每次投影副作用检查：主体 deletion fence 与事件 `deletion_epoch`（非主体范围事件使用适用 scope 版本，§13.1）、aggregate seq、canonical revision、event id、content hash。旧 revision、旧 epoch、重复 event 均不得覆盖新状态；同版本同 hash 为重复（幂等 no-op），同版本不同 hash 隔离并告警。**硬删除后外部索引版本随之消失，不得单靠 ES external versioning 或 Qdrant 版本点防迟到 upsert**（§15.3）；条件写的比较基准只能是 PG canonical 状态 + 当前 fence。
- **D8 — 删除三道防线（设计规格 §15.3、§17.4、README 不变量 6）。** ① 写前：外部写前按 PG 当前 fence 检查主体 epoch，旧 epoch 事件丢弃；② 写后：fence 检查与外部写之间的删除竞态，通过持久 epoch guard/串行化或写后复核补偿删除，并定期核对直至该主体无残留（§15.3）；③ 回执：每个 tombstone 在每个投影记录持久、可审计的处理回执（含确认无记录的 no-op）及该主体索引/集合清空核对结果；连续 watermark 追过事件只是必要条件，回执失败、DLQ 缺口或重建仍可导入旧数据时不得标记在线删除完成（§16、§17.4，ADR 0001 §8）。
- **D9 — 全量重建与原子切换（设计规格 §15.3）。** 在 PG 一致性快照上记录 outbox barrier → 构建 versioned 新 index/collection（新旧并存，**重建全程新旧投影均处理删除**）→ 消费 barrier 之后的增量追至切换 barrier → 核对删除 fence、删除回执与目标数据 → 原子切换 alias/config → 旧索引/集合隔离并按策略销毁。索引使用 versioned alias/collection（实施计划 §22）。
- **D10 — embedding 版本隔离（设计规格 §15.4）。** 每条向量必须绑定 embedding provider/model、dimension、normalization、chunker version、content hash；模型/dimension/chunker 任一变化使用新 collection（或版本字段）重建，不在原 collection 混用不可比较向量。Qdrant 必须可从 PG + embedding config 全量重建（实施计划 §16.5）。主体删除覆盖所有存活版本 collection，包括重建期的旧集合（§17.4）。
- **D11 — 读路径：有界混合检索 + 硬上限 + 最终 PG 复核（设计规格 §10.1、§10.2、§14.1、§19.2）。** ES 与 Qdrant 并行查询；RRF 或固定权重融合；最终 rerank 必须确定性、可解释并有硬上限。每来源与整体有 topK、filter、timeout、deadline 硬限制（实施计划 §15.3、§16.3、§17.4）；`memory:assemble` 初始目标 p95 ≤ 150 ms、硬超时 ≤ 250 ms（§19.2，真实负载复算前仅为设计初值）。任何搜索命中进入 MemoryPack 前必须按 `memory_ref + canonical_version` 批量回查 PG，复核权限、版本、状态、TTL 与删除 fence；陈旧、过期、撤销、未授权、被 fence 屏蔽的命中一律丢弃（§10.2、README 不变量 4）。`fresh | bounded_stale | cache_preferred` 三种一致性偏好都不能跳过 PG 复核（§14.1）。
- **D12 — 降级（设计规格 §18.2）。** ES 不可用：关闭词法补充召回；Qdrant 不可用：关闭语义补充召回；两者皆不可用：MemoryPack 仅由 canonical 来源组成；PG 不可用：不使用任何无法复核的搜索命中，已验证身份按部署策略仅返回 request-only MemoryPack 并标 degraded，`fresh` 返回不可用。Prompt 组装超时按信任顺序丢弃低优先级来源，不延长模型调用总 deadline。
- **D13 — 租户隔离（设计规格 §17.1）。** ES index alias 与向量 namespace 均按租户策略隔离；查询侧 tenant/scope 过滤条件只从已验证身份派生，不接受客户端自报授权范围；cross-tenant leakage = 0 是自动化门禁（§19.2）。

## 3. 理由

- **投影非事实源 + 读路径 PG 复核**是同一枚硬币的两面：投影承载召回效率，PG 独占授权、版本、状态与删除判定。若索引自身可判定可见性，陈旧索引就会成为越权与复活通道；因此 §7 的元数据是读取侧快速过滤的依据，而最终防线永远是 D11 的批量 canonical revalidation。
- **条件幂等以 PG 为锚**：Kafka 只保证同聚合根分区有序，主体删除与其 turn/fact 事件可能跨分区乱序（ADR 0001 §5.4）；ES/Qdrant 侧的任何版本机制在硬删除后都失去比较对象，唯一可靠的比较基准是 PG canonical revision + 持久 fence。
- **删除竞态补偿**补齐 fence 写前检查的盲区：检查与外部写之间存在时间窗，窗口内发生的删除无法被写前检查感知，只能靠持久 epoch guard 或写后复核把该窗口闭合到可审计。
- **版本隔离**使模型升级不产生静默召回漂移，评测可复现；新 collection 重建路径与 D9 相同，复用同一套 barrier/切换/回执机制。
- **fail-closed 的 embedding 边界**把合规风险从"上线后撤回"变为"批准前不存在"：未批准的 provider/区域/DPA 意味着语义召回对真实数据保持整体禁用，而词法投影不受此阻塞。

## 4. 备选方案与拒绝理由

| 备选 | 拒绝理由 |
|---|---|
| 用 Elasticsearch 插件/dense_vector 同时承担词法与向量召回 | 已确认决策：语义索引使用独立 Qdrant（设计规格 §1.6）；混排会耦合两种投影的容量、版本与故障域，且与已确认基线冲突。 |
| 应用层双写 PG + ES/Qdrant（同步索引） | 违反 README 不变量 1 与实施计划 §2.3；部分失败不可重放；主对话延迟被索引拖入，违反 D2/异步边界（ADR 0001 §5.2）。 |
| 消费者直接从 Kafka payload 取正文建索引 | 违反 ADR 0001 §5.3"Kafka 不承载正文"：outbox 只携带 opaque ref 与安全元数据；索引正文必须按 ref 从 PG 读取并过脱敏/敏感度 gate。 |
| 仅依赖 ES external versioning / Qdrant 版本点阻止迟到 upsert | 硬删除后外部版本消失，索引无从比对；跨分区乱序下写序无保证（设计规格 §15.3、§17.4）。必须使用 PG fence + canonical revision 条件写 + 写后补偿（D7/D8）。 |
| 把"watermark 追过 tombstone"当作删除完成 | ACK、TTL、watermark、tombstone 职责不同，互不替代（设计规格 §16、ADR 0001 §7/§8）；连续 watermark 只是必要条件，DLQ 缺口与回执缺失必须阻断在线完成判定。 |
| 单一全局 index/collection，仅靠 query filter 区分租户 | 设计规格 §17.1 要求 ES alias 与向量 namespace 按租户策略隔离；cross-tenant leakage = 0 是硬门禁（§19.2），隔离结构不得只依赖查询条件正确性。 |
| 模型升级在原 collection 原地覆盖向量 | 向量不可比较，召回静默漂移、评测失效且回滚不可能（设计规格 §15.4）；必须新 collection 重建（D10）。 |
| 无预算/无上限的检索与 rerank | `memory:assemble` p95/硬超时 SLO（设计规格 §19.2）与实施计划 §17.4 的每来源 deadline、topK/最终项数硬上限要求；无上限检索不可预测且可被放大攻击。 |
| 引入额外检索/向量引擎 | v1 已确认基线为 ES + Qdrant（设计规格 §1.6）；新增补充存储须按 Neo4j 先例由真实需求触发并单独评审（§1.9、§3），本 ADR 不预授权。 |

## 5. 投影职责与消费范围

- **消费事件集合（设计规格 §15.3、§13.2）：** `memory.fact.confirmed/superseded/revoked/deleted.v1`、`memory.episode.committed.v1`、`memory.procedure.approved/revoked.v1`、`memory.subject.deletion_requested/completed.v1`，以及 TTL 到期/撤销产生的失效 tombstone（§16）。turn/summary 事件只归 Redis working-memory 投影消费（§15.3），不驱动 ES/Qdrant 索引。
- **内容来源：** 由于 outbox/Kafka 不承载正文（ADR 0001 §5.3），两个 indexer 均为受信消费者，按 opaque ref 从 PG 读取内容，索引前应用脱敏与 sensitivity gate。episode 索引其 PG 已持久化的脱敏摘要（§8.6，不得保存模型隐藏推理、原始 SQL、密钥或未脱敏结果行）；confirmed fact 索引其"可公开索引的文本投影"（§15.1），该文本投影的派生规则属 Task 9 实现细节，必须通过 §6 禁止字段校验与敏感度 gate。
- **proposal 任何状态（pending/confirmed/rejected/expired）均不进入索引**（§15.1）；proposal 变为 confirmed fact 后经由 fact 事件正常投影。
- **消费进度：** ES indexer 与 Qdrant indexer 各自维护 PG inbox（`(consumer_name, event_id)` 去重）与每分区连续 watermark；watermark 记录 canonical barrier/version/time、缺口、DLQ 与重试 offset、lag、health status（ADR 0001 §7，设计规格 §8.10）。MemoryPack meta 暴露 `lexical_watermark` 与 `vector_watermark`（设计规格 §11）。
- **schema 演进：** 事件 schema 只向后兼容演进；不支持版本进入隔离队列，不静默丢弃（ADR 0001 §6）。删除与撤销事件不得因版本升级被忽略（实施计划 §22）。
- **Kafka topic/key：** 依 ADR 0001 §5.3（key = `tenant_ref + aggregate_ref`）；topic 命名/分区数/保留期为 G10 待冻结项，未冻结前不创建生产 topic。

## 6. 可索引数据与禁止字段

### 6.1 允许索引（设计规格 §15.1，配合 §17.2 敏感度约束）

| 数据 | 来源 | 约束 |
|---|---|---|
| 已脱敏 episode 摘要 | PG `memory_episodes` 脱敏摘要 | 索引摘要与安全 Artifact ref；不带原始对话文本 |
| approved procedure 模板 | PG `memory_procedures` | 仅 approved 且版本/授权兼容的记录 |
| confirmed fact 文本投影 | PG `memory_facts`（status=`confirmed`） | 只索引 §15.1 允许的脱敏文本投影；superseded/revoked/deleted 按事件移除 |
| 注册知识源安全片段 | 外部知识源 registry（不冒充 PG 用户记忆 canonical） | 仅注册源；外部知识信任级最低，须遵守授权、删除与重建契约，接入细节另行评审 |

敏感度门槛：**高敏 namespace 内容禁止进入 ES 和 Qdrant**（§17.2）；中敏内容不得外发第三方 embedding。ES 的中敏词法投影仍须通过分类、脱敏及授权校验；中敏的 Qdrant 语义路径在没有单独批准的合规 embedding 方案前保持禁用（§8、§17）。

### 6.2 禁止字段与内容（验收测试必须断言不存在，实施计划 §15.4、§19.4）

- 密钥、凭据、DSN、未脱敏业务结果行；
- 原始内部主键（自增 id、物理主键）；索引/向量点 id 只能由 opaque ref 派生；
- 未脱敏完整聊天与原始 turn 正文（raw turns 永不入索引）；
- 模型隐藏推理；
- 已拒绝或 pending proposal；
- 无 authorization metadata 的内容；
- 高敏 namespace 的任何内容；
- 原始用户标识（邮箱、手机号、原始 ID）出现在 id、字段名、log、metric、trace。

## 7. 必需元数据

每个索引文档与向量 payload 必须携带（设计规格 §15.2、实施计划 §16.3）：

| 元数据 | 作用 |
|---|---|
| memory ref | 回查 PG canonical 的锚点（`memory_ref + canonical_version`，§10.2） |
| tenant/subject scope | 租户隔离过滤与隔离审计（§17.1） |
| authorization labels | 读取侧授权预过滤；缺失即非法文档（§15.1） |
| canonical version | 条件幂等比较与 revalidation 快速校验（D7） |
| status | 撤销/替换/删除状态的投影镜像；仅 PG 状态为准 |
| valid/expires（TTL） | 读取侧过期过滤；TTL 到期不等于 canonical 删除（§16） |
| sensitivity | 敏感度 gate 与读取侧敏感度过滤（§17.2） |
| content hash | 同版本内容一致性判定；同版本不同 hash 隔离告警（D7） |
| extractor/embedding model version | 召回可解释性与版本隔离（D10、§15.4） |
| index schema version | 索引映射演进与重建判定（实施计划 §22） |

精确字段名、类型与 mapping 细节（doc values、stored fields、分片策略等）是 Task 9/10 实现细节，交付时按本节与 §6 落地并由契约测试覆盖；本 ADR 不预设未获批准的数值。

## 8. embedding 合规与版本隔离

### 8.1 外发 gate（顺序执行，任一不通过即不外发、不建向量）

1. **内容资格：** 仅低敏内容具备第三方外发资格；中敏禁止外发，高敏禁止进入 ES/Qdrant（§17.2、D6）；
2. **确定性脱敏：** 低敏文本在调用前执行确定性脱敏与 PII/secret policy（§17.2）；
3. **provider 合规：** provider 必须满足已批准的数据驻留与不训练条款——当前无任何已批准 provider，见 §17 G6；
4. **调用纪律：** embedding 请求/响应不得记录原文（实施计划 §16.3）；调用有 deadline、预算与熔断（实施计划 §14.4 为 extractor 同类约束，Task 10 复用同等纪律）。

### 8.2 G6 未批准期间的 fail-closed 行为

- 生产 embedding 外发关闭；向量投影只使用确定性测试向量（实施计划 §23.2）；
- 语义召回不对真实数据/流量开放；`vector_watermark` 可存在但语义来源保持 excluded/degraded；
- 词法投影（ES）不被此阻塞，可按 Task 9 独立交付；
- 本 ADR 与任何文档不得虚构 provider、model、dimension、区域或 DPA 条款。

### 8.3 版本隔离（设计规格 §15.4、D10）

- 向量绑定 provider/model、dimension、normalization、chunker version、content hash；
- 模型、dimension、normalization 或 chunker 任一变更 ⇒ 新 collection 重建，走 D9 全量重建协议；不同版本向量不可混排、不可互相比较；
- embedding config 是重建输入的一部分：Qdrant 必须可从 PG + embedding config 重建（实施计划 §16.5）；embedding config 变更纳入版本化记录；
- 主体删除与 tombstone 覆盖所有存活 collection（含重建期旧集合），恢复旧备份时先恢复/合并 fence（ADR 0001 §8）。

## 9. 条件幂等：乱序、重复、epoch、revision、content hash

每次外部副作用（upsert/删除）在 inbox pending lease 保护下执行以下检查（设计规格 §15.3、§18.3；ADR 0001 §6/§8）：

| 检查 | 依据 | 行为 |
|---|---|---|
| 主体 deletion fence / deletion epoch | PG 当前 fence（非主体事件用适用 scope 版本，§13.1） | 事件 epoch < 当前 fence ⇒ 丢弃，不产生外部写 |
| aggregate seq | envelope `aggregate_seq` | 同聚合根内旧 seq 不得覆盖新状态 |
| canonical revision | PG 当前 revision | 旧 revision 不得覆盖新 revision；比较基准是 PG，不是索引现存值 |
| event id | inbox `(consumer_name, event_id)` | 重复事件幂等 no-op，不产生第二次外部副作用 |
| content hash | 事件/文档 content hash | 同版本同 hash ⇒ 幂等 no-op；同版本不同 hash ⇒ 隔离并告警，不静默覆盖 |

补充约束：

- 外部写成功但 inbox `applied` 未落库 ⇒ 可安全重试，重试由上表条件吸收（ADR 0001 §6）；
- schema version 不支持的事件进入隔离队列（quarantined），DLQ 不含敏感 payload（ADR 0001 §6）；
- 隔离/DLQ 缺口保持可见；缺口修复并重放前，该分区进度不得作为 fresh 证明、删除完成回执或越过依据（ADR 0001 §7）；
- tombstone 优先于旧事件：删除/撤销的投影处理优先级高于普通更新（设计规格 §5.7、§16）。

## 10. 删除：fence 写前检查、竞态补偿、回执与核对

- **写前检查（必要不充分）：** 外部写前按 PG 当前主体 fence 校验 epoch；旧 epoch 事件丢弃。fence 是 PG 中的持久状态（`tenant_ref + subject_ref + deletion_epoch`，ADR 0001 §8），不是索引内字段。
- **删除竞态补偿：** fence 检查与外部写之间存在竞态窗口；窗口内发生的删除通过持久 epoch guard/串行化或**写后复核补偿删除**闭合，并对该主体定期核对直至无残留（设计规格 §15.3）。防复活判定永不依赖"事件顺序正确"或"索引里恰有旧版本可比较"（§17.4）。
- **迟到 upsert：** 硬删除后原索引文档及其版本消失（§15.3），迟到 upsert 的拒绝依据是 PG fence + canonical revision 条件写（D7），不是 ES/Qdrant 自身版本机制。
- **持久回执与核对（设计规格 §16、§17.4）：** 每个投影对每个 tombstone 记录持久、可审计的处理回执（含"确认无记录"的 no-op 回执），并完成该主体索引/集合清空核对；回执失败、DLQ 有缺口、或重建流程仍可导入旧数据时，删除作业不得标记在线完成。连续 watermark 追过删除事件只是必要条件。在线删除完成与备份物理清除分别报告，不互相冒充。
- **覆盖范围：** 用户删除覆盖 ES、Qdrant、重建快照与适用备份策略（§17.4）；本投影层保证所有存活 versioned index/collection（含重建期新旧两套）都处理删除并出具回执。
- **保留窗口：** tombstone 与 fence 保留期覆盖事件重放、索引重建和备份恢复窗口（ADR 0001 §8、§16）。

## 11. 全量重建与 alias/collection 原子切换

重建协议（设计规格 §15.3；实施计划 §15.3、§16.3、§20.4）：

1. **快照 barrier：** 在 PG 一致性快照上读取全量可索引数据，同时记录快照对应的 outbox barrier；
2. **构建新投影：** 构建 versioned 新 index/collection（新 analyzer/新 embedding 版本在此刻固定为该版本的属性）；**重建全程旧投影继续服务并处理删除**；
3. **双投影删除：** 新投影回放期即应用当前 fence 过滤旧 epoch，新旧投影对同一删除事件各自出具回执；
4. **增量追赶：** 消费 barrier 之后的 outbox 增量，追至切换时点的切换 barrier；
5. **切换前核对：** 核对删除 fence、删除回执、连续 watermark 无缺口及目标数据一致性（安全摘要/计数）；
6. **原子切换：** 原子切换 ES alias / Qdrant active collection 配置；
7. **旧投影处置：** 旧索引/集合隔离（保留核对与回滚窗口），按保留与删除策略销毁；销毁前仍须满足 tombstone/回执约束。

约束：任何阶段不得用"已见最大 offset"冒充进度；步骤 5 未完成不得切换；重建产生的旧数据导入路径必须同样受 fence 过滤（ADR 0001 §8"重建仍可导入旧数据 ⇒ 不得标记删除完成"）。Runbook 交付物：`docs/runbooks/elasticsearch-reindex.md`（Task 9）、`docs/runbooks/qdrant-reindex.md`（Task 10）。

## 12. 读路径：融合、硬上限、超时、revalidation、降级

### 12.1 检索与融合（设计规格 §10.2）

- ES：BM25、字段过滤、短语/前缀；Qdrant：脱敏文本 embedding 相似度；两路并行；
- 可选 domain/intent 精确加分；RRF 或固定权重融合；
- 最终 rerank 必须确定性、可解释并有硬上限（相同输入相同输出，可分解分数）；
- 检索结果作为 data 进入结构化 section，不是 instruction（§17.3）；不可信文本按 untrusted 标记。

### 12.2 硬上限与超时（实施计划 §15.3、§16.3、§17.4；设计规格 §19.2）

- 每来源：topK、filter 条件、timeout 硬限制；ES 深分页使用有界分页/PIT 策略，不开放无界 from+size（实施计划 §15.3）；
- 整体：assemble 总 deadline 与每来源 deadline；超时按信任顺序丢弃低优先级来源，不延长模型调用 deadline（§18.2）；
- 最终项数与 token budget 硬上限，超限按低信任/低相关/旧内容裁剪（§10.4）；
- `memory:assemble` p95 ≤ 150 ms、硬超时 ≤ 250 ms 为设计初值（§19.2）；真实负载复算与最终数值是 G8/G11 待批准项，批准前不得作为发布门禁。

### 12.3 一致性偏好与最终 revalidation（设计规格 §10.2、§14.1）

- `fresh`：投影须达到请求开始时取得的 PG 快照版本/事件 barrier，否则超时返回稳定不可用错误，或仅在客户端显式允许时排除该来源，不得标为 fresh；
- `bounded_stale`：补充投影仅允许在服务端配置的最大时间/版本 lag 内，且仍必须经实时 PG canonical 权限、状态、TTL 与 fence 校验；
- `cache_preferred`：仅影响候选选取，不能跳过 PG 复核；
- 所有命中按 `memory_ref + canonical_version` 批量回查 PG：过期、撤销、未授权、版本不兼容、被 fence 屏蔽的命中一律丢弃；两条 confirmed facts 冲突视为数据不变量破坏，fail closed 并报警（§10.3）；
- 输出逐来源实际 watermark、lag 与降级状态（`lexical_watermark`/`vector_watermark`/`degraded_sources`，§11）；不可验证的来源不得出现在记忆项；
- `POST /v1/memory:explain` 仅供授权运维/审核角色，只含命中 opaque ref、过滤 reason code、分数分解、watermark 与降级来源（§14.5）。

### 12.4 降级矩阵（设计规格 §18.2）

| 故障 | 行为 |
|---|---|
| ES 不可用 | 关闭词法补充召回；canonical 读路径不受影响 |
| Qdrant 不可用 | 关闭语义补充召回；G6 未批准期间该状态为默认 |
| ES + Qdrant 均不可用 | MemoryPack 仅含 canonical 来源（PG facts/working memory/procedures），标注降级 |
| PG 不可用 | 不使用任何无法复核的搜索命中；已验证身份按部署策略返回 request-only（无历史记忆项）并标 degraded；`fresh` 返回不可用 |
| 单投影消费者失败 | 不阻塞其他 consumer group；事件重试或进 DLQ，缺口可见 |

## 13. 租户隔离

- ES index alias 与 Qdrant namespace 按租户策略隔离（设计规格 §17.1）；访问控制、租户作用域与查询过滤共同强制隔离，不得仅靠调用方传入的 query filter；
- 查询侧 tenant/subject/scope 只从已验证身份与代理授权派生；路径参数与客户端 header/body 不得扩大授权（§17.1、实施计划 §10.3）；
- 索引 id 与 payload 禁止原始用户标识（D4、实施计划 §2.9）；
- cross-tenant leakage = 0：自动化隔离测试（含查询侧与重建后数据）零失败（设计规格 §19.2、§19.4）。

## 14. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 陈旧索引命中越权数据或复活已删主体 | 读取侧批量 PG revalidation（D11）+ 消费侧 fence/epoch 条件幂等（D7）+ 写后补偿（D8）；"删除复活 = 0"自动化门禁（§19.2） |
| embedding 外发泄露（未批准 provider/区域） | §8.1 顺序 gate + G6 fail-closed 全局禁用；高敏禁入 ES/Qdrant；embedding 请求不落原文 |
| 模型/chunker 升级导致召回静默漂移、评测不可比 | D10 版本隔离：新 collection 重建，向量绑定 provider/model/dimension/normalization/chunker/content hash |
| 重建期间删除丢失或旧数据复活 | D9：重建全程新旧投影均处理删除；切换前核对 fence/回执/缺口；旧数据导入路径受 fence 过滤 |
| 假新鲜 watermark 掩盖缺口 | 连续前缀语义 + DLQ/隔离缺口阻断 fresh 与删除完成证明（ADR 0001 §7） |
| 同版本不同 hash 的静默覆盖 | content hash 判定 + 隔离告警，不覆盖（D7） |
| 检索放大攻击/超时失控 | topK/filter/timeout/deadline/最终项数硬上限（D11、§12.2） |
| 高敏内容混入索引 | sensitivity gate 在索引与 embedding 两个入口执行；no-forbidden-fields 契约测试（实施计划 §15.4、§19.4） |
| 容量与成本未知即承诺生产指标 | 分片/副本/topK 等数值只允许从试点容量基线推导初值；生产声明待 G8/G11 复算批准 |
| 本 ADR 被当作全部检索架构批准 | §17 待批准项未关闭前对应能力保持禁用；本 ADR 仅覆盖 ES/Qdrant 投影层 |

## 15. Task 9/10/11 门禁（本 ADR 的落地验收）

前置：Task 8 完成；Task 9 与 Task 10 可并行开发，但最终集成与验收串行，避免共享测试环境状态冲突（实施计划 §5）。

**Task 9 — Elasticsearch 词法投影（实施计划 §15）：**

- versioned index + alias；tenant/scope/authorization/status/TTL/canonical version 元数据齐备；只索引允许的脱敏字段；
- 测试：cross-tenant filter；expired/revoked filter；stale revision rejected；duplicate/乱序事件；tombstone 与删除后迟到 upsert（含写前 fence 检查与删除竞态）；含删除的 alias rebuild 与删除回执；analyzer gold set；PG 快照/barrier 追赶、重建期删除、alias 原子切换；ES outage 降级；no forbidden fields 断言；
- 验收：任意命中进入 MemoryPack 前仍需 canonical revalidation；索引可由 PG 全量重建；删除与 projection watermark 可证明；BM25 precision@k/recall@k 记录为 baseline（测量记录，不是阈值批准）。

**Task 10 — Qdrant 向量投影（实施计划 §16）：**

- collection 按 embedding model/dimension/version 隔离；payload 元数据齐备；embedding 前脱敏与 sensitivity gate；高敏禁止外发；chunker/version 入 provenance；upsert 幂等；
- 测试：metadata filter；embedding dimension mismatch；provider timeout；duplicate/乱序；stale version；tombstone、删除后迟到 upsert（含 fence 竞态）；含删除的 collection migration 与删除回执；快照/barrier 追赶与重建；Qdrant outage；sensitive content blocked；vector gold set recall@k；
- 验收：Qdrant 不是事实源；所有命中 canonical revalidation；可从 PG + embedding config 重建；不同 embedding 版本不可混排。**G6 未批准前，生产 embedding 外发与真实数据语义召回保持禁用（fail-closed）。**

**Task 11 — MemoryPack 组装与混合排序（实施计划 §17）：**

- 读取顺序按实施计划 §17.3；ES/Qdrant 并行查询 → 融合 → PG 批量 revalidation → 冲突/去重 → 排序 → token budget 裁剪 → 返回 watermark/degraded sources；
- 测试：duplicate lexical/vector hit；stale hit revalidation；revoked/deleted/expired 过滤（主体删除中不返回旧检索结果）；ES only/Qdrant only/both down；PG fact read down 时 `fresh` 失败、request-only 无历史记忆项；fresh barrier 未达、bounded_stale 超限、cache_preferred 不绕过 PG；deadline；Prompt injection payload；cross-tenant；deterministic rerank ties；
- 验收：precision@5/recall@10/nDCG@10 达到 Task 0 批准的绝对门槛与相对词法基线改善（**G9 未批准前该验收不可能通过，对应能力不得进入灰度/发布**，设计规格 §19.4）；错误记忆率低于批准上限；未确认 proposal、敏感内容、跨租户内容进入 MemoryPack 数量为 0；`memory:assemble` p95 达 spec 初始目标或有批准例外。

以上任一门禁未通过，不得进入依赖该能力的后续 Task（实施计划 §2.1）。

## 16. 与其他 ADR 的关系

- **ADR 0001（canonical 与事件骨干）：** 本 ADR 继承其 inbox pending lease、连续 watermark、删除 fence/epoch、opaque-ref payload 与租户隔离约束（其 §13 亦声明此关系）；冲突时以 ADR 0001 与上游文档为准。
- **ADR 0003（confirmed fact policy）：** namespace/key 白名单与敏感度分类决定哪些 confirmed fact 具备"可公开索引的文本投影"资格；本 ADR 不预判白名单内容。
- **ADR 0004（Go service stack）：** ES/Qdrant 客户端库选型、版本与许可证在彼处冻结（G2）；不得引入违反本 ADR 条件幂等与 fence 约束的抽象。

## 17. 待 owner 批准项与 fail-closed 默认

以下事项**尚未获得 owner 批准**，本 ADR 不为其编造决定；仅登记 owner、最晚关闭点与未关闭时的 fail-closed 默认（与实施计划 §23.2、ADR 0001 §14 对应项一致）：

| # | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 |
|---|---|---|---|---|
| G2 | Elasticsearch/Qdrant 精确版本、许可证（含客户端库） | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像；本地 Compose 不启动 ES/Qdrant |
| G3 | 部署/数据驻留区域、Qdrant 拓扑（设计规格 §21.2） | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 |
| G6 | embedding provider/model/dimension/normalization/处理区域/DPA 与不训练条款、禁用时降级 | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量；语义召回不对真实数据开放 |
| G-A | ES analyzer 选择（含语言 analyzer、自定义 filter；实施计划 §15.3 要求此决定进入 ADR） | Tech Lead（沿用 G2 的角色指定，评审时可由 owner 更正） | Task 9 评审 | 仅引擎默认 standard analyzer，不启用自定义/语言 analyzer；analyzer gold set 通过前不得切换 |
| G9 | 检索评测数值门禁（precision@5、recall@10、nDCG@10 绝对下限、相对词法基线改善与置信区间、错误记忆率上限、删除传播 p95/p99） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；阈值必须为批准数字，禁止 `TBD` 或"优于 baseline" |
| G8/G11 | 真实负载 latency/error budget、成本、容量复算（分片/副本/topK/配额推导） | Product + SRE | Task 13 前 | 使用试点基线（50 QPS/10 万主体/100 万 turn/日）推导初值，禁止生产容量声明与发布门禁 |
| G10 | Kafka topic 命名/分区数/保留期（影响两个投影消费者，ADR 0001 §5.3 保守草案） | Tech Lead + SRE | Task 5 评审 | 不创建生产 topic，仅本地合成配置 |
| G-M | 中敏内容的语义召回路径（自建 embedding 或其他方案，未来如需要） | ML + Security（沿用 G6 的角色指定，评审时可由 owner 更正） | 需求出现时单独评审 | 默认禁用：不外发、不生成语义向量 |

已确认、无需再批准的本 ADR 基线（依据见 §2）：ES 词法 + 独立 Qdrant 语义投影且均非事实源；索引白名单与禁止字段（§6）；必需元数据集合（§7）；第三方 embedding 仅接收确定性脱敏低敏文本，中高敏禁止外发，高敏禁入 ES/Qdrant；索引使用 versioned alias/collection；tenant/scope/auth/TTL/version 元数据与 PG canonical revalidation 为读路径强制项。

## 18. 后果

**正面：**

- 词法与语义召回可独立故障、独立重建、独立降级，canonical 服务在 ES/Qdrant 全部不可用时仍完整可用；
- 删除防复活从"事件顺序假设"变为"持久 fence + 条件写 + 写后补偿 + 回执核对"的可审计机制链；
- embedding 版本隔离使模型升级可评测、可回滚，召回行为变化被限制在新 collection 内；
- fail-closed 的 embedding 边界使合规审批成为能力启用的唯一开关，不存在"先上线再补审批"的窗口。

**负面/代价：**

- 每个命中多一跳 PG 批量复核，检索延迟与 PG 负载上升——这是防越权与防复活不可省略的成本；
- 条件幂等与回执核对给两个投影带来可观的实现与测试面（Task 9/10 门禁规模可见）；
- 全量重建的双投影删除处理拉长重建窗口、增加核对复杂度；
- G6 未关闭期间语义召回整体缺席，混合检索收益在该阶段无法验证。

**修订规则：** 本 ADR 的任何修改不得违反设计规格不可变原则（§5）、README 核心不变量与 ADR 0001 骨干决策；冲突时先修订上游文档并走 Task 0 评审，再同步本 ADR。
