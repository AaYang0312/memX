# 本地合成健康检查原型（非 Task 1 交付）

- **分支**：`spike/local-bootstrap`，不并入 `main`，除非 Task 0 §6.4 的 owner 门禁关闭并完成复审。
- **状态**：实验性、仅回环地址、只用 Go 标准库；`go.mod` 的 `memx.local/memx` 是本分支临时导入路径，**不是 G1 批准的 Go module path**；Go 版本只匹配本机开发环境，不是 G2 版本冻结。
- **边界**：不创建远端 workflow、不拉镜像、不接真实 PostgreSQL/凭据/主体数据、不开放业务 API、不部署生产、不构造 MemoryPack。G1/G2/G3/G9 均未获批准；实施计划 §6.4、§23.2 以及 ADR 0004 §12 对正式 Task 1 的约束不变。

## 可运行行为

- `cmd/memx-api` 仅开放 `GET /livez`（进程存活，不探测依赖）和 `GET /readyz`（始终 503：canonical 依赖尚未实现），其余路径 404；绝不把未连接的数据库误报为 ready。
- 启动要求 `MEMX_MODE=synthetic`、`MEMX_HTTP_ADDR` 为 `127.0.0.1`/`::1`、`MEMX_PG_DSN` 指向回环地址的 `memx_test`（只作语法验证，绝不拨号）、`MEMX_TEST_SECRET` 非空。未知/重复的 `MEMX_*` 字段和缺失/非法值均拒绝；错误/日志不回显输入或 secret。
- 取消时停止接流并在有界宽限期排空 in-flight 请求；无默认生产凭据。

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

`go test ./...`、`go vet ./...` 与 `go build ./cmd/memx-api` 为原型检查。**不得**用此原型替代计划 Task 1 的依赖健康探针、Compose/CI、版本及许可冻结、评测批准或生产验收；进入正式模块前须删除/替换临时 module path 并经 owner 决策。
