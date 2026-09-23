# ADR 0004：Go 服务栈、基础设施客户端选型准则与 Task 1 版本冻结门禁

**状态：** Proposed（Task 0 文档切片草案，待 owner 评审。本 ADR 不批准任何精确版本、依赖、镜像或基础设施选择；不构成 G1/G2/G3/G9 中任何待批准项的批准，也不解除实施计划 §6.4/§23.2 的 Task 1 阻塞条件，见 §12）
**日期：** 2026-09-23
**范围（In scope）：** Go 进程边界（API / outbox relay / worker / CLI）与单模块布局；标准库、小型成熟库与框架的取舍准则及依赖准入流程（许可证、维护状态、替代方案的核对与冻结）；PostgreSQL（canonical/outbox/inbox/watermark/fence）、Kafka（本地以兼容实现 Redpanda 开发）、Redis、Elasticsearch、Qdrant、OpenTelemetry 的客户端职责与选择标准；配置与 secret 类型纪律、liveness 与 readiness 契约、结构化安全日志、超时/取消与优雅停机、迁移工具准则；版本冻结清单字段、候选观察快照与采集方法；CI/Compose 健康检查边界与 Task 1 测试门禁。
**明确排除（Out of scope）：** 任何精确版本号、Go module path、GitHub owner、部署区域/KMS 的决定——G1/G2/G3 未关闭，本 ADR 只定义冻结清单字段与决策准则（§8），不填值；`go.mod`/`go.sum`、CI workflow、Compose/Dockerfile 内容、任何依赖引入或镜像拉取（G1/G2 关闭前 fail-closed 禁止，§12）；canonical 事务边界、outbox/inbox/watermark/删除 fence 机制本身（ADR 0001，本 ADR 只继承）；ES/Qdrant 投影协议、索引契约与重建切换（ADR 0002）；confirmed fact 准入与状态机（ADR 0003）；tenant/subject 身份、JWT/JWKS 契约（G4，Task 2）；embedding provider/DPA（G6）；MemoryPack 融合公式与 token budget 数值（实施计划 Task 11）；任何代码、镜像或基础设施变更。
**权威输入（本 ADR 与其冲突时以上游为准，须修订本 ADR）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"）
- `docs/adr/0001-canonical-and-event-backbone.md`（下称"ADR 0001"，只读引用，本 ADR 继承其全部骨干约束）
- `docs/adr/0002-search-and-vector-projections.md`（下称"ADR 0002"，只读引用）
- `docs/adr/0003-confirmed-fact-policy.md`（下称"ADR 0003"，只读引用）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码、依赖与基础设施；本 ADR 属于实施计划 §6.2（Task 0）文件清单的一部分，先于 Task 1 创建。实施计划 §4 要求"候选库必须在 Task 1 ADR 中记录许可证、维护状态和替代方案"，且 §23.2 规定版本/许可证冻结最晚在 Task 1 前关闭——两者的落点都是本 ADR：Task 0 先建立准则与冻结清单（本文件，Proposed），G2 owner 批准后以修订方式填入精确值。本 ADR 的创建不构成对任何待批准项的批准；涉及本文档的验证以文件与引用一致性检查为准，不使用、不宣称 `git diff` 检查；§9 候选观察快照采集于 2026-09-23（UTC），仅作候选证据，冻结时必须重新核验。

---

## 1. 背景与问题

v1 服务端使用 Go（设计规格 §1.7 已确认）；具体框架和库由实施计划按最小依赖原则确定（设计规格 §1.7、实施计划 §4）。memX 包含一个同步 API、一个 outbox relay、多个异步消费者族和 CLI 工具，依赖五个异构存储（PG、Kafka、Redis、ES、Qdrant）与一个可选的 embedding 外发通道。

在初始化项目（Task 1）之前必须回答：

1. Go 代码如何划分为二进制与模块边界，各进程的健康语义是什么；
2. 标准库、小型成熟库、框架之间按什么准则取舍，依赖按什么流程准入；
3. 五个基础设施各自需要客户端承担什么职责、按什么标准选择，哪些约束是一票否决；
4. 配置与 secret、日志、超时/取消、迁移必须满足哪些工程纪律（多数已是实施计划 §7 的 Task 1 强制要求，本 ADR 将其固化为架构约束）；
5. 候选依赖的许可证/维护状态/替代方案如何核对，何时、由谁冻结；
6. 哪些事项是 owner 阻塞门禁（G1/G2/G3/G9），未关闭时的 fail-closed 默认是什么。

## 2. 决策

以下决策中：D1/D2/D5–D8/D10 是对设计规格与实施计划已确认基线及 Task 1 强制要求的架构固化；D3/D4/D9 是本 ADR 提出的冻结前准则与流程（Proposed，G2 评审可修订后批准）。本 ADR 的 D 编号独立，全文同时继承 ADR 0001 §2 骨干决策（D1–D8）、ADR 0002 投影约束与 ADR 0003 域契约约束。

- **D1 — 依赖优先级：标准库 > 小型成熟库 > 框架（实施计划 §4 固化）。** 标准库能满足的直接用标准库（HTTP 服务、JSON 编解码、`log/slog` 结构化日志、`context` 超时与取消、`database/sql` 接口、`crypto/*`、`time` 等）。需要第三方时优先"单一职责、接口面小、许可证宽松、可替换"的成熟库。不以框架替代可组合的标准库能力；若 G2 评审认为某框架有实质必要，须作为例外由 owner 批准并记录理由。任何依赖不得引入违反 ADR 0001 D1–D8（骨干）、ADR 0002 条件幂等/fence 约束、ADR 0003 聚合隔离与 D6 事务边界的抽象（三份 ADR 对本 ADR 的反向约束，见 §13）。
- **D2 — 单 Go 模块、多二进制进程边界（实施计划 §3 固化）。** 单一 module（module path 待 G1 冻结，占位禁止）；二进制划分为：`cmd/memx-api`（同步 Canonical API，Task 1 骨架/Task 4）、`cmd/memx-outbox-relay`（Task 5）、`cmd/memx-worker`（Kafka 消费者族：extractor、Redis/ES/Qdrant 投影、retention/deletion 编排，按 consumer group 部署拆分，Task 6/8/9/10/12）、CLI `cmd/memx-replay` 与 `cmd/memx-eval`（Task 14）。共享代码全部在 `internal/*`，公共能力只经显式构造函数注入（不引入 DI 框架；如需变更走 G2 例外）。API 进程不内嵌 relay/consumer 循环：同步路径与异步积压的故障域、扩缩容与部署生命周期必须可分离（ADR 0001 §5.2 同步/异步边界）。细节见 §5。
- **D3 — 客户端职责与选择标准（Proposed，G2 冻结）。** 每个基础设施域先固定客户端职责与可核验的选择标准（§6），候选只作评审输入；G2 批准前不采用任何候选、不引入任何依赖。硬性标准（一票否决）：显式 SQL/协议语义不被屏蔽、`context` 取消传播、超时/重试可注入、许可证落入 §D4 接受集、无未申报的 cgo 依赖（需 cgo 的候选一律须 owner 批准例外）。
- **D4 — 依赖准入与核对流程（Proposed，Task 1 执行、G2 批准）。** 每个候选依赖按以下顺序核对并留档于 §8 冻结清单：
  1. **提名**：用途、替代方案（含"为什么标准库不足"）、预估传递依赖面；
  2. **许可证核对**：读取上游仓库 LICENSE/NOTICE 及模块内各子包许可证；接受集为 MIT / BSD-2/3-Clause / ISC / Apache-2.0（及等效宽松许可）；强 copyleft（GPL/AGPL/SSPL 类）或许可证不明的依赖不得采用，除非 owner 书面批准；
  3. **维护状态核对**：只允许写入可核验证据（仓库 URL、采用 tag、最近 release 日期及来源、issue/PR 响应趋势、维护者集中度）；**无证据不得声称"活跃维护"；无法核验的字段必须标注【待验证】且该项不得进入冻结清单**；
  4. **替代方案对照**：至少一个替代（含自写）与淘汰理由；
  5. **冻结**：G2 owner 批准后写入 §8 清单，`go.mod` 固定直接依赖版本及模块图约束，`go.sum` 校验下载内容哈希（不是版本锁文件）；CI 审计 `go list -m all` 解析后的完整依赖图与批准清单，防止未审批模块进入。
- **D5 — 配置与 secret 纪律（实施计划 §7.3/§7.4 固化）。** `internal/config` 严格解析环境变量：未知字段、非法值、缺失必需项一律启动失败；各二进制只解析自己需要的配置子集。secret 使用专用类型：不实现明文 `String()`/`GoString()`，不参与默认 JSON 序列化，日志与错误输出必须 redaction；无默认生产凭据（实施计划 §7.5）。v1 本地合成配置来自环境变量；真实 KMS/secret manager 属 G3，未批准前不接任何真实凭据。
- **D6 — liveness 与 readiness 分离（实施计划 §7.3 + ADR 0001 §11 固化）。** liveness 只做进程自检（关键循环存活、未死锁），**不探测外部依赖**，避免依赖故障引发重启风暴；readiness 按角色报告"配置完整 + 必需依赖可用"：`memx-api` 必需依赖为 PostgreSQL（canonical 是 API 的必需依赖）；Kafka/Redis/ES/Qdrant 的故障按 ADR 0001 §10、ADR 0002 §12.4 降级矩阵处理，**不得进入 API liveness 判定**，outbox backlog 阈值与 readiness/告警的联动按 ADR 0001 §11（Task 5 验收）。relay/worker 的 readiness 各含其角色必需依赖（PG 与 Kafka 必需，投影目标按 consumer group 角色）。依赖"未配置"必须明确失败（进程退出），区别于"已配置但暂不可达"（readiness 置为 not ready）。细节见 §7.2。
- **D7 — 结构化安全日志（实施计划 §7.3、§2.9，设计规格 §17.2/§19.3 固化）。** 用标准库 `log/slog` 输出结构化日志；默认不输出配置值；日志、错误、指标、trace 中不得出现原始用户标识（邮箱/手机号/原始 ID）、secret、DSN、正文或敏感值；关联只用 opaque ref/`trace_ref`；secret 类型不参与序列化（D5）。审计语义进 PG/事件流，不靠日志承担。
- **D8 — 超时、取消与优雅停机（实施计划 §10.3/§17.4、Task 1 clean shutdown 固化）。** `context` 从 HTTP 请求/消费循环 → service → repository → 各客户端全程传播，取消向下生效；每来源/整体 deadline 硬上限的机制在本层提供，数值归 Task 11/G8；所有定时器与时钟可注入（测试确定性）；优雅停机 = 停止接流 → 排空 in-flight → 释放 inbox pending lease/连接（Task 1 clean shutdown 测试；Task 5 backpressure/优雅停机验收）。
- **D9 — 迁移纪律（Proposed 准则，实施计划 §9.2/§9.4/§22 固化部分）。** migration 只前进、不依赖 down migration 恢复（实施计划 §22）；顺序编号 001–009（实施计划 §9.2）；幂等或有明确不可重放边界；时间统一 UTC（实施计划 §9.4）。工具选择按 §6"迁移"行标准在 Task 1/G2 冻结其一（候选：`pressly/goose`、`golang-migrate`、或仓内最小 SQL runner）；本 ADR 不预批任何迁移工具。
- **D10 — CI/Compose 与 Task 1 门禁边界（实施计划 §7/§21 固化）。** Task 1 交付 `go.mod`/`Makefile`/`.golangci.yml`/健康检查入口/严格 config/CI workflow/Compose 文件，但**全部以 G1/G2 关闭为前提**；门禁命令与验收见 §10。G1/G2/G3 关闭前的 fail-closed 默认：不初始化 `go.mod`、不创建远端 workflow、不引入依赖、不拉取镜像、仅本地合成测试配置（实施计划 §23.2）。

## 3. 理由

- **标准库优先**把供应链面、审计面与升级面压到最小：Go 标准库的兼容性与安全补丁承诺由上游承担，第三方依赖每多一个，许可证、漏洞与弃维风险的核对义务就多一份；这与设计规格 §1.7"最小依赖原则"一致。
- **小型成熟库**保持可替换性：当某库停止维护或许可证变化时，单一职责的库替换成本远低于框架锁定。
- **进程边界即故障域边界**：API 的可用性承诺（月 99.9%、append p95 ≤ 100 ms，设计规格 §19.2）不应被消费者积压拖垮；relay 与消费族分离使 backlog 告警、扩容与发布可独立操作（ADR 0001 §11）。
- **先定职责与标准、后定候选**：G2 未关闭时任何"钦定版本"都是编造批准；把选择标准写成可核验条目，冻结时只需逐条打勾，评审可追溯。
- **secret 类型化 + redaction 测试**把"日志泄露"从运行时纪律变成编译期/测试期失败（实施计划 §7.4 明确要求该测试）。
- **liveness/readiness 分离**防止"依赖抖动 → 全体重启 → 故障放大"；readiness 语义与降级矩阵（ADR 0001/0002）对齐，保证"ES/Qdrant 全挂时 API 仍服务"不被健康检查破坏。
- **候选观察快照（§9）如实标注采集日期与非批准属性**：既给 G2 评审提供起点，又不制造"已有版本基线"的假象；快照会陈旧，冻结必须重核。

## 4. 备选方案与拒绝理由

| 备选 | 拒绝/限制理由 |
|---|---|
| 以全栈 Web 框架作为 v1 默认（路由+中间件+DI 一体） | 与"标准库优先"冲突（D1）；v1 的 HTTP 面（spec §14）标准库可覆盖；不绝对禁止——G2 评审给出实质理由可作例外批准。 |
| ORM/重型查询构建器访问 canonical | 屏蔽显式 SQL、CAS、事务内 `SET LOCAL` RLS 注入与同事务 outbox/幂等形状（ADR 0001 §5.1、spec §17.1）；审计与隔离语义必须显式可控（一票否决，D3）。 |
| 依赖 cgo 的 Kafka 客户端（librdkafka 绑定类）作为默认 | 构建矩阵、镜像与升级复杂度上升；除非 G2 批准例外，候选限于纯 Go 实现（D3）。 |
| 微服务框架/服务网格 v1 引入 | 单区域试点、进程边界已定（D2）；生产化阶段（设计规格 Phase 5）再评估，本 ADR 不预授权。 |
| relay/consumer 内嵌进 API 进程 | 混淆故障域与扩缩容；违反 D2 与实施计划 §3 结构、Task 5 独立验收。 |
| 在 Task 0/本 ADR 直接冻结精确版本 | G2 owner 未批准（§12）；§9 快照即陈旧；编造版本或维护状态违反实施计划 §23.2 与本 ADR D4。fail-closed：不引入依赖、不拉镜像。 |
| 在本 ADR 预置 Compose/Dockerfile/workflow/go.mod 内容 | 属 Task 1 交付物且以 G1/G2 关闭为前提（实施计划 §7.2、§23.2）；Task 0 只写准则。 |
| 为日志/指标引入第三方日志库 | `log/slog` 已满足结构化与性能需求（D7）；引入第三方须按 D4 流程证明标准库不足。 |
| 引入 DI 框架、通用 server 基座库 | 显式构造函数注入足够（D2）；新抽象须按 D4 提名并证明替代方案不足。 |

## 5. 进程边界与契约

| 二进制 | 职责（上游依据） | 必需依赖 | liveness | readiness（判 not ready 的条件） | 交付 |
|---|---|---|---|---|---|
| `cmd/memx-api` | 同步 Canonical API：turn append/read、fact commands、删除 fence 原子入口（实施计划 §10；设计规格 §14） | PostgreSQL | 进程自检 | 配置缺失 ⇒ 启动失败；PG 不可达 ⇒ not ready；Kafka/Redis/ES/Qdrant 故障**不进入** readiness 判定（降级矩阵，ADR 0001 §10、ADR 0002 §12.4） | Task 1 骨架 / Task 4 |
| `cmd/memx-outbox-relay` | outbox 批量锁取 → Kafka durable 发布 → 标记 published（实施计划 §11.3） | PostgreSQL、Kafka | 进程自检 | PG/Kafka 配置缺失 ⇒ 启动失败；不可达或 backlog 超阈 ⇒ not ready + 告警（ADR 0001 §11 联动，Task 5 验收） | Task 5 |
| `cmd/memx-worker` | Kafka 消费者族：extractor（Task 8）、Redis/ES/Qdrant 投影（Task 6/9/10）、retention/deletion 编排（Task 12）；按 consumer group 独立部署与独立 inbox/watermark（ADR 0001 §6/§7） | PostgreSQL、Kafka + 角色必需的投影目标（如 ES 投影实例需 ES） | 进程自检 | 按角色：配置缺失 ⇒ 启动失败；所属角色依赖不可达 ⇒ 该实例 not ready；单投影失败不阻塞其他 consumer group（设计规格 §18.2） | Task 6/8/9/10/12 |
| `cmd/memx-replay`、`cmd/memx-eval` | 重放/评测 CLI（实施计划 §20.2）；非服务进程，无健康端点 | PostgreSQL（按子命令） | 无 | 无 | Task 14 |

约束：

- 单一 module；`cmd/*` 只做装配（config → 依赖构造 → 信号处理 → 启动/停机），业务在 `internal/*`；
- `memx-worker` 是部署聚合名：实施时按 consumer group 拆分为多实例/多部署，不改变模块边界与 inbox/watermark 归属；
- 各二进制的 config 是全集的子集视图，未知/缺失字段失败语义一致（D5）。

## 6. 客户端职责与选择标准

选择标准在 G2 冻结时逐条核验；"候选"列仅为评审输入（观察快照见 §9），**非批准**：

| 依赖域 | 客户端职责（只做什么） | 选择标准（冻结时逐条核验） | 候选（非批准） |
|---|---|---|---|
| PostgreSQL | canonical 全部读写：事务边界、事务内 `SET LOCAL` RLS 注入、CAS、outbox/inbox/watermark/fence/幂等账本（ADR 0001 §5/§6/§8/§9） | 显式 SQL、参数化；`context` 取消传播；事务作用域连接卫生（会话状态不跨事务残留，spec §17.1）；batch/COPY；连接池指标暴露；**无 ORM**（D3 一票否决） | `jackc/pgx/v5`（native 接口或经 `database/sql`）；备选 `database/sql` + 其他驱动（维护状态冻结时核验） |
| Kafka（生产协议） | outbox 事件发布、消费族 offset 提交（inbox applied 后）、DLQ 重定向、重放（ADR 0001 §5–§7） | 纯 Go、无 cgo（例外须 owner 批准）；手动/精确 offset 提交；rebalance 钩子可控；producer durable acks；背压与优雅停机；消费语义可在单测中 fake（不依赖 broker）；**生产协议按 Kafka，本地开发可用兼容实现 Redpanda（实施计划 §4）；两者版本均在 G2 冻结** | `twmb/franz-go`、`IBM/sarama`、`segmentio/kafka-go`（三选一由 G2 按 D3/D4 评审冻结） |
| Redis | 仅 working-memory 投影读写、TTL、重建回补（设计规格 §9；实施计划 Task 6） | `context` 支持；TTL/条件写语义透明；pipeline；不引入缓存框架式封装；单区域试点不要求集群拓扑，但选型不得阻断未来切换（G3 范围） | `redis/go-redis/v9` |
| Elasticsearch | 词法投影索引/删除与 BM25 检索；bulk/PIT/alias（ADR 0002） | 与冻结的 ES 服务器版本有明确兼容矩阵；bulk / point-in-time / alias API 覆盖；超时/重试可注入；优先官方客户端且版本线与服务器对齐 | `elastic/go-elasticsearch/v8`（版本线随 G2 冻结的 ES 服务器对齐） |
| Qdrant | 向量投影 upsert/删除/过滤检索；collection 版本隔离（ADR 0002） | 与冻结的 Qdrant 版本兼容；gRPC 或 REST 之一明确；metadata filter / delete-by-filter 覆盖；若官方客户端与冻结版本不兼容，则以 `net/http` + 显式 DTO 实现薄客户端，不引第三方封装 | `qdrant/go-client` |
| OpenTelemetry | trace/metric 采集与 OTLP 导出（实施计划 §4） | OTLP exporter；默认不采集敏感属性；可整体关闭（降级/本地场景）；指标命名可映射 spec §19.1 清单 | `go.opentelemetry.io/otel` 及 otlp exporter |
| Embedding provider（HTTP） | 脱敏低敏文本外发调用（G6 批准后才有配置） | `net/http` + 显式 deadline/预算/熔断；不引 SDK；请求/响应不落原文（实施计划 §16.3）；**G6 未批准前不存在任何 provider 配置** | 无（标准库） |
| 迁移 | 执行 `migrations/001–009` 顺序迁移 | 只前进、不依赖 down（实施计划 §22）；单实例执行（advisory lock 或等价）；事务性 DDL 可控；可在测试中针对 `memx_test` 库运行（实施计划 §9.5） | `pressly/goose`、`golang-migrate` 或仓内最小 SQL runner（Task 1/G2 冻结其一，D9） |
| JWT/JWKS 验证 | Task 4 身份验证基线（实施计划 §10.3） | **受 G4 身份契约约束**：先冻结 issuer/audience/scope/代理授权契约，再按 D4 流程选库或以标准库 `crypto/*` 实现 | 候选观察见 §9（`golang-jwt/jwt/v5` 等）；冻结不得早于 G4 |

## 7. 工程约束（Task 1 实现要求的固化）

### 7.1 配置与 secret

- 严格环境变量解析：未知字段/非法值/缺失必需项启动失败（实施计划 §7.4"配置未知字段/非法值/缺少 secret 失败测试"）；
- secret 专用类型：无明文 `String()`/`GoString()`，不参与默认 JSON 序列化；与日志 redaction 一起由测试门禁覆盖（实施计划 §7.4"secret 不出现在日志和错误测试"）；
- 无默认生产凭据（实施计划 §7.5）；真实 KMS/secret manager 接入前必须 G3 关闭（§12）。

### 7.2 liveness/readiness 契约（D6 的可测条目）

1. **L1** liveness 端点不执行任何外部依赖探测（结构上不可配置探测目标）；
2. **L2** 关键循环（relay 取批循环、consumer poll 循环）停滞 ⇒ liveness 失败 ⇒ 允许被编排重启（这是进程级故障）；
3. **R1** 依赖未配置 ⇒ 启动失败（不是 not ready 常态）；
4. **R2** 已配置依赖不可达 ⇒ 对应 readiness 置 not ready，liveness 不变；不可达的判定带独立超时，不阻塞请求路径；
5. **R3** API 的 readiness 只含 PostgreSQL；补充设施故障按降级矩阵处理；outbox backlog 阈值与 readiness/告警联动仅适用于 relay/消费路径（ADR 0001 §11，Task 5 验收）。

### 7.3 结构化安全日志

- `log/slog` JSON 输出；日志默认结构化且不输出配置值（实施计划 §7.3）；
- 禁止字段：原始用户标识、secret、DSN、正文、原始异常串（错误只映射稳定 reason code，spec §17.2）；关联用 opaque ref/`trace_ref`（spec §19.3）；
- secret 类型不实现序列化旁路（D5）。

### 7.4 超时、取消与优雅停机

- `context` 全程传播；每个外部客户端可注入超时/重试（§6 标准项）；
- 定时器与时钟可注入（Task 3 scheduler、Task 12 TTL、Task 8 模型 deadline 都依赖）；
- 优雅停机序列（D8）作为 Task 1 clean shutdown 测试的契约，Task 5 复用于 consumer/relay。

### 7.5 迁移纪律

- 顺序迁移 `001–009`，只前进（实施计划 §9.2、§22）；允许的重放边界显式声明（实施计划 §9.4/§9.5）；
- 测试只连 `memx_test`/`*_test` 数据库（实施计划 §9.5/§9.6）；
- 工具冻结前不写任何迁移代码（G2/D9）。

## 8. 版本冻结清单（字段模板——G1/G2/G3 关闭前必须保持空值）

| 冻结项 | 必填字段 | 门禁（Owner） | 当前值 |
|---|---|---|---|
| GitHub owner / Go module path / CI 权限模型 | owner、module path、workflow 权限模型、批准记录引用 | G1（项目 Owner） | **未批准——禁止填入** |
| Go 工具链 | 精确版本、`toolchain` 指令策略、CI 构建镜像及 digest、升级/回滚策略 | G2（Tech Lead） | **未批准——禁止填入** |
| 基础设施服务器版本 | PostgreSQL、生产 Kafka、本地 Redpanda、Redis、Elasticsearch、Qdrant 的精确版本 + 镜像 digest + 兼容矩阵（ES 客户端↔服务器、Qdrant 客户端↔服务器） | G2（Tech Lead） | **未批准——禁止填入** |
| 每个 Go 依赖 | 模块路径@精确版本、许可证及证据来源、维护状态证据（§D4 第 3 条格式）、替代方案与淘汰理由、传递依赖审计结论 | G2（Tech Lead） | **未批准——禁止填入** |
| 迁移工具 | §6"迁移"行标准逐条核验结论 | G2（Tech Lead） | **未批准——禁止填入** |
| 部署/数据驻留区域、KMS/secret manager | 区域、KMS 选型、合成测试配置边界 | G3（Platform + Security） | **未批准——禁止填入** |
| JWT/JWKS 库 | 依赖 G4 身份契约，按 §6 标准核验 | G4（Security + API Owner），冻结动作归 G2 | **未批准——禁止填入** |
| Embedding provider HTTP 配置 | 依赖 G6；未批准前该配置不存在 | G6（ML + Security） | **未批准——禁止填入** |

规则：批准时以修订本 ADR 的方式填入，并引用批准记录（决策文件/评审纪要）；清单内不得出现 `TBD`/`待定` 字样充当通过条件（与 G9"禁止 TBD"同一纪律）；任何"候选版本"只允许出现在 §9 观察快照并带非批准标记。

## 9. 候选观察快照（采集于 2026-09-23 UTC；非批准、非冻结）

**采集方法（可复现）：**

- 版本与发布时间：`GET https://proxy.golang.org/<module>/@latest`（Go module proxy 官方端点）；
- Go 稳定版本：`GET https://go.dev/dl/?mode=json`；
- 许可证：`https://pkg.go.dev/<module>@<version>?tab=licenses`；`IBM/sarama` 经 GitHub API `GET /repos/IBM/sarama/license`（pkg.go.dev 对该路径返回 400）。

**观察值：**

| 目标 | 观察最新版本 | 发布时间（UTC） | 许可证（观察值） | 证据来源 |
|---|---|---|---|---|
| Go 工具链（当前稳定线） | go1.27.1 / go1.26.8 | — | 冻结时核验 | go.dev/dl |
| `github.com/jackc/pgx/v5` | v5.11.0 | 2026-09-07 | MIT | pkg.go.dev licenses |
| `github.com/twmb/franz-go` | v1.22.0 | 2026-09-18 | BSD-3-Clause | pkg.go.dev licenses |
| `github.com/IBM/sarama` | v1.61.0 | 2026-09-22 | MIT | GitHub API license 端点 |
| `github.com/segmentio/kafka-go` | v0.4.51 | 2026-04-23 | MIT | pkg.go.dev licenses |
| `github.com/redis/go-redis/v9` | v9.22.0 | 2026-08-03 | BSD-2-Clause | pkg.go.dev licenses |
| `github.com/elastic/go-elasticsearch/v8` | v8.19.7 | 2026-08-04 | Apache-2.0 | pkg.go.dev licenses |
| `github.com/qdrant/go-client` | v1.19.2 | 2026-09-06 | Apache-2.0 | pkg.go.dev licenses |
| `go.opentelemetry.io/otel` | v1.46.0 | 2026-08-25 | Apache-2.0 / BSD-3-Clause（模块内多许可证，按子包核验） | pkg.go.dev licenses |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` | v1.46.0 | 2026-08-25 | 同上 | pkg.go.dev licenses |
| `github.com/golang-migrate/migrate/v4` | v4.20.1 | 2026-09-09 | MIT | pkg.go.dev licenses |
| `github.com/pressly/goose/v3` | v3.28.0 | 2026-09-02 | MIT | pkg.go.dev licenses |
| `golang.org/x/sync` | v0.23.0 | 2026-08-31 | BSD-3-Clause | pkg.go.dev licenses |
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | 2026-01-28 | MIT | pkg.go.dev licenses |
| `github.com/google/uuid` | v1.6.0 | 2024-01-23 | BSD-3-Clause | pkg.go.dev licenses |

**基础设施服务端候选矩阵（采集于 2026-09-23 UTC；候选观察，非批准、非冻结；为 §8“基础设施服务器版本”行的候选评审输入，不填入 §8 当前值）：**

| 域 | 候选版本线（观察值，非冻结） | 服务端发行版许可证（观察值，独立于客户端许可） | 镜像发行/使用条款（独立核对对象） | G2 待验证项（适配 / 许可证 / 镜像 digest） | 替代受支持版本与升级决策（裁决归 G2） |
|---|---|---|---|---|---|
| PostgreSQL | 18.6（发布公告 2026-08-13 UTC，同批 17.11 / 16.15 / 15.19 / 14.24 / 19 Beta 3） | PostgreSQL Licence（宽松型；官方 licence 页 2026-09-23 已访问，全文冻结时复核） | 发行渠道条款未核【待验证】；镜像 digest 未采集【待验证】 | pgx/v5 与迁移工具对 PG 18 的支持矩阵；事务内 `SET LOCAL`/RLS/CAS 行为回归；镜像 digest | 同批受支持线 17.x 为备选；18.x 补丁跟进与未来 19 正式线升级规则由 G2 决定 |
| Apache Kafka（生产协议） | 4.3.1（2026-06-25 发布；官方下载页同时列 4.2.1 / 4.1.2 为受支持版本） | Apache-2.0（Apache 软件基金会） | 官方镜像 tag `apache/kafka:4.3.1`、`apache/kafka-native:4.3.1` 已在下载页记载；digest 未采集【待验证】 | 与 §9 三个 Go 客户端候选的 broker 协议兼容矩阵（新旧消费组协议支持差异逐库实测）；镜像 digest | 4.2.1 / 4.1.2 等受支持线为备选；升级节奏由 G2 决定 |
| Redpanda（本地开发兼容实现，§6 Kafka 行） | 26.2.1【待复核：许可总览页不载具体版本号，版本号与发布日期须以官方 release 通道复核】 | 社区版免费、源码可得，BSL 条款（含转 Apache-2.0 的转换条款）；企业版需 license key、含企业功能（企业版以 Redpanda Community License 发行，含 BSL 自由功能 + 企业功能）——**社区版与商用功能条款归属另核** | 镜像/二进制分发条款未核【待验证】；digest 未采集【待验证】 | 与生产 Kafka 4.3.x 的协议对齐；社区/企业功能边界与商用条款核验；本地—生产一致性测试边界；镜像 digest | 改用 Apache Kafka 官方镜像做本地开发（消除兼容实现差异）为备选；版本选择与升级由 G2 决定 |
| Redis | 8.10.1（release 页时间戳 2026-08-17T16:44:04Z） | RSALv2 / SSPLv1 / AGPLv3（官方 licenses 页并列记载）——**不属于 §2 D4 接受集**；合法适用模型待 Tech Lead + Legal/Security 决定 | 镜像分发与使用条款未核【待验证】；digest 未采集【待验证】 | go-redis/v9 对 8.x 的支持矩阵；许可适用模型决定；镜像 digest | BSD 许可的历史版本线（精确版本与支持状态【待复核】）或经批准接受现许可；升级决策归 G2 |
| Elasticsearch | 8.19.21（官方博客 2026-09-02）与 9.5.4（官方博客 2026-09-15）两条线并列观察；“最新补丁”属性不宣称 | ELv2 / SSPL / AGPLv3（官方许可 FAQ 记载）——**不属于 §2 D4 接受集**；合法适用模型待 Tech Lead + Legal/Security 决定 | 镜像分发条款未核【待验证】；digest 未采集【待验证】 | 大版本线选择（8.19.x vs 9.x，直接决定 §6 ES 行客户端 module 线）；官方语言客户端兼容矩阵；许可适用模型；镜像 digest | 8.19 线或 9.x 线由 G2 择一；跨大版本升级路径随线选择一并决定 |
| Qdrant | 1.19.0（release 页时间戳 2026-08-05T11:26:05Z） | Apache-2.0（master 分支 LICENSE 文件 2026-09-23 已核实）；企业功能/商业发行边界未核 | 镜像分发条款未核【待验证】；digest 未采集【待验证】 | `qdrant/go-client` 1.19.x 的 gRPC 契约兼容；镜像 digest；企业功能边界 | 上游受支持的次新版本线（冻结时复核）；升级决策归 G2 |

**服务端候选采集入口（2026-09-23 UTC 访问核实；“已核实”指当日对官方入口的实际访问与内容核对，冻结时必须重访；与 §9 上半部分同纪律）：**

- PostgreSQL 18.6：https://www.postgresql.org/about/news/postgresql-186-1711-1615-1519-1424-and-19-beta-3-released-3365/（已核实：标题确认 18.6/17.11/16.15/15.19/14.24/19 Beta 3 同批发布，页面 Posted on 2026-08-13）；许可页 https://www.postgresql.org/about/licence/（已访问；全文冻结时复核）
- Apache Kafka 4.3.1：https://kafka.apache.org/community/downloads/（已核实：4.3.1 Released June 25, 2026；镜像 tag 与受支持版本列表同上表）
- Redis 8.10.1：https://github.com/redis/redis/releases/tag/8.10.1（已核实：release 页时间戳 2026-08-17T16:44:04Z）；许可页 https://redis.io/legal/licenses/（已核实：RSALv2 / SSPLv1 / AGPLv3）
- Elasticsearch 8.19.21：https://www.elastic.co/blog/elastic-stack-8-19-21-released（已核实：2026-09-02）；9.5.4：https://www.elastic.co/blog/elastic-stack-9-5-4-released（已核实：2026-09-15）；许可 FAQ：https://www.elastic.co/pricing/faq/licensing（已核实：ELv2 / SSPL / AGPLv3）
- Qdrant 1.19.0：https://github.com/qdrant/qdrant/releases/tag/v1.19.0（已核实：release 页时间戳 2026-08-05T11:26:05Z）；服务端 LICENSE（master 分支，2026-09-23 核实）：Apache-2.0
- Redpanda 26.2.1：许可总览 https://docs.redpanda.com/streaming/current/get-started/licensing/overview/（页面内容已核实：社区版免费源码可得、企业版 license key、BSL / Redpanda Community License 与商用功能条款并存；**26.2.1 版本号与发布日期未在该页出现——【待复核】**）

**§9 客户端候选与服务端版本线匹配审查（非批准、非结论；仅为评审输入，G2 冻结时逐项核验）：**

| 服务端候选 | §9 客户端候选（客户端许可证为 §9 观察值） | 初步匹配观察（非结论） | G2 冻结前必须核验 |
|---|---|---|---|
| PostgreSQL 18.6 | `jackc/pgx/v5` v5.11.0（MIT）；备选 `database/sql` + 其他驱动 | 服务端候选为 18.x 稳定线；§6 首选候选方向未变 | pgx 对 PG 18 的官方支持矩阵；`SET LOCAL`/RLS/CAS 行为回归；`pressly/goose` / `golang-migrate` 对 PG 18 的支持（D9 工具域） |
| Kafka 4.3.1（生产）+ Redpanda 26.2.1【待复核】（本地） | `twmb/franz-go` v1.22.0（BSD-3-Clause）、`IBM/sarama` v1.61.0（MIT）、`segmentio/kafka-go` v0.4.51（MIT） | 生产与本地两套服务端线均在 G2 冻结范围（§6 Kafka 行）；各客户端协议版本上限未核验 | 各客户端宣告的最大 broker 协议版本 vs 4.3.x；新/旧消费组协议支持差异；Redpanda 26.2.x 宣告的 Kafka API 兼容版本；三选一仍按 §6 评审，不因本矩阵预决 |
| Redis 8.10.1 | `redis/go-redis/v9` v9.22.0（BSD-2-Clause） | 客户端 v9 大版本线与 Redis 8 服务端的匹配未核验 | go-redis 对 8.x 命令/协议支持矩阵；服务端许可适用模型决定（见下方边界块第 3 条）前不得引入镜像 |
| Elasticsearch 8.19.21 / 9.5.4 | `elastic/go-elasticsearch/v8` v8.19.7（Apache-2.0） | **版本线错位风险**：现有候选客户端为 v8 module 线，仅与 8.19.x 服务端候选对齐；若 G2 选 9.x 服务端线，须按 D4 重新提名 v9 线并同步修订 §6/§9 候选 | 官方语言客户端兼容矩阵；bulk/PIT/alias 在两线的差异；服务端许可适用模型决定前不得引入镜像 |
| Qdrant 1.19.0 | `qdrant/go-client` v1.19.2（Apache-2.0） | 客户端与服务器候选同处 1.19 minor 线（客户端补丁号领先服务器候选 tag，仅作节奏观察，不作结论） | gRPC 契约兼容声明；collection 版本隔离行为；镜像 digest |

**本快照的明确边界（防止误读）：**

1. **非批准**：以上任何行都不构成 G2 批准，不得据此引入依赖、更新 `go.mod` 或拉取镜像；G2 冻结时必须重新核验（快照即刻开始陈旧）；
2. **【待验证】字段**：维护健康度（issue/PR 响应趋势、维护者集中度、发布节奏趋势）、是否依赖 cgo、传递依赖及间接许可证——本快照**未核验**，进入冻结清单前必须按 D4 第 3 条补齐证据；
3. 事实记录不构成维护状态结论：`segmentio/kafka-go` 观察到的最近发布为 2026-04-23（相对其余候选较早）；`google/uuid` 观察到的最近发布为 2024-01-23——仅记录日期，不作"活跃/停滞"判断；
4. **评审倾向（非批准）**：PostgreSQL=`pgx/v5` 方向、Redis=`go-redis/v9` 方向、ES/Qdrant=官方客户端方向为对应域的首选候选；Kafka 客户端三个候选按 §6 标准评审后由 G2 择一；`uuid` 可能可用标准库 `crypto/rand` 自生成替代（Task 2 按 D4 提名时决定）；
5. 观察到的许可证与版本若与上游仓库当前状态冲突，以上游仓库为准。

**服务端候选矩阵的明确边界（防止误读）：**

1. **非批准、非冻结**：服务端矩阵任何行不构成 G2 批准，不得据此拉取镜像、部署服务或填写 §8 当前值；冻结时必须对版本号、日期、许可证文本与镜像 digest 全部重核（本矩阵采集后即刻开始陈旧，与 §9 上半部分同纪律）；
2. **三层许可相互独立**：服务端发行版许可证、Go 客户端库许可证、镜像发行/使用条款是三个独立核对对象；**不得以客户端 MIT/Apache-2.0 推断服务端许可满足 §2 D4 接受集**（D4 约束 Go 依赖准入；服务端许可的合法适用是独立决定，两个结论互不推导）；
3. **不在 D4 接受集的服务端许可须先决**：Redis 8（RSALv2/SSPLv1/AGPLv3）、Elasticsearch（ELv2/SSPL/AGPLv3）、Redpanda（BSL/企业条款，社区版与商用功能条款归属另核）的合法适用发行/部署模型必须由 **Tech Lead + Legal/Security** 决定后方可进入 §8 冻结；决定前不拉取镜像、不部署（与 §12 G2 fail-closed 同口径）；
4. **匹配审查非结论**：客户端↔服务端版本线匹配列为评审输入，不构成兼容性结论或已获批准的法务结论；ES 服务端大版本线（8.19.x vs 9.x）的选择决定客户端 module 线是否须按 D4 重新提名，本 ADR 不预决；
5. **审批状态不变**：本矩阵不改变 §12 任何门禁状态——G1 保持待决；G3 未关闭前仅本地合成测试配置、不部署真实服务；G9 数值评测门禁仍待 Product + ML/Eval + Security + SRE 批准并继续阻断 Task 1。

## 10. CI/Compose 健康检查与 Task 1 测试门禁

**Task 1 命令门禁（实施计划 §7.4/§21，全部必须通过）：**

```bash
go test ./...
go vet ./...
golangci-lint run
```

**Task 1 先写测试（实施计划 §7.4，本 ADR 附加断言以【】标注）：**

- 配置未知字段/非法值/缺少 secret 失败；
- secret 不出现在日志和错误；
- health handler 契约测试【liveness 不含依赖探测（L1）；readiness 按 §5 角色表与 R1–R3】；
- clean shutdown 测试【D8 停机序列】；
- 【依赖树审计：`go list -m all` 输出与 §8 冻结清单一致，无未审批模块】。

**Task 1 验收（实施计划 §7.5）与 Compose 边界：**

- clean checkout 可执行 `make check`；未启动外部依赖时单元测试通过；
- `deploy/compose/docker-compose.yml` 创建但默认不自动启动；包含 PG、Kafka/Redpanda、Redis、Elasticsearch、Qdrant 的 **healthcheck 与持久卷**，测试可按 profile 分批启动；可被 `docker compose config` 验证；
- **G2 关闭前不拉取镜像、不启动任何服务**（实施计划 §23.2 fail-closed 默认）；Compose 文件中的镜像 tag 只能引用 §8 已批准值；
- 无默认生产凭据；没有业务表和记忆逻辑。

**集成测试边界（实施计划 §21，涉及外部依赖时）：**

```bash
docker compose -f deploy/compose/docker-compose.yml up -d <required-services>
go test -tags=integration ./tests/integration/...
docker compose -f deploy/compose/docker-compose.yml down
```

**全局门禁（实施计划 §21）：** 无 unexplained skip；无原始 secret/PII 出现在 Git、日志、fixture；staged 文件与 Task 白名单一致；未执行的 live/生产/多区域验证明确列出。

## 11. 风险与缓解

| 风险 | 缓解 |
|---|---|
| §9 快照被误当作已批准版本基线 | 快照强制标注采集日期与非批准属性（§9.1）；§8 清单空值规则；G2 冻结时强制重核 |
| 候选库停维/许可证变更 | D4 维护证据 + 替代方案留档；CI 依赖树审计使未审批变更可见；`go.mod` 约束版本、`go.sum` 校验哈希 |
| secret/配置值泄漏到日志 | D5 类型化 secret + redaction + Task 1 专项测试（§10） |
| readiness 语义错误导致依赖抖动引发重启风暴 | D6/L1：liveness 结构上不可探测外部依赖；契约测试固化 |
| 迁移工具破坏"只前进"或测试库隔离 | D9 准则 + Task 3 迁移测试门禁（顺序/重放/`*_test` 隔离） |
| Kafka 客户端选型低估（offset 提交、rebalance、背压语义不符） | §6 标准逐条核验 + Task 5 故障测试矩阵（publish/消费崩溃回放）为最终裁判 |
| 框架例外被滥用绕过标准库优先 | D1：例外必须 owner 批准并记录理由；D4 流程留档可审计 |
| 本 ADR 被当作依赖/版本批准 | §12 明示 G1/G2/G3/G4/G6/G9 未批准与 fail-closed 默认；本 ADR 状态为 Proposed |

## 12. 待 owner 批准项与 fail-closed 默认

以下事项**尚未获得 owner 批准**，本 ADR 不为其编造决定；仅登记 owner、最晚关闭点与未关闭时的 fail-closed 默认（与实施计划 §23.2、ADR 0001 §14、ADR 0002 §17、ADR 0003 §13 对应项一致）：

| # | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 |
|---|---|---|---|---|
| G1 | GitHub owner、Go module path、CI 权限模型 | 项目 Owner | Task 1 前 | 不初始化 `go.mod`，不创建远端 workflow |
| G2 | Go 与 PostgreSQL/Kafka（含本地 Redpanda）/Redis/ES/Qdrant 精确版本、镜像、客户端依赖与许可证（本 ADR §8 清单） | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像；本地 Compose 不启动 ES/Qdrant（ADR 0002 §17 同口径） |
| G3 | 部署/数据驻留区域、KMS/secret manager | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据；v1 配置只读环境变量 |
| G4 | tenant/subject identity 签发者、JWT/JWKS 与服务代理授权契约（JWT 库冻结的前置） | Security + API Owner | Task 2 前 | 不开放业务 API；Task 4 前必须实现并验收身份验证、授权及 RLS 上下文 |
| G6 | embedding provider/model/dimension/区域/DPA 与降级（embedding HTTP 客户端配置的前置） | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量 |
| G9 | 评测门禁数值（gold set 分层/样本/一致性；precision@5、recall@10、nDCG@10 绝对下限与相对词法基线改善及置信区间；错误记忆率上限；成对任务差异；删除传播 p95/p99） | Product + ML/Eval + Security + SRE | Task 0 退出前 | **不开始 Task 1**；阈值必须为批准的数字，禁止 `TBD` 或"优于 baseline" |

**明确陈述：**

1. **G9 数值评测门禁仍阻断 Task 1**：在 `docs/evaluation-gates.md` 按 owner 批准的数值关闭前，Task 1 不得开始（实施计划 §23.2、ADR 0001 §14 G9 同口径）；
2. **本 ADR 状态为 Proposed，不构成任何批准**：§2 中"固化"部分以上游文档已确认基线与 Task 1 强制要求为限；§6/§7.5/§9 的准则、候选与倾向均待 G2 评审；G1/G2/G3 关闭前，§8 清单保持空值，且不得初始化 `go.mod`、引入依赖、拉取镜像或创建 workflow；
3. 本 ADR 与 ADR 0001 §14 / ADR 0002 §17 / ADR 0003 §13 的同编号门禁语义一致，任何不一致以上游文档与实施计划 §23.2 为准。

## 13. 与其他 ADR 的关系

- **ADR 0001（canonical 与事件骨干）**：其 §13 约定"ADR 0004：精确版本与库选型——不得引入违反 D1–D8 的依赖"。本 ADR 将其操作化：D4 准入流程把"不违反骨干约束（单一事实源、同事务 outbox、at-least-once+幂等、fence 防复活、RLS、opaque ref）"列为一票否决；§5/§6 的进程与客户端边界是实现其 §5–§10 机制的载体。
- **ADR 0002（search/vector 投影）**：其 §16 约定"ES/Qdrant 客户端库选型、版本与许可证在彼处冻结（G2）；不得引入违反条件幂等与 fence 约束的抽象"。本 ADR §6 两行给出对应职责与标准；投影协议本身（条件幂等、回执、重建切换）不在本 ADR 范围。
- **ADR 0003（confirmed fact policy）**：其 §14 约定"域类型必须能表达 FactStatus/ProposalStatus/reason_code/trust/sensitivity 契约（实施计划 §8.3）；不得引入违反 D1/D3 隔离或 D6 事务边界的依赖"。本 ADR D3/D4 将该反向约束纳入依赖准入；域类型实现归 Task 2。
- 本 ADR 不修改上述任何 ADR 的决策；冲突时以上游文档与本 ADR 权威输入清单为准，须修订本 ADR。

## 14. 后果

**正面：**

- 依赖面最小化且每项依赖可追溯到许可证/维护/替代方案证据，G2 冻结变成逐条打勾而非临场拍板；
- 进程边界与健康语义先行固定，Task 4/5/6/8–12 的验收有统一口径（readiness 不与降级矩阵打架）；
- secret/日志/超时纪律在 Task 1 即有测试门禁，安全属性前移；
- fail-closed 默认保证"文档先行、批准驱动"：owner 未关闭前不存在任何版本、依赖或镜像事实。

**负面/代价：**

- G1/G2/G3/G9 未关闭前 Task 1 完全无法启动（有意为之的阻塞成本）；
- §9 快照需要冻结时重核，产生一次重复劳动；
- 标准库优先在个别场景（如路由中间件、迁移工具）比框架方案多写少量胶水代码；
- 候选 Kafka 客户端的评审（三选一）给 G2 增加一项实质工作量。

**修订规则：** 本 ADR 的任何修改不得违反设计规格不可变原则（§5）、README 核心不变量与 ADR 0001 骨干决策；冲突时先修订上游文档并走 Task 0 评审，再同步本 ADR。G2 批准后，版本/依赖值以修订本 ADR §8 的方式记录，并保持 §9 快照的历史标注不变。
