# 本地合成健康检查与纯领域检查原型（非 Task 1/2 交付）

- **历史**：初建于 `spike/local-bootstrap`，按 [ADR 0005](../adr/0005-personal-development-scope.md) 的个人开发范围决定并入 `main`；不代表生产 Task 1/2 验收。
- **状态**：实验性、仅回环地址、只用 Go 标准库；`go.mod` 现为使用者提供的 GitHub 路径 `github.com/AaYang0312/memX`。Go 版本只匹配本机开发环境，不是生产 G2 版本冻结。
- **边界**：不拉镜像、不接真实 PostgreSQL/凭据/主体数据、不开放业务 API、不部署生产、不构造 MemoryPack。个人开发范围可推进源码，G2/G3/G4/G5/G9 的生产审批仍未完成；实施计划 §6.4、§23.2 的生产验收约束不变。

## 可运行行为

- `cmd/memx-api` 仅开放 `GET /livez`（进程存活，不探测依赖）和 `GET /readyz`（始终 503：canonical 依赖尚未实现），其余路径 404；绝不把未连接的数据库误报为 ready。
- 启动要求 `MEMX_MODE=synthetic`、`MEMX_HTTP_ADDR` 为 `127.0.0.1`/`::1`、`MEMX_PG_DSN` 指向回环地址的 `memx_test`（只作语法验证，绝不拨号）、`MEMX_TEST_SECRET` 非空。未知/重复的 `MEMX_*` 字段和缺失/非法值均拒绝；错误/日志不回显输入或 secret。
- 取消时停止接流并在有界宽限期排空 in-flight 请求；无默认生产凭据。
- `internal/domain/ref` 只生成/解析随机 opaque ref；不同 ref 类型不能混用，原始邮箱/电话和 zero ref 被拒绝。JSON 传输形状未冻结，故刻意拒绝序列化。
- `internal/domain/fact` 仅校验 pending proposal 的 reject/expire 和已确认 fact 的终态迁移；**confirmed proposal 一律返回待批准错误**，无 confirmed fact 创建入口。检查不执行授权、TTL scheduler、PG CAS、audit/outbox 或删除 fence；`pending_conflict` 仅为 reason code，fact 到期使用 `revoked(reason_code=expired)`。
- `internal/domain/projection` 的 epoch/seq/revision/content hash 预检查只用于本地纯函数试验，`Eligible` **不等于可写投影的许可**：还须 PG canonical/fence 复核、inbox pending lease、外部条件写、写后补偿与持久回执。当前无任何投影存储或消费者。

仅用 **虚构值** 运行（PowerShell 示例，不要粘贴真实凭据）：

```powershell
$env:MEMX_MODE = 'synthetic'
$env:MEMX_HTTP_ADDR = '127.0.0.1:18081'
$env:MEMX_PG_DSN = 'postgres://memx:fake@127.0.0.1:5432/memx_test?sslmode=disable'
$env:MEMX_TEST_SECRET = 'fake-local-only'
go run ./cmd/memx-api
# 另一终端：curl.exe http://127.0.0.1:18081/livez   # 200
#          curl.exe http://127.0.0.1:18081/readyz  # 503
```

`go test ./...`、`go vet ./...` 与 `go build ./cmd/memx-api` 为原型检查。**不得**用此原型替代计划 Task 1/2 的依赖健康探针、身份/授权/契约、Compose/CI、版本及许可冻结、评测批准或生产验收；正式部署前须完成相应 owner 决策。
