# memX 数据分类与保留基线（Task 0 文档切片）

**状态：** Proposed（Task 0 数据分类草案，待 owner 评审。本文档不批准 G1–G12 中任何待批准项，不设定任何 namespace→sensitivity 映射、任何 namespace/key 白名单、任何法务/区域/KMS/DPA 条款，不解除实施计划 §6.4/§23.2 的 Task 1 阻塞条件，见 §10/§11。）
**日期：** 2026-09-23
**范围（In scope）：** 设计规格所定义系统的数据类别分类矩阵（低/中/高敏与 secret/PII 标记，覆盖一时值、raw turns、摘要、confirmed facts、proposals、episodes/procedures、opaque refs/metadata、审计与台账、tombstone/fence、事件、备份、日志/trace）；PostgreSQL/Redis/Kafka/Elasticsearch/Qdrant/日志 trace 各存储与通道的允许及禁止形状；第三方 embedding 与可选模型 extractor 两类外发边界；按类别的保留矩阵（设计基线）；Redis TTL、canonical 清除、在线投影删除、备份物理清除与法务保全五层语义的区分；风险、owner、最晚阶段、fail-closed 默认与自动化验收/重审触发。
**明确排除（Out of scope）：** 任何代码、依赖、镜像或基础设施变更；namespace→sensitivity_class 映射内容与 confirmed namespace/key 白名单内容（G5，本文件只提供待批准映射必须满足的层级约束规则）；法务保留、诉讼保全、备份物理清除期限与区域/KMS/DPA 具体条款（G3/G6/G7，只登记 owner 与 fail-closed 默认）；`docs/threat-model.md`、`docs/capacity-baseline.md`、`docs/evaluation-gates.md` 的内容修订（已另行交付或另行切片）。

**权威输入（本文档与其冲突时以上游为准，须修订本文档）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"，§n 引用）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"，Task n 或 §n 引用）
- `docs/adr/0001-canonical-and-event-backbone.md`、`0002-search-and-vector-projections.md`、`0003-confirmed-fact-policy.md`（下称 ADR 0001–0003，§n 引用）
- `docs/threat-model.md`（T-n 引用）
- `docs/capacity-baseline.md`（B-n 引用，仅保留基线相关项）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码、依赖与基础设施；本文件属于实施计划 §6.2（Task 0）文件清单的一部分，先于 Task 1 创建。本文件的创建不构成对任何待批准项的批准；涉及本文档的验证以文件与引用一致性检查为准，不使用、不宣称 `git diff` 检查。

---

## 0. 阅读本文档前必须接受的四个事实

1. **本仓库尚无任何实现。** 未初始化 Git、无 Go 源码、无依赖、无基础设施（README"当前状态"）。本文档全部分类规则、允许/禁止形状与保留基线都是**设计层约束**，没有任何一条已在代码或基础设施中实施并验证。凡标注"归属 Task n"者均为未来门禁，不得读成已完成。
2. **已确认的保留基线只有设计规格给出的那组数字。** raw turns 默认 90 天、audit 默认 365 天（设计规格 §1.12）、Redis recent turns 24 小时滑动可配置、pending proposal 7 天、PG working summary 归档后 30–90 天（归档前保留有效 checkpoint）、episodic 默认 90 天、confirmed preference 按 namespace 定期重确认或 365 天、procedure 无固定 TTL 受 revalidate_at 控制、tombstone 保留到投影确认删除并满足审计窗口（§16 表）、幂等账本保留至少覆盖已公布重试窗口（首轮建议 7 天，§8.11）、当前请求请求结束即失效（§16 表）。其性质是"**首轮规划基线**"，且 §16 明示"法务保留、备份物理清除和特定 namespace 仍可通过批准后的策略收紧"。
3. **本文档不做三类决定。** ① 不设定 namespace→sensitivity 映射（G5 联动，ADR 0003 §6/§13）；② 不列任何具体 namespace/key 白名单（G5）；③ 不编造法务/区域/KMS/DPA 条款（G3/G6/G7）。凡需要这些决定处，本文只登记 owner、最晚关闭阶段与 fail-closed 默认。
4. **默认保留不是无条件清理保证。** retention scheduler 属 Task 12 交付（实施计划 §18）；在其落地并通过门禁前，任何"90 天/365 天到期清除"都只是设计意图。Redis TTL 到期不等于 canonical 删除；搜索投影删除也不等于事实删除；备份物理清除在 G7 批准前不得声称（设计规格 §16、实施计划 §18.5）。

## 1. 方法与分类维度

每类数据按三个正交维度登记：

1. **敏感度层级**：低敏 / 中敏 / 高敏（§2 的层级约束规则）；
2. **内容标记**：是否可能承载 PII、是否可能承载 secret/凭据（§2）；
3. **存储与通道形状**：该类数据在每个存储/通道中允许出现的形状与明确禁止的形状（§4）。

分类矩阵（§3）回答"是什么、放哪里、最高什么层级、哪些形状禁止"；保留矩阵（§6）回答"放多久、到期语义是什么"；§7 回答"几种'删除'分别指什么"。本文档只定义层级约束规则与形状约束，不把任何具体业务 namespace 指派到层级——该指派随 G5 评审批准，且必须逐条满足 §2。

## 2. 敏感度层级与 secret/PII 判定规则（Proposed）

以下层级定义是从设计规格 §17.2、ADR 0002 §6/§8、ADR 0003 §6 推导的**操作化约束规则**，不是新决策；每个 namespace 的具体层级指派是 G5 待批准项。

| 层级/标记 | 判定（内容画像） | 允许的目的地（上限） | 明确禁止 | 上游依据 |
|---|---|---|---|---|
| **低敏** | 确定性脱敏后具备第三方 embedding 外发资格的内容（外发仍以 G6 批准为前提） | ES/Qdrant（带 ADR 0002 §7 全部必需元数据 + gate）；经 §5.1 顺序 gate 后的第三方 embedding | 未脱敏即外发；缺元数据进入投影 | 设计规格 §1.11、§17.2；ADR 0002 §6.1、§8 |
| **中敏** | 不具备第三方 embedding 外发资格、但可通过分类/脱敏/授权校验进入词法投影的内容 | ES 词法投影（脱敏 + 授权元数据齐备）；PG canonical | 第三方 embedding 外发；Qdrant 语义路径（G-M 未单独批准前默认禁用：不外发、不生成语义向量，ADR 0002 §17） | 设计规格 §17.2；ADR 0002 §6.1、§17 G-M |
| **高敏** | 不得外发、不得进入任何搜索/向量投影的内容 | 仅 PG canonical + 受保护存储（字段级 sensitivity_class + 加密/envelope，机制待 G3） | 进入 ES 和 Qdrant；一切第三方外发 | 设计规格 §17.2 |
| **secret（标记）** | 密钥、凭据、DSN、令牌、验证码、自由文本秘密 | 仅受保护存储或作为请求执行所必需的瞬时输入；secret 类型化配置对象不得实现明文 `String()`、不得进日志/错误 | 自动确认或写入长期事实；进入补充投影、Kafka payload、Redis key、日志/trace/错误 | 设计规格 §12.3、§15.1、§11；实施计划 §7.3、§13.4、§19.4 |
| **一次性/临时值（标记）** | 一次性数值、临时目标、预算、价格、库存阈值等随请求变化的值 | 当前请求内按授权使用；若作为 raw turn 留存则遵守其 90 天受保护存储策略 | 自动确认、继承为长期槽位；不得仅因出现于当前请求就长期保留 | 设计规格 §12.3、§16；实施计划 §12.5、§13.5 |
| **PII（标记）** | 可识别自然人的字段（邮箱、手机号、原始用户标识等） | 原始形态仅限 PG 受保护存储；进入投影/embedding/持久派生物前必须通过确定性脱敏与 PII/secret policy | 作为 Redis key、公开 ref、索引 id/字段名、日志/指标/trace 内容；未脱敏即参与模型调用或持久化 | 设计规格 §9.1、§17.2、§17.3；实施计划 §2.9、§8.4、§14.4 |

补充约束：

- 字段级 sensitivity 分类落在 `memory_facts.sensitivity_class`、`memory_summaries.sensitivity`、`memory_episodes.sensitivity` 等字段上（设计规格 §8.3、§8.2.1、§8.6）；读取侧按敏感度过滤是 MemoryPack 组装的必经步骤（§10.1 步骤 8）。
- PII/secret 扫描先于模型调用与持久化执行（实施计划 §14.4）；embedding 前执行 PII/secret policy（设计规格 §17.2）。
- 一次性敏感值不得升级为长期事实（README 不变量 5）、不得被 working memory 槽位继承（实施计划 §12.5）、不得自动确认（ADR 0003 §6 denylist 最低集合）。
- JSON 字段（`value_json`、summary content 等）施加递归敏感键约束（实施计划 §9.4）。

## 3. 数据类别分类矩阵（Proposed）

| # | 类别 | canonical 位置 | 敏感度上限 | PII/secret 注记 | 允许形状（摘要） | 明确禁止 | 上游依据 |
|---|---|---|---|---|---|---|---|
| C1 | 当前请求与提取槽位（**一时值**） | 请求上下文；不落 canonical（turn 本身另归 C2） | 请求原文可瞬时含任意层级 | 可能含 PII/secret；一次性值 denylist | 白名单式高精度槽位提取；请求结束即失效；冲突未消解时本轮可用但不写回长期事实 | 持久化到请求上下文之外（作为长期事实/槽位继承）；一次性值自动确认 | 设计规格 §10.1、§12.4、§16；README 不变量 5；实施计划 §12.5 |
| C2 | 原始 turn（raw turns） | PG `memory_turns`：`content_ciphertext` 或受保护内容引用 + `content_hash` | 按内容实际分类，可能为高敏或含 secret | 原始 PII/secret 仅在受保护原文中保留，进入派生物前按分类策略处理 | 加密/envelope 存储（机制待 G3）；`retention_class` 元数据；受信消费者按 ref 回读；经授权/脱敏/预算约束的最近回合安全片段可进入 working memory 和 MemoryPack | 原始完整 turn 明文进入 Kafka、Redis key、ES、Qdrant、日志；未经校验的原文进入 MemoryPack；无保留期约束的无限存放 | 设计规格 §8.2、§9.2、§11、§17.2；ADR 0001 §5.3 |
| C3 | canonical 摘要（summary） | PG `memory_summaries`：结构化且经脱敏/加密的内容或受保护引用 | 中敏为上限（须过 schema 与敏感信息策略） | 脱敏后残留 PII 须可控；禁止含 secret | 连续覆盖区间、revision、checkpoint CAS、sensitivity、expires_at 齐备后才提交 | 未过敏感信息策略即提交；隐藏推理、密钥；revision 静默覆盖 | 设计规格 §8.2.1、§9.4 |
| C4 | Redis working memory 投影 | 非 canonical，可重建（设计规格 §1.4） | 会话内容可达中/高敏，仅限信任边界内、TTL 有界 | key/value 禁原始用户标识；一次性敏感槽位禁止继承 | key=`wm:v1:{tenant_hash}:{subject_ref}:{conversation_ref}`；value 为 §9.2 有界形状（head/checkpoint、typed summary ref、受数量+token 预算约束的 recent_turns、slots、clarifications、artifact refs、projection_version、expires_at） | key 含邮箱/手机号/原始 ID；未验证内容进入 MemoryPack；pending proposal；作为事实源 | 设计规格 §9.1–§9.3；实施计划 §12.4/§12.6 |
| C5 | confirmed facts | PG `memory_facts`：typed value（`value_type`+`value_json`）+ `sensitivity_class` | 按 namespace 指派（G5）；高敏事实仅 PG | secret 类值禁止确认；PII 值的投影资格按层级 | 白名单 namespace/key + typed schema + provenance 完整后确认；低/中敏的"可公开索引文本投影"可入 ES（Task 9 规则 + gate）；高敏永不索引 | G5 未批准前的任何生产确认；secret/一次性值确认；高敏内容进入 ES/Qdrant | 设计规格 §8.3、§15.1、§17.2；ADR 0003 §6/§13；ADR 0002 §6.1 |
| C6 | proposals（任何状态） | PG `memory_proposals` | 同派生来源（源自 turn 内容） | 可能含未脱敏用户陈述 | 仅本人/授权审核者可见未过期 pending；7 天 TTL；确认后经由 fact 事件正常投影 | 进入 facts read、MemoryPack、ES/Qdrant/Redis 任何投影（任何状态） | 设计规格 §8.5、§14.3、§19.2；ADR 0003 D1 |
| C7 | episodic memory（episodes） | PG `memory_episodes` | 脱敏摘要形态，实际分类决定索引资格 | PII 须在持久化前脱敏 | 脱敏摘要 + Artifact opaque refs + tags + authorization labels + sensitivity + expires_at；符合索引白名单/分类者可入 ES；仅脱敏低敏且 G6 批准后可走第三方 embedding 建向量 | 模型隐藏推理、原始 SQL、密钥、未脱敏结果行、完整聊天原文 | 设计规格 §8.6、§15.1、§17.2 |
| C8 | procedural memory（procedures） | PG `memory_procedures` | 模板形态，低/中敏 | 槽位模板禁含凭据/DSN | 仅 approved 且版本/授权兼容可读；可索引 approved 模板（可选） | 未批准/版本失效/过 revalidate_at 的记录可读；模板中内嵌凭据 | 设计规格 §8.7、§15.1 |
| C9 | opaque refs 与安全元数据 | 各处 | 不含原始标识，不代表公开或天然低敏；scope/authorization labels 仍按最小披露处理 | 原始 PII 不应出现 | opaque ref、content_hash、revision/epoch、watermark、authorization labels、reason code、schema version 可作为事件/投影元数据；日志和公开 API 只披露各自契约允许的安全子集，不输出原始值 | 以原始内部主键、邮箱、手机号替代 opaque ref；把所有内部元数据当作公开字段 | 设计规格 §8.8、§9.1、§11、§19.3；实施计划 §2.9 |
| C10 | 审计与台账（fact revisions、幂等账本） | PG `memory_fact_revisions`（INSERT-only）、`memory_idempotency_requests` | 安全投影/hash 形态，低-中敏 | 不存原始 key/请求/敏感响应；主体删除时清除敏感结果、保留防重/审计元数据 | before/after 安全投影或 hash、actor_ref、reason_code；key_digest + keyed digest + 安全响应 ref；账本保留 ≥ 重试窗口（首轮建议 7 天） | 原始 key、原始请求、敏感响应入账；revision UPDATE/DELETE | 设计规格 §8.4、§8.11 |
| C11 | tombstone / deletion fence / 投影删除回执 | PG fence 表 + 各投影回执 | 内容低敏，**完整性极高**（泄露或损坏即破坏删除承诺，威胁模型 A6） | 不含 | `tenant_ref + subject_ref + deletion_epoch`、tombstone 事件、含 no-op 的持久回执、空索引核对结果 | 提前清除（短于重放/重建/备份恢复窗口）；用 watermark 追过冒充回执 | 设计规格 §16、§17.4；ADR 0001 §8 |
| C12 | outbox/Kafka 事件（含 DLQ、隔离队列） | PG `memory_outbox` → Kafka | 仅 C9 形态，低敏 | 不含正文 | envelope（§13.1 字段 + `deletion_epoch`）+ 仅 opaque ref/版本/安全元数据 payload；`content_hash` 允许 | 原始正文、原始用户标识、敏感值进入 payload；DLQ 收纳敏感 payload | 设计规格 §8.8、§13.1；ADR 0001 §5.3；实施计划 §11.3 |
| C13 | 备份与恢复产物 | 备份存储 | 继承所含全部 canonical 数据的层级（含已过保留期、物理清除前的历史副本） | 含 | 受保留与物理清除策略约束；恢复前先恢复/合并 fence | G7 批准前声称物理清除；恢复旧备份未合并 fence 即暴露流量 | 设计规格 §16、§17.4；ADR 0001 §8 |
| C14 | 日志、指标、trace、错误响应 | telemetry 后端 | 仅 C9 形态 | 不含 | opaque ref、`trace_ref`、reason code、watermark、计数、时延、token 分配、降级来源（§19.1 清单） | 原始内容、原始用户标识、secret 类型配置值、embedding/模型调用原文、底层异常回显 | 设计规格 §17.2、§19.1、§19.3、§14.5；实施计划 §7.3 |

## 4. 各存储与通道的允许/禁止形状

### 4.1 PostgreSQL（canonical）

- **允许：** 全部 canonical 表（§8.1–§8.11）；敏感正文以 `content_ciphertext`/受保护引用形态存储（加密或 KMS envelope，**机制与选型待 G3**）；每条记忆带 `sensitivity_class`/`sensitivity`；JSON 字段递归敏感键约束；fact revisions 与事件只追加；RLS + `SET LOCAL` 租户上下文，缺失即拒绝。
- **禁止：** 明文 secret/凭据列；无 sensitivity 元数据的记忆记录；application role 获得 DDL/越权权限；连接池跨事务残留租户身份。
- **归属：** Task 3（schema/RLS/JSON 约束/最小权限）、Task 4（上下文注入与连接卫生）；加密机制依赖 **G3**（Platform + Security，Task 1 前；未关闭仅本地合成测试配置，不处理真实数据）。

### 4.2 Redis working-memory 投影

- **允许：** key 形状 `wm:v1:{tenant_hash}:{subject_ref}:{conversation_ref}`（禁邮箱/手机号/原始 ID，§9.1）；value 为 §9.2 有界 JSON（head_seq、checkpoint_seq、typed summary ref/revision/覆盖区间、recent_turns 受回合数+token/字符预算、active_slots、pending_clarifications、artifact_refs、projection_version、updated_at、expires_at）；由 turn/summary/tombstone 事件驱动；24 小时滑动 TTL（可配置）；删除时清空相关 key 并出具持久回执。
- **禁止：** 原始用户标识入 key/value 字段名；一次性敏感值继承为槽位；pending proposal；未经 PG 验证的内容进入 MemoryPack；把 TTL 当作 canonical 删除；无界 recent turns。
- **归属：** Task 6（形状、回建、TTL 与删除回执测试，实施计划 §12.4–§12.6）。

### 4.3 Kafka（含 DLQ 与隔离队列）

- **允许：** §13.1 envelope（`event_id`、`event_type`、`schema_version`、`tenant_ref`、`aggregate_ref/seq`、`deletion_epoch`、`occurred_at`、`trace_ref`）+ 仅 opaque ref/版本/安全元数据的 payload（如 `turn_ref`、`subject_ref`、`content_hash`）；正文由受信消费者按 ref 回读 PG；key=`(tenant_ref, aggregate_ref)`；schema 不支持事件进隔离队列（有界、可见、可治理）。
- **禁止：** 任何正文/敏感值/原始用户标识进入 payload；DLQ 收纳敏感 payload；把隔离队列当作无治理数据沉淀点。
- **归属：** Task 5（relay/消费框架、DLQ 纪律、崩溃回放测试，实施计划 §11.3–§11.5）；topic/分区/保留期属 **G10**（Tech Lead + SRE，Task 5 评审，未冻结不创建生产 topic）。

### 4.4 Elasticsearch

- **允许：** §15.1 白名单四类（脱敏 episode 摘要、approved procedure 模板、可公开索引的 confirmed fact 文本投影、注册知识源安全片段）且每文档带 ADR 0002 §7 全部必需元数据（memory ref、tenant/subject scope、authorization labels、canonical version、status、valid/expires、sensitivity、content hash、extractor/model version、index schema version）；缺元数据文档视为非法，须可识别并清理；versioned index + alias。
- **禁止：** 密钥/凭据/DSN；原始内部主键（id 只能由 opaque ref 派生）；未脱敏完整聊天与 raw turn 正文（raw turns 永不入索引）；模型隐藏推理；已拒绝或 pending proposal；无 authorization metadata 的内容；**高敏 namespace 全部内容**；原始用户标识出现在 id、字段名。
- **归属：** Task 9（白名单索引、no forbidden fields 测试、删除回执与 alias 重建，实施计划 §15.3–§15.5）；可索引 confirmed fact 文本投影的资格随 G5 映射 + Task 9 派生规则（ADR 0002 §5/§16）。

### 4.5 Qdrant 与 embedding 通道

- **允许：** 仅经 §5.1 全部 gate 通过的**脱敏低敏**文本所生成的向量；collection 按 embedding model/dimension/version 隔离；payload 带 ADR 0002 §7 元数据；G6 未批准期间只允许**确定性测试向量**。
- **禁止：** 高敏内容（禁入 Qdrant，同 ES）；中敏内容的外发语义向量（G-M 默认禁用：不外发、不生成语义向量）；未脱敏文本建向量；不同版本向量混排；embedding 请求/响应记录原文；真实数据语义召回在 G6 批准前开放。
- **归属：** Task 10（embedding gate/chunker/provider 客户端、sensitive content blocked 测试，实施计划 §16.3–§16.5）；**G6** 批准是生产外发的唯一开关（ML + Security，Task 10 前）。

### 4.6 日志、指标、trace 与错误响应

- **允许：** §19.1 指标清单（latency、hit/miss、watermark/lag、DLQ/backlog、deletion lag 等计数与时延）、§19.3 safe trace（request/trace opaque ref、数据源、命中 ref 与 reason code、watermark、降级来源、token 分配）；错误仅稳定 reason code + trace ref。
- **禁止：** 原始内容与最终 Prompt 落日志；原始用户标识；secret 类型配置值（无明文 `String()`）；embedding/模型调用原文；底层异常或输入值回显。
- **归属：** Task 1（secret 类型化、结构化日志不输出配置值）、Task 2（稳定错误码）、Task 13（safe logs、error/log/trace leakage 测试，实施计划 §19.4）。

### 4.7 备份与恢复产物

- **允许：** 在批准的保留与物理清除策略内存在；恢复旧备份前先恢复/合并 deletion fence 才能暴露流量；在线完成与物理清除状态分开展示。
- **禁止：** G7 批准前声称任何物理清除完成；未合并 fence 恢复暴露流量；用"在线删除完成"冒充"备份物理清除完成"。
- **归属：** Task 12（备份/法务例外写明）、Task 13（backup/restore 演练）；期限与手段属 **G7**（Legal + Security，Task 12 前；fail-closed：不接真实主体数据、不声称物理清除完成）。

## 5. 外发边界（egress boundaries）

### 5.1 第三方 embedding

外发 gate 按序执行，任一不通过即不外发、不建向量（ADR 0002 §8.1）：

1. **内容资格：** 仅低敏内容具备外发资格；中敏禁止外发；高敏禁止进入 ES 和 Qdrant（设计规格 §17.2）；
2. **确定性脱敏：** 低敏文本调用前执行确定性脱敏与 PII/secret policy；
3. **provider 合规：** provider 必须满足已批准的数据驻留与不训练条款——**当前无任何已批准 provider**（G6）；
4. **调用纪律：** 请求/响应不落原文；deadline、预算、熔断。

**G6 未批准期间的 fail-closed 行为（当前状态）：** 生产 embedding 外发整体关闭；向量投影只用确定性测试向量；语义召回不对真实数据/流量开放（`vector_watermark` 可存在但语义来源 excluded/degraded）；词法投影（ES）不受此阻塞；任何文档不得虚构 provider、model、dimension、区域或 DPA 条款（ADR 0002 §8.2、实施计划 §23.2）。

### 5.2 可选模型 extractor（独立外发边界）

- 可选模型 extractor 若由外部服务承担模型调用，构成**与 embedding 无关的单独外发边界**：不得假定其自动继承 embedding 的任何批准（威胁模型 T-06 入口）。
- **未经安全审批的默认（当前状态）：** 仅运行本地确定性规则 extractor，不向外部模型发送 turn 内容（威胁模型 T-06"Task 8 前默认仅运行本地确定性规则"；实施计划 §14.3 两级提取中模型层为 optional）。
- 即使未来获批，模型调用仍须遵守：PII/secret 扫描先于模型调用与持久化；输出严格 JSON schema 且只产 pending proposal；deadline/预算/熔断；不保存隐藏推理；embedding 的 G6 批准对模型 extractor **不发生**效力，须独立审批其外部提供方、文本外发分类与契约（Security + ML）。
- 模型不可用或无法判断时不写；模型关闭时规则提取仍可工作（实施计划 §14.4–§14.6）。

### 5.3 新增外发通道

任何新增外发通道（新 provider、新区域、新推理服务、新知识源 registry 出站）都必须先作为独立边界补入本节与威胁模型（威胁模型 §11"架构变更须先修订威胁模型再实施"），经对应 owner 审批后才能启用；默认 fail closed。

## 6. 保留矩阵（设计基线）

TTL 按类别配置，不能只有一个全局值（设计规格 §16）。下表数值全部为 §16/§1.12/§8.11 已确认的**首轮规划基线**；法务保留、备份物理清除和特定 namespace 仍可通过批准后的策略收紧。**在 Task 12 scheduler 落地并通过门禁前，这些基线不是已实施的清理行为。**

| 类别 | 首轮基线 | 到期触发/执行者 | 到期语义 | 例外与依赖 | 上游依据 |
|---|---|---|---|---|---|
| 当前请求（含提取槽位） | 请求结束即失效 | 请求生命周期 | 生命周期语义，非存储保留；持久轨迹是 turn 本身（C2） | — | 设计规格 §16 |
| Redis recent turns | 24 小时滑动 TTL，可配置 | Redis TTL | 缓存条目消失，保留期内可从 PG 回建；**不等于 canonical 删除** | 常规清理由 TTL 完成；ACK 不是清理依据 | 设计规格 §16、§9.4 |
| Canonical raw turns | 默认 90 天 | retention scheduler（Task 12） | PG 行清除；清除后只能从仍存 turn 范围 + 已保留有效 summary 覆盖范围内重建，不能承诺恢复已物理清除原文 | 法务保留例外须显式登记；收紧须批准策略 | 设计规格 §1.12、§8.2.1、§16 |
| Pending proposal | 7 天 | scheduler | 转 `expired`（终态）并写 resolution 事件；proposal 本就不进任何投影 | 过期/已拒绝不得确认（稳定 409） | 设计规格 §8.5、§14.3、§16 |
| PG canonical working summary | 会话归档后 30–90 天；归档前至少保留有效 checkpoint | scheduler | 清除/撤销摘要发失效事件；过期后旧回合若也已到期则**不再可重建** | 归档前有效 checkpoint 保留是硬约束 | 设计规格 §16、§9.3.5 |
| Episodic memory | 默认 90 天 | scheduler | 到期产生失效 tombstone，投影独立清理并出回执 | 同上收紧规则 | 设计规格 §16 |
| Confirmed preference | 按 namespace，定期重确认或 365 天 | scheduler + 重确认流程 | 到期 confirmed fact 转 `revoked(reason_code=expired)`（无 `expired` 状态）并写 tombstone | 具体 namespace TTL/重确认策略属 G5/保留策略评审 | 设计规格 §16；ADR 0003 §10/§13 |
| Approved procedure | 无固定 TTL | 版本失效与 `revalidate_at` 控制 | 版本/授权不兼容或过 revalidate_at 即不可读 | — | 设计规格 §16、§8.7 |
| Audit（含 fact revisions） | 默认 365 天 | scheduler | INSERT-only 记录；法务删除经受控归档/密钥销毁策略 | 法务保留例外必须显式登记 | 设计规格 §1.12、§8.4、§16 |
| Tombstone / deletion fence | 至少保留到所有投影确认删除，并满足审计窗口 | 删除编排（Task 12） | 提前清除会重新打开复活窗口；保留期必须覆盖事件重放、索引重建和备份恢复窗口 | 恢复旧备份先恢复/合并 fence | 设计规格 §16、§17.4 |
| 幂等账本 | 至少覆盖已公布重试窗口（首轮建议 7 天，未批准数值） | scheduler | 到期后 key 可重用语义须在 API 契约写明；主体删除时清除敏感结果、保留防重/审计元数据 | 与 canonical 变更同事务提交 | 设计规格 §8.11 |
| Outbox/Kafka 事件 | Kafka 保留期未冻结（G10 保守草案） | Kafka 保留 + relay 状态更新 | 保留期外事件必须仍能从 PG 当前状态重建；outbox 事件内容不可改写 | G10（Task 5 评审）前不创建生产 topic | 实施计划 §20.4；ADR 0001 §5.3/§5.4 |
| 备份与恢复产物 | 无已批准期限 | G7 批准的物理清除策略 | 物理清除与在线删除分别报告；清除前历史副本仍含已删/已过期数据 | **G7 未关闭前不接真实主体数据、不声称物理清除完成** | 设计规格 §16、§17.4；ADR 0001 §14 |
| 投影 watermark、inbox、删除回执等运维元数据 | 本文档不设数值 | Task 3/12 评审登记 | 非用户内容；保留须覆盖删除完成证明与审计需要 | 避免虚构，随实现评审冻结 | 设计规格 §8.9–§8.10 |

## 7. 五层"删除/清理"语义的区分（不得混用）

| 层 | 触发 | 效果 | **不**意味着 | 上游依据 |
|---|---|---|---|---|
| ① Redis TTL 到期 | 24h 滑动 TTL | 缓存条目消失，保留期内可从 PG 回建 | canonical 删除；投影删除完成；可免除 tombstone 传播 | 设计规格 §16 |
| ② canonical 清除（PG 保留期清理） | retention scheduler（Task 12） | PG 行清除；该内容此后**不再可重建**（仅剩 summary 覆盖范围的摘要形态） | 投影已清理；备份已清除；审计已删除 | 设计规格 §8.2.1、§16；实施计划 §12.5 |
| ③ 在线投影删除 | tombstone 事件 → 各投影清理 → 持久回执（含 no-op）+ 空索引核对 → 全部完成后才可标记 online_completed | 该主体在该投影在线可见数据消失，且迟到事件被 fence 阻断 | 备份物理清除；canonical 已清除（②是独立流程） | 设计规格 §16、§17.4；实施计划 §18.5 |
| ④ 备份物理清除 | G7 批准的策略与手段 | 历史副本物理消失 | 可由③"在线完成"替代或冒充——两者必须分别报告 | 设计规格 §16；实施计划 §18.5 |
| ⑤ 法务保全（litigation/legal hold） | 显式登记的法务例外 | 在其范围内覆盖②③④的普通保留/清除节奏 | 默认状态——未经登记不存在；audit 365 天基线同样允许例外但必须显式登记 | 设计规格 §16 |

配套纪律：到期流程为 scheduler → 到期状态迁移（proposal→`expired`；fact→`revoked(reason_code=expired)`）→ tombstone outbox → 各投影独立清理 → 持久回执 + 在线核对 → 在线完成（设计规格 §16）。消费者 ACK、TTL、watermark、tombstone 各自职责不同，互不替代；连续 watermark 追过事件只是删除完成的必要条件，DLQ 缺口与回执缺失必须阻断 online_completed（设计规格 §9.4、§16；ADR 0001 §7/§8）。

## 8. 风险、owner、最晚阶段与 fail-closed 默认

### 8.1 本文分类体系依赖的门禁（逐字对齐 ADR 0001 §14 / 实施计划 §23.2 / ADR 0002 §17）

| 门禁 | 待批准项（与本文相关的部分） | Owner | 最晚关闭 | 未批准时 fail-closed 默认 |
|---|---|---|---|---|
| G3 | 部署/数据驻留区域、KMS/secret manager（C2 加密机制、C13 恢复纪律的前提） | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 |
| G4 | tenant/subject identity、JWT/JWKS 与服务代理授权契约（authorization labels 的可信来源） | Security + API Owner | Task 2 前 | 不开放业务 API |
| G5 | confirmed namespace/key 白名单，**含 namespace→sensitivity_class 映射**、typed schema、denylist 条目、各 namespace TTL/重确认策略 | Product + Security | Task 7 前 | 白名单与映射为空：proposal 不得转 confirmed；高敏/中敏规则按 §2 默认从紧执行；`auto_confirm` 恒不自动确认 |
| G6 | embedding provider/model/dimension/normalization/处理区域/DPA 与不训练条款、禁用时降级（§5.1） | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量；语义召回不对真实数据开放 |
| G7 | 法务保留、诉讼保全、备份物理清除期限与手段（§6/§7 ④⑤） | Legal + Security | Task 12 前 | 不接真实主体数据，不声称物理清除完成 |
| G9 | 评测门禁数值（含删除传播 p95/p99；确定性脱敏完备性的评测归属） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；阈值必须为批准数字 |
| G10 | Kafka topic 命名/分区数/保留期（C12 保留） | Tech Lead + SRE | Task 5 评审 | 不创建生产 topic，仅本地合成配置 |
| G-M | 中敏内容的语义召回路径（自建 embedding 或其他方案） | ML + Security | 需求出现时单独评审 | 默认禁用：不外发、不生成语义向量（ADR 0002 §17） |

### 8.2 分类与保留相关的具体风险

| # | 风险 | 影响 | Owner | 最晚阶段 | 缓解/fail-closed |
|---|---|---|---|---|---|
| R1 | namespace→sensitivity 映射缺失期间误分级 | 内容按错误层级投影或外发 | Product + Security（G5） | Task 7 前 | 空白名单 + §2 层级上限规则；未映射即按最高禁用处理（只入 PG） |
| R2 | 确定性脱敏对"可逆推断"的覆盖未评测 | 低敏外发文本被还原 PII | ML/Eval（随 G9 评测设计） | Task 0 退出（评测门禁）/ Task 10（启用前） | 脱敏失败按外发失败处理（gate 任一步不通过即不外发） |
| R3 | 模型 extractor 被误认为继承 embedding 批准 | turn 内容未经审批外发 | Security + ML | Task 8 前 | §5.2 默认仅本地确定性规则；独立审批记录为启用前提 |
| R4 | 保留基线被当作已实施清理 | 合规声明失实 | Tech Lead + SRE | Task 12 | §0 事实 4；online_completed 与物理清除状态分报 |
| R5 | tombstone/fence 被提前清除 | 删除复活窗口重开 | Legal + Security + SRE（G7） | Task 12 前 | §6 保留覆盖重放/重建/备份窗口；恢复先合并 fence |
| R6 | 中敏内容误入语义召回 | 未批准外发/向量生成 | ML + Security（G-M） | 需求出现时 | 默认禁用；启用须单独评审 |
| R7 | 备份含已删数据且恢复未合并 fence | 删除承诺被恢复操作破坏 | Platform + SRE + Security | Task 12/13 | §4.7；backup/restore 演练（Task 13） |
| R8 | 敏感键扫描完备性无形式化保证 | 递归 JSON/嵌套结构漏检 | Security | Task 13 评审 | 契约测试 + 评审双道（威胁模型 T-05 同口径） |

## 9. 自动化验收锚点与重审触发

### 9.1 自动化验收锚点（合同锚点；测试本体尚未编写）

| 断言 | 来源 | 归属 Task | 相关类别 |
|---|---|---|---|
| secret 不出现在日志和错误；配置缺失/非法即启动失败 | 实施计划 §7.4 | Task 1 | C14、secret 标记 |
| 原始邮箱/手机号不能作为 Redis key 或公开 ref | 实施计划 §8.4 | Task 2 | C4、C9、PII 标记 |
| PG JSON 递归敏感键约束 | 实施计划 §9.4 | Task 3 | C5、C3 |
| DLQ 无敏感 payload；重放不产生重复 canonical side effect | 实施计划 §11.3–§11.5 | Task 5 | C12 |
| 不继承一次性敏感槽位；raw turn 90 天清除后仅可从保留 summary 回建、summary 亦过期时显式标缺口 | 实施计划 §12.5 | Task 6、Task 12 | C1、C2、C3 |
| forbidden temporary values；read path 永远只看到 confirmed | 实施计划 §13.5–§13.6 | Task 7 | C1、C5、C6 |
| PII/secret 扫描先于模型调用与持久化；model proposal 进入 Prompt = 0 | 实施计划 §14.4–§14.6 | Task 8 | §5.2、C6 |
| no forbidden fields in indexed document；cross-tenant filter | 实施计划 §15.4 | Task 9 | §4.4 |
| sensitive content blocked；embedding 请求不记录原文；dimension mismatch 拒绝 | 实施计划 §16.3–§16.4 | Task 10 | §4.5、§5.1 |
| 未确认 proposal/敏感内容/跨租户内容进入 MemoryPack = 0 | 实施计划 §17.6 | Task 11 | C5、C6 |
| TTL expiry；tombstone retention；备份仍保留时在线完成与物理清除状态分开展示 | 实施计划 §18.4–§18.5 | Task 12 | §6、§7 |
| Kafka/ES/Qdrant document forbidden fields、Redis key leakage、error/log/trace leakage、high-sensitivity embedding block、replay after delete | 实施计划 §19.4 | Task 13 | 全局 |
| 无原始 secret/PII 出现在 Git、日志、fixture | 实施计划 §21 | 每 Task | 全局 |

以上均引用设计规格 §19.2/§19.4 的零失败门禁（cross-tenant、未确认 proposal、删除复活）；样本观测 0 不等于证明生产永不发生。

### 9.2 重审触发（change control）

1. **G5 评审/批准时：** 每个新增 namespace/key 必须同时提交层级映射提案，并逐条核对 §2 层级约束与 §3/§4 形状约束；映射批准记录须回填至门禁台账，本文不代行批准。
2. **新增或变更以下任一项时，须先修订本文档（及威胁模型）再实施：** 数据类别、存储/投影组件、外发通道（embedding provider/model/区域/DPA 变更 → G6 重新批准；模型 extractor 外部化 → §5.2 独立边界审批）、保留参数、区域/KMS（G3）、法务要求（G7）。
3. **脱敏策略或其实现变更时：** 重跑 R2 评测并经 ML/Eval + Security 确认。
4. **安全事件、审计发现、删除/外发相关门禁失败后：** 强制重审对应类别与边界。
5. **真实负载/业务构成变化后：** 按变更控制重新批准相关基线（与设计规格 §19.4"真实负载变化后按变更控制重新批准"同口径）。

## 10. 本文档不是什么（防误读声明）

1. **不是 namespace→sensitivity 映射或白名单。** 本文只给出层级约束规则与形状约束；任何 namespace 的层级指派、key 白名单、typed schema、denylist 扩充均属 G5。
2. **不是法务/合规结论。** 法务保留、诉讼保全、备份物理清除期限与手段、数据驻留区域、KMS、DPA/不训练条款均未确定（G3/G6/G7）；本文未引用任何具体法域、区域或供应商条款。
3. **不是已实施控制的证明。** 全部规则是设计约束；对应 Task 门禁通过前不得视为存在，本文不得被引用为"已具备数据分级/保留能力"。
4. **默认保留不是无条件清理保证。** §6 数值是首轮规划基线；scheduler 未落地前不发生任何自动清除；物理清除在任何情况下都须 G7 与独立报告。
5. **不批准任何门禁。** G1–G12 与 G-M 全部处于待批准状态；本文的评审不替代任何 owner 的批准。

## 11. 验证记录

- **一致性核对（人工交叉引用）：** 本文层级规则逐条对照设计规格 §1.5/§1.11/§1.12、§5.5、§8.2–§8.11、§9、§10、§11、§12.3、§13.1、§14.5、§15、§16、§17、§18.2、§19、§21；实施计划 §2、§6.2–§6.4、§7–§20、§21、§23；ADR 0001 §5–§10、§14；ADR 0002 §5–§8、§11–§13、§16–§17；ADR 0003 §6、§10、§13；threat-model T-05/T-06 与 §2 资产表；capacity-baseline B4/B5/B7；README 不变量。保留数值逐项与设计规格 §16 表核对一致（90d/365d/24h 滑动/7d/30–90d/90d/重确认或 365d/revalidate/tombstone）；未发现与上游冲突的陈述，未新增任何数值型承诺。
- **写入范围：** 本次仅新增 `docs/data-classification.md` 一个文件；未修改 README、设计规格、实施计划、ADR、threat-model、capacity-baseline 及任何其他文件；未执行 git init/commit、未安装依赖、未拉取镜像、未启动任何基础设施。
- **VCS 状态：** `D:/Projects/memX` 当前不是 Git 仓库；因此本文档与任务验证**不使用、也不宣称 `git diff` 检查通过**（与 ADR 0002/0003 工作约定及 threat-model §10 同口径）。
- **已知未决（非本文档可关闭）：** namespace→sensitivity 映射与白名单（G5，Task 7 前）；区域/KMS（G3，Task 1 前）；embedding provider/DPA（G6，Task 10 前）；法务/备份清除（G7，Task 12 前）；评测数值（G9，Task 0 退出前）；Kafka 保留期（G10，Task 5 评审）；幂等账本保留期批准数值（§8.11"首轮建议 7 天"为建议值）；运维元数据（watermark/inbox/回执）保留期（Task 3/12 评审）。

## 12. 后续维护

- 任一 Task 落地把本文"设计约束"变为"已实施"时，须同步修订 §4–§7 的归属标注并附门禁证据引用，不得只在本文宣称完成。
- G5/G6/G7/G10 任一门禁关闭后，由对应 owner 更新 §8.1 状态并引用批准记录；本文不代行批准。
- 新增数据类别、存储、外发通道或保留策略变更时，按 §9.2 重审触发执行，先修订本文再实施。
