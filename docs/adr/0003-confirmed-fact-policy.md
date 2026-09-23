# ADR 0003：Confirmed Fact 准入策略与冲突状态机

**状态：** Proposed（Task 0 文档切片草案，待 owner 评审；本 ADR 不批准任何决策，也不解除实施计划 §6.4 的 Task 1 阻塞条件；G5 namespace/key 白名单关闭前，confirmed 准入保持空白名单 fail-closed，见 §13）
**日期：** 2026-09-22
**范围（In scope）：** proposal 与 confirmed fact 的严格隔离边界；proposal/fact 两套状态机与 reason_code 语义（`pending_conflict` 是 reason_code 而非状态；fact 无 `expired` 状态）；confirmed 准入要求（数据来源/provenance、来源证据、信任、敏感度、typed schema、namespace allowlist、一次性敏感值 denylist、TTL/revalidate）；confirmed 的形成路径与模型边界（模型仅产 pending proposal，高 confidence 也不自动确认）；读路径可见性谓词；显式 confirm/reject/revoke/replace 命令契约（proposal_ref 与 fact_ref 区分、乐观 revision/expected active fact CAS、持久幂等 key、审计 revision 与 outbox 同事务）；冲突处理（不静默覆盖、当前请求临时覆盖但不写回、不覆盖系统策略与授权）；事实删除/过期的 tombstone 与投影/检索防复活；Task 7/8/11 测试门禁。
**明确排除（Out of scope）：** 具体 namespace/key 白名单内容、可自动确认字段清单与任何数值阈值（G5 未批准，本 ADR 保持空白名单，不编造任何条目）；typed value schema 的具体 JSON Schema 文件内容；ES/Qdrant/Redis 投影内部实现（ADR 0002）；canonical 事务边界、outbox/inbox/watermark 与删除 fence 机制本身（ADR 0001，本 ADR 只继承）；Go 服务栈与依赖版本（ADR 0004）；MemoryPack 融合打分公式与 token budget 数值（实施计划 Task 11）；任何代码、依赖、镜像或基础设施变更。
**权威输入（本 ADR 与其冲突时以上游为准，须修订本 ADR）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"）
- `docs/adr/0001-canonical-and-event-backbone.md`（下称"ADR 0001"，只读引用，本 ADR 继承其全部骨干约束，特别是 D8 与 §5.1 事务边界）
- `docs/adr/0002-search-and-vector-projections.md`（下称"ADR 0002"，只读引用，本 ADR 继承其投影与防复活约束）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码、依赖与基础设施；本 ADR 属于实施计划 §6.2（Task 0）文件清单的一部分，先于 Task 1 创建。本 ADR 的创建不构成对任何待批准项（§13）的批准；涉及本文档的验证以文件与引用一致性检查为准，不使用、不宣称 `git diff` 检查。

---

## 1. 背景与问题

memX 的长期用户记忆只读取 `confirmed` 事实；模型推断不得直接成为已确认事实（设计规格 §1.5、README 不变量 3）。同时，事实会更新、冲突、过期、被撤销和被删除，投影与检索层必须防复活（README 不变量 4/6）。若准入边界含糊，会出现三类不可接受的结果：模型幻觉固化为"已确认事实"、冲突被静默覆盖丢失用户意图、一次性敏感值（密码、验证码、临时预算）升级为长期记忆。

在写 Task 7/8 代码前必须回答：

1. proposal 与 confirmed fact 的隔离边界画在哪里，读路径、MemoryPack 与投影各看到什么；
2. 两套状态机如何精确区分，`pending_conflict` 与"过期"归状态还是归 reason_code；
3. confirmed 只能通过哪些路径形成，模型输出在其中扮演什么角色；
4. 一条事实要满足哪些准入要求（来源、证据、信任、敏感度、类型、白名单、TTL）才允许长期存在；
5. 显式 confirm/reject/revoke/replace 命令如何用 ref、revision CAS 与幂等 key 做到可审计、可重试、不静默覆盖；
6. 删除与过期如何通过 tombstone 阻止投影与检索复活。

## 2. 决策（已确认基线的固化）

以下决策来自设计规格"已确认决策"（§1）、不可变原则（§5）、信任顺序（§6.2）、数据模型（§8.3/§8.4/§8.5）、确认与冲突流程（§12.2–§12.4）、API 草案（§14.3）、TTL 流程（§16）与 README 核心不变量，属于已确认基线，本 ADR 予以固化为架构约束。**G5 白名单内容等待批准项不在此列（§13）。** 本 ADR 的 D1–D9 独立编号，全文同时继承 ADR 0001 §2 骨干决策（尤其 D8）与 ADR 0002 的投影约束。

- **D1 — proposal 与 confirmed fact 严格隔离。** `memory_proposals` 与 `memory_facts` 是两个独立 canonical 聚合，各自的状态机、唯一键、TTL 与事件契约独立（设计规格 §8.3、§8.5）。proposal 任何状态（pending/confirmed/rejected/expired）都不属于可读长期事实：不出现在 facts read（实施计划 §10.3"proposal 永不出现在 facts read"）、不进入 MemoryPack（设计规格 §11、§19.2"未确认 proposal 进入 Prompt = 0"）、不进入 ES/Qdrant/Redis 任何投影（ADR 0002 §5"proposal 任何状态均不进入索引"）、不得构造成 confirmed fact 类型（实施计划 §8.4）。确认成功后，事实经由 fact 事件正常产生投影，而非 proposal"就地转正"。

- **D2 — 两套状态机与 reason_code 语义精确区分（实施计划 §13.3）。**
  - proposal：`pending -> confirmed | rejected | expired`；`pending` 是唯一非终态，`confirmed/rejected/expired` 均为终态。**`pending_conflict` 是 pending 的 reason_code，不是状态**（设计规格 §8.5）；reason_code 与状态正交，附加原因不得改变状态集合。
  - fact：`confirmed -> superseded | revoked | deleted`，均为终态。**到期 confirmed fact 转 `revoked` 并记录 `reason_code=expired`，不引入事实的 `expired` 状态**（设计规格 §16）；revoke 的 reason_code 区分成因（显式撤销 / 策略或来源失效 / 到期，设计规格 §12.4）。
  - terminal 状态不可回到 confirmed；重新启用必须创建新 fact/revision 链（实施计划 §13.3）。proposal 的 expired/rejected 同为终态，过期或已拒绝 proposal 不得确认（设计规格 §14.3）。

- **D3 — confirmed 只有三条形成路径，模型输出永不直接确认。**
  1. **白名单确定性规则：** 在 G5 批准的 namespace/key 内，当前用户明确陈述且规则解析无歧义（设计规格 §12.3）；
  2. **可信服务端签名事件：** 通过签名/身份验证且属于允许的 source kind 的服务端事件（设计规格 §12.3、实施计划 §13.4）；
  3. **显式确认：** 用户或授权审核者经通用 API 的显式 `confirm` 命令（设计规格 §1.13、§14.3）。
  三条路径都必须同时满足 §6 的全部准入要求。**LLM 单独推断只能产生 pending proposal；confidence 数值本身不构成任何确认依据，高 confidence 不自动确认、不缩短等待、不放宽 §6 任一要求**（设计规格 §1.5、§12.2 步骤 4、README 不变量 3、ADR 0001 D8、实施计划 §2.7）。模型 extractor 只能作为 proposal 的来源证据（extractor 类型与版本），不是事实的确认来源；白名单确定性规则 extractor 通过准入后可作为独立确认来源。

- **D4 — confirmed 准入要求（全部满足才可确认；设计规格 §12.3、实施计划 §13.4）：** namespace/key 白名单（G5，当前为空）、typed value schema（value_type + value_json 通过对应 namespace 的 JSON Schema）、sensitivity policy、provenance 完整、无未解决冲突、一次性敏感值 denylist。denylist 的最低集合为设计规格 §12.3 已确认类别：LLM 单独推断、一次性数值、临时目标、预算、价格、库存阈值、凭据和自由文本秘密一律不能自动确认（细节见 §6）。

- **D5 — 读路径可见性谓词。** facts read 与 MemoryPack 的 confirmed facts 区只返回**同时满足**以下条件的记录：status=`confirmed`、未过期（`expires_at`/`valid_to`）、授权匹配、未被主体 deletion fence 屏蔽（设计规格 §14.3、§17.4，实施计划 §10.3）。同一 `(tenant_ref, subject_ref, namespace, fact_key)` 至多一条 active confirmed fact（设计规格 §8.3 唯一键）；两条 confirmed facts 冲突视为数据不变量被破坏，读取 fail closed 并报警（设计规格 §10.3、ADR 0001 §10）。

- **D6 — 显式命令契约。** proposal 路径（`:confirm`/`:reject`）使用 `proposal_ref` 与 expected proposal 状态/版本；无 proposal 的显式事实确认（`facts:confirm`）与替换使用 expected active `fact_ref + revision` 或声明 expected absence；`fact_ref` 与 `proposal_ref` 不混用、不以 fact_ref 代表尚不存在的 proposal（设计规格 §14.3、实施计划 §10.2/§13.4）。替换在同一事务内锁定 active key、记录 provenance、supersede 旧 fact、写新 revision 与 outbox（设计规格 §14.3、§8.3"冲突更新必须在事务内锁定当前 revision，并使用 CAS"）。所有可重试命令使用 §8.11 持久 Idempotency-Key（同 key 同 digest 返回原安全结果、不同 digest 409、并发串行化、回滚不占用 key）。每次 fact 状态变化记录恰好一条 INSERT-only `memory_fact_revisions` 审计记录和对应 outbox 事件；proposal 状态变化使用自身 revision CAS 与 `memory.proposal.resolved.v1` 事件，审计事件同事务提交（设计规格 §8.4–§8.5、§12.2，ADR 0001 §5.1）。拒绝 proposal 不隐式撤销已确认事实（设计规格 §12.4、§14.3）。冲突返回稳定 409 reason code，不回显底层异常或输入值（设计规格 §14.3、§17.2）。

- **D7 — 冲突处理（设计规格 §12.4 状态机，逐条固化）：** new explicit same value -> refresh provenance/TTL；new explicit different value -> 同一事务 supersede 旧 fact；ambiguous different value -> 写 proposal（status=`pending`，reason_code=`pending_conflict`）。**冲突不得静默覆盖**：冲突未解决时，当前请求可在本轮使用显式新值（信任顺序：当前请求高于历史事实，README 不变量 5），但不得写回长期事实；低信任来源（episodic/search/外部知识/模型推断）不得覆盖高信任内容（设计规格 §6.2、§10.3）。**任何来源不得覆盖系统策略与授权**（设计规格 §5.1 不可变原则 1：当前请求显式值覆盖历史记忆，但不能覆盖系统策略与授权；§3 非目标"让召回内容覆盖系统策略、授权、当前请求或服务端事实"）。

- **D8 — 删除/过期 tombstone 与投影防复活。** fact `revoked/deleted`（含到期转 revoked）产生失效 tombstone outbox；proposal 到期转 `expired` 并产生 resolution 事件，但 proposal 从不进入投影，无需为其索引删除出具回执（设计规格 §16 到期流程、§13.2）。fact 投影必须移除并出具持久回执，迟到事件、重放与全量重建不得复活：消费者检查 deletion epoch、aggregate seq、canonical revision、event id、content hash（设计规格 §18.3，ADR 0001 §8，ADR 0002 §9/§10）。fact 的复活面由本决策与 ADR 0001/0002 的 fence+条件写+回执机制共同封闭。"删除后被迟到事件复活 = 0" 是自动化门禁（设计规格 §19.2）。

- **D9 — fail-closed 的准入默认（G5 未关闭期间）。** namespace/key 白名单为空 ⇒ 没有任何 namespace/key 具备确认资格：proposal 不得转 confirmed（实施计划 §23.2）、未列入白名单的内容只能保持 proposal（设计规格 §21.5）、`facts:confirm` 与自动确认规则对一切生产 namespace/key 按稳定 reason code 拒绝；typed schema 目录为空 ⇒ 无 value 可通过类型校验，同样 fail closed。单元/契约测试只允许使用合成测试 namespace 验证状态机与拒绝行为，不构成任何生产白名单批准。

## 3. 理由

- **隔离先于过滤。** 把 proposal 与 fact 建模为独立聚合，读路径与投影的隔离是结构性的（不同表、不同事件、不同类型），不依赖每个调用点都记得加 `WHERE status != 'pending'`；单一表加状态过滤的实现只要一处遗漏即把未确认推断泄入长期记忆，且无法用契约测试穷举调用点。
- **状态少、原因归 reason_code。** `pending_conflict` 表达的是"为什么还在 pending"，不是生命周期阶段；若升级为状态，则状态×原因组合爆炸，事件契约与 UI 语义都会歧义。同理，"过期"对事实而言与撤销、策略失效是同一种投影语义（移除 + tombstone，设计规格 §16），单独设 `expired` 状态只会给防复活与一致性增加第三套分支。
- **confidence 不是授权。** 模型置信度衡量的是模型自评，不承担来源可追溯、类型合法、敏感度合规与冲突消解中的任何一项；把阈值作为自动确认开关，等于把幻觉风险乘以长期记忆的持久性。三条确认路径分别锚定"规则可复核""服务端可验证""用户可追责"，共同前提才是 §6 的准入要求。
- **CAS + 幂等 + 同事务审计**让确认操作在 at-least-once 重试、响应丢失与并发竞争下恰好产生一次副作用，且每次状态变化可沿 revision 链审计到 actor、来源与 reason code（设计规格 §8.4、§8.11）。
- **"临时覆盖不写回"**同时保住两个目标：本轮对话不被陈旧记忆拖偏（信任顺序），长期记忆不被未消解的冲突静默改写（用户意图）；消解途径是显式的 supersede 命令或 `pending_conflict` proposal，而不是隐式覆盖。
- **空白名单 fail-closed**把"哪些内容允许长期存在"变成显式审批对象（G5）：批准前系统退化为"只有 working memory / episodic / procedure 可用"的降级形态，而不是带着未审定的默认白名单上线。

## 4. 备选方案与拒绝理由

| 备选 | 拒绝理由 |
|---|---|
| LLM confidence ≥ 阈值即自动确认 | 违反设计规格 §1.5、§3 非目标与 README 不变量 3；confidence 不承担 provenance、类型、敏感度与冲突判断；阈值无法消除幻觉，反而将其固化进长期记忆并进入 Prompt（§19.2 错误记忆率门禁不可控）。 |
| 把 `pending_conflict` 作为独立状态 | 状态与原因正交性破坏（设计规格 §8.5 明确其为 reason_code）；"等待用户"与"因冲突等待"语义耦合，状态机与事件契约膨胀，UI 与 API 语义歧义。 |
| 为 fact 引入 `expired` 状态 | 设计规格 §16 已定：到期转 `revoked(reason_code=expired)`；过期与撤销/失效对投影是同一类 tombstone 语义，多一个状态只增加防复活与读取过滤分支。 |
| 冲突 last-write-wins 静默覆盖 | 违反设计规格 §12.4 与不可变原则 6（版本化而非覆盖）；不可解释、不可审计，且允许低信任来源覆盖高信任事实。 |
| 当前请求冲突值直接写回长期事实 | 设计规格 §12.4 明确禁止：本轮可用，冲突未解决不得静默改写；写回等于把未消解冲突冒充用户确认。 |
| proposal 与 fact 同表，用 status/kind 列区分 | 隔离退化为查询过滤，任何遗漏即泄露（实施计划 §10.3"proposal 永不出现在 facts read"门禁失效）；唯一键、TTL、事件契约、审计语义完全不同。 |
| 拒绝 proposal 时隐式撤销对应旧 fact | 设计规格 §12.4/§14.3：拒绝候选不改变已有 fact；隐式撤销把一次 UI 误操作放大为数据丢失。 |
| 服务端签名事件免白名单直接 confirmed | 签名只证明来源身份，不证明该 namespace/key 允许持久化；类型、敏感度、denylist 与冲突检查对服务端事件同样适用（设计规格 §12.3 准入为合取条件）。 |
| 靠事件顺序或投影延迟写阻止删除/过期后复活 | 同聚合根外无全局顺序（设计规格 §13.1、ADR 0001 §5.4）；必须用持久 fence + tombstone + revision 条件写 + 回执（ADR 0001 §8、ADR 0002 §9/§10）。 |
| 以"提取时去重"替代确认前的冲突检查 | 去重只处理相同内容；冲突是语义层的（同 key 不同值），必须走 §12.4 状态机，且需在确认时刻重验（proposal 存续期间事实可能已变化）。 |

## 5. 状态机

### 5.1 proposal（设计规格 §8.5、实施计划 §13.3）

```text
                 confirm（用户/授权审核者显式命令；白名单 namespace；
                          §6 全部准入通过；expected status/version 匹配）
pending ─────────────────────────────────────────────────▶ confirmed（terminal）
   │─── reject（用户/授权审核者显式命令）─────────────────▶ rejected（terminal）
   └─── TTL 到期（scheduler，基线 7 天，§16）─────────────▶ expired（terminal）

pending 可携带 reason_code（如 pending_conflict）；reason_code 不改变状态集合。
confirmed/rejected/expired 均不可逆；过期或已拒绝 proposal 不得确认（§14.3，稳定 409）。
```

- proposal 不属于可读长期事实；仅本人或授权审核者可查看未过期 pending proposal（`GET /v1/subjects/{ref}/proposals`，设计规格 §14.3）。
- 事件对应：创建 `memory.proposal.created.v1`；三种终态迁移均经 `memory.proposal.resolved.v1` 表达（resolution 与 reason code 的 payload schema 在 Task 2 事件契约冻结，设计规格 §13.2）。

### 5.2 fact（设计规格 §8.3、§12.4、§16）

```text
              被 explicit different value 替换（同事务 supersede）
confirmed ───────────────────────────────────────────────▶ superseded（terminal）
   │── revoke：显式撤销 / 策略或来源失效 / 到期
   │        （reason_code 区分成因；到期 reason_code=expired）──▶ revoked（terminal）
   └── 用户删除（deletion fence + tombstone）─────────────▶ deleted（terminal）

无 `expired` 状态；terminal 状态不可回到 confirmed；
重新启用 = 新 fact/revision 链（新 fact_ref 或新 revision，旧记录保留审计）。
```

- 事件对应：`memory.fact.confirmed.v1`、`memory.fact.superseded.v1`、`memory.fact.revoked.v1`、`memory.fact.deleted.v1`（设计规格 §13.2），每次 fact 状态变化恰好一条 fact revision 审计 + 对应事件，与 canonical 变更同事务（D6）。
- 活跃唯一键 `(tenant_ref, subject_ref, namespace, fact_key) where status=confirmed`（设计规格 §8.3）：同 key 第二条 active confirmed fact 在结构上不可插入；若因绕过唯一键的路径出现两条，读取按不变量破坏 fail closed（D5）。

### 5.3 确认路径的事务形状

proposal `:confirm` 成功 = 同一 PG 事务内：proposal 行状态 CAS（pending→confirmed）+ 创建 confirmed fact + fact revision + `memory.proposal.resolved.v1` 与 `memory.fact.confirmed.v1` 两个 outbox 事件 + 幂等账本安全结果（ADR 0001 §5.1 边界，设计规格 §12.2 步骤 8/9）。无 proposal 的 `facts:confirm`/替换 = 同一事务内锁定 active key、记录 provenance、supersede 旧 fact、新 fact/revision、`memory.fact.superseded.v1` + `memory.fact.confirmed.v1`（或首个 confirmed）、outbox 与幂等结果（设计规格 §14.3）。

## 6. confirmed 准入要求（字段级策略）

以下要求对三条确认路径（D3）**合取**适用，全部满足才可确认；任一不满足 ⇒ 保持 pending 或按稳定 reason code 拒绝：

| 维度 | 约束 | 上游依据 |
|---|---|---|
| namespace/key 白名单 | 仅 G5 批准的 namespace/key 可存在 confirmed fact（无论确认来源）；当前白名单为空（D9）；白名单须区分"仅可显式确认"与"可自动确认"两级资格，未标注自动确认资格者只能走显式确认 | 设计规格 §21.5、实施计划 §23.2 |
| typed value schema | `value_type + value_json` 必须通过对应 namespace 的 JSON Schema（`contracts/jsonschema/fact-values/*`，Task 7 交付）；无 schema 的 namespace 不可确认 | 实施计划 §13.2/§13.4 |
| 数据来源（source_kind） | 事实必须记录确认来源类别，且只能落在 D3 三类之内；source_kind 枚举值在 Task 2 契约冻结；模型/extractor 不是合法确认来源 | 设计规格 §8.3、§12.3 |
| 来源证据（provenance） | evidence event refs、source_event_ref、extractor/model 版本（proposal 侧）、`confirmed_by/confirmed_at`（fact 侧）完整；provenance 不完整不可确认；extraction/model version 入 provenance | 设计规格 §8.3/§8.5/§12.3、实施计划 §14.4 |
| 信任（trust_level） | 值域在 Task 2 契约冻结，必须能表达设计规格 §6.2 信任顺序中事实相关层级；低信任内容不得覆盖高信任内容 | 设计规格 §6.2、§10.3 |
| 敏感度（sensitivity_class） | 字段级分类；namespace→sensitivity 映射与 `docs/data-classification.md`、G5 评审联动；高敏 namespace 内容禁止进入 ES/Qdrant，中敏禁止外发第三方 embedding（约束投影准入，归 ADR 0002 执行） | 设计规格 §17.2、ADR 0002 §6 |
| 一次性敏感值 denylist | 最低集合（设计规格 §12.3，已确认）：LLM 单独推断、一次性数值、临时目标、预算、价格、库存阈值、凭据、自由文本秘密——不得自动确认；具体条目扩充属 G5/data-classification 范围；Task 7 测试含 forbidden temporary values | 设计规格 §12.3、实施计划 §13.5 |
| TTL / revalidate | pending proposal 基线 7 天，到期转 expired（§16）；confirmed facts 按 namespace 设置 `valid_from/valid_to/expires_at`（preference 类基线：按 namespace 定期重确认或 365 天）；读取侧与确认时刻都执行过期检查；revalidate/重确认策略按 namespace 配置 | 设计规格 §8.3、§16 |
| 无未解决冲突 | 确认时刻重验；其他未消解的 `pending_conflict` 不得被静默忽略。若本次显式确认正是解决该冲突，须在同一事务内校验 proposal 与当前 active fact 的预期版本、完成 supersede/确认并关闭冲突 | 设计规格 §12.3、§14.3 |
| 身份与授权 | 确认者（用户/授权审核者）身份从已验证身份/代理授权派生；无已验证身份的命令一律拒绝（G4 未关闭前不开放业务 API） | 设计规格 §17.1、实施计划 §10.3 |

## 7. confirmed 的形成路径与模型边界

### 7.1 三条路径（对应 D3）

1. **白名单确定性规则：** 输入是当前用户明确陈述，规则解析无歧义，namespace/key 在 G5 白名单且标注自动确认资格，value 满足 §6 全部约束（设计规格 §12.3）。规则层必须可重放、确定性（相同输入相同输出，实施计划 §14.5 replay determinism）。
2. **可信服务端签名事件：** 事件签名/身份验证通过、source kind 在允许清单内，其余 §6 约束同样适用（设计规格 §12.3、实施计划 §13.4）。
3. **显式确认：** 用户/授权审核者经 `POST /v1/proposals/{proposal_ref}:confirm` 或 `POST /v1/subjects/{ref}/facts:confirm` 显式提交；首版确认 UX 即通用 API 显式 `confirm`/`revoke`（设计规格 §1.13），设置页与审核台后置。

### 7.2 模型边界

- 模型 extractor 是异步消费者：输出严格 schema 的 pending proposal，带 evidence event refs 与 extractor/model 版本（设计规格 §12.2 步骤 4/8，实施计划 §14.3/§14.4）；
- 模型 proposal 只能经用户/授权审核者显式确认升级（实施计划 §13.4"模型来源只能在用户/授权审核者明确确认后升级"）；
- confidence 只作为 proposal 排序/展示参考，不是 §6 任一准入要求的替代；不存在"高 confidence 自动通过"的路径，本 ADR 与任何文档不得暗示或实现之；
- 模型不可用或无法判断时不写（实施计划 §14.4）；模型版本变更产生 re-evaluation path，但不静默修改已 confirmed 事实（实施计划 §14.5）；
- 对抗性 prompt injection 不得经由提取管线产生 confirmed fact（实施计划 §14.5；§19.2"未确认 proposal 进入 Prompt = 0"）。

### 7.3 自动确认开关的 fail-closed 实现

`internal/policy/auto_confirm.go`（Task 8 文件）在 G5 关闭前必须等价于"永不自动确认"：白名单为空时规则层不产出 confirmed，服务端事件路径对未列入允许 source kind/namespace 的事件只产 proposal 或拒绝（实施计划 §14.4"自动确认仅限 ADR 白名单"）。该开关的打开唯一依据是 G5 批准文件，不接受代码内默认值或环境变量旁路。

## 8. 读路径可见性与冲突处理

### 8.1 可见性谓词（D5 的操作化）

facts read / MemoryPack confirmed facts 区返回且仅返回：`status=confirmed` ∧ 未过期 ∧ 授权匹配 ∧ 未被主体 deletion fence 屏蔽的记录（设计规格 §14.3、§17.4，实施计划 §10.3）。补充投影（ES/Qdrant/Redis）中的候选在进入 MemoryPack 前仍须按 `memory_ref + canonical_version` 批量回查 PG 复核同样谓词（ADR 0002 §12.3，README 不变量 4）。

### 8.2 冲突状态机（设计规格 §12.4，逐条固化）

| 输入 | 行为 |
|---|---|
| new explicit same value | refresh provenance / TTL（不产生新语义版本冲突） |
| new explicit different value | 同一事务 supersede 旧 fact（D6 事务形状） |
| ambiguous different value | proposal(status=`pending`, reason_code=`pending_conflict`)，不写任何 fact |
| user rejects proposal | proposal 转 rejected；**不隐式撤销已确认事实** |
| explicit revoke / policy or source invalidated / expiry | fact → revoked（reason_code 区分成因；到期 = `expired`） |
| user deletion | fact → deleted + tombstone（fence + 防复活，§10） |

### 8.3 当前请求：临时覆盖，不写回

冲突未解决时，当前请求可在本轮使用显式新值（信任顺序：当前请求显式值高于历史 confirmed facts，设计规格 §6.2/§10.3、README 不变量 5），并产生 reconciliation signal；但该值不得写回长期事实——消解途径仅为显式 supersede 命令或 `pending_conflict` proposal 的后续处理（设计规格 §12.4"current request 可在本轮使用显式新值，但不能在冲突未解决时静默改写长期事实"）。

### 8.4 系统策略与授权不可覆盖

任何事实、当前请求值或召回内容都不得覆盖系统策略与授权（设计规格 §5.1、§3 非目标）；system policy 不由 MemoryPack 提供，由应用在 MemoryPack 之外强制（设计规格 §17.3，实施计划 §17.4）。确认命令自身的授权校验不可被历史事实或 proposal 内容放宽。

### 8.5 不变量破坏

同一 key 出现两条 active confirmed fact（唯一键被绕过）或已知语义冲突未消解即进入读取结果，视为数据不变量被破坏：fail closed 并报警（设计规格 §10.3，ADR 0001 §10）。

## 9. 显式命令契约

| 命令 | ref 语义 | 并发/幂等约束 |
|---|---|---|
| `POST /v1/proposals/{proposal_ref}:confirm` | proposal_ref + expected proposal status/version | 过期/已拒绝 proposal 不得确认（稳定 409）；成功时同事务产生 confirmed fact（§5.3） |
| `POST /v1/proposals/{proposal_ref}:reject` | proposal_ref + expected status/version | 只改 proposal，不触碰任何 fact |
| `POST /v1/subjects/{ref}/facts:confirm` | expected active fact_ref + revision，或声明 expected absence | 无 active fact 而未声明 absence ⇒ 409；替换时事务内锁定 active key（设计规格 §14.3） |
| `POST /v1/facts/{fact_ref}:revoke` | fact_ref + expected revision | 仅现存 confirmed fact；revision conflict 返回稳定 409（实施计划 §10.4） |

- 全部命令要求：已验证身份/所有权、§8.11 持久 Idempotency-Key（同 key 同 digest 返回原安全结果——含 commit 成功但响应丢失后的重试；不同 digest 409；并发同 key 由数据库唯一约束/锁串行化）、namespace/sensitivity/typed schema 校验（设计规格 §14.3、§8.11）。
- `proposal_ref` 与 `fact_ref` 是不同聚合的 opaque ref，不混用；Task 4 不以 fact_ref 代表尚不存在的 proposal（实施计划 §10.2），proposal 确认/拒绝与无 proposal 显式确认接口在 Task 7 交付（实施计划 §10.2/§13）。
- 审计：fact 的每次状态变化写一条 INSERT-only `memory_fact_revisions`（before/after 安全投影或 hash、event_kind、actor_ref、reason_code、source_event_ref）与对应 outbox 事件；proposal 状态变化推进自身 revision 并写 resolution outbox 事件。两者都与 canonical 变更同事务；fact revision 与事件内容不可改写，outbox 交付状态可按 relay 协议更新（设计规格 §8.4–§8.5，ADR 0001 §5.4）。
- 错误只返回稳定 reason code，不回显底层异常或输入值（设计规格 §17.2）；reason code 与 metrics 不得泄露事实值（实施计划 §13.6）。

## 10. TTL、过期与删除防复活

- **TTL 基线（设计规格 §16，首轮规划基线）：** pending proposal 7 天，到期转 `expired`；confirmed facts 按 namespace（preference 类：定期重确认或 365 天基线）；具体 namespace TTL 属 G5/保留策略评审范围，本 ADR 不新增数值。
- **到期流程：** scheduler 触发到期处理 → 到期 proposal 转 `expired` 并写 resolution 事件；到期 confirmed fact 转 `revoked(reason_code=expired)` 并写 tombstone outbox → fact 投影独立清理并出具持久回执（含确认无记录的 no-op）（设计规格 §16，实施计划 §18.3）。
- **防复活：** tombstone 优先于旧事件；迟到的 fact upsert/重放/重建不得覆盖 revoked/deleted 状态或复活记录——依据是 PG 当前 fence + canonical revision 条件写 + 持久回执，不是事件顺序或投影内残留版本（设计规格 §18.3，ADR 0001 §8，ADR 0002 §9/§10）。Redis TTL 到期不等于 canonical 删除；投影删除也不等于事实删除（设计规格 §16）。
- 用户删除走 Task 4/§10 的原子 fence + tombstone 入口，事实级 `deleted` 状态与主体删除命令同事务或由删除流程驱动；"删除复活 = 0" 门禁适用于事实与投影（设计规格 §19.2）。

## 11. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 模型幻觉固化为长期事实 | D1/D3 结构隔离 + 模型 proposal 必经显式确认（确定性规则与可信事件走各自独立准入路径）+ "model proposal 进入 Prompt = 0"自动化门禁（Task 8/11，§19.2） |
| 白名单过宽（错误内容可确认）或过窄（业务不可用） | G5 由 Product + Security 审批并区分显式/自动确认资格；空白名单期间接受降级（D9）；白名单变更走评审修订本 ADR |
| `pending_conflict` 被误实现为状态或 fact `expired` 被实现为状态 | Task 2 域契约测试（非法状态迁移被拒绝，实施计划 §8.4）+ Task 7 状态机门禁（§12） |
| 冲突静默覆盖 / 高信任被低信任覆盖 | §8.2 状态机逐条测试 + 信任顺序 priority matrix（Task 11）+ revision 审计可回放 |
| 当前请求值被误写回长期事实 | §8.3 约束 + Task 8 "current temporary value" 与 Task 7 same-value/explicit-different 分路径测试 |
| 确认/拒绝/撤销的重复重试产生双重副作用 | §8.11 幂等账本 + proposal/fact 双重 CAS + 同事务幂等结果（Task 7 duplicate confirm 测试，含响应丢失重试） |
| proposal 过期与 confirm 竞态 | confirm 在同事务内校验 expected status + `expires_at`；竞态失败返回稳定 409（§14.3） |
| 一次性敏感值进入长期记忆 | §6 denylist 最低集合 + Task 7 forbidden temporary values + Task 6 "不继承一次性敏感槽位" + 投影层敏感度 gate（ADR 0002 §6/§8） |
| 状态迁移遗漏 revision/事件（审计断裂） | "每个状态变化恰好一条 revision/event"验收（实施计划 §13.6）+ INSERT-only 约束（Task 3 门禁） |
| reason code / metrics 泄露事实值 | 稳定 reason code 清单 + metrics 不含值断言（实施计划 §13.6，设计规格 §17.2） |
| 两条 active confirmed fact 并发写入 | 活跃唯一键 + 事务内锁/CAS（设计规格 §8.3，Task 3 并发 CAS 门禁）；绕过即 fail closed（§8.5） |
| 本 ADR 被当作白名单或数值批准 | §13 明示 G5/G4/G9 未批准与 fail-closed 默认；本 ADR 不批准任何决策 |

## 12. Task 7/8/11 门禁（本 ADR 的落地验收）

前置说明：G5 关闭前，确认能力只可在合成测试 namespace 上验证状态机与 fail-closed 拒绝行为；G4 关闭前不开放业务 API；G9 关闭前 Task 11 的数值验收不可能通过，对应能力不得进入灰度/发布（实施计划 §23.2，ADR 0002 §15 同口径）。以上任一门禁未通过，不得进入依赖该能力的后续 Task（实施计划 §2.1）。

**Task 7 — Confirmed Facts、Proposal 与冲突状态机（实施计划 §13）：**

- proposal 状态机：`pending -> confirmed | rejected | expired`，terminal 不可逆；`pending_conflict` 以 reason_code 表达且不改变 pending 状态；fact 状态机：`confirmed -> superseded | revoked | deleted`，到期转 `revoked(reason_code=expired)`，无 `expired` 状态；
- same-value refresh；explicit different value 同事务 supersede；ambiguous → proposal(pending, pending_conflict) 且不写 fact；
- `proposal_ref` 与 `fact_ref` 不混用；proposal 列表权限（本人/授权审核者）；过期/已拒绝 proposal 确认失败；直接 `facts:confirm` 的 expected absence 与旧 fact revision 均 409；stale revision CAS 失败；
- duplicate confirm 幂等（含 HTTP 响应丢失后的同 Idempotency-Key 重试返回原结果、不同 payload 409）；拒绝 proposal 不撤销旧 fact；
- **model proposal cannot auto-confirm**（规则与服务端事件路径外，任何模型来源确认被拒绝）；forbidden temporary values（§6 denylist 最低集合）；TTL/revalidate 到期行为；revoke/delete 后 facts read 不可见；full revision audit（每次状态变化恰一条 revision/event，INSERT-only）；
- 验收：read path 永远只看到 confirmed；冲突不静默覆盖；reason code 和 metrics 不泄露值。

**Task 8 — 异步提取与 Proposal Worker（实施计划 §14）：**

- deterministic extractor + optional model extractor；模型输出严格 schema、只产 proposal（spec §12.2 步骤 4）；
- **model proposal 进入 Prompt 数量恒为 0**；G5 未关闭期间 `auto_confirm` 恒不自动确认（§7.3）；
- adversarial prompt injection 不产生 confirmed fact；schema invalid 输出丢弃；duplicate/乱序/重放事件不产生重复 confirmed fact（Phase 3 退出条件，设计规格 §20）；replay determinism（规则层）；model version change 产生 re-evaluation path 但不静默修改 confirmed；
- PII/secret 扫描先于模型调用与持久化；无法判断时不写；不保存隐藏推理；
- 验收：主 append 延迟不包含提取；模型关闭时规则提取仍可工作；extraction lag/失败/DLQ 可观测。

**Task 11 — MemoryPack 组装与混合排序（实施计划 §17）：**

- 未确认 proposal、敏感内容、跨租户内容进入 MemoryPack 数量为 0；
- conflict matrix（§8.2 全表）与 priority matrix（当前请求覆盖历史事实；系统策略不经 MemoryPack；低信任不覆盖高信任）；
- revoked/deleted/expired 事实不可见（含主体删除中不返回旧事实）；stale hit 经 canonical revalidation 丢弃；两条 confirmed facts 冲突 fail closed；
- 当前请求临时值不写回：冲突未解决时本轮可用、下轮事实读取仍为旧值（与 Task 7/8 测试衔接）；
- 验收：precision@5/recall@10/nDCG@10 达到 Task 0 批准的绝对门槛与相对词法基线改善、错误记忆率低于批准上限——**G9 未批准前该验收不可能通过，对应能力不得进入灰度/发布**。

## 13. 待 owner 批准项与 fail-closed 默认

以下事项**尚未获得 owner 批准**，本 ADR 不为其编造决定、不列任何具体 namespace/key、字段或数值；仅登记 owner、最晚关闭点与未关闭时的 fail-closed 默认（与实施计划 §23.2、ADR 0001 §14、ADR 0002 §17 对应项一致）：

| # | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 |
|---|---|---|---|---|
| G5 | confirmed namespace/key 白名单，含：每个 namespace/key 的确认资格与是否具备自动确认资格；各 namespace 的 typed value schema（`contracts/jsonschema/fact-values/*`）；一次性敏感值 denylist 具体条目；namespace→sensitivity_class 映射（与 `docs/data-classification.md` 联动）；各 namespace TTL/重确认策略 | Product + Security | Task 7 前 | 白名单为空：proposal 不得转 confirmed（实施计划 §23.2）；`:confirm`/`facts:confirm`/自动确认对一切生产 namespace/key 按稳定 reason code 拒绝；`internal/policy/auto_confirm.go` 恒不自动确认；测试仅可用合成 namespace |
| G4 | tenant/subject identity 签发者、JWT/JWKS 与服务代理授权契约（确认者与授权审核者的身份/授权依据） | Security + API Owner | Task 2 前 | 不开放业务 API；无已验证身份的 confirm/reject/revoke 命令一律拒绝 |
| G9 | 评测数值门禁（precision@5、recall@10、nDCG@10、错误记忆率上限、删除传播 p95/p99 等，必须为批准数字） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；Task 11 数值验收在关闭前不可能通过，对应能力不得灰度/发布 |

**本 ADR 不批准任何决策**：§2 的 D1–D9 是对上游文档已确认基线的架构固化；G5 白名单内容、自动确认字段、typed schema 清单、denylist 条目与数值阈值均须按上表由 owner 批准后方可启用。

已确认、无需再批准的本 ADR 基线（依据见 §2）：只允许 confirmed 用户事实参与长期记忆读取、模型推断不得直接成为已确认事实（设计规格 §1.5）；通用 API 显式 `confirm`/`revoke` 确认 UX（§1.13）；两套状态机与 reason_code 语义（§8.3/§8.5/§12.4/§16、实施计划 §13.3）；准入要求合取集与 denylist 最低集合（§12.3）；proposal 7 天 / preference 365 天 TTL 基线（§16）；pending proposal 7 天短 TTL 且绝不进入 confirmed_user_facts（§8.5）。

## 14. 与其他 ADR 的关系

- **ADR 0001（canonical 与事件骨干）：** 本 ADR 继承其 §5.1 事务边界（canonical 变更 + revision + outbox + 幂等结果同事务）、D8（模型抽取只产 proposal，准入白名单与冲突状态机归本 ADR）与 §8 删除 fence/epoch 机制；其 §13 亦声明此分工。冲突时以 ADR 0001 与上游文档为准。
- **ADR 0002（search/vector 投影）：** 本 ADR 的 namespace/key 白名单与 sensitivity 分类决定哪些 confirmed fact 具备"可公开索引的文本投影"资格（ADR 0002 §16）；proposal 任何状态不进入任何投影（ADR 0002 §5）；事实 tombstone 的投影防复活由 ADR 0002 §9/§10 机制执行，本 ADR 只定义 canonical 侧状态与事件语义。
- **ADR 0004（Go service stack）：** 域类型必须能表达 FactStatus/ProposalStatus/reason_code/trust/sensitivity 契约（实施计划 §8.3）；不得引入违反 D1/D3 隔离或 D6 事务边界的依赖。

## 15. 后果

**正面：**

- 模型污染长期记忆在结构上不可能：提案与事实分表、分事件、分读路径，配合"进入 Prompt = 0"门禁形成三重防线；
- 冲突可解释、可审计、可恢复：revision 链 + reason_code + 显式 supersede/reject 命令，无静默覆盖；
- "哪些内容允许长期存在"成为显式审批对象（G5），fail-closed 默认下不存在未经批准的默认白名单；
- 状态机小而正交（3+1 proposal 状态、1+3 fact 状态，原因归 reason_code），事件契约与投影语义稳定。

**负面/代价：**

- 空白名单期间长期事实记忆整体不可用（提取管线只能产 pending），这是有意的降级成本；
- 首版确认 UX 为通用 API，用户/审核者需显式 `confirm`，无设置页与审核台（设计规格 §1.13 后置），pending_conflict 的消解依赖澄清流程与人工处理，冲突率需按 §19.1 监控；
- 状态机、CAS、幂等与审计的契约测试面较大（Task 7/8/11 门禁规模可见）；
- 每次确认都多一跳同事务审计与事件写入，写放大是可审计性的必要成本。

**修订规则：** 本 ADR 的任何修改不得违反设计规格不可变原则（§5）、README 核心不变量与 ADR 0001 骨干决策；冲突时先修订上游文档并走 Task 0 评审，再同步本 ADR。
