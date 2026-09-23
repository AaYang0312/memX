# memX 生产级通用记忆服务实施计划

**状态：** Draft for review  
**日期：** 2026-09-20  
**最近更新：** 2026-09-22  
**设计前置：** [生产级通用记忆服务设计规格](../specs/2026-09-20-production-memory-service-design.md)  
**项目根目录：** `D:/Projects/memX`  
**本轮决定：** Go、PostgreSQL + transactional outbox、Kafka、Redis、Elasticsearch、Qdrant；私有 GitHub + GitHub Actions；单区域试点按峰值 50 QPS、10 万主体、100 万 turn/日和 99.9% 月可用性规划；第三方 embedding 仅接收脱敏低敏文本；canonical raw turns 默认 90 天、审计默认 365 天；事实通过通用 API 显式 `confirm` / `revoke`；首版长期用户记忆只读取 confirmed facts。  
**当前动作边界：** 本文件只定义计划，不初始化 Git/源码、不安装依赖、不启动基础设施。

## 1. 交付目标

将 memX 建成独立、可部署、可重放、可审计的多租户记忆服务：

- PostgreSQL 保存 canonical 会话、事实、修订、情景记忆、流程记忆和 outbox；
- Redis 保存可重建 working-memory 投影；
- Kafka 分发可重放事件；
- Elasticsearch 提供词法召回；
- Qdrant 提供向量召回；
- 同步读路径生成有界 MemoryPack；
- 同步写路径只提交来源事件，抽取、索引和压缩异步执行；
- confirmed facts、授权、版本、TTL、删除 fence 和 canonical revalidation 始终优先；
- 所有投影支持幂等、重试、DLQ、watermark 和全量重建。

## 2. 实施原则

1. 每个 Task 一个逻辑提交；未通过本 Task 门禁不得进入下一 Task。
2. 先写失败测试和契约，再写最小实现。
3. PostgreSQL 是唯一 source of truth；禁止应用层直接双写 PG 与 ES/Qdrant。
4. 原始 turn append 与 outbox 必须同事务；派生记忆全部异步。
5. Redis、ES、Qdrant 删除或更新必须由事件驱动，且可从 PG 重建。
6. 消费语义为 at-least-once；通过 event id、aggregate seq、revision、deletion epoch 实现 effectively-once。
7. 模型抽取只能生成 proposal；只有白名单确定性规则、可信服务端事件或显式人工确认可以生成 confirmed fact。
8. 任何检索命中进入 MemoryPack 前必须通过 canonical 权限、版本、状态和 TTL 复核。
9. 不在日志、指标、trace、Kafka payload、Redis key 中写原始用户标识或敏感值。
10. 第一版不接 Neo4j，不接 BI Agent，不做跨区域部署。

## 3. 建议项目结构

```text
memX/
├─ README.md
├─ go.mod
├─ go.sum
├─ Makefile
├─ .gitignore
├─ .golangci.yml
├─ cmd/
│  ├─ memx-api/
│  │  └─ main.go
│  ├─ memx-outbox-relay/
│  │  └─ main.go
│  └─ memx-worker/
│     └─ main.go
├─ api/
│  ├─ openapi.yaml
│  └─ examples/
├─ contracts/
│  ├─ events/
│  ├─ memory-pack/
│  └─ jsonschema/
├─ internal/
│  ├─ app/
│  ├─ auth/
│  ├─ config/
│  ├─ domain/
│  │  ├─ conversation/
│  │  ├─ fact/
│  │  ├─ episode/
│  │  ├─ procedure/
│  │  └─ memorypack/
│  ├─ ports/
│  ├─ service/
│  │  ├─ append/
│  │  ├─ assemble/
│  │  ├─ extraction/
│  │  ├─ retention/
│  │  └─ deletion/
│  ├─ storage/
│  │  ├─ postgres/
│  │  ├─ redis/
│  │  ├─ elasticsearch/
│  │  └─ qdrant/
│  ├─ messaging/
│  │  └─ kafka/
│  ├─ projection/
│  ├─ policy/
│  ├─ telemetry/
│  └─ httpapi/
├─ migrations/
├─ deploy/
│  ├─ compose/
│  ├─ kubernetes/
│  └─ dashboards/
├─ tests/
│  ├─ contract/
│  ├─ integration/
│  ├─ replay/
│  ├─ security/
│  ├─ performance/
│  └─ fixtures/
└─ docs/
   ├─ specs/
   ├─ plans/
   ├─ adr/
   └─ runbooks/
```

## 4. 技术栈基线

实施 Task 1 时冻结精确版本并写 ADR；本计划先固定组件职责：

- Go：服务、worker 与 CLI；
- PostgreSQL：canonical 与事务 outbox；
- Kafka：事件日志、consumer group 与重放；本地可用 Kafka-compatible Redpanda，但生产协议按 Kafka；
- Redis：working-memory projection；
- Elasticsearch：BM25、过滤与词法候选；
- Qdrant：独立向量投影；
- OpenAPI + JSON Schema：HTTP 和事件契约；
- OpenTelemetry：trace/metric；
- Docker Compose：本地集成环境；
- Kubernetes manifests/Helm 是否采用在生产化阶段再确认。

Go 依赖优先级：标准库 > 小型成熟库 > 框架。候选库必须在 Task 1 ADR 中记录许可证、维护状态和替代方案。

## 5. 总体依赖链

```text
Task 0 契约冻结
  -> Task 1 项目骨架
  -> Task 2 核心领域契约
  -> Task 3 PostgreSQL canonical schema
  -> Task 4 Canonical API
  -> Task 5 Outbox + Kafka
  -> Task 6 Redis Working Memory
  -> Task 7 Facts / Proposal / Conflict
  -> Task 8 Async Extractor
  -> Task 9 Elasticsearch Projection + 删除处理
  -> Task 10 Qdrant Projection + 删除处理
  -> Task 11 MemoryPack Assemble + 删除过滤
  -> Task 12 TTL / 删除编排 / Export
  -> Task 13 Security / Observability / SLO
  -> Task 14 Replay / Shadow / Release
```

Task 9 与 Task 10 可在 Task 8 后并行开发，但最终集成和验收必须串行，避免同一测试环境共享状态冲突。

## 6. Task 0：冻结契约、容量假设与威胁模型

### 6.1 目标

在写代码前关闭会影响数据模型和 SLO 的关键问题。

### 6.2 文件

- 更新：`docs/specs/2026-09-20-production-memory-service-design.md`
- 新增：`docs/adr/0001-canonical-and-event-backbone.md`
- 新增：`docs/adr/0002-search-and-vector-projections.md`
- 新增：`docs/adr/0003-confirmed-fact-policy.md`
- 新增：`docs/adr/0004-go-service-stack.md`
- 新增：`docs/threat-model.md`
- 新增：`docs/capacity-baseline.md`
- 新增：`docs/data-classification.md`
- 新增：`docs/evaluation-gates.md`（经 owner 批准的指标、数据集和数值门禁）

### 6.3 决策清单

已冻结的规划基线：

- 私有 GitHub 仓库 + GitHub Actions；
- 单区域试点，不在 v1 承诺跨区域部署；
- 峰值 50 QPS、10 万主体、100 万 turn/日、服务月可用性 99.9%；
- 第三方 embedding 只允许处理确定性脱敏后的低敏文本，中高敏内容禁止外发；
- canonical raw turns 默认保留 90 天，审计默认保留 365 天；
- 通用 API 显式 `confirm` / `revoke`，设置页、审核台和具体应用 adapter 后置。

Task 0 仍需把以下项写入 ADR、容量基线或数据分类文档：

- GitHub owner 与 Go module path；
- Go 与依赖精确版本；
- Kafka、Elasticsearch、Qdrant、Redis、PostgreSQL 的开发和生产版本；
- 平均/峰值消息长度、租户分布、增长余量、单租户配额和总容量；
- 实际部署/数据驻留区域、密钥管理方式和 Qdrant 拓扑；
- embedding provider/model、dimension、处理区域、DPA/不训练条款与禁用时降级；
- confirmed fact namespace/key 白名单；
- tenant/subject identity 签发者与 JWT/JWKS 契约；
- 法务保留、诉讼保全、备份物理清除和 deletion SLO 例外；
- latency/error budget、成本上限与 MemoryPack token budget；
- 评测门禁：离线 gold set 按租户/domain/时间分层、最小样本数及标注一致性；precision@5、recall@10、nDCG@10 绝对下限和相对词法 baseline 的最小改善幅度与置信区间；错误记忆率分母/上限；成对无记忆任务成功率和 Tool 正确率差异；各来源 deadline、token/成本与删除传播 p95/p99。阈值须批准为数字，禁止使用 `TBD` 或“优于 baseline”作为通过条件。

### 6.4 验收

- §23 decision ledger 中所有未决项都有 owner、最晚关闭阶段和默认 fail-closed 决策；
- Task 1 的阻塞项全部关闭；允许后置的决策必须写明最晚关闭 Task，且在此之前相关能力保持禁用；
- threat model 覆盖跨租户、Prompt 注入、迟到事件复活、索引泄露、embedding 外发和删除不完整；
- capacity baseline 能推导分区、连接池、索引和 SLO 初值；
- `docs/evaluation-gates.md` 有 Product + ML/Eval + Security + SRE 批准的数值、数据集版本、样本量/统计方法和 fail-closed 规则；证据不足不能算通过；
- spec 状态从 `Draft for discussion` 改为 `Approved for implementation` 后才能启动 Task 1。

### 6.5 提交

`docs: approve memX architecture contracts`

## 7. Task 1：初始化 Go 项目和开发门禁

### 7.1 目标

创建最小可编译项目，不实现业务行为。

### 7.2 文件

- 创建项目结构 §3；
- 创建 `go.mod`、`Makefile`、`.gitignore`、`.golangci.yml`；
- 创建 `cmd/memx-api/main.go` 健康检查入口；
- 创建 `internal/config` 严格环境变量解析；
- 创建 `deploy/compose/docker-compose.yml`，但默认不自动启动；
- 创建 CI workflow；
- 创建 `README.md`。

### 7.3 实现要求

- 配置缺失必须启动失败；
- secret 类型不得实现明文 `String()`；
- 健康检查区分 liveness 与 readiness；
- readiness 在依赖未配置时明确失败；
- 日志默认为结构化且不输出配置值；
- 依赖版本全部锁定；
- 本地 Compose 包含 PG、Kafka/Redpanda、Redis、Elasticsearch、Qdrant 的健康检查和持久卷，但测试可按 profile 分批启动。

### 7.4 测试

先写：

- 配置未知字段/非法值/缺少 secret 失败测试；
- secret 不出现在日志和错误测试；
- health handler 契约测试；
- clean shutdown 测试。

命令：

```bash
go test ./...
go vet ./...
golangci-lint run
```

### 7.5 验收

- clean checkout 可执行 `make check`；
- 未启动外部依赖时单元测试通过；
- Compose 配置可被 `docker compose config` 验证；
- 无默认生产凭据；
- 没有业务表和记忆逻辑。

### 7.6 提交

`chore: bootstrap memx go service`

## 8. Task 2：定义领域模型、错误码与公开契约

### 8.1 目标

先冻结内存内领域契约和序列化形状。

### 8.2 文件

- `internal/domain/ids.go`
- `internal/domain/conversation/*`
- `internal/domain/fact/*`
- `internal/domain/episode/*`
- `internal/domain/procedure/*`
- `internal/domain/memorypack/*`
- `internal/policy/trust.go`
- `internal/policy/sensitivity.go`
- `internal/auth/claims.go`（身份与授权上下文契约，不接外部身份源）
- `api/openapi.yaml`
- `contracts/events/*.schema.json`
- `contracts/memory-pack/v1.schema.json`
- `internal/httpapi/errors.go`
- `tests/contract/*`

### 8.3 契约

实现：

- opaque ref 强类型；
- aggregate sequence、revision、deletion epoch；
- TrustLevel、SensitivityClass、FactStatus、ProposalStatus；
- MemoryPack v1；
- EventEnvelope v1；
- 稳定错误码，不含底层异常和输入值；
- validation reason code；
- schema version 与向后兼容规则；
- 租户/主体身份的签发者、audience、作用域、代表用户的服务调用和拒绝规则契约；不得把客户端 header/body 当作身份来源。

### 8.4 失败测试

- 原始邮箱/手机号不能作为 Redis key 或公开 ref；
- 未确认 proposal 不能构造成 confirmed fact；
- 当前请求优先级低于系统策略但高于历史事实；
- 非法状态迁移被拒绝；
- unknown JSON fields 按公开 API 策略拒绝；
- event id、aggregate ref、seq 缺失时拒绝；
- MemoryPack 超预算时按低信任顺序裁剪。

### 8.5 验收

- OpenAPI、JSON Schema 与 Go 类型有契约测试；
- 示例 payload 可 round-trip；
- 错误响应只含稳定 code/message/trace ref；
- 不访问数据库和外部服务。

### 8.6 提交

`feat: define memx domain contracts`

## 9. Task 3：PostgreSQL canonical schema 与 repository

### 9.1 目标

建立可审计、版本化、租户隔离的 canonical 数据层。

### 9.2 迁移

建议顺序：

- `001_roles_and_schemas.sql`
- `002_conversations_turns_and_summaries.sql`
- `003_facts_and_revisions.sql`
- `004_proposals.sql`
- `005_episodes_and_procedures.sql`
- `006_outbox_and_consumer_inbox.sql`
- `007_projection_watermarks.sql`
- `008_retention_deletion_fences_and_receipts.sql`
- `009_idempotency_requests.sql`

### 9.3 Repository

- `internal/storage/postgres/conversation_repository.go`
- `internal/storage/postgres/summary_repository.go`
- `internal/storage/postgres/fact_repository.go`
- `internal/storage/postgres/proposal_repository.go`
- `internal/storage/postgres/episode_repository.go`
- `internal/storage/postgres/procedure_repository.go`
- `internal/storage/postgres/outbox_repository.go`
- `internal/storage/postgres/inbox_repository.go`
- `internal/storage/postgres/watermark_repository.go`
- `internal/storage/postgres/idempotency_repository.go`

### 9.4 强制约束

- tenant + subject 所有权；
- conversation seq 单调递增；PG canonical summary 的 from/to seq 连续覆盖、revision 不覆盖、checkpoint CAS + summary outbox 原子提交；
- active confirmed fact 唯一；
- revision CAS；
- revision/event 只追加；
- 幂等账本以 tenant、已验证 actor、operation、target、key 摘要唯一约束；请求使用 keyed digest，只存安全响应结果/引用，和 canonical 更新及 outbox 同事务提交；
- outbox 与 canonical 更新同事务；
- consumer inbox pending lease/applied 状态和幂等；每分区 watermark 只计连续成功前缀，DLQ 缺口不得越过；
- 持久主体 deletion fence（tenant + subject + epoch）及可查询的投影删除回执/核对状态；fence 生命周期覆盖重放、重建和备份恢复；
- deletion epoch 阻止旧事件复活；
- JSON 递归敏感键约束；
- service role 最小权限与 RLS；
- migration 幂等或有明确不可重放边界；
- 时间统一 UTC。

### 9.5 测试

使用独立本机 `memx_test` 数据库：

- 从 001 到最新顺序迁移；
- 空库迁移和升级迁移；
- 允许的 migration 重放；
- 跨租户读写拒绝；
- 并发 append 只有合法 seq 成功；summary checkpoint 缺口/并发更新拒绝，提交摘要与 outbox 原子；
- 并发 fact update 只有一个 CAS 成功；
- outbox、canonical 与幂等结果三者原子性；并发同 key 只能有一次副作用，回滚不占用 key；
- 事件/revision 不可更新删除；
- 删除 fence 拒绝旧 epoch；
- application role 无 DDL 和越权表权限。

### 9.6 验收

- repository 不泄露 SQL、DSN 或原始错误；
- 失败事务不留下孤立 outbox；
- 每个表有必要索引与查询计划基线；
- 测试只连接 `*_test` 数据库。

### 9.7 提交

`feat: add canonical memory persistence`

## 10. Task 4：实现 Canonical Append、Read 与 Fact Command API

### 10.1 目标

交付不依赖 Kafka、Redis、ES、Qdrant 的最小可用 canonical service。

### 10.2 API

- `POST /v1/conversations/{ref}/turns`
- `GET /v1/conversations/{ref}/turns`
- `GET /v1/subjects/{ref}/facts`
- `POST /v1/facts/{ref}:revoke`（仅现存 confirmed fact；此阶段按保守策略实施）

proposal 确认/拒绝及无 proposal 的显式事实确认接口在 Task 7 实施，不以 fact_ref 代表尚不存在的 proposal。
- `DELETE /v1/subjects/{ref}/memory`（此阶段仅原子建立 fence、标记主体不可读/写并发出 tombstone，返回 pending job；完整编排在 Task 12）

### 10.3 实现要求

- 在开放任何业务 API 前实现 `internal/auth/*` 的最小可用 JWT/JWKS 或受信服务身份验证：校验签名、issuer、audience、有效期与 scope；身份/授权未知时拒绝请求；
- tenant 与 subject 由已验证的身份/代理授权导出；请求路径中的 subject 必须逐次校验所有权；客户端 header/body 不能覆盖身份；
- PostgreSQL 连接池中的每笔事务使用 `SET LOCAL` 注入已验证 tenant/subject 上下文并以 RLS 约束；缺失上下文拒绝访问，事务结束不得串租户；
- append、revoke、删除等可重试命令使用持久 Idempotency-Key；按 tenant/actor/operation/target 范围查账本，命中同请求 digest 优先返回原安全结果（先复核当前授权与删除 fence），不同 digest 返回 409；
- 同 key 并发串行化；相同请求在成功 commit 但 HTTP 响应丢失后仍返回原 seq/job_ref，不因 expected seq 已前进误报 409；账本保留期至少覆盖对外重试窗口；
- 事务提交 turn + conversation head + outbox + 幂等结果；
- confirmed fact read 只返回 active、未过期、授权匹配且未被主体 deletion fence 屏蔽的记录；
- 同一事务提交主体不可读/写状态、递增 epoch 与删除 outbox；Task 12 完成前不得报告在线删除完成，更不得声称备份物理清除；
- proposal 永不出现在 facts read；
- 请求限制、body 上限和 deadline；
- 连接取消向下传播。

### 10.4 测试

- duplicate request replay、同 key 不同 payload/target/scope、同 key 并发、提交成功但 HTTP 响应丢失后重试及账本过期策略；
- stale expected seq；
- cross-tenant/subject、IDOR、伪造 tenant header 与跨请求连接复用；
- JWT/JWKS 无效签名、错误 issuer/audience、过期、失效密钥、缺少或越权 scope、服务身份代理授权失败；
- RLS 上下文缺失时拒绝，事务回滚/连接复用后不残留租户身份；
- body oversize；
- timeout/cancel；
- commit 前失败无残留；
- current confirmed fact 读取；
- expired/revoked/deleted 不可见；删除请求提交后即使投影滞后，canonical read 和新 append 均拒绝该主体；
- revoke 命令 revision conflict；确认/拒绝的完整冲突测试在 Task 7。

### 10.5 验收

- API 在所有补充基础设施关闭时可工作；未完成身份与 RLS 验证前不得向非测试流量开放；
- append/read p95 基线被记录；
- HTTP 错误与数据库错误分离；
- 完整审计链可通过 opaque ref 查询。

### 10.6 提交

`feat: expose canonical memory api`

## 11. Task 5：Transactional Outbox Relay 与 Kafka 消费框架

### 11.1 目标

可靠发布 canonical 事件，并提供所有投影复用的幂等消费框架。

### 11.2 文件

- `cmd/memx-outbox-relay/main.go`
- `internal/messaging/kafka/producer.go`
- `internal/messaging/kafka/consumer.go`
- `internal/service/outbox/relay.go`
- `internal/service/consumer/handler.go`
- `internal/storage/postgres/inbox_repository.go`
- `docs/runbooks/outbox-backlog.md`
- `docs/runbooks/kafka-replay.md`

### 11.3 实现要求

- outbox 批量锁取，避免多个 relay 重复占用；
- Kafka key=`tenant_ref + aggregate_ref`；
- producer ack 配置满足 durable 要求；
- publish 成功后标记 published；
- publish 成功但 DB 标记失败时允许重复发布；
- consumer 通过 inbox 去重，并以当前主体 fence 拒绝旧 epoch；跨聚合根/分区不假设有序；
- 对 PG 内的副作用，业务变更 + inbox applied 同事务提交后才提交 offset；对 Redis/ES/Qdrant 等外部副作用，先持久占用 inbox pending lease，再做 epoch/revision 条件幂等写，确认外部成功后记 applied，最后提交 offset；pending 超时恢复、外部写已成功但 applied 失败时可重试，不允许先标 applied；
- 每分区 watermark 只推进连续已成功的 offset；隔离/DLQ 的缺口保持可见，修复并重放前不得作为 fresh 或删除完成证明；
- schema version 不支持时进入隔离队列；
- 可重试与永久错误分类；
- DLQ 不包含敏感 payload；
- backpressure 和优雅停机。

### 11.4 故障测试

- publish 前崩溃；
- publish 后、mark 前崩溃；
- consumer 处理前/事务中/事务后崩溃；外部投影写前、写后但 inbox applied 前、applied 后但 offset 前崩溃并回放；
- inbox pending lease 超时、DLQ 缺口下 watermark 不前进；
- 重复事件；
- 同 aggregate 乱序；
- Kafka 短暂不可用；
- poison event；
- backlog 恢复。

### 11.5 验收

- 重放不会重复产生 canonical side effect；
- 同聚合根顺序可验证；
- outbox backlog、retry、DLQ、inbox pending 和连续 watermark 缺口有指标和 runbook；
- Kafka 不可用时 canonical API 继续提交，超过阈值 readiness/告警按 ADR 执行。

### 11.6 提交

`feat: publish memory events through outbox`

## 12. Task 6：Redis Working Memory 与 PG 回建

### 12.1 目标

提供低延迟最近回合、结构化摘要和 active slots，同时保持 Redis 可丢弃、可重建。

### 12.2 文件

- `internal/storage/redis/working_memory.go`
- `internal/projection/redis/consumer.go`
- `internal/service/assemble/working_memory.go`
- `internal/service/summary/checkpoint.go`
- `internal/service/summary/worker.go`（异步生成 PG canonical summary）
- `docs/runbooks/redis-rebuild.md`

### 12.3 数据形状

实现 spec §9 的：

- head seq；
- checkpoint seq；
- typed summary（canonical summary_ref/revision、覆盖区间、有效期）；
- recent turns；
- active slots；
- pending clarifications；
- artifact refs；
- projection version；
- expires at。

### 12.4 实现要求

- key 只用 hash/opaque refs；
- Redis 更新由 turn/summary/tombstone 事件驱动；主体删除时清空相关 key、持久记录投影回执；旧事件/重建必须查当前 fence；
- read 时比较 canonical head、checkpoint 与 summary_ref/revision/有效期；
- 异步生成脱敏、schema 有效的 PG summary；CAS 提交 summary + checkpoint + outbox，失效/过期事件使 Redis 摘要失效；
- 落后时 PG 增量回补；
- 超前或非法缓存丢弃；
- Redis miss/flush 后仅从仍有效 PG summary 和仍保留 raw turns 重建，缺失区间显式降级，不声称恢复已物理清除原文；
- 最近回合同时受数量和 token/字符预算；
- summary checkpoint 连续覆盖才可压缩；
- ACK 不触发删除；TTL 与 checkpoint 分离。

### 12.5 测试

- Redis hit；
- miss rebuild；
- stale rebuild；
- impossible future seq；
- partial cache write；
- flushall 恢复；
- 主体删除后 Redis 不返回旧会话、迟到 turn 不复活、删除回执可查询；
- TTL；
- checkpoint gap、并发 CAS、summary revision 失效/过期与 Redis 同 head 但旧 checkpoint；
- raw turn 90 天清除后仍可从保留的 PG summary 回建；summary 也过期时只返回可保留区间并标缺口；
- summary 非法；
- 当前 turn read-your-writes；
- 不继承一次性敏感槽位。

### 12.6 验收

- Redis 全量丢失不丢仍在保留期的 PG canonical 信息；已过保留期的原文不在回建承诺内；
- working memory p95 与 rebuild p95 有基线；
- 未经 PG 验证的 Redis 内容不进入 MemoryPack；
- 压缩前后关键结构状态一致。

### 12.7 提交

`feat: project working memory to redis`

## 13. Task 7：Confirmed Facts、Proposal 与冲突状态机

### 13.1 目标

实现长期用户事实的准入、确认、替换、撤销和冲突处理。

### 13.2 文件

- `internal/service/fact/commands.go`
- `internal/service/fact/policy.go`
- `internal/service/fact/conflict.go`
- `internal/policy/namespaces.go`
- `contracts/jsonschema/fact-values/*`
- `tests/fixtures/fact-policy/*`
- `internal/httpapi/proposals.go`、`internal/httpapi/fact_confirm.go`

### 13.3 状态机

- proposal：`pending -> confirmed | rejected | expired`，`pending_conflict` 是 pending 的 reason_code，不是状态；
- fact：`confirmed -> superseded | revoked | deleted`；到期转 `revoked(reason_code=expired)`；
- terminal 状态不可回到 confirmed；重新启用创建新 fact/revision 链。

### 13.4 准入要求

- namespace/key 白名单；
- typed value schema；
- sensitivity policy；
- provenance 完整；
- 无未解决冲突；
- 一次性值 denylist；
- 模型来源不能直接 confirmed；
- trusted server event 需要签名/身份和允许的 source kind；
- `GET /v1/subjects/{ref}/proposals` 仅返回本人/授权审核者可见的未过期 pending proposal；`POST /v1/proposals/{ref}:confirm` 和 `POST /v1/proposals/{ref}:reject` 使用 proposal_ref 与 expected status/version；
- `POST /v1/subjects/{ref}/facts:confirm` 允许无 proposal 的显式 typed value 确认，需 expected active fact_ref+revision 或 expected absence；替换时事务内锁定 active key，supersede 旧 fact 并更新 revision/outbox；
- 用户显式确认/revoke 命令均需身份授权、Idempotency-Key、类型与敏感度校验；拒绝 proposal 不隐式撤销 fact；模型来源只能在用户/授权审核者明确确认后升级。

### 13.5 测试

- same-value refresh；
- explicit new value supersede；
- ambiguous conflict proposal(status=pending, reason_code=pending_conflict)；
- proposal_ref 与 fact_ref 不混用，列表权限、过期/拒绝后确认失败、直接确认 expected absence/旧 fact revision 409；
- stale revision；
- duplicate confirm（包括 HTTP 响应丢失后的重试：同一 Idempotency-Key 相同结果、不同 payload 409）；拒绝 proposal 不撤销旧 fact；
- model proposal cannot auto-confirm；
- forbidden temporary values；
- TTL/revalidate；
- revoke/delete invisibility；
- full revision audit。

### 13.6 验收

- read path 永远只看到 confirmed；
- 每个状态变化恰好一条 revision/event；
- 冲突不静默覆盖；
- reason code 和 metrics 不泄露值。

### 13.7 提交

`feat: enforce confirmed fact lifecycle`

## 14. Task 8：异步高精度提取与 Proposal Worker

### 14.1 目标

从 turn 事件异步提取候选事实，不阻塞对话主路径。

### 14.2 文件

- `internal/service/extraction/worker.go`
- `internal/service/extraction/rules.go`
- `internal/service/extraction/model_adapter.go`
- `internal/service/extraction/sanitize.go`
- `internal/service/extraction/deduplicate.go`
- `internal/policy/auto_confirm.go`
- `tests/fixtures/extraction-gold.jsonl`

### 14.3 两级提取

1. deterministic extractor：白名单字段、语法和枚举；
2. optional model extractor：只产生 proposal，输出严格 schema。

### 14.4 实现要求

- worker 通过 turn ref 从 PG 取内容，Kafka 不携带正文；
- PII/secret 扫描在模型调用和持久化前执行；
- 模型调用有 deadline、预算和熔断；
- 输出严格 JSON schema；
- dedupe 使用 namespace/key/value hash/evidence；
- 自动确认仅限 ADR 白名单；
- extraction version 与 model version 入 provenance；
- 无法判断时不写；
- 不保存隐藏推理。

### 14.5 测试

- gold set precision/recall；
- adversarial prompt injection；
- schema invalid output；
- timeout；
- duplicate event；
- current temporary value；
- contradiction；
- PII/secret；
- model unavailable；
- replay determinism（规则层）；
- model version change creates re-evaluation path but不静默改 confirmed。

### 14.6 验收

- 主 append 延迟不包含提取；
- 模型关闭时规则提取仍可工作；
- model proposal 进入 Prompt 数量恒为 0；
- extraction lag、失败和 DLQ 可观测。

### 14.7 提交

`feat: extract memory proposals asynchronously`

## 15. Task 9：Elasticsearch 词法投影

### 15.1 目标

建立 BM25、短语、前缀和 metadata filter 的可重建词法投影。

### 15.2 文件

- `internal/storage/elasticsearch/client.go`
- `internal/projection/elasticsearch/indexer.go`
- `internal/service/retrieval/lexical.go`
- `deploy/elasticsearch/index-template.json`
- `docs/runbooks/elasticsearch-reindex.md`

### 15.3 实现要求

- versioned index + alias；
- tenant/scope/authorization/status/TTL/canonical version metadata；
- 只索引允许的脱敏字段；
- tombstone 删除并持久回执/清空核对；删除后的迟到 upsert 和 alias 重建必须再次校验主体 fence，不能只依赖索引里已被物理删除的版本；
- external versioning 或应用层 revision guard；
- bulk index backpressure；
- point-in-time/分页策略；
- PG 一致性快照记录 outbox barrier；构建新 index 后增量追赶至切换 barrier，复核 fence、删除回执和数据，再原子切换 alias；重建期间新旧索引均处理删除；
- 查询硬上限和 timeout；
- analyzer 选择写 ADR。

### 15.4 测试

- cross-tenant filter；
- expired/revoked filter；
- stale revision rejected；
- duplicate/乱序 event；
- tombstone、删除后迟到 upsert（含写前 fence 检查与删除竞态）、含删除的 alias rebuild 与删除回执；
- analyzer gold set；
- snapshot/barrier 追赶、重建时删除、alias 切换；
- ES outage degradation；
- no forbidden fields in indexed document。

### 15.5 验收

- 任意命中进入 MemoryPack 前仍需 canonical revalidation；
- 索引可由 PG 全量重建；
- 删除和 projection watermark 可证明；
- BM25 precision@k/recall@k 记录为 baseline。

### 15.6 提交

`feat: add elasticsearch lexical projection`

## 16. Task 10：Qdrant 向量投影

### 16.1 目标

建立独立、版本化、可重建的语义召回投影。

### 16.2 文件

- `internal/storage/qdrant/client.go`
- `internal/projection/qdrant/indexer.go`
- `internal/service/embedding/provider.go`
- `internal/service/embedding/chunker.go`
- `internal/service/retrieval/vector.go`
- `docs/runbooks/qdrant-reindex.md`

### 16.3 实现要求

- collection 按 embedding model/dimension/version 隔离；
- payload 含 tenant/scope/auth/status/TTL/canonical version/content hash；
- embedding 前脱敏和 sensitivity gate；
- 高敏内容禁止外发；
- chunker/version 入 provenance；
- upsert 幂等；
- tombstone 删除并持久回执/清空核对；删除后的迟到 upsert 与重建须查主体 fence；
- 新模型使用新 collection；PG 快照记录 outbox barrier，完成 backfill、增量追赶至切换 barrier、fence/删除核对后切换 active alias/config；重建期间新旧 collection 均处理删除；
- topK、filter 和 timeout 有硬限制；
- embedding 请求不记录原文。

### 16.4 测试

- metadata filter；
- embedding dimension mismatch；
- provider timeout；
- duplicate/乱序；
- stale version；
- tombstone、删除后迟到 upsert（含写前 fence 检查与删除竞态）、含删除的 collection migration 与删除回执；
- snapshot/barrier 追赶、重建时删除、collection migration；
- Qdrant outage；
- sensitive content blocked；
- vector gold set recall@k。

### 16.5 验收

- Qdrant 不是事实源；
- 所有命中 canonical revalidation；
- 可从 PG + embedding config 重建；
- 不同 embedding 版本不可混排。

### 16.6 提交

`feat: add qdrant vector projection`

## 17. Task 11：MemoryPack 同步组装与混合排序

### 17.1 目标

交付应用调用模型前使用的核心同步读接口。

### 17.2 文件

- `internal/service/assemble/service.go`
- `internal/service/assemble/budget.go`
- `internal/service/assemble/conflicts.go`
- `internal/service/retrieval/fusion.go`
- `internal/service/retrieval/revalidate.go`
- `internal/httpapi/assemble.go`
- `tests/fixtures/retrieval-gold.jsonl`

### 17.3 读取顺序

1. auth context；
2. 当前请求 deterministic extraction；
3. working memory；
4. PG confirmed facts；
5. approved procedures；
6. ES 与 Qdrant 并行查询；
7. RRF/固定权重融合；
8. PG 批量 canonical revalidation；
9. conflict/dedupe；
10. trust + relevance + recency 排序；
11. token budget 裁剪；
12. 返回 MemoryPack + watermark/degraded sources。

### 17.4 实现要求

- 整体和每来源 deadline；
- 当前请求覆盖历史事实；
- system policy 不由 MemoryPack 提供；
- 搜索内容作为 data，不是 instruction；
- 单来源故障隔离；
- PG 不可用时不使用任何无法复核的历史缓存或搜索命中；身份仍可验证且策略允许时仅返回 request-only 并标 degraded，`fresh` 返回不可用；
- `fresh` 取得请求开始时的 PG barrier，投影未追上则失败或只在客户端明确允许时排除来源；`bounded_stale` 用服务端最大 lag 并始终 PG revalidate；`cache_preferred` 不能绕过 PG 复核；返回实际 lag/watermark；
- token 估算器版本化；
- topK 和最终项数硬上限；
- explain trace 只含 opaque refs/reason codes；
- consistency preference：fresh/bounded-stale/cache-preferred。

### 17.5 测试

- priority matrix；
- conflict matrix；
- token budget；
- duplicate lexical/vector hit；
- stale hit revalidation；
- revoked/deleted/expired；主体删除中也不得返回旧会话、事实或检索结果；
- current request override；
- ES only/Qdrant only/both down；
- PG fact read down：`fresh` 失败，允许的 request-only 模式无历史记忆项；
- fresh barrier 未达、bounded_stale 超限、cache_preferred 不绕过 PG、授权不可验证时拒绝；
- deadline；
- Prompt injection payload；
- cross-tenant；
- deterministic rerank ties；
- watermarks and degraded sources。

### 17.6 验收

- offline retrieval 的 precision@5、recall@10、nDCG@10 达到 Task 0 批准的绝对门槛和相对词法基线改善；无记忆 baseline 仅用于端到端成对任务评测，不作为 retrieval 分母；
- 错误记忆率低于批准的数值上限，样本和统计要求未达到视为未通过；
- `memory:assemble` p95 达 spec 初始目标或有批准例外；
- 未确认 proposal、敏感内容、跨租户内容进入 MemoryPack 数量为 0。

### 17.7 提交

`feat: assemble bounded memory packs`

## 18. Task 12：TTL、归档、导出与删除传播

### 18.1 目标

在 Task 3–11 已交付的 fence、各投影删除处理和读取过滤基础上，完成分层 TTL、用户导出、删除编排、回执核对及备份/法务例外；不得把防复活延后到此 Task 才首次实施。

### 18.2 文件

- `internal/service/retention/scheduler.go`
- `internal/service/deletion/orchestrator.go`
- `internal/service/export/service.go`
- `internal/projection/*/delete_handler.go`
- `internal/httpapi/deletion.go`
- `internal/httpapi/export.go`
- `docs/runbooks/subject-deletion.md`

### 18.3 实现要求

- TTL policy 按类别/namespace；raw turn 与 PG summary 分别按保留期清理，清理/撤销摘要发失效事件；
- scheduler 使用可重复扫描与锁；
- 到期 proposal 转 `expired`，到期 confirmed fact 转 `revoked(reason_code=expired)` 并写 tombstone；主体删除沿用 Task 4 原子 fence/不可读状态，不在本 Task 才首次实施；
- 每个投影对删除事件报告持久回执和该主体的空索引/缓存核对；连续 watermark 越过事件仅是必要条件，DLQ 缺口不得视作完成；
- 迟到事件不能复活；重放与备份恢复前校验/恢复 fence；
- 导出只返回授权主体自身的安全可读记录；
- 删除作业可查询；
- 备份与法务保留例外写明。

### 18.4 测试

- TTL expiry；
- retry；
- partial projection failure；
- late event resurrection；
- repeated delete；
- export/delete race；
- subject ownership；
- tombstone retention；
- all watermarks contiguous、无 DLQ 缺口、每投影回执与空索引核对完成；备份仍保留时在线完成与物理清除状态分开展示。

### 18.5 验收

- 在线投影删除满足目标 SLO；
- 全部在线投影回执和核对完成前 API 不返回 online_completed；备份/法务保留不作为在线完成证明，也不得被误报为物理清除完成；
- 删除后搜索与 assemble 不返回记录；
- replay 不复活删除主体。

### 18.6 提交

`feat: enforce memory retention and deletion`

## 19. Task 13：安全、可观测性与运行门禁

### 19.1 目标

补齐生产运行所需的安全和 SRE 能力。

### 19.2 文件

- `internal/auth/*`（对 Task 4 基线的加固）
- `internal/telemetry/*`
- `deploy/dashboards/*`
- `docs/runbooks/*`
- `tests/security/*`
- `tests/performance/*`

### 19.3 能力

- 对 Task 4 已验收的身份/授权增加密钥轮换、撤销、缓存失效和多环境运维演练，不在此阶段首次引入验证；
- rate limit、quota、body limit；
- circuit breaker 和 bulkhead；
- OpenTelemetry；
- safe logs；
- SLO dashboard；
- outbox、consumer lag、watermark、DLQ、rebuild、deletion 指标；
- secret rotation；
- dependency readiness；
- backup/restore 演练；
- chaos/fault injection。

### 19.4 安全测试

- cross-tenant matrix；
- IDOR；
- forged tenant header；
- Prompt injection；
- Redis key leakage；
- Kafka/ES/Qdrant document forbidden fields；
- error/log/trace leakage；
- replay after delete；
- stale auth scope；
- high-sensitivity embedding block。

### 19.5 性能测试

至少覆盖：

- canonical append；
- confirmed fact read；
- Redis hit/rebuild；
- ES/Qdrant 并行检索；
- assemble p50/p95/p99；
- outbox backlog 恢复；
- consumer throughput；
- index rebuild；
- deletion fan-out。

### 19.6 验收

- 所有 SLO 有 dashboard 和告警；
- 关键 runbook 经演练；
- 无高危安全问题；
- 容量测试支持首版流量与增长余量；
- 生产配置默认 fail closed。

### 19.7 提交

`feat: harden memx operations and security`

## 20. Task 14：重放、Shadow、灰度与首版发布

### 20.1 目标

证明记忆系统可重建、可评测、可回滚，再允许真实应用接入。

### 20.2 文件

- `cmd/memx-replay/`
- `cmd/memx-eval/`
- `tests/replay/*`
- `tests/fixtures/retrieval-gold.jsonl`
- `docs/runbooks/release.md`
- `docs/runbooks/rollback.md`
- `docs/acceptance/v1-release.md`

### 20.3 发布阶段

1. offline replay；
2. shadow read：检索但不注入应用 Prompt；
3. explain-only；
4. 小流量 MemoryPack 注入；
5. 按租户/应用灰度；
6. 全量前再次评审错误记忆率和删除传播。

### 20.4 验收

- 从 canonical PG 一致性快照及 outbox barrier 重建 Redis、ES、Qdrant，追上切换 barrier 后比较安全摘要/计数/连续 watermark 和删除回执；重建期间删除不得复活；
- 重复 replay 不产生重复副作用，Kafka 事件保留期外仍能从 PG 当前状态重建；
- shadow 仅验证候选覆盖、错误候选率和时延门槛；任务成功率与 Tool 正确率必须经成对离线评测或批准的在线对照组达到 Task 0 数值门槛，不能用 shadow 的未注入请求推断收益；
- 无记忆模式与降级模式可一键切换；
- 回滚不删除 canonical 数据；
- release report 包含测试、SLO、风险、未执行外部验证和已知限制。

### 20.5 提交

`feat: prepare memx v1 release gates`

## 21. 全局测试门禁

每个 Task 至少运行：

```bash
go test ./...
go vet ./...
golangci-lint run
```

涉及外部依赖时再运行：

```bash
docker compose -f deploy/compose/docker-compose.yml up -d <required-services>
go test -tags=integration ./tests/integration/...
docker compose -f deploy/compose/docker-compose.yml down
```

提交前：

```bash
git diff --check
git status --short
go test ./...
```

里程碑门禁：

- 所有单元、契约、集成、安全、重放测试通过；
- migration 从空库顺序执行通过；
- 无 unexplained skip；
- 无原始 secret/PII 出现在 Git、日志、fixture；
- staged 文件与 Task 白名单一致；
- 未执行的 live、生产、多区域验证明确列出。

## 22. 发布与兼容策略

- HTTP API 使用 `/v1`，破坏性变更进入新 major path；
- Event type 带 `.v1`，消费者声明支持版本；
- MemoryPack 带 schema version；
- 数据库 migration 只前进，不依赖 down migration 恢复；
- 索引使用 versioned alias/collection；
- Go SDK 与服务 API 分开版本；
- 旧事件在保留期内必须仍可读取或有显式迁移器；
- 删除和撤销事件不得因版本升级被忽略。

## 23. Task 0 决策台账

### 23.1 已确认的规划基线（2026-09-22）

| 决策 | 基线 | 状态 |
|---|---|---|
| 代码托管与 CI | 私有 GitHub 仓库 + GitHub Actions | 已确认；精确 owner/module path 待关闭 |
| 部署级别 | v1 单区域试点，不承诺跨区域 | 已确认 |
| 容量 | 峰值 50 QPS、10 万主体、100 万 turn/日 | 已确认；上线前按真实负载复算 |
| 可用性 | 服务月可用性 99.9% | 已确认；延迟与错误预算待压测 |
| Embedding 外发 | 仅确定性脱敏后的低敏文本可调用第三方；中高敏禁止外发 | 已确认；provider/model/DPA 待关闭 |
| 保留 | canonical raw turns 90 天；audit 365 天 | 已确认；法务与备份例外待关闭 |
| 确认 UX | 通用 API 显式 `confirm` / `revoke` | 已确认 |
| 首个应用 | 不绑定具体应用；只允许公开 API/SDK 接入 | 已确认；首个真实客户端在发布前确定 |

### 23.2 阶段门禁与 fail-closed 默认

| 待关闭决策 | Owner | 最晚关闭 | 未关闭时默认 |
|---|---|---|---|
| GitHub owner、Go module path、CI 权限模型 | 项目 Owner | Task 1 前 | 不初始化 `go.mod`，不创建远端 workflow |
| Go 与基础设施精确版本、许可证 | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像 |
| 部署/数据驻留区域、KMS/secret manager | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 |
| tenant/subject identity、JWT/JWKS 与服务代理授权契约 | Security + API Owner | Task 2 前 | 不开放业务 API；Task 4 前必须实现并验收身份验证、授权及 RLS 上下文 |
| confirmed namespace/key 白名单 | Product + Security | Task 7 前 | proposal 不得转 confirmed |
| embedding provider/model/dimension/DPA | ML + Security | Task 10 前 | 生产 embedding 外发关闭；只用确定性测试向量 |
| 平均消息长度、租户分布、配额与增长余量 | Product + SRE | Task 13 前 | 使用试点基线且禁止生产容量声明 |
| 法务保留、诉讼保全、备份物理清除 | Legal + Security | Task 12 前 | 不接真实主体数据，不声称物理清除完成 |
| 离线/成对评测指标、样本与统计方法的数值门禁 | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1，不以主观“优于 baseline”通过 |
| 真实负载 latency/error budget、成本与 token budget 复算 | Product + SRE | Task 13 前 | 使用设计初值，不能进入发布门禁 |
| 首个真实客户端与 shadow 数据集 | Product | Task 14 前 | 不发布 GA，只完成通用服务离线验收 |

Task 0 的完成标准不是凭空猜完所有业务参数，而是：已确认项写入 ADR；未确认项具有明确 owner、最晚关闭 Task 和可执行的 fail-closed 默认；所有 Task 1 阻塞项已关闭。

## 24. 完成定义

memX v1 只有在以下条件全部满足时才算完成：

- canonical conversation、confirmed facts、revisions 和 outbox 已生产化；
- Redis 可完全从 PG 重建；
- Kafka 重复、乱序、重放和 DLQ 已验收；
- Elasticsearch 与 Qdrant 命中必须 canonical revalidate；
- 模型 proposal 永不直接成为长期可读事实；
- MemoryPack 优先级、冲突、token budget 和降级已验收；
- TTL、撤销、用户删除和防复活已端到端验收；
- 跨租户泄露、未确认事实注入和删除复活均为 0；
- 离线检索达到批准的绝对阈值和词法 baseline 改善；shadow 候选/时延达标，端到端收益由成对离线或批准的在线对照组单独证明；
- SLO、dashboard、runbook、备份恢复和回滚完成；
- 首个应用只能通过公开 API/SDK 接入，不能绕过 canonical service 直接写投影。
