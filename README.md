# memX

独立、可部署、可重放、可审计的多租户 AI 记忆服务。

**当前状态：** 已关联使用者提供的公开 GitHub 仓库 `AaYang0312/memX`；个人开发轨道的 Go module path 为 `github.com/AaYang0312/memX`。项目使用者已明确批准**个人版 Task 1** 以合成数据启动，并授权决定本地技术版本（见 [ADR 0005](docs/adr/0005-personal-development-scope.md) 与 [ADR 0006](docs/adr/0006-personal-local-toolchain-and-images.md)）。当前代码仅有本地健康检查和纯领域预检查，尚无真实数据、业务 API 或外部模型调用。原生产规格仍为 Draft；本授权不代表生产 Task 1/2 或 G9 门禁通过。

## 文档

| 文档 | 用途 |
|---|---|
| [设计规格](docs/specs/2026-09-20-production-memory-service-design.md) | 记忆分层、契约、一致性、安全与 SLO |
| [实施计划](docs/plans/2026-09-20-production-memory-service-implementation.md) | Task 0–14、门禁、测试命令与完成定义 |
| [ADR 草案与个人阶段决定](docs/adr/) | 生产基线、Go 技术栈、公开仓库与个人开发范围例外 |
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

个人版 Task 1 按实施计划 §6.6/§7.7 与 ADR 0005/0006 开发和单独验收，不将试验结果当作生产验收；公开仓库只放源码/文档，不放记忆数据或密钥。原计划 §6.4 / §23.2 的生产审批仍待关闭，尤其 G2 版本/许可与 G9 评测数字。真实个人文本调用外部模型前还须明确 provider、外发范围与隐私条款。业务 API 上线前须完成身份授权；任何检索投影开放前须具备删除防复活能力。

## 目录规划

目标源码、部署与测试目录结构见实施计划 §3；当前 `main` 仅集成个人开发用的 health-only 入口与纯领域预检查，并非完整服务。详情见 [本地原型说明](docs/spikes/2026-09-23-local-bootstrap.md)。
