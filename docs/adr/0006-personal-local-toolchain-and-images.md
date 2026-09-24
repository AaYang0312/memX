# ADR 0006 — 个人版 Task 1 的本地工具链与镜像锁定

- **日期：** 2026-09-23
- **状态：** 个人版合成数据开发选择（项目使用者依 ADR 0005 授权技术选择）；不是 ADR 0004 §8 的生产 G2/G3 冻结或法务意见。
- **范围：** 只为个人开发轨道的 `go test`、只读 CI 和不自动启动的 Compose 静态配置选择可复算版本。真实主体数据、公开服务、生产镜像及第三方模型外发不在此授权内。

## 工具链与依赖

| 项 | 本地选择 | 核验与升级边界 |
|---|---|---|
| Go | `go1.27.0`（`go.mod`，本机 `go version` 与 CI `actions/setup-go` 的 `go-version-file` 一致） | 开发期精确固定；ADR 0004 §9 观察到后续补丁线，接真实数据/生产前评估升级及安全公告，不把当前选择视为最新安全补丁。 |
| Go 第三方依赖 | 无；当前仅标准库 | `go.sum` 不存在是零第三方模块的结果，不伪造锁文件。未来引入驱动/客户端时按 ADR 0004 D4 核对许可证、传递依赖、兼容性并固定模块版本。 |
| CI | checkout v5 `fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09`，setup-go v6 `924ae3a1cded613372ab5595356fb5720e22ba16` | GitHub 官方 tag 指向的 commit 与 action.yml Node 24 runtime 于 2026-09-23 核对；只读 token、无凭据持久化、无生产 secret。 |

## Compose 本地镜像（Linux/amd64 manifest index，未拉取/未运行）

以下 SHA-256 是 `docker buildx imagetools inspect <tag>` 于 2026-09-23 返回的**多架构 manifest index digest**，且均观察到 `linux/amd64`。Compose 使用 `tag@sha256:<digest>`，运行时仍应核对所选平台清单、镜像自身许可与健康检查行为；tag 会变，digest 不随 tag 漂移。

| 服务 | 个人开发选择 | index digest | 许可证/条件（服务端与镜像分开核对） |
|---|---|---|---|
| PostgreSQL | `postgres:18.6-bookworm` | `sha256:3725f4e2499eef5134592b3b4ab79a543ed7f8e533b05b5b637af926630f6650` | PostgreSQL License；仅本地 `memx_test`，PG18 卷挂载到 `/var/lib/postgresql`。 |
| Apache Kafka | `apache/kafka:4.3.1` | `sha256:77e3df9054047a88b520d0cc46e16696d3b22022e1d580aeccd2632df6532837` | Apache-2.0；单节点 KRaft，本地使用官方 Kafka 代替 Redpanda。 |
| Redis | `redis:8.10.1` | `sha256:8a1efc5f479551822b47424ccae982026b633f28818eab0387348120a61e10e2` | Redis 8 官方许可证提供 RSALv2/SSPLv1/AGPLv3 选项；个人本地测试不是生产/再分发法律许可结论。原拟 `8.10.1-bookworm` tag 查不到，故改用已核实的 `8.10.1`。 |
| Elasticsearch | `docker.elastic.co/elasticsearch/elasticsearch:8.19.21` | `sha256:cbf5cd6cfe5532a9c02d510c66d238bf329cd51fe3d57170a2a880aca7d47419` | 官方许可 FAQ 列 ELv2/SSPL/AGPLv3；仅合成数据本地单节点，不作为合法云服务/再分发的批准。与候选 v8 Go 客户端同大版本线，兼容仍须实测。 |
| Qdrant | `qdrant/qdrant:v1.19.0` | `sha256:057ee3a8da769fe7310dd3537b4dc7583bf87a95ce8ac43c0af5a46bc580d1fc` | 官方开源代码 Apache-2.0；镜像具体发行条款与健康命令运行前复核。 |

## 运行纪律与回退

1. Compose 所有服务位于显式 profile；裸 `docker compose up` 不启动任何依赖。没有真实账号或默认生产口令、不开放公网端口；所有配置值只允许合成测试用途。`docker compose --profile core --profile search --profile vector config` 只解析文件，不拉取或启动镜像。
2. PG 是唯一 canonical；Kafka/Redis/ES/Qdrant 即使启动也只是开发依赖，没有业务写入路径。未接入经核验的 PG 客户端前 API readiness 一律 503，不得以 TCP 端口可达冒充健康。
3. 本文不批准任何生产 Go 客户端/镜像/法务结论；部署生产或处理真实个人文本前重新审查 ADR 0004 §8/§12、G3/G4/G6/G7/G9，并完成基础设施版本、镜像 digest、许可证和数据驻留决策。
4. 镜像 manifest 若发生撤销/平台缺失，静态验证失败或运行健康检查不匹配，须更新本 ADR 与 Compose 引用并重新核验；不静默去掉 digest 或回退到 `latest`。
