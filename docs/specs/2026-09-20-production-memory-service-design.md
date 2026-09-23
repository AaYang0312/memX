# 生产级通用记忆服务设计规格

**状态：** Draft for discussion  
**日期：** 2026-09-20  
**最近更新：** 2026-09-22  
**范围：** 通用、可独立部署的 AI Memory Service；本规格不要求立即接入 BI Agent。  
**目标读者：** 架构、后端、数据、搜索、安全、SRE 与应用接入团队。

## 1. 已确认决策

1. PostgreSQL 是 canonical source of truth。
2. canonical 写入与 transactional outbox 在同一 PostgreSQL 事务中提交。
3. Kafka 作为默认事件分发、重放和多投影日志；不同时引入 RabbitMQ。
4. Redis 只保存可重建的 working-memory 投影，不是事实源。
5. 第一版只允许 `confirmed` 用户事实参与长期记忆读取；模型推断不得直接成为已确认事实。
6. 词法索引使用 Elasticsearch，语义索引使用独立 Qdrant；二者都不是事实源。
7. 首版服务端使用 Go；具体框架和库由实施计划按最小依赖原则确定。
8. 当前阶段只定义通用规格，不绑定当前 BI Agent 的实现和部署。
9. Neo4j/图数据库是后续可选投影，必须由真实多跳查询需求触发。
10. 首轮按单区域试点规划：峰值 50 QPS、10 万主体、100 万 turn/日，服务月可用性目标 99.9%；上线前必须用真实负载复算。
11. 第三方 embedding 只允许处理经过确定性脱敏的低敏文本；中高敏内容不得外发。
12. canonical 原始 turn 默认保留 90 天，审计记录默认保留 365 天；法务保留、备份物理清除和例外仍需单独批准。
13. 首版确认 UX 采用通用 API 的显式 `confirm` / `revoke`；设置页、审核台和具体应用 adapter 后置。
14. 代码托管与 CI 采用私有 GitHub 仓库和 GitHub Actions；精确 owner/module path 在实施 Task 0 冻结。

剩余需要在实施阶段关闭的决策见 §21。

## 2. 目标

### 2.1 功能目标

系统应提供：

- 会话内短期工作记忆；
- 用户显式确认或服务端验证的长期事实；
- 历史事件与安全摘要的情景记忆；
- 经批准的流程/工具样例记忆；
- 词法与向量混合召回；
- 同步低延迟读取和异步派生写入；
- 跨 PostgreSQL、Redis、搜索、向量投影的可观测最终一致性；
- TTL、撤销、冲突、遗忘、用户删除和审计；
- 可被不同应用通过稳定 API/SDK 接入。

### 2.2 非功能目标

- canonical 数据不因 Redis、Kafka、搜索或向量库故障而丢失；
- 主对话路径不等待事实抽取、embedding 或索引完成；
- 不发生跨租户、跨主体或跨授权范围召回；
- 所有异步消费者支持幂等、重试、DLQ 和重放；
- 所有投影暴露版本与 watermark，不能把陈旧状态伪装成最新状态；
- 补充记忆故障时可降级，当前请求和已确认事实仍可服务；
- 可删除数据在所有投影中可追踪地传播。

## 3. 非目标

首版不做：

- 把模型自由生成的结论自动写成已确认事实；
- 使用 Redis、ES、向量库或 Neo4j 作为 canonical source；
- 跨存储分布式事务或宣称端到端 exactly-once；
- 无限制保存原始 Prompt、模型隐藏推理、密钥、DSN 或未脱敏业务结果；
- 让召回内容覆盖系统策略、授权、当前请求或服务端事实；
- 在没有真实关系型查询需求时引入图数据库；
- 在没有评测基线时仅凭主观感受上线新的召回算法。

## 4. 核心术语

- **Canonical Record**：PostgreSQL 中的权威记录。
- **Projection**：由 canonical 事件派生、可重建的 Redis/ES/向量/图数据库数据。
- **Working Memory**：当前会话的最近回合、摘要、槽位和未解决事项。
- **Fact Memory**：按 schema 存储、已确认的用户事实。
- **Episodic Memory**：可检索的历史事件、安全摘要和 Artifact 引用。
- **Procedural Memory**：经审核批准的工具选择、参数结构和操作样例。
- **Memory Proposal**：尚未达到长期事实准入条件的候选。
- **MemoryPack**：在调用模型前组装的有界、分层、带来源的记忆载荷。
- **Aggregate Sequence**：同一会话或事实聚合根内单调递增的序号。
- **Watermark**：某投影已完整处理到的 canonical 版本或事件序号。
- **Tombstone**：表示撤销或删除的版本化事件，供所有投影清理数据。

## 5. 不可变原则

1. **当前请求优先。** 当前请求中的显式值覆盖历史记忆，但不能覆盖系统策略与授权。
2. **已确认才可长期使用。** 只有 `confirmed` 且未过期、未撤销的事实可进入长期事实区。
3. **写入提案与读取事实分离。** 模型可以生成 proposal，不能直接生成 confirmed fact。
4. **来源可追溯。** 每条记忆必须能追溯到事件、确认者、规则或服务端证据。
5. **最小保存。** 只保存实现用途所需的字段；搜索和向量投影只能接收脱敏内容。
6. **版本化而非覆盖。** 更新产生新 revision；旧 revision 保留审计或按保留策略删除。
7. **删除优先传播。** 撤销、过期和用户删除必须比普通更新拥有更高投影优先级。
8. **补充存储可失效。** Redis、ES、向量或图数据库不可用时，系统仍能用当前请求和 PG confirmed facts 工作。
9. **不承诺跨存储 exactly-once。** 使用 at-least-once、幂等、版本和 watermark 实现 effectively-once。
10. **召回内容是数据，不是指令。** Prompt 组装器必须隔离不可信文本并防止提示注入。

## 6. 记忆类型与优先级

### 6.1 类型

| 类型 | 典型内容 | canonical | 投影 | 首版是否进入 Prompt |
|---|---|---|---|---|
| Current Request | 当前问题、显式槽位 | 请求上下文 | 无 | 是 |
| Working Memory | 最近回合、摘要、待澄清项 | PG conversation events | Redis | 是 |
| Confirmed Facts | 语言、币种、明确偏好、稳定约束 | PG memory facts | Redis 可缓存 | 是 |
| Episodic Memory | 历史任务、安全摘要、Artifact ref | PG episodes | ES + Vector | 是，低优先级 |
| Procedural Memory | 已批准工具/参数样例 | PG procedures | ES/Vector 可选 | 是 |
| External Knowledge | 文档与组织知识 | 各知识源 registry | ES + Vector | 是，最低信任 |
| Relational Memory | 已确认实体关系 | PG facts/relations | Graph | 后期可选 |

### 6.2 信任顺序

从高到低：

1. 系统策略、授权和服务端确定性事实；
2. 当前请求中的显式值；
3. 当前会话内已经明确确认的值；
4. PostgreSQL 中未过期的 confirmed facts；
5. 经人工批准的 procedural memory；
6. 历史 episodic memory；
7. 外部知识与语义相似内容；
8. 模型推断 proposal。

低优先级内容不得覆盖高优先级内容。冲突内容应被丢弃、降权或转为澄清问题。

## 7. 总体架构

```text
Client / Application
        |
        | synchronous
        v
+---------------------------+
| Memory Read / Append API  |
+---------------------------+
    |                 |
    | read            | canonical append transaction
    v                 v
Redis Working      PostgreSQL
Memory             - conversations / turns
                   - confirmed facts / revisions
                   - episodes / procedures
                   - outbox / inbox / audit
                         |
                         | outbox relay
                         v
                       Kafka
                         |
        +----------------+------------------+------------------+
        |                |                  |                  |
        v                v                  v                  v
 Fact Extractor     Redis Projector   Lexical Indexer    Vector Indexer
        |                |                  |                  |
        v                v                  v                  v
 PostgreSQL          Redis            Elasticsearch         Qdrant
 facts + outbox
        |
        +----------------------> optional Graph Projector -> Graph DB
```

### 7.1 同步与异步边界

同步写入只负责：

- 验证身份、租户、幂等键和事件形状；
- 持久化用户可见会话事件或明确确认命令；
- 更新 aggregate sequence；
- 同事务写入 outbox；
- 提交后可 best-effort 更新 Redis，但不得依赖该更新保证耐久性。

异步处理负责：

- 事实提取与 proposal 生成；
- 冲突检测与策略判断；
- 摘要与 episodic memory；
- embedding；
- ES、向量和图投影；
- TTL 到期、撤销和删除传播。

## 8. Canonical PostgreSQL 数据模型

以下为逻辑模型；物理 schema 可在实施计划中细化。

### 8.1 `memory_conversations`

- `conversation_ref`：opaque 主键；
- `tenant_ref`；
- `subject_ref`；
- `head_seq`；
- `summary_checkpoint_seq`；
- `status`：`active | archived | deleted`；
- `created_at`、`updated_at`、`expires_at`；
- `schema_version`。

唯一约束：`(tenant_ref, conversation_ref)`。

### 8.2 `memory_turns`

- `turn_ref`；
- `tenant_ref`、`subject_ref`、`conversation_ref`；
- `seq`；
- `role`：`user | assistant | system_event`；
- `content_ciphertext` 或受保护内容引用；
- `content_hash`；
- `status`：`complete | error | redacted | deleted`；
- `created_at`；
- `retention_class`；
- `schema_version`。

约束：

- `(tenant_ref, conversation_ref, seq)` 唯一；
- subject 必须与 conversation owner 匹配；
- 删除使用 redaction/tombstone，不复用 seq。

#### 8.2.1 `memory_summaries`

PG 中持久化可重建 Redis 的 canonical 摘要，不能只保存到缓存：`summary_ref`、tenant/subject/conversation ref、`from_seq`、`to_seq`、前一 checkpoint ref、摘要 revision、结构化且经脱敏/加密的内容或受保护引用、content hash、生成器/schema 版本、source turn 范围、sensitivity、status、`created_at`、`expires_at`。摘要由异步 worker 生成；仅当覆盖连续、schema/隐私策略通过时，与 conversation 的 `summary_checkpoint_seq` CAS 更新及 `memory.summary.committed.v1` outbox 在同一 PG 事务提交。摘要修订不可静默覆盖；删除、撤销来源或过期时产生失效事件，Redis 不得继续使用。原始 turn 被按 90 天策略清除后，只能在已保留且有效的摘要覆盖范围和仍存的 turn 范围内重建，不能承诺恢复已物理清除的原文。

### 8.3 `memory_facts`

- `fact_ref`；
- `tenant_ref`、`subject_ref`；
- `namespace`；
- `fact_key`；
- `value_type`；
- `value_json`；
- `value_hash`；
- `status`：`confirmed | superseded | revoked | deleted`；
- `trust_level`；
- `sensitivity_class`；
- `valid_from`、`valid_to`、`expires_at`；
- `revision`；
- `source_kind`；
- `source_event_ref`；
- `confirmed_by`、`confirmed_at`；
- `created_at`、`updated_at`；
- `schema_version`。

活跃事实建议唯一键：

`(tenant_ref, subject_ref, namespace, fact_key)` where status=`confirmed`。

冲突更新必须在事务内锁定当前 revision，并使用 CAS。

### 8.4 `memory_fact_revisions`

不可变审计记录：

- `fact_ref`、`revision`；
- before/after 的安全投影或 hash；
- `event_kind`；
- `actor_ref`；
- `reason_code`；
- `source_event_ref`；
- `created_at`。

只允许 INSERT，不允许 UPDATE/DELETE；法务删除通过受控归档/密钥销毁策略处理。

### 8.5 `memory_proposals`

proposal 不属于可读长期事实：

- `proposal_ref`；
- tenant/subject；
- namespace/key/value；
- extractor 类型与版本；
- confidence；
- evidence event refs；
- `revision`（proposal 命令的 CAS 版本）；
- `status`：`pending | confirmed | rejected | expired`；
- `reason_code`：例如 `pending_conflict`，不是额外的 status；
- `expires_at`；
- created/updated。

模型生成的 proposal 默认短 TTL，绝不进入 `confirmed_user_facts`。

### 8.6 `memory_episodes`

- `episode_ref`；
- tenant/subject/conversation；
- covered seq range；
- 脱敏摘要；
- Artifact opaque refs；
- topic/intent tags；
- authorization labels；
- sensitivity；
- version；
- expires_at；
- status。

不得存储模型隐藏推理、原始 SQL、密钥或未脱敏结果行。

### 8.7 `memory_procedures`

- `procedure_ref`；
- tenant 或组织作用域；
- domain；
- intent signature；
- 槽位化模板；
- 规范化动作/Tool；
- version requirements；
- approval status/revision；
- authorization labels；
- expires/revalidate_at。

只有 approved 且版本、授权兼容的记录可被读取。

### 8.8 `memory_outbox`

- `outbox_id`；
- `event_id`；
- aggregate type/ref/seq；
- event type；
- schema version；
- payload 或 payload ref；
- occurred_at；
- published_at；
- attempts；
- last_error_code。

约束：`event_id` 唯一，`(aggregate_ref, aggregate_seq, event_type)` 按语义唯一。

Outbox 优先只携带 opaque ref、版本和安全元数据；需要正文的受信消费者按 ref 从 PG 读取，避免原始文本扩散到 Kafka。

### 8.9 `memory_consumer_inbox`

每个消费者保存：

- `consumer_name`；
- `event_id`；
- aggregate ref/seq；
- processing status：`pending | applied | quarantined`；
- attempt/lease expiry；
- processed_at；
- projection version。

唯一键 `(consumer_name, event_id)` 用于去重，但 PG inbox 与外部 Redis/ES/Qdrant 写入不是同一事务。消费者先持久占用 pending lease，再执行具有 epoch/revision 条件的幂等外部副作用；确认成功后才将 inbox 标记 applied 并提交 Kafka offset。崩溃后 pending 可重试，副作用成功但 applied 未落库也必须安全重试；不得先记 applied 后执行外部写。

### 8.10 `memory_projection_watermarks`

- projection name；
- partition/shard；
- 连续成功处理的 processed offset（每个 partition 独立，不能用已见最大 offset）；
- canonical barrier/version/time；
- 缺口、DLQ、重试中的 offset 和 last success；
- lag；
- health status。

### 8.11 `memory_idempotency_requests`

所有可重试写命令共享 PG 幂等账本：`tenant_ref`、已验证 `actor_ref`、operation、target/subject ref、`idempotency_key` 的不可逆摘要、规范化请求的 keyed digest、完成结果的安全 response code/ref、`created_at`、`expires_at`。唯一键为 `(tenant_ref, actor_ref, operation, target_ref, key_digest)`；原始 key、原始请求及敏感响应不得存入账本。命中同 key、同 digest 返回原响应（仍重验当前授权/删除 fence），不同 digest 返回 409；并发相同 key 需由数据库唯一约束/锁串行化，不能产生两个 canonical side effect。账本结果与 canonical 变更、outbox 在同一事务提交；事务回滚不留下已完成记录。保留期至少覆盖已公布的重试窗口（首轮建议 7 天），到期后 key 可重用的语义须在 API 契约写明；主体删除时清除敏感结果并保留必要的防重/审计元数据。

## 9. Redis Working Memory 投影

### 9.1 Key

禁止在 key 中使用邮箱、手机号或原始用户 ID。建议：

`wm:v1:{tenant_hash}:{subject_ref}:{conversation_ref}`

### 9.2 Value

```json
{
  "head_seq": 18,
  "checkpoint_seq": 12,
  "summary": {"summary_ref": "sum-...", "revision": 1, "from_seq": 1, "to_seq": 12, "content": {}},
  "recent_turns": [],
  "active_slots": {},
  "pending_clarifications": [],
  "last_artifact_refs": [],
  "projection_version": 3,
  "updated_at": "...",
  "expires_at": "..."
}
```

### 9.3 读取规则

1. 读取 PG conversation head 或可信的短期 head token；
2. Redis `head_seq == canonical head_seq` 时直接使用；
3. Redis 落后时，从 PG 增量回补并异步修复缓存；
4. Redis 超前、形状非法或 subject 不匹配时丢弃缓存并告警；
5. Redis 不可用时，从 PG 中仍有效的 canonical summary checkpoint 与仍保留的 raw turns 重建；已过保留期且无有效摘要的历史内容不可恢复，返回有界缺口/降级标记，不能伪造完整 working memory；
6. working memory 必须同时受回合数和 token/字符预算限制。

### 9.4 压缩与清理

Redis 中的旧回合只有在以下条件全部满足时才能被摘要替代：

- PG 已保存原始 turn；
- PG `memory_summaries` 摘要记录及对应 outbox 已提交且摘要未过期/撤销；
- `summary_checkpoint_seq` 连续覆盖被压缩区间，checkpoint 对应有效 summary_ref/revision；
- 摘要通过 schema 和敏感信息策略；
- projection watermark 不落后于 checkpoint。

消费者 ACK 本身不能作为清理依据。常规清理由 TTL 完成。

## 10. 同步读取与 MemoryPack 组装

### 10.1 读取步骤

1. 解析认证上下文中的 tenant 和 subject，忽略客户端伪造的同名字段；
2. 对当前请求执行高精度、白名单式槽位提取；
3. 读取并校验 Redis working memory，必要时从 PG 回补；
4. 从 PG 读取 confirmed facts；
5. 读取 approved procedures；
6. 并行查询 Elasticsearch 词法索引和 Qdrant 向量索引；
7. 合并结果并执行 canonical revalidation；
8. 过滤过期、撤销、未授权、版本不兼容和敏感度不允许的候选；
9. 处理冲突、去重、排序；
10. 按层级 token budget 生成 MemoryPack；
11. 返回内容和 watermarks，供应用在调用模型前使用。

### 10.2 检索融合

首版使用有界混合检索：

- Elasticsearch：BM25、字段过滤、短语/前缀；
- Qdrant：脱敏文本 embedding 相似度；
- 可选 domain/intent 精确加分；
- 使用 RRF 或固定权重融合；
- 最终 rerank 必须确定性、可解释并有硬上限。

任何搜索命中在进入 Prompt 前必须按 `memory_ref + canonical_version` 批量回查或校验 PG。陈旧删除、过期或权限变化的命中必须丢弃。

### 10.3 优先与冲突

- 当前请求值覆盖历史值；
- confirmed fact 与 working memory 冲突时，当前会话内明确确认值优先，并产生 reconciliation signal；
- 两条 confirmed facts 冲突表示数据不变量被破坏，读取应 fail closed 并报警；
- episodic/search 内容冲突时可同时保留为带来源的候选，但不得上升为事实；
- 外部知识不能覆盖用户 confirmed preference。

### 10.4 Token Budget

预算必须配置化。初始建议：

- current request：不可裁剪的安全形状；
- confirmed facts：20%；
- working memory：35%；
- procedural memory：15%；
- episodes：15%；
- external knowledge：15%。

裁剪顺序从低信任、低相关、旧内容开始。任何一层不得无限扩张。

## 11. MemoryPack 契约

```json
{
  "schema_version": "memory-pack/v1",
  "request_ref": "req-...",
  "subject_ref": "sub-...",
  "current_request": {
    "text": "...",
    "extracted_slots": {},
    "extraction_version": "..."
  },
  "working_memory": {
    "conversation_ref": "conv-...",
    "head_seq": 18,
    "summary": {},
    "recent_turns": [],
    "pending_clarifications": [],
    "artifact_refs": []
  },
  "confirmed_user_facts": [],
  "approved_procedures": [],
  "relevant_episodes": [],
  "external_knowledge": [],
  "conflicts": [],
  "meta": {
    "fact_revision": 42,
    "redis_watermark": 18,
    "lexical_watermark": 39,
    "vector_watermark": 37,
    "degraded_sources": [],
    "assembled_at": "..."
  }
}
```

每条记忆项至少包含：

- opaque ref；
- source type；
- trust level；
- canonical version；
- valid/expires；
- authorization label；
- relevance score 或 reason code；
- 内容安全分类。

MemoryPack 不包含 DSN、密钥、原始内部主键、模型隐藏推理和未经批准的 proposal。

## 12. 写入与事实确认流程

### 12.1 Turn append

`append_turn` 使用 `Idempotency-Key` 与 `expected_conversation_seq`；幂等账本优先于 expected seq 冲突判断，以便成功提交但丢失 HTTP 响应后的原样重试：

1. 校验租户、主体和 conversation ownership；
2. 校验请求形状和内容策略；
3. 锁定或 CAS 更新 conversation head；
4. 插入 turn；
5. 插入 `turn.committed` outbox 与安全幂等结果记录；
6. 同事务提交；
7. 返回新 seq；
8. best-effort 更新 Redis，失败不回滚 canonical commit。

### 12.2 异步提取

Extractor 收到 `turn.committed` 后：

1. 用 event id 检查 inbox；
2. 按 ref 获取允许处理的 turn；
3. 运行高精度规则提取；
4. 可选运行受控模型提取，模型只产 proposal；
5. 应用 namespace allowlist、类型、敏感度和 TTL 策略；
6. 去重并检查与 confirmed fact 的冲突；
7. 满足自动确认规则时写 confirmed fact，否则写短期 proposal；
8. 仅事实创建/变更时写 fact revision 与对应 fact outbox；仅提案创建/变更时写 proposal outbox；
9. 与相应 canonical 更新同事务提交；
10. 提交 Kafka offset/ACK。

### 12.3 自动确认准入

首版自动确认只允许：

- allowlisted namespace/key；
- 当前用户明确陈述，且规则解析无歧义；或
- 可信服务端签名事件；
- value 满足严格类型、长度、枚举和敏感度约束；
- 没有未解决冲突；
- provenance 完整。

LLM 单独推断、一次性数值、临时目标、预算、价格、库存阈值、凭据和自由文本秘密一律不能自动确认。

### 12.4 冲突状态机

```text
new explicit same value -> refresh provenance / TTL
new explicit different value -> supersede old fact in one transaction
ambiguous different value -> proposal(status=pending, reason_code=pending_conflict)
user rejects proposal -> rejected proposal（不隐式撤销已确认事实）
explicit revoke / policy or source invalidated / confirmed fact expiry -> revoked (reason_code distinguishes cause)
user deletion -> deleted + tombstone
```

current request 可在本轮使用显式新值，但不能在冲突未解决时静默改写长期事实。

## 13. 事件契约

### 13.1 Envelope

```json
{
  "event_id": "evt-...",
  "event_type": "memory.turn.committed.v1",
  "schema_version": 1,
  "tenant_ref": "ten-...",
  "aggregate_type": "conversation",
  "aggregate_ref": "conv-...",
  "aggregate_seq": 18,
  "deletion_epoch": 0,
  "occurred_at": "...",
  "trace_ref": "trc-...",
  "payload": {
    "turn_ref": "turn-...",
    "subject_ref": "sub-...",
    "content_hash": "..."
  }
}
```

主体级事件必须携带写入事务观察到的 `deletion_epoch`（非主体范围事件使用适用的 scope 版本），消费者按 PG 当前 fence 判定；Kafka key 使用 `(tenant_ref, aggregate_ref)`，只保证同一聚合根内有序，主体删除与其 turn/fact 可能跨分区乱序。

### 13.2 必需事件

- `memory.turn.committed.v1`
- `memory.turn.redacted.v1`
- `memory.summary.committed.v1`
- `memory.fact.confirmed.v1`
- `memory.fact.superseded.v1`
- `memory.fact.revoked.v1`
- `memory.fact.deleted.v1`
- `memory.proposal.created.v1`
- `memory.proposal.resolved.v1`
- `memory.episode.committed.v1`
- `memory.procedure.approved.v1`
- `memory.procedure.revoked.v1`
- `memory.subject.deletion_requested.v1`
- `memory.subject.deletion_completed.v1`

事件 schema 只向后兼容演进；破坏性变化使用新 event type/version。

## 14. API 草案

### 14.1 同步读取

`POST /v1/memory:assemble`

输入：

- subject/conversation opaque ref；
- current request；
- domain 与授权上下文引用；
- token budget；
- source timeout budget；
- consistency preference：`fresh | bounded_stale | cache_preferred`；服务端限制最大允许 lag、deadline 与 token budget，客户端不能放宽授权或删除规则。

语义：`fresh` 必须成功读取 PG canonical head、facts、权限与删除 fence；声明参与结果的投影须达到请求开始时已取得的 PG 快照版本/事件 barrier，否则超时返回稳定不可用错误或按客户端显式允许的降级策略排除该来源，不得标为 fresh。`bounded_stale` 只允许补充投影在服务端配置的最大时间/版本 lag 内，仍必须经实时 PG canonical 权限、状态、TTL 和 fence 校验；`cache_preferred` 仅影响 Redis/搜索候选选取，不能跳过 PG 复核。输出逐来源实际 watermark、lag 与降级状态；不可验证的来源不得出现于记忆项。

输出：MemoryPack。

### 14.2 追加回合

`POST /v1/conversations/{conversation_ref}/turns`

要求：

- `Idempotency-Key`（按 §8.11 持久化，可从丢失响应的提交中恢复结果）；
- `expected_seq`；
- role/content/status；
- 客户端不能提交 tenant；tenant 来自认证上下文。

### 14.3 确认/拒绝事实

- `GET /v1/subjects/{subject_ref}/facts`：仅返回当前授权可见的未过期 confirmed facts；
- `GET /v1/subjects/{subject_ref}/proposals`：仅本人或授权审核者查看未过期 pending proposal；
- `POST /v1/proposals/{proposal_ref}:confirm`：用户/授权审核者显式确认候选；
- `POST /v1/proposals/{proposal_ref}:reject`：拒绝候选，不改变已有 fact；
- `POST /v1/subjects/{subject_ref}/facts:confirm`：在无 proposal 时显式提交 typed fact；
- `POST /v1/facts/{fact_ref}:revoke`：撤销现有 confirmed fact。

proposal 路径用 proposal_ref 和 expected proposal 状态/版本；显式事实创建或替换需 expected active fact_ref+revision（不存在时声明 expected absence），在同一事务内锁定 active key、记录 provenance、supersede 原事实、创建/更新 fact revision 与 outbox。所有命令需 §8.11 的持久 Idempotency-Key、身份/所有权与 namespace/sensitivity 校验；过期或已拒绝 proposal 不得确认，冲突返回稳定 409 reason code。删除命令与其返回 job_ref 也使用同一账本规则。

### 14.4 删除与导出

- `POST /v1/subjects/{subject_ref}:export`
- `DELETE /v1/subjects/{subject_ref}/memory`
- `GET /v1/deletion-jobs/{job_ref}`

删除为异步作业，返回 projection 清理进度与最终证明；认证和审批策略由部署环境决定。

### 14.5 调试解释

`POST /v1/memory:explain` 只对授权运维/审核角色开放，返回：

- 命中 opaque ref；
- 过滤 reason code；
- 分数分解；
- watermarks；
- 降级来源。

不得返回其他主体内容、原始敏感值或数据库异常。

## 15. Elasticsearch 与 Qdrant 投影

### 15.1 可索引内容

只允许：

- 已脱敏的 episode 摘要；
- approved procedure 模板；
- 可公开索引的 confirmed fact 文本投影；
- 注册知识源的安全片段。

禁止：

- 密钥、凭据、DSN；
- 原始内部主键；
- 未脱敏完整聊天；
- 模型隐藏推理；
- 已拒绝 proposal；
- 无 authorization metadata 的内容。

### 15.2 必需元数据

每个索引文档必须包含：

- memory ref；
- tenant/subject scope；
- authorization labels；
- canonical version；
- status；
- valid/expires；
- sensitivity；
- content hash；
- extractor/embedding model version；
- index schema version。

### 15.3 投影更新

- 搜索/向量投影消费 fact/episode/procedure 变化及主体删除/过期事件；Redis 另消费 turn/summary 事件；
- 每次投影副作用检查主体 fence 与该项 epoch/revision：旧版本不得覆盖新版本；同版本同 hash 为重复，不同 hash 隔离告警；硬删除索引文档后原索引版本可能消失，不能单靠外部索引版本保护迟到 upsert；
- tombstone 优先于旧事件；若 fence 检查与外部写之间发生删除，canonical 读取必须立即拒绝旧命中，投影须通过持久 epoch guard/串行化或写后复核补偿删除并定期核对，直至该主体无残留；
- 外部写成功、inbox/offset 提交失败时必须可安全重试；重试/DLQ 缺口不得推进连续 watermark 或产生删除完成回执；
- 全量重建在 PG 一致性快照上记录 outbox barrier，构建新 index/collection 后消费 barrier 之后的增量并复核删除 fence；追至切换 barrier、核对删除回执和目标数据后才原子切换 alias/config，旧索引隔离并按策略销毁；重建期间旧/新投影均须保持删除处理。

### 15.4 Embedding 版本

向量记录必须绑定：

- embedding provider/model；
- dimension；
- normalization；
- chunker version；
- content hash。

模型升级使用新 collection 或版本字段重建，不在原 collection 中混用不可比较向量。

## 16. TTL、保留与遗忘

TTL 按类别配置，不能只有一个全局值。以下为首轮规划基线；法务保留、诉讼保全、备份物理清除和特定 namespace 仍可通过批准后的策略收紧：

| 类别 | 首轮策略 |
|---|---|
| 当前请求 | 请求结束即失效 |
| Redis recent turns | 24 小时滑动 TTL，可配置 |
| Canonical raw turns | 默认 90 天 |
| Pending proposal | 7 天 |
| PG canonical working summary | 会话归档后 30–90 天，归档前至少保留有效 checkpoint；过期后旧回合若也已到期则不再可重建 |
| Episodic memory | 默认 90 天 |
| Confirmed preference | 按 namespace，定期重确认或 365 天 |
| Approved procedure | 无固定 TTL，但受版本失效与 revalidate_at 控制 |
| Audit | 默认 365 天；法务保留例外必须显式登记 |
| Tombstone | 至少保留到所有投影确认删除，并满足审计窗口 |

到期流程：

1. scheduler 产生 expiration event；
2. pending proposal 转 `expired`；到期 confirmed fact 转 `revoked` 并记录 `reason_code=expired`（不引入事实的 `expired` 状态）；
3. 写 tombstone outbox；
4. Redis/ES/Vector/Graph 独立清理；
5. 每个受影响投影持久记录该 tombstone 的成功处理回执（含确认无记录的 no-op），并完成该主体在线索引/缓存的核对；连续 watermark 追过事件只能作为必要条件，不能替代回执或越过 DLQ 缺口；
6. 所有在线投影回执与核对完成后才标记在线删除完成；备份物理清除、审计保留和法务例外分别报告，不能用“在线完成”冒充物理清除完成。

Redis TTL 到期不等于 canonical 删除；搜索投影删除也不等于事实删除。

## 17. 安全与隐私

### 17.1 隔离

- 在开放任何业务读写 API 前验证签名、issuer、audience、有效期及所需 scope；服务代理用户调用需验证代理授权；tenant/subject 只从已验证身份派生，未知或失效身份一律拒绝；
- 所有 canonical 查询同时约束 tenant 和 subject/scope；路径参数不得扩大身份授权；
- PostgreSQL 使用最小权限 service role 与 RLS；每笔事务用 `SET LOCAL` 注入已验证租户/主体上下文，缺失上下文拒绝访问，连接池复用不得保留上笔身份；
- Kafka topic/ACL、Redis keyspace、ES index alias 和向量 namespace 均按租户策略隔离；
- 不接受客户端自报授权范围作为最终依据。

### 17.2 敏感数据

- 字段级 sensitivity 分类；
- 原始会话内容加密存储或使用 KMS envelope encryption；
- 日志、指标、trace 不记录原始内容；
- embedding 前执行 PII/secret policy；
- 第三方 embedding 只接收经过确定性 PII/secret 脱敏的低敏文本，且 provider 必须满足批准的数据驻留和不训练条款；
- 中敏、高敏 namespace 禁止外发到第三方 embedding；高敏 namespace 也禁止进入 ES 和 Qdrant；
- 错误只返回固定 reason code，不回显底层异常或输入值。

### 17.3 Prompt 注入

- 检索结果放在结构化 data section，不作为 system instruction；
- 外部知识与历史用户文本标记为 untrusted；
- procedure 必须 approved；
- system policy 和授权在 MemoryPack 之外由应用强制；
- 模型输出仍需服务端 schema、权限和能力校验。

### 17.4 删除

用户删除必须覆盖：

- PG 当前事实和会话；
- Redis；
- ES；
- Qdrant；
- Graph；
- 缓存、重建快照和可适用备份策略；
- 后续迟到事件不得复活已删除数据。

为防复活，PG 须在删除命令的事务中先建立持久 deletion fence：`tenant_ref + subject_ref + deletion_epoch`，并将主体置为不可读/不可写，再写删除 outbox；任何读取、消费者、重放及全量重建均以当前 fence 过滤旧 epoch。不同聚合根/分区的事件无全局顺序，不能仅靠 Kafka 顺序或搜索索引中的旧文档版本阻止删除后迟到 upsert。fence 保留期必须覆盖事件重放、索引重建和备份恢复窗口；恢复旧备份时先恢复/合并 fence，才能暴露流量。

每个投影必须对删除事件记录持久、可审计的处理回执及该主体索引清空核对结果；若回执失败、DLQ 存在缺口、索引重建仍可导入旧数据，删除作业不得标记在线完成。用户删除与过期的公共 fence/过滤能力必须先于任何投影对外可读；完整导出、编排及备份处置可以后续交付。

## 18. 一致性、故障与降级

### 18.1 一致性保证

| 数据 | 保证 |
|---|---|
| 当前 turn append | PG commit 后 durable |
| Confirmed facts | PG strong read |
| Redis working memory | read-through，可回建 |
| ES/Vector/Graph | eventual，带 watermark |
| 用户删除 | 异步传播，状态可查询 |
| Read-your-writes | 当前请求直接使用；下一请求 Redis 落后时 PG 回补 |

### 18.2 故障策略

- PG 不可用：写入失败，不虚假确认；无法确认身份/授权时拒绝读取。默认不使用无法复核的缓存历史事实、working memory、procedure 或搜索命中；允许已验证身份的 `memory:assemble` 按部署策略返回仅包含当前请求的 request-only MemoryPack，标记 degraded 且各历史来源为空；`fresh` 返回不可用。将来若引入离线快照须单独评审可验证授权、删除 fence 和最大陈旧时间，不由 `cache_preferred` 暗中开启；
- Redis 不可用：从 PG 重建 working memory；
- Kafka 不可用：outbox 累积，canonical 写入继续，超过 backlog 阈值告警；
- ES 不可用：关闭词法补充召回；
- Qdrant 不可用：关闭语义补充召回；
- 单个投影失败：不阻塞其他 consumer group；
- Extractor 失败：不影响已提交会话；事件重试或进入 DLQ；
- Prompt 组装超时：按信任顺序丢弃低优先级来源，不延长模型调用总 deadline。

### 18.3 防止乱序与复活

每个投影应用事件时检查：

- deletion epoch；
- aggregate seq；
- canonical revision；
- event id；
- content hash。

旧 revision、旧 epoch 和重复 event 均不得覆盖新状态；写前的 fence 检查不能替代写后删除竞态处理。watermark 只记录每个 partition 的连续成功前缀，不能以已见最大 offset 或有 DLQ 缺口的进度证明新鲜度/删除完成。

## 19. 可观测性、SLO 与验收

### 19.1 指标

必须记录但不得带原始内容：

- assemble latency 和各来源 latency；
- Redis hit/miss/rebuild；
- confirmed fact 数量、冲突率、过期率；
- proposal confirm/reject/expire；
- lexical/vector recall 数量与融合后保留数；
- stale hit 丢弃数；
- projection lag/watermark；
- outbox backlog、relay failure、consumer retry、DLQ；
- deletion propagation lag；
- token budget 使用；
- 记忆启用前后的任务成功率、Tool 选择正确率和错误记忆率。

### 19.2 初始 SLO 建议

以下目标以单区域试点基线（峰值 50 QPS、10 万主体、100 万 turn/日）规划，上线前必须用真实消息长度、租户分布和增长率压测复算：

- 服务月可用性 ≥ 99.9%；
- `memory:assemble` p95 ≤ 150 ms，硬超时 ≤ 250 ms；
- confirmed facts PG 读取可用性 ≥ 99.9%；
- turn append p95 ≤ 100 ms；
- Redis 回建成功率 ≥ 99.99%；
- canonical event 到 facts 更新 p95 ≤ 5 s，p99 ≤ 30 s；
- 普通投影 lag p95 ≤ 30 s；
- 用户删除在线投影传播 p95 ≤ 5 min；
- cross-tenant leakage = 0；
- 未确认 proposal 进入 Prompt = 0；
- 删除后被迟到事件复活 = 0。

### 19.3 Trace

每次组装产生 safe trace：

- request/trace opaque ref；
- 使用的数据源；
- 命中 ref 和 reason code；
- watermarks；
- 降级来源；
- token 分配；
- 最终 Prompt 不落日志。

### 19.4 测试与验收

必须包含：

1. 数据模型和状态机单元测试；
2. tenant/subject/authorization 隔离测试；
3. 当前请求覆盖历史事实测试；
4. 未确认 proposal 永不进入 Prompt；
5. Redis 丢失后从 PG 重建；
6. Kafka 重复、乱序、延迟事件；
7. 消费者事务提交前崩溃与 ACK 后重启；
8. ES/Vector 单独和同时故障降级；
9. tombstone、删除 fence 和迟到事件防复活；
10. embedding 版本重建与索引切换；
11. 全量 replay 产生与 canonical 一致的投影；
12. Prompt 注入和敏感字段泄露测试；
13. TTL、撤销、冲突与重确认测试；
14. 离线 retrieval gold set：precision@k、recall@k、nDCG；
15. shadow mode 只评估检索覆盖、错误候选和性能；任务成功率/Tool 选择的因果影响须用成对离线评测或获得批准的小流量在线对照组验证，不能从未注入 Prompt 的 shadow 流量推断；
16. 负载、背压、DLQ 和 outbox backlog 压测；
17. 用户导出与删除端到端验收。

发布前须由 Product + ML/Eval + Security + SRE 在 Task 0 的 `docs/evaluation-gates.md` 冻结可执行门禁：gold set 的来源、时间切分、去重与 tenant/domain 分层、每层最小样本数/标注一致性；precision@5、recall@10、nDCG@10 的绝对下限及相对词法基线的最小改善幅度（未达到预定样本数或置信区间要求视为证据不足）；错误记忆率的分母/人工标注方法与上限；成对无记忆任务成功率/Tool 正确率的最低差异与置信区间；每来源 latency/deadline、token 与成本上限、删除传播 p95/p99。各阈值必须是经 owner 批准的数值而非“优于 baseline”，未批准前仅可离线开发，不得进入灰度或发布；真实负载变化后按变更控制重新批准。跨租户、未确认提案和删除复活的自动化测试必须零失败，样本中观测到 0 不等于证明生产永不发生。

任何投影测试通过都不能替代 canonical、授权和删除门禁。

## 20. 分阶段交付

### Phase 0：契约与评测

- 冻结 MemoryPack、Fact、Event、Watermark 和错误码；
- 建立无记忆端到端 baseline、词法检索 baseline、离线 gold set 和 shadow 指标；批准带数值、样本与统计方法的评测门禁；
- 完成威胁建模、数据分类和 retention matrix；
- 明确容量和合规要求。

**退出条件：** 契约评审与评测门禁批准；cross-tenant、未确认事实、删除复活三类风险有自动化验收设计。

### Phase 1：Canonical Core

- PostgreSQL conversations/turns/summaries/facts/revisions/outbox/inbox；
- append/read/revoke 及删除 fence/异步作业入口；proposal 确认与完整冲突状态机在 Phase 3（实施 Task 7）交付；
- transactional outbox relay；
- 不接 ES、向量或图。

**退出条件：** durable append、幂等、CAS、审计、删除 fence 和 replay 通过。

### Phase 2：Working Memory

- Redis 投影；
- 最近回合 + token-aware 裁剪；
- PG canonical summary 与 checkpoint 原子提交、版本/失效事件；
- PG read-through rebuild（受 raw turn 与 summary 各自保留期限制）；
- read-your-writes 验收。

**退出条件：** Redis 全量丢失后可从仍有效的 PG summary/raw turns 回建；已过保留期的历史缺口明确标记；ACK 不会误删短期记忆。

### Phase 3：异步事实提取

- Kafka 与 extractor consumer；
- 规则提取、proposal、确认和冲突；
- DLQ、重试、watermark；
- 模型提取只产 proposal。

**退出条件：** 重复/乱序/重放不产生重复 confirmed fact；主对话不等待 extractor。

### Phase 4：混合检索

- Elasticsearch lexical projection；
- Qdrant vector projection；
- embedding version；
- canonical revalidation；
- RRF/rerank；
- shadow mode 与灰度。

**退出条件：** 检索绝对阈值及相对词法基线改善、错误记忆率、延迟、隔离和删除均满足 Task 0 批准的数值门禁；shadow 结果不得充当端到端任务成功率验证。

### Phase 5：独立服务生产化

- 多租户配额、限流、熔断；
- SDK、契约版本和兼容策略；
- dashboard、SLO、on-call runbook；
- 灾备、备份恢复和索引重建演练；
- 首个应用 adapter。

### Phase 6：可选图投影

仅在实际需求证明以下查询稳定存在时启动：

- 多跳实体关系；
- 时间化关系；
- 关系冲突解释；
- 仅靠 PG/ES/Vector 难以满足的图遍历。

Graph DB 仍为可重建投影，不能成为用户事实源。

## 21. 残余待确认问题

已冻结的规划基线见 §1；以下问题必须按实施计划 §23 的阶段门禁关闭，未关闭时采用 fail-closed 默认：

1. 私有 GitHub 仓库的精确 owner 与 Go module path；
2. 单区域试点的实际云/机房区域、数据驻留区域、密钥管理和 Qdrant 拓扑；
3. 平均/峰值消息长度、租户分布、增长余量与单租户配额；
4. 第三方 embedding provider/model、dimension、处理区域、DPA/不训练条款与降级模型；未批准前生产语义外发保持禁用；
5. 首批可确认或可自动确认的 namespace/key 白名单；未列入白名单的内容只能保持 proposal；
6. tenant/subject identity 的签发者、JWT/JWKS 契约和服务间身份；
7. 组织级事实是否进入后续版本，以及 user/team/org 的继承与冲突规则；
8. 法务保留、诉讼保全、备份物理清除期限和审计 365 天基线的例外；
9. 是否允许 Redis 故障时使用 PG 派生的 bounded-stale 缓存快照；默认只回源 PG，不使用未验证快照；
10. 最终 latency/error budget、成本上限、MemoryPack token budget，以及首个真实接入应用；
11. 离线/成对评测的数据集、样本与统计方法以及各指标数值门禁；Task 0 退出前必须批准，未批准不得启动 Task 1。

## 22. 设计结论

本系统采用“**同步持久化来源事件，异步派生记忆与投影；同步读取并按可信度组装**”的模型：

- PostgreSQL 保证事实与审计；
- Outbox + Kafka 保证可重放传播；
- Redis 提供可重建短期记忆；
- Elasticsearch 与 Qdrant 提供补充召回；
- 所有补充命中在进入 Prompt 前回到 canonical 权限、版本和 TTL 规则；
- 只有 confirmed facts 可以作为长期用户事实；
- ACK、TTL、watermark 和 tombstone 各自承担不同职责，不互相替代。

该边界允许系统从单体内核演进为通用服务，同时避免多存储双写、缓存被误当事实源和模型自动污染长期记忆。
