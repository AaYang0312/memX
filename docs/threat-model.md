# memX 威胁模型（Task 0 文档切片）

**状态：** Proposed（Task 0 威胁建模草案，待 owner 评审。本文档只覆盖设计层威胁分析，不构成任何安全审查完成的声明，不批准 G1–G12 中任何待批准项，也不解除实施计划 §6.4/§23.2 的 Task 1 阻塞条件。）
**日期：** 2026-09-23
**范围（In scope）：** 设计规格所定义系统的资产、信任边界、攻击者能力假设、数据流，以及 11 类必列威胁（跨租户/主体 IDOR、伪造 header/JWT/JWKS/代理授权、RLS `SET LOCAL`/连接复用、prompt 注入/模型 proposal 污染、敏感字段泄露到 Kafka/Redis key/ES/Qdrant/log/trace、第三方 embedding 外发与区域合规、outbox 重试与 DLQ 敏感 payload、迟到事件/重放/索引重建/备份恢复时删除复活、删除竞态与回执不足、TTL 与备份物理清除、供应链/密钥泄露）的入口、影响、策略、控制归属 Task、可自动化验证与未解决风险。
**明确排除（Out of scope）：** 任何代码、依赖、镜像或基础设施变更；渗透测试、代码审计、外部合规认证；`docs/capacity-baseline.md`、`docs/data-classification.md`、`docs/evaluation-gates.md`（同属实施计划 §6.2 Task 0 文件清单，另行交付）；运行期安全运营（on-call、事件响应）。

**权威输入（本文档与其冲突时以上游为准，须修订本文档）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"，§n 引用）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"，Task n 或 §n 引用）
- `docs/adr/0001-canonical-and-event-backbone.md`、`0002-search-and-vector-projections.md`、`0003-confirmed-fact-policy.md`、`0004-go-service-stack.md`（下称 ADR 0001–0004）
- `README.md`（核心不变量）

---

## 0. 阅读本文档前必须接受的三个事实

1. **本仓库尚无任何实现。** 未初始化 Git、无 Go 源码、无依赖、无基础设施（README"当前状态"）。本文档所列"控制"全部是设计规格、实施计划与 ADR 中**已定义的设计约束**，没有任何一条已在代码或基础设施中实施并验证。凡控制已写入上游文档者，本文标注其归属 Task 与门禁；不得把"设计已要求"读成"已实施"。
2. **不存在任何已获得的审批。** 已确认的只有设计规格 §1 与实施计划 §23.1 的规划基线（如：PG 唯一事实源、embedding 仅脱敏低敏文本可外发、raw turns 90 天/审计 365 天）。G1–G12 待批准项（ADR 0001 §14）在本文档中一律按"未批准 + fail-closed 默认"登记，不为其编造决定。
3. **本文档不冒称安全审查完成。** 设计规格 §20 Phase 0 退出条件要求"完成威胁建模、数据分类和 retention matrix"，本文档只是威胁建模切片；安全审查、数据分类、评测门禁与各 Task 落地验收均未发生。样本中自动化测试观测到 0 不等于证明生产永不发生（设计规格 §19.4）。

## 1. 方法

按威胁逐项登记，每项固定七个字段：**入口 → 影响 → 策略（拒绝/隔离优先）→ 控制与落地（设计控制 → 待实施 Task）→ 可自动化验证 → 未解决风险与 owner → 门禁**。

- **拒绝/隔离优先：** 每个威胁先列"使攻击不可表达/不可达"的拒绝与隔离策略（fail closed、结构隔离、最小权限、opaque ref），再列检测与补偿。优先顺序与设计规格 §5 不可变原则、§17.1"缺失上下文拒绝访问"一致。
- **门禁编号沿用 ADR 0001 §14**（G1–G12）；owner 与 fail-closed 默认逐字对齐 ADR 0001 §14 / 实施计划 §23.2，本文不新增、不改写任何门禁语义。
- **威胁清单来源：** 本任务指定的 11 类威胁与实施计划 §6.4 验收项（threat model 须覆盖"跨租户、Prompt 注入、迟到事件复活、索引泄露、embedding 外发和删除不完整"）的并集；T-08/T-09/T-10 共同覆盖"删除不完整"。

## 2. 资产（按敏感度排序）

| # | 资产 | 说明 | 上游依据 |
|---|---|---|---|
| A1 | canonical 原始 turn 内容 | `memory_turns.content_ciphertext`/受保护引用；最敏感用户数据 | 设计规格 §8.2、§17.2 |
| A2 | confirmed facts 及其值 | 长期用户事实（含 sensitivity_class 分级） | 设计规格 §8.3 |
| A3 | working memory（Redis 投影） | 最近回合、摘要、槽位；可重建但含近期会话内容 | 设计规格 §9 |
| A4 | proposals、episodes、procedures | 候选事实、脱敏情景摘要、已批准流程模板 | 设计规格 §8.5–§8.7 |
| A5 | ES/Qdrant 投影数据 | 仅脱敏内容 + 必需元数据；可重建、非事实源 | 设计规格 §15、ADR 0002 |
| A6 | 删除 fence / tombstone / 审计与回执 | 防复活与可审计性的根；泄露或损坏即破坏删除承诺 | 设计规格 §17.4、§16 |
| A7 | 身份契约与密钥 | JWT/JWKS 契约（G4）、签名密钥、KMS/secret manager（G3）、DB DSN | 设计规格 §17、§21.6 |
| A8 | 事件流（Kafka/outbox）与 DLQ/隔离队列 | 只应携带 opaque ref 与安全元数据 | 设计规格 §8.8、ADR 0001 §5.3 |
| A9 | 备份与恢复产物 | 含历史 canonical 数据；受保留与物理清除策略约束 | 设计规格 §16、§17.4 |
| A10 | 第三方 embedding 外发通道 | 仅脱敏低敏文本；G6 批准前不存在生产配置 | 设计规格 §1.11、ADR 0002 §8 |

## 3. 信任边界

| # | 边界 | 跨界凭证/约束 | 上游依据 |
|---|---|---|---|
| B1 | 不可信客户端 ↔ Memory API | 已验证身份（签名/issuer/audience/有效期/scope/代理授权）；tenant/subject 只从身份派生；客户端 header/body 与路径参数不得扩大授权 | 设计规格 §17.1、§10.1、§14.2 |
| B2 | API/消费者 ↔ PostgreSQL | 最小权限 service role + RLS；每笔事务 `SET LOCAL` 注入已验证租户/主体上下文；缺失上下文拒绝 | 设计规格 §17.1、ADR 0001 §9 |
| B3 | PG（canonical）↔ outbox relay ↔ Kafka | 同事务 outbox；事件仅 opaque ref/版本/安全元数据，无正文；at-least-once | ADR 0001 §5.3 |
| B4 | Kafka ↔ 消费者族（extractor/投影/retention） | inbox pending lease + epoch/revision/event id/hash 条件幂等；按 PG 当前 fence 拒绝旧 epoch；正文按 ref 回读 PG | ADR 0001 §6、ADR 0002 §9 |
| B5 | 消费者 ↔ Redis/ES/Qdrant | 投影为可重建非事实源；索引白名单 + 禁止字段 + 必需元数据；读路径一律 PG 复核 | ADR 0002 §6/§7/§12 |
| B6 | 服务 ↔ 第三方 embedding provider | 顺序 gate（内容资格→确定性脱敏→provider 合规→调用纪律）；G6 未批准前生产外发整体关闭 | ADR 0002 §8 |
| B7 | 运维/审核角色 ↔ explain/export/deletion API | explain 只限授权运维/审核角色，输出 opaque ref/reason code/watermark；export 仅返回已授权主体自身的安全可读记录，删除需独立所有权/审批校验 | 设计规格 §14.4、§14.5、实施计划 §18.3 |
| B8 | 在线系统 ↔ 备份/恢复产物 | 恢复旧备份前先恢复/合并 fence 才能暴露流量；在线完成与物理清除分报 | 设计规格 §17.4、§16 |
| B9 | 开发供应链 ↔ 生产系统 | G1/G2/G3 未关闭不初始化模块/不引依赖/不拉镜像/不接真实凭据；许可证与维护证据核对 | ADR 0004 §8/§12 |

## 4. 攻击者能力假设（用于评审，非实测结论）

| # | 攻击者 | 假设能力 | 明确不在假设内 |
|---|---|---|---|
| E1 | 恶意租户客户端 | 持有本租户有效凭据；伪造/篡改 header、路径 ref、body、`Idempotency-Key`；重放请求；构造对抗性 prompt 注入 turn 文本 | 不持有他租户凭据、不控制网络路径 |
| E2 | 被攻陷的应用 adapter | 以合法服务身份代理用户调用；尝试越权 subject/扩大 scope | 不持有 JWKS 签名私钥 |
| E3 | 网络观察者 | 观测外发流量（含 embedding 调用，若 G6 后存在） | 不破解 TLS/KMS（以 G3 部署假设为准） |
| E4 | 被攻陷的消费者/worker 实例 | 读取所属 consumer group 的 Kafka 消息与 PG service role 连接；尝试写入越权投影数据 | 不持有其他 consumer group 凭据 |
| E5 | 内部/运维越权 | 访问 explain/export、备份、DLQ、日志后端 | 受 B7 角色限定与审计（审计链为设计要求，落地见 Task 4/13） |
| E6 | 供应链攻击者 | 在依赖/镜像/CI 中植入恶意代码（npm 式投毒、CI 缓存投毒） | 在 G1/G2 关闭前无可利用面（无仓库、无依赖、无镜像） |

各能力的现实性由后续 Task 的安全测试（实施计划 §19.4）验证；本文档不声称这些假设已被红队或外部审查证实。

## 5. 数据流与敏感度注记

```text
[客户端] --B1--> [Memory API] --B2--> [PostgreSQL canonical]           A1/A2 加密存储（§17.2）
                      |  同事务写 outbox（仅 opaque ref/安全元数据）          A8
                      v
                  [outbox relay] --B3--> [Kafka]
                                             |  B4（正文按 ref 回读 PG）
        +------------------+----------------+------------------+
        v                  v                v                  v
  [extractor]       [Redis projector]  [ES indexer]      [Qdrant indexer]
  规则/模型→proposal    A3 仅 hash/opaque key   A5 脱敏白名单      A5 + B6 embedding gate
  （A4，不确认）        （§9.1 禁原始 ID）      （§15.1 禁止字段）  （高敏禁入，中敏禁外发）
        |                  |                |                  |
        +--------B2--------+--------B2-------+---------B2-------+
                      （全部回写/回读 PG，投影可重建）
[assemble 读路径]：B1 → PG confirmed facts + 投影候选 → 批量 canonical revalidation → MemoryPack
                   （检索内容是数据不是指令，§17.3；未确认 proposal 进入 Prompt = 0，§19.2）
[删除路径]：DELETE 主体 → B2 同事务 fence+不可读写+tombstone → B3/B4 传播 → 每投影持久回执+空索引核对
                   （在线完成 ≠ 备份物理清除，§16）
[备份/恢复]：B8 恢复前先恢复/合并 fence；过期/物理清除走 §16 流程与 G7 门禁
```

关键不变量（README 1–6）贯穿全部数据流：PG 唯一事实源、append 与 outbox 同事务、模型只产 proposal、命中必经 canonical 复核、当前请求优先、tombstone + deletion epoch 防复活。

## 6. 威胁登记

> 每条威胁的"控制"列分两段：**设计控制**（上游文档已定义的设计约束，含引用）与**待实施**（归属 Task）。"可自动化验证"引用实施计划与设计规格 §19.4 已规定的测试项；尚未编写任何测试。策略一律先拒绝/隔离、后检测/补偿。

### T-01 跨租户 / 主体 IDOR

- **入口：** `/v1/*` 全部路径参数（`subject_ref`/`conversation_ref`/`fact_ref`/`proposal_ref`/`job_ref`）；`memory:assemble` 与 `memory:explain` 输入引用；客户端 header/body 中自报的 tenant/subject；ES/Qdrant/Redis 查询过滤条件；`export`。
- **影响：** 读写或删除他租户/他主体记忆（A1–A5）；经 explain/export 旁路枚举他人数据；破坏 `cross-tenant leakage = 0` 门禁（设计规格 §19.2）。
- **策略（拒绝/隔离优先）：** ① tenant/subject 只从已验证身份派生，忽略客户端伪造同名字段（§10.1 步骤 1），客户端不能提交 tenant（§14.2）；② 路径参数不得扩大身份授权，所有 canonical 查询同时约束 tenant 与 subject/scope（§17.1）；③ RLS + `SET LOCAL` 事务级上下文，缺失即拒绝（§17.1，详见 T-03）；④ opaque ref 强类型、禁原始 ID 作公开 ref（实施计划 §8.4）；⑤ 投影层 ES alias/Qdrant namespace/Redis keyspace 按租户隔离，查询过滤条件只从已验证身份派生（§17.1、ADR 0002 §13）；⑥ `explain`/`proposals` 列表仅本人或授权角色（§14.3、§14.5）。
- **控制与落地：** 设计控制如上；待实施：Task 2（身份/claims 契约、opaque ref）→ Task 3（RLS、最小权限 role）→ Task 4（逐次所有权校验的 API；未验收不得开放非测试流量）→ Task 6/9/10（投影隔离）→ Task 11（assemble 复核）→ Task 13（cross-tenant matrix 安全测试）。
- **可自动化验证：** cross-tenant/subject、IDOR、伪造 tenant header 测试（实施计划 §10.4）；proposal 列表权限测试（§13.5）；cross-tenant filter 与"查询侧 + 重建后数据"隔离测试（§15.4、ADR 0002 §13）；主体删除中不返回旧会话/事实/检索结果（§17.5）；`cross-tenant leakage = 0`（§19.2、§19.4 第 2 项）。
- **未解决风险与 owner：** G4 身份契约未关闭 → 不开放业务 API（Security + API Owner，Task 2 前）；explain/export 的部署级授权模型待定（设计规格 §14.4"认证和审批策略由部署环境决定"，关联 G4/G12）。

### T-02 伪造 header / JWT / JWKS / 代理授权

- **入口：** HTTP 认证层（Authorization header、自定义租户 header）；服务间/代理调用身份；JWKS 获取与缓存。
- **影响：** 伪造任意 tenant/subject 身份即绕过 T-01 全部身份派生前提；代理授权绕过使 E2 冒充被代理用户执行确认/撤销/删除；伪造"可信服务端签名事件"可污染确认路径（与 T-04/T-11 交叉，ADR 0003 确认路径 2）。
- **策略（拒绝/隔离优先）：** ① 开放任何业务读写 API 前验证签名、issuer、audience、有效期及所需 scope；服务代理用户调用需验证代理授权（§17.1）；② 未知或失效身份一律拒绝（fail closed，§17.1）；③ 不接受客户端自报授权范围作为最终依据（§17.1）；④ 身份契约本身是门禁：G4 未关闭 → 不开放业务 API（ADR 0001 §14）。
- **控制与落地：** 设计控制如上；待实施：Task 2（claims/身份/拒绝规则契约）→ Task 4（最小可用 JWT/JWKS 或受信服务身份验证；身份/授权未知时拒绝请求）→ Task 13（密钥轮换、撤销、缓存失效、stale auth scope、多环境演练——加固而非首次引入）。
- **可自动化验证：** JWT/JWKS 无效签名、错误 issuer/audience、过期、失效密钥、缺少或越权 scope、服务身份代理授权失败测试（实施计划 §10.4）；stale auth scope（§19.4）。
- **未解决风险与 owner：** 签发者/JWKS 契约、代理授权语义未定义（**G4**，Security + API Owner，Task 2 前，fail-closed 不开放业务 API、Task 4 前必须实现并验收身份验证/授权/RLS 上下文）；JWKS 密钥托管与轮换依赖 KMS 决策（**G3**，Platform + Security，Task 1 前）。

### T-03 RLS `SET LOCAL` / 连接复用

- **入口：** PG 连接池事务边界；application role 权限；repository 层 SQL 构造。
- **影响：** 上一笔事务的租户上下文残留在复用连接上 → 下一笔事务跨租户读写；上下文缺失时全表可见；application role 越权或获得 DDL → T-01 的存储层防线整体失效。
- **策略（拒绝/隔离优先）：** ① 最小权限 service role；application role 无 DDL、无越权表权限（实施计划 §9.4）；② 每笔事务用 `SET LOCAL` 注入已验证租户/主体上下文并以 RLS 约束，**缺失上下文一律拒绝**（§17.1、ADR 0001 §9）；③ 连接池复用不得保留上笔身份（§17.1、实施计划 §10.3）；④ 无 ORM、显式参数化 SQL，屏蔽事务内 `SET LOCAL`/CAS 形状的依赖一票否决（ADR 0004 D3）。
- **控制与落地：** 设计控制如上；待实施：Task 3（`001_roles_and_schemas.sql` + RLS 与权限测试）→ Task 4（每笔事务注入与连接卫生回归）。
- **可自动化验证：** 跨租户读写拒绝；RLS 上下文缺失时拒绝；事务回滚/连接复用后不残留租户身份（实施计划 §9.5、§10.4）；application role 无 DDL 和越权表权限（§9.5）。
- **未解决风险与 owner：** PG 客户端与连接池实现未冻结，连接卫生语义（会话状态不跨事务残留）须在选型时逐条核验（**G2**，Tech Lead，ADR 0004 §6 PostgreSQL 行）；RLS 策略表达遗漏（如新增表漏挂策略）依赖 Task 3 迁移评审纪律，无独立自动化兜底，属于待补的评审检查项（Tech Lead，Task 3 评审）。

### T-04 Prompt 注入 / 模型 proposal 污染

- **入口：** 用户 turn 文本 → 异步提取管线（规则/模型）；检索命中（ES/Qdrant/外部知识/历史文本）→ MemoryPack → 应用 Prompt；未经审批的 procedure；对抗性内容经"可信服务端事件"伪造（需先突破 T-02/T-11）。
- **影响：** 注入载荷经记忆持久化并在后续 Prompt 中放大（记忆回路投毒）；模型幻觉固化为 confirmed fact；错误记忆改变下游模型行为（错误记忆率门禁，§19.1/§19.4）。
- **策略（拒绝/隔离优先）：** ① 结构隔离：`memory_proposals` 与 `memory_facts` 独立聚合/表/事件/读路径，proposal 任何状态不进 facts read、不进 MemoryPack、不进任何投影（ADR 0003 D1）；② 模型只产 `pending` proposal，confidence 数值不构成任何确认依据，确认仅三条路径（白名单确定性规则/可信服务端签名事件/显式确认，ADR 0003 D3）；③ 召回内容是数据不是指令：检索结果放结构化 data section、外部知识与历史文本标 untrusted、procedure 必须 approved、system policy 在 MemoryPack 之外由应用强制（§17.3、§5.10）；④ 一次性敏感值 denylist 最低集合（LLM 单独推断、一次性数值、临时目标、预算、价格、库存阈值、凭据、自由文本秘密不得自动确认，§12.3）；⑤ **G5 fail-closed：白名单为空 ⇒ proposal 不得转 confirmed**（ADR 0003 D9）；⑥ PII/secret 扫描先于模型调用与持久化、模型输出严格 JSON schema、无法判断不写、不保存隐藏推理（实施计划 §14.4）。
- **控制与落地：** 设计控制如上；待实施：Task 7（状态机/准入）→ Task 8（提取管线 + `internal/policy/auto_confirm.go` 恒不自动确认）→ Task 11（assemble 隔离与注入 payload 过滤）。
- **可自动化验证：** model proposal 进入 Prompt 数量恒为 0（实施计划 §14.6）；adversarial prompt injection 不产生 confirmed fact（§14.5）；prompt injection payload 测试（§17.5）；未确认 proposal/敏感内容/跨租户内容进入 MemoryPack 数量为 0（§17.6）；`未确认 proposal 进入 Prompt = 0`（§19.2、§19.4 第 4 项）。
- **未解决风险与 owner：** 注入手法持续演化，自动化测试只能覆盖已知样本——观测 0 不等于证明生产永不发生（§19.4）；注入到检索内容（外部知识源）的攻击面取决于知识源 registry 的准入评审，该 registry 尚未定义（ML/Eval + Security，关联 **G5** namespace 白名单、**G9** 错误记忆率上限）。

### T-05 敏感字段泄露到 Kafka / Redis key / ES / Qdrant / log / trace

- **入口：** outbox payload 与事件 envelope 组装；Redis key/value 构造；ES 文档与 Qdrant payload 构建；日志/指标/trace 记录；错误响应体；MemoryPack 组装；DLQ 与隔离队列。
- **影响：** A1/A2 级数据（正文、PII、凭据、原始 ID）在事实源之外形成多份旁路副本，扩大 T-07/T-09 的暴露面并破坏最小保存原则（§5.5）与数据驻留承诺；一旦落盘，删除传播（T-08–T-10）须覆盖全部旁路。
- **策略（拒绝/隔离优先）：** ① outbox 只携带 opaque ref、版本与安全元数据，需要正文的受信消费者按 ref 回读 PG——Kafka 不承载正文（§8.8、ADR 0001 §5.3）；② Redis key 禁止邮箱、手机号、原始用户 ID（§9.1）；③ ES/Qdrant 只索引白名单内容并执行禁止字段清单（密钥/凭据/DSN、原始内部主键、未脱敏聊天、隐藏推理、已拒绝或 pending proposal、无 authorization metadata 内容、高敏 namespace 全部内容，§15.1、§17.2、ADR 0002 §6.2），缺必需元数据的文档视为非法（ADR 0002 D5）；④ 日志、指标、trace 不记录原始内容，关联只用 opaque ref/`trace_ref`；错误只返回稳定 reason code，不回显底层异常或输入值（§17.2、§19.3）；⑤ MemoryPack 不含 DSN、密钥、原始内部主键、隐藏推理和未批准 proposal（§11）；⑥ PG JSON 字段施加递归敏感键约束（实施计划 §9.4）。
- **控制与落地：** 设计控制如上；待实施：Task 1（secret 类型化 + 日志 redaction 纪律）→ Task 2（opaque ref/稳定错误码契约测试）→ Task 3（JSON 敏感键约束）→ Task 5（DLQ 无敏感 payload）→ Task 6（Redis key 形状）→ Task 9/10（no forbidden fields）→ Task 13（专项安全测试）。
- **可自动化验证：** 原始邮箱/手机号不能作为 Redis key 或公开 ref（实施计划 §8.4）；no forbidden fields in indexed document（§15.4）；Kafka/ES/Qdrant document forbidden fields、Redis key leakage、error/log/trace leakage（§19.4）；secret 不出现在日志和错误（Task 1，§7.4）；payload 禁止字段契约测试断言（ADR 0001 §11）。
- **未解决风险与 owner：** 字段级 sensitivity 分类的判定依据 `docs/data-classification.md` 尚未产出（Task 0 另一切片，不在本文件范围）；namespace→sensitivity 映射随 **G5** 批准（Product + Security，Task 7 前）；敏感键扫描的完备性无形式化保证，依赖契约测试 + 评审双道（Security，Task 13 评审）。

### T-06 第三方 embedding 外发与区域合规

- **入口：** Qdrant 投影管线中的第三方 embedding provider HTTP 调用；可选模型 extractor 的模型调用若由外部服务承担，也构成单独的跨境/跨信任域外发路径，不能假定其自动继承 embedding 的批准。
- **影响：** 中/高敏内容或未脱净 PII 外发到未批准区域或 provider；违反数据驻留与 DPA/不训练条款；外发文本被 provider 保留或用于训练即不可撤回。
- **策略（拒绝/隔离优先）：** ① 资格白名单：只有低敏内容具备外发资格；中敏禁止外发；高敏同时禁止进入 ES 和 Qdrant（§1.11、§17.2）；② 顺序 gate：内容资格 → 确定性脱敏与 PII/secret policy → provider 合规（数据驻留 + 不训练条款）→ 调用纪律（不落原文、deadline/预算/熔断），任一不通过即不外发、不建向量（ADR 0002 §8.1）；③ **G6 fail-closed：批准前生产 embedding 外发整体关闭，向量投影只用确定性测试向量，语义召回不对真实数据开放**（ADR 0002 §8.2、实施计划 §23.2）；④ 不引 SDK，`net/http` 薄客户端，减少供应链面（ADR 0004 §6）；⑤ 中敏语义召回路径默认禁用，需求出现时单独评审（ADR 0002 §17 G-M）。
- **控制与落地：** 设计控制如上；待实施：Task 10（embedding gate/chunker/provider 客户端）；门禁：**G6**（provider/model/dimension/区域/DPA/降级，ML + Security，Task 10 前）、**G3**（数据驻留区域，Platform + Security，Task 1 前）。
- **可自动化验证：** sensitive content blocked（实施计划 §16.4）；high-sensitivity embedding block（§19.4）；embedding dimension mismatch、provider timeout（§16.4）；embedding 请求不记录原文（§16.3）。
- **未解决风险与 owner：** 当前无任何已批准 embedding provider/model/DPA/处理区域（**G6**，ML + Security）；区域与数据驻留承诺未定（**G3**，Platform + Security）；可选模型 extractor 的外部提供方、文本外发分类与契约尚未获批准（Security + ML，Task 8 前默认仅运行本地确定性规则，不向外部模型发送 turn）；确定性脱敏对"可逆推断"类风险的覆盖能力未评测（ML/Eval，随 **G9** 评测设计）。

### T-07 outbox 重试与 DLQ 敏感 payload

- **入口：** outbox relay 批量锁取与重试；Kafka 重复投递；DLQ；schema 不支持的隔离队列（quarantined）；poison event。
- **影响：** at-least-once 语义下重试/重投递天然放大事件暴露面；若 DLQ 收纳完整 payload，则形成 A1/A2 级旁路存储且脱离删除传播范围；隔离队列成为不受治理的数据沉淀点。
- **策略（拒绝/隔离优先）：** ① 与 T-05 同源的结构性拒绝：事件 payload 仅 opaque ref/版本/安全元数据——即使 DLQ 被转储也无正文可泄（ADR 0001 §5.3）；② DLQ 不包含敏感 payload（实施计划 §11.3）；③ schema 不支持的事件进入隔离队列，不静默丢弃（§11.3）——隔离是有界、可见、可治理的，不是丢弃；④ 重试安全性由 inbox pending lease + epoch/revision/event id/hash 条件幂等吸收，不依赖重试次数上限（ADR 0001 §6）。
- **控制与落地：** 设计控制如上；待实施：Task 3（outbox/inbox 表）→ Task 5（relay/consumer 框架、DLQ 纪律、backpressure）。
- **可自动化验证：** publish 前崩溃、publish 后 mark 前崩溃、消费各阶段崩溃并回放；poison event；重复事件；DLQ 缺口下 watermark 不前进（实施计划 §11.4）；DLQ 无敏感 payload 断言（§11.3/§11.5）；重放不产生重复 canonical side effect（§11.5）。
- **未解决风险与 owner：** Kafka topic 命名/分区/保留期为保守草案，DLQ 与隔离队列的保留/清理策略随之未冻结（**G10**，Tech Lead + SRE，Task 5 评审，未冻结前不创建生产 topic）；owner：Tech Lead + SRE。

### T-08 迟到事件 / 重放 / 索引重建 / 备份恢复时删除复活

- **入口：** Kafka 重放与跨分区乱序（主体删除与其 turn/fact 事件可能跨分区乱序）；outbox 重复发布；消费者崩溃恢复；全量重建（alias/collection 切换）；旧备份恢复暴露流量。
- **影响：** 已删除主体/事实在任何投影重新出现（A1–A5 复活）；破坏"删除后被迟到事件复活 = 0"门禁（§19.2）与删除合规承诺；复活数据可再次进入 Prompt。
- **策略（拒绝/隔离优先）：** ① 删除命令同事务建立持久 fence（`tenant_ref + subject_ref + deletion_epoch`）、主体置为不可读/不可写、写删除 outbox（§17.4、ADR 0001 §8）；② 任何读取、消费者、重放及全量重建一律以 PG **当前** fence 过滤旧 epoch；应用事件时检查 deletion epoch、aggregate seq、canonical revision、event id、content hash——旧 epoch/旧 revision/重复事件不得覆盖新状态（§18.3）；③ 明确不依赖事件顺序：跨聚合根/分区无全局顺序，任何"靠 Kafka 顺序或索引内旧版本防复活"的设计被排除（§13.1、§17.4、ADR 0001 §5.4）；④ 重建协议：PG 一致性快照记录 outbox barrier → 新旧投影全程均处理删除 → 增量追至切换 barrier → 核对 fence/回执/数据 → 原子切换 → 旧投影隔离销毁（§15.3、ADR 0002 D9/§11）；⑤ 恢复旧备份先恢复/合并 fence 才能暴露流量（§17.4）；⑥ fence 保留期覆盖事件重放、索引重建和备份恢复窗口（§17.4）。
- **控制与落地：** 设计控制如上；待实施：Task 3（fence 持久化）→ Task 4（原子 fence/不可读写入口；完整编排在 Task 12，防复活不延迟到 Task 12 才首次实施，实施计划 §18.1）→ Task 5（消费 fence 检查）→ Task 6/9/10（投影删除与重建）→ Task 12（恢复编排）→ Task 14（replay 演练）。
- **可自动化验证：** tombstone、删除 fence 和迟到事件防复活（§19.4 第 9 项）；late event resurrection、repeated delete、replay 不复活删除主体（实施计划 §18.4、§20.4）；主体删除后 Redis 不返回旧会话、迟到 turn 不复活（§12.5）；含删除的 alias rebuild/collection migration（§15.4、§16.4）；重建期间删除不得复活（§20.4）；`删除后被迟到事件复活 = 0`（§19.2）。
- **未解决风险与 owner：** fence/tombstone 保留期与备份保留窗口的数值匹配未批准（**G7**，Legal + Security，Task 12 前，fail-closed：不接真实主体数据、不声称物理清除完成）；备份恢复演练在 Task 13 才发生，本文档不声称其已演练；owner：Legal + Security + SRE。

### T-09 删除竞态与回执不足

- **入口：** fence 检查与外部投影写之间的竞态窗口；投影删除回执与"空索引核对"；连续 watermark 与 DLQ 缺口；export 与 delete 竞态。
- **影响：** 删除被误报在线完成而投影残留数据；删除完成证明不可审计（回执缺失）；`deletion propagation p95/p99` 门禁（**G9**）失去度量基础。
- **策略（拒绝/隔离优先）：** ① 写前 fence 检查只是必要非充分：检查与外部写之间的窗口用持久 epoch guard/串行化或**写后复核补偿删除**闭合，并定期核对直至该主体无残留（§15.3、ADR 0002 §10）；② 每个投影对每个 tombstone 记录持久、可审计的处理回执（含确认无记录的 no-op）及该主体在线索引/缓存清空核对（§16、§17.4）；③ 连续 watermark 追过事件只是必要条件：回执失败、DLQ 存在缺口、重建仍可导入旧数据时，删除作业不得标记在线完成（§16）；④ 在线删除完成与备份物理清除分别报告，不得互相冒充（§16）；⑤ 导出与删除共享幂等账本与 job_ref 规则，作业进度可查询（§14.4）；⑥ 删除优先传播：撤销/过期/删除的投影处理优先级高于普通更新（§5.7）。
- **控制与落地：** 设计控制如上；待实施：Task 4（删除请求仅原子建立 fence 并返回 pending job，Task 12 完成前不得报告在线删除完成，实施计划 §10.3）→ Task 9/10（delete handler + 回执 + 竞态测试）→ Task 12（orchestrator、回执核对、export 竞态）。
- **可自动化验证：** 含删除的 alias rebuild 与删除回执（实施计划 §15.4）；collection migration 与删除回执（§16.4）；partial projection failure、export/delete race、repeated delete（§18.4）；"all watermarks contiguous、无 DLQ 缺口、每投影回执与空索引核对完成"方可完成（§18.4）；全部在线投影回执和核对完成前 API 不返回 online_completed（§18.5）。
- **未解决风险与 owner：** 大规模主体"空索引核对"的成本与时限、删除传播 SLO 数值未批准（**G8/G9**，Product + SRE / Product + ML/Eval + Security + SRE）；no-op 回执的持久存储形状属 Task 3/12 实现细节，未经实现验证（SRE + Security，Task 12 评审）。

### T-10 TTL 与备份物理清除

- **入口：** retention scheduler；备份与审计存储；tombstone/fence 保留期；法务保留与诉讼保全例外。
- **影响：** 过期/已删数据未按策略物理清除（合规违约，A1/A2 长期残留于备份）；或清除过度——破坏审计链或提前销毁 fence/tombstone，重新打开 T-08 的复活窗口。
- **策略（拒绝/隔离优先）：** ① TTL 按类别配置（§16 基线：raw turns 90 天、audit 365 天、pending proposal 7 天、Redis recent turns 24 小时滑动等），到期流程为 scheduler → 状态迁移（proposal 转 `expired` 并发 resolution 事件；到期 fact 转 `revoked(reason_code=expired)` 并发 tombstone outbox，不引入 fact 的 `expired` 状态）→ 仅受影响的 fact 投影独立清理 → 回执 + 核对 → 在线完成（§16、ADR 0003 D2）；② Redis TTL 到期不等于 canonical 删除；投影删除不等于事实删除（§16）；③ 审计记录 INSERT-only，法务删除走受控归档/密钥销毁策略（§8.4）；④ 在线完成 ≠ 物理清除：备份物理清除、审计保留和法务例外分别报告（§16、实施计划 §18.5）；⑤ **G7 fail-closed：法务保留/诉讼保全/备份物理清除未关闭前，不接真实主体数据、不声称物理清除完成**（ADR 0001 §14）；⑥ fence/tombstone 至少保留到所有投影确认删除并满足审计窗口（§16）。
- **控制与落地：** 设计控制如上；待实施：Task 12（scheduler/orchestrator/export）→ Task 13（备份/restore 演练）；门禁：**G7**（Legal + Security，Task 12 前）。
- **可自动化验证：** TTL expiry、tombstone retention（实施计划 §18.4）；备份仍保留时在线完成与物理清除状态分开展示（§18.4）；raw turn 90 天清除后仅可从保留 summary 回建、summary 亦过期时显式标缺口（§12.5）。
- **未解决风险与 owner：** 法务保留、诉讼保全、备份物理清除期限未批准（**G7**，Legal + Security）；物理清除的技术手段（KMS 密钥销毁 vs 行级/卷级清除）与"备份仍保留"期间的可审计证明方式未批准（Legal + Security + Platform，随 G7 评审）；owner：Legal + Security + Platform。

### T-11 供应链 / 密钥泄露

- **入口：** `go.mod` 依赖树与容器镜像（未来）；GitHub Actions CI 与仓库权限（G1 后）；JWKS/服务签名密钥；KMS/secret manager 与 DB DSN（G3 后）；embedding provider 凭据（G6 后）。
- **影响：** 恶意依赖/镜像获得数据面访问（A1–A9 全部）；签名密钥泄露 → 伪造"可信服务端签名事件"污染确认路径（绕过 ADR 0003 确认路径 2 的来源信任，与 T-04 交叉）；KMS 凭据泄露 → A1 密文可解；CI 权限滥用 → 向发布物注入代码。
- **策略（拒绝/隔离优先）：** ① 依赖最小化：标准库 > 小型成熟库 > 框架；许可证接受集（MIT/BSD/ISC/Apache-2.0），强 copyleft 或许可证不明不得采用（ADR 0004 D1/D4）；② **G1/G2 fail-closed：未关闭前不初始化 `go.mod`、不引入依赖、不拉取镜像、不创建远端 workflow**（ADR 0004 §12）——当前阶段攻击面为零；③ `go.sum` 校验下载内容哈希 + CI 依赖树审计（`go list -m all` 对比冻结清单，未审批模块即失败）（ADR 0004 D4/§10）；④ secret 类型化：无明文 `String()`/`GoString()`、不参与默认序列化、日志 redaction；无默认生产凭据（ADR 0004 D5、实施计划 §7.5）；⑤ **G3 fail-closed：真实 KMS/secret manager 未批准前仅本地合成测试配置，不处理真实数据**（ADR 0004 §12）；⑥ 密钥轮换、撤销、缓存失效与演练在 Task 13 加固（实施计划 §19.3）——身份验证本身在 Task 4 已是开放业务 API 的前置（T-02）。
- **控制与落地：** 设计控制如上；待实施：Task 1（config/secret/CI 纪律）→ Task 13（rotation/演练）；门禁：**G1**（项目 Owner，Task 1 前）、**G2**（Tech Lead，Task 1 前）、**G3**（Platform + Security，Task 1 前）、**G4**（JWT 库冻结前置，Security + API Owner）。
- **可自动化验证：** 配置未知字段/非法值/缺少 secret 失败；secret 不出现在日志和错误（实施计划 §7.4）；依赖树审计与冻结清单一致性（ADR 0004 §10）；提交前无原始 secret/PII 出现在 Git/日志/fixture、staged 文件与 Task 白名单一致（实施计划 §21）。
- **未解决风险与 owner：** 私有仓库与 CI 尚不存在，CI 权限模型未定义（**G1**，项目 Owner）；所有候选依赖的维护健康度证据【待验证】（ADR 0004 §9，**G2** 冻结时必须重核）；密钥托管、轮换与撤销契约未批准（**G3/G4**）；owner：项目 Owner / Tech Lead / Platform + Security。

## 7. 门禁映射（owner 与 fail-closed，逐字对齐 ADR 0001 §14 / 实施计划 §23.2）

| 门禁 | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 | 本文档相关威胁 |
|---|---|---|---|---|---|
| G1 | GitHub owner、Go module path、CI 权限模型 | 项目 Owner | Task 1 前 | 不初始化 `go.mod`，不创建远端 workflow | T-11 |
| G2 | Go 与 PG/Kafka/Redis/ES/Qdrant 精确版本、许可证 | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像 | T-03、T-11 |
| G3 | 部署/数据驻留区域、KMS/secret manager | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 | T-02、T-06、T-10、T-11 |
| G4 | tenant/subject identity 签发者、JWT/JWKS 与服务代理授权契约 | Security + API Owner | Task 2 前 | 不开放业务 API；Task 4 前必须实现并验收身份验证、授权及 RLS 上下文 | T-01、T-02 |
| G5 | confirmed namespace/key 白名单 | Product + Security | Task 7 前 | proposal 不得转 confirmed | T-04、T-05 |
| G6 | embedding provider/model/dimension/DPA 与降级 | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量 | T-06 |
| G7 | 法务保留、诉讼保全、备份物理清除期限 | Legal + Security | Task 12 前 | 不接真实主体数据，不声称物理清除完成 | T-08、T-10 |
| G9 | 评测门禁数值（gold set 分层/样本/一致性、precision@5/recall@10/nDCG@10、错误记忆率上限、成对任务差异、删除传播 p95/p99） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；阈值必须为批准数字，禁止 `TBD` 或"优于 baseline" | T-04、T-09 |

同表补充（ADR 0001 §14 其余门禁，本文威胁涉及的）：G8（真实负载 latency/error budget 复算，Product + SRE，Task 13 前，未关闭不进入发布门禁 → T-09）；G10（Kafka topic/分区/保留期，Tech Lead + SRE，Task 5 评审，未冻结不创建生产 topic → T-07）；G11（容量/配额，Product + SRE → T-01 检索放大面）；G12（首个真实客户端，Product，Task 14 前 → T-01 授权模型）。

## 8. 覆盖矩阵

| 威胁 | 主要入口 | 关键设计引用 | 落地 Task | 门禁 | 自动化验证锚点 |
|---|---|---|---|---|---|
| T-01 IDOR | 路径 ref / 自报身份 / 查询过滤 | §17.1、§10.1、ADR 0002 §13 | 2/3/4/6/9/10/11/13 | G4、G12 | 计划 §10.4、§15.4、§19.4；spec §19.2 |
| T-02 伪造身份/代理 | 认证层 / JWKS | §17.1、ADR 0001 §14 | 2/4/13 | G4、G3 | 计划 §10.4、§19.4 |
| T-03 RLS/连接复用 | PG 连接池 | §17.1、ADR 0001 §9、ADR 0004 D3 | 3/4 | G2 | 计划 §9.5、§10.4 |
| T-04 注入/proposal 污染 | turn 文本 / 检索命中 | §17.3、§12.3、ADR 0003 D1/D3/D9 | 7/8/11 | G5、G9 | 计划 §14.5/§14.6、§17.5/§17.6；spec §19.2 |
| T-05 敏感字段泄露 | payload/key/文档/log/trace | §8.8、§9.1、§15.1、§17.2、ADR 0002 §6 | 1/2/3/5/6/9/10/13 | G5（映射） | 计划 §7.4、§8.4、§15.4、§19.4 |
| T-06 embedding 外发 | provider 调用 | §1.11、§17.2、ADR 0002 §8 | 10 | G6、G3 | 计划 §16.4、§19.4 |
| T-07 outbox/DLQ | relay 重试 / DLQ / 隔离队列 | ADR 0001 §5.3/§6 | 3/5 | G10 | 计划 §11.4/§11.5 |
| T-08 删除复活 | 重放 / 重建 / 备份恢复 | §17.4、§18.3、ADR 0001 §8、ADR 0002 D9 | 3/4/5/6/9/10/12/14 | G7 | spec §19.4-9；计划 §12.5、§15.4、§16.4、§18.4、§20.4 |
| T-09 删除竞态/回执 | fence↔外部写窗口 / 回执 | §15.3、§16、ADR 0002 §10 | 4/9/10/12 | G8、G9 | 计划 §15.4、§16.4、§18.4/§18.5 |
| T-10 TTL/物理清除 | scheduler / 备份 | §16、§8.4 | 12/13 | G7 | 计划 §12.5、§18.4 |
| T-11 供应链/密钥 | 依赖 / 镜像 / CI / KMS | ADR 0004 D1/D4/D5/§12 | 1/13 | G1、G2、G3、G4 | 计划 §7.4、§21；ADR 0004 §10 |

计划 §6.4 要求威胁模型覆盖的六类对照：跨租户（T-01/T-02/T-03）、Prompt 注入（T-04）、迟到事件复活（T-08）、索引泄露（T-05）、embedding 外发（T-06）、删除不完整（T-08/T-09/T-10）——全部覆盖。

## 9. 本文档不是什么（防误读声明）

1. **不是安全审查结论。** 威胁建模是 Phase 0 交付物之一（设计规格 §20）；代码审计、渗透测试、外部合规认证均未发生，也不在 Task 0 范围内。
2. **不是控制已实施的证明。** 所有"控制"均为设计约束；每个 Task 的门禁（实施计划 §6–§20）通过前，对应控制不得视为存在。
3. **不是任何门禁的批准。** G1–G12 全部处于待批准状态；本文档的评审不替代任何 owner 的门禁批准。
4. **不是完备性证明。** 威胁清单覆盖本任务指定的 11 类与计划 §6.4 六类；未在列的威胁（如可用性攻击/DoS 的量化分析、多区域一致性威胁——v1 单区域不做跨区域）需在架构变更时修订本文档。

## 10. 验证记录

- **一致性核对（人工交叉引用）：** 本文所有 §引用、Task 归属、门禁 owner 与 fail-closed 文本逐条对照 README、设计规格（§1、§5、§8、§9、§10、§11、§12、§13、§14、§15、§16、§17、§18、§19、§20、§21）、实施计划（§2、§3、§6、§7–§20、§21、§23、§24）与 ADR 0001（§5–§10、§14）、ADR 0002（§6–§13、§17）、ADR 0003（§2、§6、§13）、ADR 0004（§6、§8、§10、§12）完成；未发现与上游冲突的陈述。
- **写入范围：** 本次仅新增 `docs/threat-model.md` 一个文件；未修改 README、设计规格、实施计划、ADR 及任何其他文件；未执行 git init/commit、未安装依赖、未启动任何基础设施。
- **VCS 状态：** `D:/Projects/memX` 当前不是 Git 仓库（`git status` 返回 not a git repository）；因此本文档与任务验证**不使用、也不宣称 `git diff` 检查通过**（与 ADR 0002/0003/0004 工作约定同口径）。
- **已知未决（非本文档可关闭）：** `docs/data-classification.md`、`docs/capacity-baseline.md`、`docs/evaluation-gates.md` 未产出，T-05/T-06/T-09 的部分 owner 决策依赖它们；G9 未关闭前 Task 1 不得开始。

## 11. 后续维护

- 任一 Task 落地把"设计控制"变为"已实施"时，须修订本文档对应威胁的"控制与落地"列并附门禁证据引用，不得只在本文宣称完成。
- 架构变更（新增存储、新增外发通道、跨区域、新确认路径、知识源 registry 接入）须先修订威胁模型再实施。
- 门禁关闭后，§7 表格由对应 owner 更新状态并引用批准记录；本文档不代行批准。
