# memX

独立、可部署、可重放、可审计的多租户 AI 记忆服务。

**当前状态：** 本地 Git 仓库已初始化；Task 0 架构文档为 Proposed 草案。`spike/local-bootstrap` 分支另有仅供本地合成验证的标准库 Go 健康检查与纯领域检查原型（`memx.local/memx` 为临时路径，并非批准的 G1 module path）；尚无外部依赖、基础设施或远端仓库。G1/G2/G3/G9 均未获所需 owner 批准；原型不代表 Task 1 启动或验收。

## 文档

| 文档 | 用途 |
|---|---|
| [设计规格](docs/specs/2026-09-20-production-memory-service-design.md) | 记忆分层、契约、一致性、安全与 SLO |
| [实施计划](docs/plans/2026-09-20-production-memory-service-implementation.md) | Task 0–14、门禁、测试命令与完成定义 |
| [ADR 草案](docs/adr/) | canonical/事件、检索投影、事实确认与 Go 技术栈 |
| [威胁模型](docs/threat-model.md) | 资产、边界、威胁及待实施控制 |
| [容量基线](docs/capacity-baseline.md) | 试点假设与可复算容量推导 |
| [数据分类](docs/data-classification.md) | 存储、外发与保留边界 |
| [评测门禁草案](docs/evaluation-gates.md) | G9 候选数字；须四方批准后才能生效 |

## 架构基线

```text
PostgreSQL    canonical source of truth + transactional outbox
Kafka         事件分发、重放、多投影
Redis         working-memory 投影（可重建）
Elasticsearch 词法召回投影（可重建）
Qdrant        向量召回投影（可重建）
Go            API 服务、outbox relay、worker
```

投影全部可从 PostgreSQL 重建，均不是事实源。

## 核心不变量

1. PostgreSQL 是唯一 source of truth；禁止应用层双写投影。
2. 原始 turn append 与 outbox 同事务；派生记忆全部异步。
3. 模型抽取只能产生 `proposed`，不能直接成为 `confirmed` 事实。
4. 任何检索命中进入 MemoryPack 前必须做 canonical 权限、版本、状态和 TTL 复核。
5. 当前请求值优先于历史记忆；一次性敏感值不得升级为长期事实。
6. 删除使用 tombstone + deletion epoch，迟到事件不得复活数据。

## 下一步

先按实施计划 §6.4 / §23.2 完成 Task 0 的 owner 审批：G1（GitHub owner/module path 与 CI 权限）、G2（精确版本与许可证）、G3（区域/KMS）及 G9（四方批准的评测数字）未关闭前不创建 `go.mod`、远端 workflow，不引入依赖、不拉取镜像，也不启动 Task 1。身份契约等后续决策仍按 §23.2 的阶段门禁关闭。业务 API 上线前须完成身份授权；任何检索投影开放前须具备删除防复活能力。

## 目录规划

正式源码、部署与测试目录结构见实施计划 §3，须在批准后按 Task 1 建立。当前仅有隔离分支的 health-only 原型；详情见 [本地原型说明](docs/spikes/2026-09-23-local-bootstrap.md)。
