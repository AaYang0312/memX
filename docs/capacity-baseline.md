# memX 容量基线（Task 0 文档切片）

**状态：** Proposed（Task 0 容量基线草案，待 owner 评审。本文档不批准 G1–G12 中任何待批准项，不冻结任何分区数、连接池大小、副本数或机器规格，不解除实施计划 §6.4/§23.2 的 Task 1 阻塞条件，见 §14/§15。）
**日期：** 2026-09-23
**范围（In scope）：** 以设计规格 §1 与实施计划 §23.1 已确认基线（峰值 50 QPS、10 万主体、100 万 turn/日、raw turns 90 天、audit 365 天、服务月可用性 99.9%）为前提，严谨推导当前**可推导**的量（平均 append 速率、峰均比上界、条件化 90 日库存、可用性误差预算、并发上界），给出事件/outbox/Kafka 分区、索引、Redis、带宽、磁盘、PG 连接池的**推导公式**（符号化，不填未批准值），登记删除传播与 SLO 初值及其"非批准门槛"属性，并定义测量、基线更新与 Task 13 压测门禁、未决输入与决策台账对齐。
**明确排除（Out of scope）：** 任何采购、实例选型或生产 sizing 结论；Kafka 分区数、PG 连接池大小、ES/Qdrant 分片与副本数、index/embedding dimension 等未批准数值的"填值"；平均/峰值消息长度、租户分布、增长余量、抽取产出率等未决输入的编造（§14 登记 owner 与 fail-closed 默认）；任何代码、依赖、镜像或基础设施变更；`docs/data-classification.md`、`docs/evaluation-gates.md`（同属实施计划 §6.2 Task 0 文件清单，另行交付）。

**权威输入（本文档与其冲突时以上游为准，须修订本文档）：**

- `docs/specs/2026-09-20-production-memory-service-design.md`（下称"设计规格"，§n 引用）
- `docs/plans/2026-09-20-production-memory-service-implementation.md`（下称"实施计划"，Task n 或 §n 引用）
- `docs/adr/0001-canonical-and-event-backbone.md`（下称 ADR 0001，§n 引用）
- `docs/adr/0002-search-and-vector-projections.md`（下称 ADR 0002，§n 引用）
- `docs/adr/0004-go-service-stack.md`（下称 ADR 0004，§n 引用）
- `docs/threat-model.md`（T-n 引用）
- `README.md`（架构基线与核心不变量）

**工作约定：** 当前仓库尚未初始化 Git、源码、依赖与基础设施；本文件属于实施计划 §6.2（Task 0）文件清单的一部分，先于 Task 1 创建。本文件的创建不构成对任何待批准项的批准；涉及本文档的验证以文件与引用一致性检查和算术复算为准，不使用、不宣称 `git diff` 检查。

---

## 0. 阅读本文档前必须接受的四个事实

1. **已确认基线只有 6 个数。** 峰值 50 QPS、10 万主体、100 万 turn/日、raw turns 90 天、audit 365 天、服务月可用性 99.9%（设计规格 §1.10/§1.12、实施计划 §23.1）。其中容量三项均标注"上线前按真实负载复算"（实施计划 §23.1）。除此之外的一切数值——消息长度、租户倾斜、读写比、增长率、抽取产出率、写放大、压缩比、副本/分片数、分区数、连接池、index/embedding dimension——都是**未批准输入**，本文只以符号或明确标注的合成假设出现。
2. **本文档推导"公式与边界"，不推导"配置值"。** 实施计划 §6.4 对本文件的验收要求是"capacity baseline 能推导分区、连接池、索引和 SLO 初值"——推导公式与初值来源，而非冻结数值。分区数归 G10（Task 5 评审）、容量复算归 G8/G11（Task 13 前）、连接池与部署规格归 Task 13 容量测试。
3. **SLO 初值不是批准门槛。** 设计规格 §19.2 的 p95/p99 数字是"初始 SLO 建议"，其前言明确"上线前必须用真实消息长度、租户分布和增长率压测复算"；ADR 0002 §12.2 明确"批准前不得作为发布门禁"；真实负载 latency/error budget 复算是 G8 待批准项（实施计划 §23.2）。
4. **本文档不做采购或生产 sizing。** §4.3 的合成场景仅用于展示公式敏感性，禁止用于采购、实例选型或生产容量声明（实施计划 §23.2 G11 fail-closed 默认："使用试点基线且禁止生产容量声明"）。

### 0.1 推导覆盖索引（对应实施计划 §6.4 验收项）

| 要求 | 本文位置 |
|---|---|
| 分区（Kafka）推导公式 | §6.3 |
| 连接池推导公式 | §11 |
| 索引（ES/Qdrant）推导公式 | §7 |
| SLO 初值（含删除传播） | §12 |
| 事件/outbox、带宽、磁盘、Redis | §6、§9、§10、§8 |
| 可直接推导的量与算术 | §2、§3 |
| 测量、更新与压测门禁 | §13 |
| 未决输入与决策台账 | §14 |

### 0.2 符号表

| 符号 | 含义 | 状态 |
|---|---|---|
| λ_peak | 服务峰值请求速率（HTTP 总 QPS） | 已确认 = 50/s |
| μ_app | turn append 平均速率 | 可推导（§2） |
| p_app | turn append 峰值速率 | 未知，仅知上界 p_app ≤ λ_peak（§2） |
| α | append 请求占服务总请求比例（峰值或时均） | 未批准（G11） |
| N_d | 第 d 日 turn 新增数 | 已确认基线 = 10^6/日（复算前） |
| g | 日增长率 | 未批准（G11）；§4.3 仅合成场景 |
| e_turn | 每 turn 产生的基础事件数（`memory.turn.committed.v1`） | ≥ 1（设计规格 §13.2） |
| m_type | 其他事件类型相对 turn 的事件倍率 | 未知（§6.1） |
| λ_audit | 每 turn/每操作产生的审计记录数 | 未知（§5） |
| w_x | 各表平均行宽（字节） | 未批准（消息长度，G11） |
| a_idx | PG 索引/写放大系数 | 未批准（§10） |
| R | 副本因子（ES/Qdrant/Kafka/PG 拓扑） | 未批准（§7、§6.3） |
| dim / b / k_chunk | 向量维度 / 每分量字节数 / 每文档分块数 | 未批准（G6 / Task 10，§7.2） |
| C_active | 24h 滑动窗口内活跃 conversation 数 | 未知（§8） |
| T_txn | 平均 PG 事务时长 | 未测（§11） |
| W_retry | 幂等账本保留天数 | 设计建议 7 天（设计规格 §8.11"首轮建议"），未批准 |

## 1. 已确认基线（推导前提）

| # | 基线 | 值 | 上游依据 | 附加确认约束 |
|---|---|---|---|---|
| B1 | 峰值请求速率 λ_peak | 50 QPS（服务总峰值） | 设计规格 §1.10；实施计划 §23.1 | "上线前按真实负载复算"；请求统计口径（计入哪些 endpoint）未定义，属 G11 输入 |
| B2 | 主体数 | 10 万 | 设计规格 §1.10 | 注册主体存量，**不是**并发活跃数（§8） |
| B3 | turn 新增量 | 100 万 turn/日 | 设计规格 §1.10 | "上线前按真实负载复算" |
| B4 | raw turns 保留 | 90 天 | 设计规格 §1.12、§16 表 | 法务保留与例外未关闭（G7） |
| B5 | audit 保留 | 365 天 | 设计规格 §1.12、§16 表 | "法务保留例外必须显式登记"（§16） |
| B6 | 服务月可用性 | ≥ 99.9% | 设计规格 §1.10、§19.2 | "延迟与错误预算待压测"（实施计划 §23.1）；月窗口口径（自然月/滚动 30 天）未定义（G8 输入） |
| B7 | Redis recent turns TTL | 24 小时滑动，可配置 | 设计规格 §16 表 | 已确认基线的一部分 |
| B8 | 单区域试点 | 不承诺跨区域 | 设计规格 §1、实施计划 §23.1 | 容量推导不引入多区域放大 |

## 2. request QPS 与 turn append TPS 不可自动等同

**这是本基线最重要的一条纪律。** 50 QPS 是服务**所有 HTTP 请求**的峰值（`memory:assemble` 读、turn append 写、fact confirm/revoke、proposals 读写、删除/导出、explain 等，设计规格 §14），而 turn append 只是其中一个写端点族。两者不可混用：

- **平均 append 速率（可推导）：**

  ```text
  μ_app = N_turn_日 ÷ 86,400 s = 1,000,000 ÷ 86,400 ≈ 11.574 turn/s
        ≈ 694.44 turn/min ≈ 41,666.67 turn/h
  ```

- **峰值 append 速率（未知，仅知上界）：** append 请求是总请求的子集，任意瞬间 append 速率 ≤ 总请求速率，故

  ```text
  μ_app ≤ p_app ≤ λ_peak = 50/s（假定同一统计窗口内每个 append 请求恰写一个 turn）
  ```

  等号仅在"峰值时 100% 流量都是 append"这一极端情形成立。append 份额 α、读写比、各 endpoint 占比均未批准（G11），因此 **p_app 是 [μ_app, 50] 内的未知数，不得令 p_app = 50 作为"预期值"**——50 只能作为设计必须容忍的上界，而预期值必须等 G11 测量。
- **不得做的换算：** 把 50 QPS 直接当作"append 峰值 TPS"来推导 PG 写 TPS、outbox 速率或 Kafka 分区数，会将未知读写比伪装成事实。所有下游公式（§6、§10、§11）均以 p_app 为符号，标注其上界。

## 3. 当前可直接推导的量（附复算算术）

以下每项算术均可独立复算（复算记录见 §16.1）。

| # | 量 | 推导 | 结果 | 性质与边界 |
|---|---|---|---|---|
| D1 | 平均 append 速率 | 10^6 ÷ 86,400 | ≈ 11.574 turn/s | 由 B3 直推；随真实负载复算更新 |
| D2 | append 峰均比上界 | λ_peak × 86,400 ÷ N_turn = 50 × 86,400 ÷ 10^6 | = 4.32（精确） | 见下文解释：它是"峰值总请求速率 ÷ 平均 append 速率"的**跨量比值**，同时是 p_app/μ_app 的上界；**不是**总流量的峰均比（μ_total 未知，G11） |
| D3 | 可用性误差预算 | 30 天月 = 43,200 min × 0.1% = 43.2 min（= 2,592 s）；31 天月 = 44,640 min × 0.1% = 44.64 min | 43.2 / 44.64 min | B6 的直接推论；**各组件（PG/Redis/Kafka/ES/Qdrant）误差预算分配未定义**，不得以"各组件各自 99.9%"相乘后宣称端到端达标；月窗口口径未定（G8） |
| D4 | 速率×时延的合成算术示意（不是 Little 定律算出的并发上界） | 50/s × 100 ms = 5；50/s × 150 ms = 7.5；50/s × 250 ms = 12.5 | 5 / 7.5 / 12.5 | 设计规格 §19.2 的 100/150 ms 是 p95、250 ms 是硬超时；Little 定律须使用同一稳定窗口的**平均**到达率与**平均**在途时间。p95 不是均值，50 QPS 的统计窗口也不约束瞬时突发，故这三项仅演示量纲，**绝非并发或连接池上界**（§11） |
| D5 | canonical 事件量下界 | 每 turn 恰一条 `memory.turn.committed.v1`（设计规格 §13.2） ⇒ ≥ 10^6 事件/日 | ≥ 11.574 msg/s（均值） | 其余 13 类事件率未知（§6.1）；`turn.committed` 的**生成**速率受 append 速率约束，但总事件峰值及 Kafka relay 突发不受 50 QPS 上界约束 |
| D6 | 稳态保留清除 churn | 稳态 + 恰好 90 天过期时，每日清除量 = 90 天前当日新增 = N_d | ≈ 10^6 行/日 ≈ 11.574 行/s | 供 Task 12 retention scheduler 与删除 fan-out（实施计划 §19.5）参考；增长时清除滞后于插入（§4） |
| D7 | 均匀分布示意值 | 10^6 ÷ 10^5 = 10 turn/主体/日；9×10^7 ÷ 10^5 = 900 turn/主体（90 日窗口） | 10 / 900 | **仅在完全均匀分布时成立**；租户倾斜未批准（G11），此值禁止用于配额、分片或热点规划 |

**D2 的精确含义（防误读）：** 4.32 = 50 ÷ (10^6/86,400)。它回答的问题是："在同一统计窗口与单 turn append 请求假设下，append 峰值最多是其日均值的 4.32 倍"——这是一个条件化上界，不意味着实际峰值已测得。总流量自身的峰均比 r_total = λ_peak ÷ μ_total 需要每日总请求数（未知，G11），当前不可推导。任何把 4.32 写成"系统峰均比为 4.32"的引用都是对本文档的误读。

## 4. 90 日 raw turn 库存：条件化估算

### 4.1 基本公式

```text
Stock_90(t) = Σ_{d=0}^{89} N_(t−d)      （t 日仍存活的 raw turn 行数上界，未计物理行宽）
```

### 4.2 "1,000,000 × 90 = 90,000,000" 成立的三个条件

**9 × 10^7（9000 万）行仅在以下三条件同时成立时才是库存值**，否则只是名义上界：

1. **稳态：** 每日新增恒为 10^6（无增长、无季节性、无投放波动）；
2. **全量保留：** 每条 turn 都存活满 90 天——主体删除的提前清除（设计规格 §17.4）、redaction（§8.2 `status=redacted/deleted`、§12.1）、保留策略收紧（§16 允许"批准后的策略收紧"）都不发生；
3. **恰好 90 天过期清除：** retention scheduler 按 90 天整点清除，无清除积压。

任一条件被破坏：条件 2 使实际库存 **低于** 9×10^7；持续增长使库存 **高于** 9×10^7（见 4.3）。因此 9×10^7 应表述为："**稳态、无增长、全量保留前提下的条件化估算值**"，禁止作为无条件的库存事实引用。

### 4.3 增长敏感性（合成场景，非批准值）

以今日 N=10^6 为起点、日增长率 g 持续，窗口末端（最大值）库存：

```text
Stock_90 = N × ((1+g)^90 − 1) ÷ g        （g > 0；g = 0 时退化为 90N）
```

| 场景 | g | 因子 ((1+g)^90−1)/g | Stock_90 | 算术 |
|---|---|---|---|---|
| 稳态 | 0 | 90 | 9.00 × 10^7 | 10^6 × 90 |
| 合成假设 A | +1%/日 | 144.863 | ≈ 1.4486 × 10^8 | 10^6 × (1.01^90−1)/0.01 |
| 合成假设 B | +2%/日 | 247.157 | ≈ 2.4716 × 10^8 | 10^6 × (1.02^90−1)/0.02 |

**红字边界：本表是公式敏感性的合成演示。** g 的真实值属 G11 未批准输入（"平均/峰值消息长度、租户分布、配额与增长余量"，Product + SRE，Task 13 前；未关闭时"使用试点基线且禁止生产容量声明"，实施计划 §23.2）。**上表数值不可用于采购、实例选型或任何生产 sizing。**

### 4.4 仍不可推导的部分

行宽 w_turn 取决于平均/峰值消息长度（未批准）与加密/受保护引用格式（数据分类切片未交付），因此**库存行数 ≠ 磁盘字节数**；磁盘推导见 §10，在 G11 关闭前不可能得出任何 GB 级结论。

## 5. audit 365 天记录数：不能由 turn 数推出

audit 记录数由**审计覆盖定义**决定（哪些操作/决策写审计记录：fact 变更的 revision 审计是 §8.4 明确的；读请求、授权拒绝、管理操作、删除链路各写多少审计行，设计规格未定义），而 turn 数只是可能的输入之一：

```text
R_audit(365d) = Σ_365d Σ_op (ops_op(日) × λ_audit,op)      λ_audit,op 未知
```

- **不可做的推导：** "365 天 × 100 万 turn/日 = 3.65 × 10^8 条审计" **不是**可引用的估算——它隐含 λ_audit = 1（每 turn 恰一条）且审计不覆盖读/授权/管理操作。读审计会使其显著更高；部分操作不审计会使其更低。该算式至多是算术示意，连可靠上界都不是。
- **保留窗口可被拉长：** 法务保留/诉讼保全/备份例外可使个别记录存续超 365 天（设计规格 §16"法务保留例外必须显式登记"；门禁 G7，Legal + Security，Task 12 前），库存上限在 G7 关闭前无定义。
- **登记：** 审计覆盖定义与 λ_audit 归 Security + SRE（随 `docs/data-classification.md` 切片与审计设计落地）；在此之前任何 audit 存量/磁盘推导均不可下结论。

## 6. 事件、outbox 与 Kafka 分区推导公式

### 6.1 事件量

设计规格 §13.2 定义 14 个必需事件类型。每日事件量：

```text
E_day = N_turn × e_turn + Σ_type C_type × m_type
      ≥ 10^6 × 1                （e_turn = 1：每 turn 恰一条 turn.committed）
```

其中 C_type/m_type（fact confirmed/superseded/revoked/deleted、proposal created/resolved、summary committed、episode committed、procedure approved/revoked、turn redacted、subject deletion requested/completed）取决于：抽取产出率（Task 8 未实现）、G5 白名单与确认率、摘要节奏（Task 6/8）、删除率——**全部未知**。因此 E_day 目前只有下界 10^6/日（= D5）。`turn.committed` 的 canonical 生成速率不超过 p_app ≤ 50/s；其余异步事件、总事件生成峰值，以及 relay 从积压中向 Kafka 发布的突发峰值均未测，**不得用 50/s 作为 Kafka 总吞吐上界**。

### 6.2 outbox

```text
outbox 插入率 = E_day ÷ 86,400
backlog B(t) = ∫ (λ_in − μ_relay) dt ≥ 0        （稳态要求 μ_relay ≥ λ_in）
```

- Kafka 不可用时 canonical 写入继续、outbox 累积，超 backlog 阈值告警（设计规格 §18.2；告警阈值与 readiness 联动归 Task 5，实施计划 §11.5）。
- 已 published outbox 行的清除/归档策略是 Task 5 设计点，未定义 → outbox 表的稳态行数当前不可推导（只能给 churn 下界）。
- outbox 只携带 opaque ref/版本/安全元数据，Kafka 不承载正文（设计规格 §8.8、ADR 0001 §5.3）→ 单事件尺寸与消息长度解耦，但精确 envelope 尺寸待 Task 2 事件契约冻结。

### 6.3 Kafka 分区推导公式（不冻结数值）

设单分区安全吞吐 t_p（vendor/部署相关，未测）、目标消费并行度 C_consumer、单分区单消费组并行 c_g = 1：

```text
P ≥ max( ceil(λ_peak_event ÷ t_p),  ceil(C_consumer ÷ c_g),  P_skew_headroom )
其中 λ_peak_event 为总事件发布峰值（包括异步派生与积压追赶），目前未知；11.574/s 仅是 `turn.committed` 的日均下界，50/s 仅约束其 canonical 生成速率
```

- **顺序语义（已确认，不随 P 变化）：** key = `(tenant_ref, aggregate_ref)`（设计规格 §13.1），同一聚合根分区内有序；分区数只影响并行度与倾斜暴露，不影响顺序保证；跨聚合根/分区从不假设顺序（ADR 0001 §5.4）。
- **倾斜不可量化：** 租户分布未批准（G11），hot-partition 风险无法数值化；倾斜处理机制（如大租户隔离 topic）属 G10 评审内容，本文不预选。
- **门禁：** topic 命名/分区数/保留期为 ADR 0001 §5.3 的保守草案，由 **G10（Tech Lead + SRE，Task 5 评审）** 冻结；未冻结前不创建任何生产 topic。**本文不给出分区数推荐值。**

## 7. 索引投影（ES/Qdrant）推导公式与不可推导项

### 7.1 Elasticsearch（词法）

可索引内容仅白名单四类（设计规格 §15.1、ADR 0002 §6.1）：已脱敏 episode 摘要、approved procedure 模板、confirmed fact 文本投影、注册知识源安全片段。**raw turns 永不入索引**（ADR 0002 §6.2）。

```text
D_es(t) = D_episodes + D_procedures + D_fact_text + D_knowledge
Bytes_es ≈ Σ (s_doc × (1 + o_analyzer)) × (1 + R)
shards ≥ ceil(Bytes_total ÷ target_shard_bytes)          （管理公式，target 为运维目标值）
```

- **D_es 不可由 turn 数推出：** episode/fact 文本产出率 = 抽取产出率 × 白名单资格，抽取产出率未实现未测、白名单 G5 为空（fail-closed，ADR 0003 D9）、知识源 registry 未定义——四项输入全部未知。
- s_doc（文档平均字节）取决于脱敏文本长度（未批准）；o_analyzer（分析器膨胀系数）取决于 analyzer 选择（ADR 0002 §17 G-A，Task 9 评审）；R（副本数）未批准。**本文不冻结分片数与副本数。**

### 7.2 Qdrant（向量）

```text
N_vec = D_indexable × k_chunk
Bytes_qdrant ≈ N_vec × dim × b × (1 + payload 开销) × (1 + R)
```

- dim（维度）绑定 embedding provider/model——**G6 未批准**（ML + Security，Task 10 前）；b 与量化选项未选；k_chunk 取决于 chunker（未实现）；R 未批准。
- **当前基线：G6 未关闭期间生产 embedding 外发整体关闭，向量投影只用确定性测试向量，语义召回不对真实数据开放（fail-closed，ADR 0002 §8.2、实施计划 §23.2）⇒ 生产向量存量基线 = 0**，直至 G6 批准。本条件同时意味着当前无法测得任何真实语义召回性能，相关容量结论全部缺位。

## 8. Redis working set 推导公式

```text
S_redis ≈ C_active × w_wm
```

- key 粒度为每 conversation 一条 working-memory 条目（设计规格 §9.1）；recent turns 受 24 小时滑动 TTL 约束（B7，已确认），因此存量天然以 24h 活跃窗口为界。
- **C_active（24h 活跃 conversation 数）未知：** B2 的 10 万是注册主体存量，不是并发/日活；日活主体比例未批准（G11）。
- w_wm 取决于保留回合数与 token/字符预算（设计规格 §9.3 规则 6"同时受回合数和 token/字符预算限制"）——MemoryPack token budget 数值未批准（G8 覆盖其复算）。
- Redis 回建成功率 ≥ 99.99% 是 SLO 初值非门槛（§12）；回建风暴（全量丢失）时的 PG 读放大计入 §11 连接池压力，数值待 Task 6/13 实测。

## 9. 带宽推导公式

```text
南北向：Ingress ≈ λ_req × s_req          Egress ≈ λ_req × s_resp
东西向：PG ≈ 请求数 × 每请求 PG 往返数 × 行宽（往返数待 Task 11 读路径设计）
        Kafka → 消费组：E_day × s_envelope × G_groups（G_groups ≥ 4：extractor/Redis/ES/Qdrant 投影 + retention/deletion）
        ES/Qdrant 查询响应 ≈ QPS × topK × 单条候选字节（topK 硬上限归 Task 11，数值未批准）
        embedding 外发 = 0               （G6 未批准，fail-closed，ADR 0002 §8.2）
```

s_req/s_resp 取决于平均/峰值消息长度与 MemoryPack token budget（均未批准：G11/G8）。**在 G11 关闭前不得得出任何 NIC/云出口带宽结论。**

## 10. PostgreSQL 磁盘构成公式

```text
S_pg ≈  T_rows × w_turn            （raw turns，T_rows ≤ 9×10^7 条件化，§4）
      + S_rows × w_summary         （canonical summaries，速率未测，Task 6/8）
      + F_rows × w_fact + Rv_rows × w_revision   （facts + INSERT-only revisions，产出率未知）
      + P_rows × w_proposal        （proposals，抽取产出率未知；TTL 7 天为设计规格 §16 基线）
      + E_rows × w_episode + Pr_rows × w_procedure
      + O_rows × w_outbox          （churn ≥ E_day；published 行清除策略未定，§6.2）
      + I_rows × w_idem            （幂等账本：I_rows ≥ N_turn × W_retry = 10^6 × 7 = 7×10^6，
                                     仅计 turns、W_retry=7 为设计规格 §8.11"首轮建议"非批准值；
                                     fact/proposal/delete 命令另计，占比未知；回滚不留已完成记录）
      + A_rows × w_audit           （audit：不可推导，§5）
      + fence/tombstone/删除回执    （覆盖事件重放、索引重建与备份恢复窗口，时长未批准，G7）
      + WAL + Σ indexes × a_idx    （写放大 a_idx 未批准，不估值）
```

**结论：** 在 G11（消息长度 → 各 w_x）与抽取产出率实测之前，任何 GB 级磁盘结论都不可成立。可引用的只有：条件化行数上界（§4）、事件行数下界（§6.1）、幂等账本条件化下界（上文）。

## 11. PG 连接池推导公式（不冻结数值）

```text
预算约束：C_app ≤ max_connections − C_reserved        （C_reserved：superuser/运维/迁移保留，部署决策）
进程分解：C_app = N_api × c_api + N_relay × c_relay + Σ_group N_g × c_g
        （memx-api / memx-outbox-relay / memx-worker 各 consumer group 独立进程与池，ADR 0004 §5）
规划条件：有效事务槽位容量须覆盖实测到达率、事务平均占用时间及突发排队目标；稳态在途均值 L = λ_avg,tx × E[T_txn]（Little 定律），不能用 p95 或峰值 QPS 直接证明池容量（T_txn/突发形状未测）
回建风暴附加：Redis 全量回建时的 PG 读并发额外计入，量级待 Task 6/13 实测
```

- 每笔事务以 `SET LOCAL` 注入租户/主体上下文、连接复用不得残留上笔身份、缺失上下文拒绝（设计规格 §17.1、ADR 0001 §9）→ 池实现必须支持事务级连接卫生，属 G2 客户端选型核验项（ADR 0004 §6 PostgreSQL 行）。
- 池大小、实例数 N_*、PG max_connections 及其分配比例是部署与 Task 13 容量测试决策；**本文不冻结任何池数值。**

## 12. 删除传播与 SLO 初值（设计初值，非批准门槛）

### 12.1 删除传播时间预算分解

```text
T_online_complete ≈ T_fence_commit（同步建立 fence）
                  + T_outbox_relay + T_kafka_delivery
                  + max_{k ∈ 在线投影}(T_consume,k + T_receipt,k + T_verify,k)
                  + T_orchestration_check
                  （各投影并行处理，但必须全部出具持久回执并完成空索引核对；
                    串行化/重试/DLQ 缺口使公式只是分解模型，非 SLO 保证）
```

- 每投影处理成本随**该主体的投影存量**线性增长（每个已索引文档/缓存键都要出具回执，含 no-op）；单主体投影存量 = 抽取产出 × 时间，当前未知 → 大主体删除的峰值负载与"空索引核对"成本不可估（threat model T-09 已登记为未解决风险）。
- 在线删除完成与备份物理清除分别报告，不互相冒充（设计规格 §16）；物理清除窗口属 G7。

### 12.2 SLO 初值表（全部为设计规格 §19.2 初始建议，非批准门槛）

> 设计规格 §19.2 前言："以下目标以单区域试点基线……规划，上线前必须用真实消息长度、租户分布和增长率压测复算"。ADR 0002 §12.2：assemble 初值"批准前不得作为发布门禁"。实施计划 §23.2：真实负载 latency/error budget 复算归 G8（Product + SRE，Task 13 前，未关闭"使用设计初值，不能进入发布门禁"）。

| # | SLO 初值 | 测量来源（设计规格 §19.1 指标） | 相关门禁 |
|---|---|---|---|
| S1 | 服务月可用性 ≥ 99.9% | 可用性指标；误差预算见 D3 | G8 |
| S2 | `memory:assemble` p95 ≤ 150 ms，硬超时 ≤ 250 ms | assemble latency 与各来源 latency | G8 |
| S3 | confirmed facts PG 读取可用性 ≥ 99.9% | PG 读可用性 | G8 |
| S4 | turn append p95 ≤ 100 ms | append latency | G8 |
| S5 | Redis 回建成功率 ≥ 99.99% | Redis hit/miss/rebuild | G8 |
| S6 | canonical event → facts 更新 p95 ≤ 5 s，p99 ≤ 30 s | projection lag/watermark | G8 |
| S7 | 普通投影 lag p95 ≤ 30 s | projection lag/watermark | G8 |
| S8 | 用户删除在线投影传播 p95 ≤ 5 min | deletion propagation lag | G9（p95/p99 数值门禁）+ G8 |
| S9 | cross-tenant leakage = 0 | 隔离测试 | 零失败自动化门禁（§19.4） |
| S10 | 未确认 proposal 进入 Prompt = 0 | 组装过滤指标 | 零失败自动化门禁（§19.4） |
| S11 | 删除后被迟到事件复活 = 0 | 删除回执/复活测试 | 零失败自动化门禁（§19.4） |

**纪律：** 上表任何数字在 G8/G9 批准前仅用于设计推导与 Task 13 压测的对照起点；S1–S8 的组件级误差预算分配（如 PG/Redis/Kafka 各自允许的不可用份额在 D3 的 43.2 min/月内如何切分）未定义，归 G8。

## 13. 测量、基线更新与压测门禁（Task 13）

### 13.1 本基线的版本语义

- **v0（本文件）：** 已确认基线 + 公式 + 条件化推导。作用是让 Task 5/6/9/10/11/12 的设计推导有统一、可复算、不虚构的出发点。
- **v1 及以后：** G11 输入（消息长度/租户分布/配额/增长余量）与 Task 13 压测数据落地后，由 Product + SRE 按 G8/G11 复算并修订本文；真实负载变化后按变更控制重新批准（设计规格 §19.4）。

### 13.2 必测指标（与 §19.1 对齐，本文相关子集）

assemble latency 与各来源 latency；Redis hit/miss/rebuild；outbox backlog 与 relay failure；consumer retry/DLQ；projection lag/watermark；deletion propagation lag；lexical/vector recall 数量与融合保留数；token budget 使用。全部不带原始内容（设计规格 §19.1）。

### 13.3 Task 13 压测覆盖与验收（实施计划 §19.5/§19.6）

性能测试至少覆盖：canonical append（对照 D1/D2 与 S4）；confirmed fact read；Redis hit/rebuild（S5）；ES/Qdrant 并行检索；assemble p50/p95/p99（S2）；outbox backlog 恢复（§6.2）；consumer throughput（对照 §6.1 事件率）；index rebuild；deletion fan-out（对照 D6 churn 与 §12.1）。

验收要求："容量测试支持首版流量与增长余量"（实施计划 §19.6）——增长余量以 G11 批准的增长假设为准，不以 §4.3 合成场景为准。

### 13.4 中间纪律（G8/G11 关闭前）

1. 本文所有数值与公式只可用于**设计初值推导**，禁止用于生产容量声明、采购、实例选型或发布门禁（实施计划 §23.2 fail-closed）；
2. 分区数（G10）、连接池/部署规格、ES/Qdrant 分片副本、dimension（G6）各自在其门禁/Task 评审冻结，本文不代行；
3. 评测类数值门禁（含删除传播 p95/p99 的批准值）在 `docs/evaluation-gates.md` 由 Product + ML/Eval + Security + SRE 批准（G9）；未批准前不得开始 Task 1（§14）。

## 14. 决策台账对齐与 Task 1 阻塞

下表逐字对齐 ADR 0001 §14 与实施计划 §23.2（本文不新增、不改写任何门禁语义），并标注容量相关性：

| 门禁 | 待批准项 | Owner | 最晚关闭 | 未批准时 fail-closed 默认 | 容量相关性 |
|---|---|---|---|---|---|
| G1 | GitHub owner、Go module path、CI 权限模型 | 项目 Owner | Task 1 前 | 不初始化 `go.mod`，不创建远端 workflow | —（但为 Task 1 阻塞） |
| G2 | Go 与 PG/Kafka/Redis/ES/Qdrant 精确版本、许可证 | Tech Lead | Task 1 前 | 不引入依赖、不拉取镜像 | 客户端连接卫生/批量能力核验（§11） |
| G3 | 部署/数据驻留区域、KMS/secret manager | Platform + Security | Task 1 前 | 仅本地合成测试配置，不处理真实数据 | 部署规格前提 |
| G4 | tenant/subject identity、JWT/JWKS 与服务代理授权契约 | Security + API Owner | Task 2 前 | 不开放业务 API | — |
| G5 | confirmed namespace/key 白名单 | Product + Security | Task 7 前 | proposal 不得转 confirmed | 决定 fact 投影量（§7.1） |
| G6 | embedding provider/model/dimension/DPA 与降级 | ML + Security | Task 10 前 | 生产 embedding 外发关闭，只用确定性测试向量 | 决定 dim/向量存量（§7.2、§9） |
| G7 | 法务保留、诉讼保全、备份物理清除期限 | Legal + Security | Task 12 前 | 不接真实主体数据，不声称物理清除完成 | 决定 audit/fence 库存上限（§5、§10） |
| G8 | 真实负载 latency/error budget、成本与 token budget 复算 | Product + SRE | Task 13 前 | 使用设计初值，不能进入发布门禁 | S1–S8 组件预算分配、§12.2 |
| G9 | 评测门禁数值（gold set 分层/样本/一致性；precision@5、recall@10、nDCG@10 绝对下限与相对词法基线改善及置信区间；错误记忆率上限；成对任务差异；删除传播 p95/p99） | Product + ML/Eval + Security + SRE | Task 0 退出前 | 不开始 Task 1；阈值必须为批准的数字，禁止 `TBD` 或"优于 baseline" | S8 批准值来源 |
| G10 | Kafka topic 命名/分区数/保留期（ADR 0001 §5.3 保守草案） | Tech Lead + SRE | Task 5 评审 | 不创建生产 topic，仅本地合成配置 | §6.3 公式的唯一填值点 |
| G11 | 平均/峰值消息长度、租户分布、配额与增长余量 | Product + SRE | Task 13 前 | 使用试点基线且禁止生产容量声明 | §2 α/p_app、§4 g、§5（间接）、全部 w_x、D7 有效性的前提 |
| G12 | 首个真实客户端与 shadow 数据集 | Product | Task 14 前 | 不发布 GA | 真实流量形态来源 |

**Task 1 阻塞（明确陈述）：** G1/G2/G3 未批准前，不初始化 `go.mod`、不引入依赖、不拉取镜像、仅本地合成测试配置；**G9 未批准前不开始 Task 1**（G9 为 Task 0 退出门禁；实施计划 §23.2、ADR 0001 §14、ADR 0004 §12 同口径）。本文档属于 Task 0 切片，其交付不解除上述任一阻塞。

## 15. 本文件不是什么（防误读声明）

1. **不是采购或生产 sizing。** §4.3 合成场景、D4 并发上界、D7 均匀示意值都禁止用于采购、实例选型或生产容量声明（G11 fail-closed）。
2. **不冻结任何基础设施数值。** Kafka 分区（G10）、PG 连接池（§11）、ES/Qdrant 分片与副本（§7）、dimension（G6）在本文中只有公式与门禁指向，没有数值。
3. **不把设计初值当批准门槛。** §12.2 全部 SLO 数字为设计初值；发布门禁只能引用 G8/G9 批准后的数值。
4. **不把 50 QPS 当 append 峰值。** §2 纪律适用于本文所有下游公式。
5. **不把 9×10^7 当无条件事实。** 该值仅在稳态、无增长、全量保留三条件下成立（§4.2）。
6. **不推出 audit 记录数。** 365 天保留 ≠ 可由 turn 数推导的记录数（§5）。
7. **不批准任何门禁。** 本文档的评审不替代任何 owner 的 G1–G12 批准。

## 16. 验证记录

### 16.1 算术复算

以下关键算式已用脚本（node）独立复算通过，结果与正文一致：

```text
10^6 ÷ 86,400                    = 11.574074            （D1 μ_app）
10^6 ÷ 1,440                     = 694.444444           （D1 每分钟）
10^6 ÷ 24                        = 41,666.666667        （D1 每小时）
50 × 86,400 ÷ 10^6               = 4.320000             （D2 峰均比上界，精确值）
30 × 24 × 60 × 0.001             = 43.2 min (= 2,592 s) （D3 30 天月误差预算）
31 × 24 × 60 × 0.001             = 44.64 min            （D3 31 天月误差预算）
50 × 0.1 / 50 × 0.15 / 50 × 0.25 = 5 / 7.5 / 12.5       （D4 速率×时延算术示意，非并发上界）
10^6 × 90                        = 90,000,000           （§4.2 条件化库存）
(1.01^90 − 1) ÷ 0.01             = 144.863267           （§4.3 因子）
(1.02^90 − 1) ÷ 0.02             = 247.156656           （§4.3 因子）
10^6 × 365                       = 365,000,000          （§5 仅为 λ_audit=1 算术示意，非估算）
10^6 ÷ 10^5 = 10；9×10^7 ÷ 10^5 = 900                    （D7 均匀示意）
10^6 × 7                         = 7,000,000            （§10 幂等账本条件化下界）
```

### 16.2 引用核对

本文所有 §引用、Task 归属、门禁 owner 与 fail-closed 文本已逐条人工对照：README；设计规格 §1、§8.2、§8.4、§8.8、§8.11、§9.1、§9.3、§13.1、§13.2、§14、§15.1、§16、§17.1、§17.4、§18.2、§19.1–§19.4、§21；实施计划 §6.2–§6.4、§11.5、§19.5–§19.6、§23.1–§23.2、§24；ADR 0001 §5.3–§5.4、§8、§9、§11、§14；ADR 0002 §6、§8.2、§12.2、§17；ADR 0003 D9；ADR 0004 §5、§6、§12；threat model T-09。未发现与上游冲突的陈述；如上游修订，以修订后的上游为准并回改本文。

### 16.3 写入范围与 VCS 状态

- 本次仅新增 `docs/capacity-baseline.md` 一个文件；未修改 README、设计规格、实施计划、ADR、threat model 或任何其他文件；未执行 git init/commit、未安装依赖、未启动任何基础设施。
- `D:/Projects/memX` 当前不是 Git 仓库（`git rev-parse` 返回 not a git repository）；因此本文档与任务验证**不使用、也不宣称 `git diff` 检查通过**（与 ADR 0002/0003/0004 及 threat model 工作约定同口径）。

### 16.4 已知未决（非本文档可关闭）

1. `docs/data-classification.md`、`docs/evaluation-gates.md` 未产出（同属 Task 0 文件清单）：前者关距字段级敏感度与审计覆盖定义（影响 §5、§10），后者承载 G9 数值门禁；
2. G1–G12 全部处于待批准状态；其中 G9 直接阻塞 Task 1，G2/G3/G1 为 Task 1 前置；
3. 抽取产出率（episode/fact/proposal 每 turn 产出）在 Task 8 落地并实测前不可知，§7.1/§10 的行数推导持续缺位；
4. §4.3 增长场景与 D7 均匀值在 G11 批准真实分布前仅为合成演示。

## 17. 后续维护

- G11 输入落地、Task 8/9/10 实测产出率、Task 13 压测完成三者任一发生时，按 §13.1 修订本文并引用批准记录/实测报告；不得只在本文宣称已复算。
- 架构变更（新增投影存储、跨区域、新事件类型、审计覆盖定义变化）须先修订本文再实施。
- 门禁关闭后，§14 表格状态由对应 owner 更新；本文档不代行批准。
