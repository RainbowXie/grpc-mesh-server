# grpc-mesh-server

`grpc-mesh-server` 是运行在数据中心的控制平面服务，负责统一管理所有 `@grpc-mesh-node` 节点，维护 TLS + Yamux 隧道，并通过反向 gRPC 向内网设备发起远程调用。本文档概述其架构、目录以及使用方式，帮助你快速理解并接入系统。

## 开发状态

- ✅ **Phase 1**: TLS 准备与证书自动化 - 已完成
- ✅ **Phase 2**: Yamux 隧道打底 - 已完成
- ✅ **Phase 3**: 控制流、握手与注册 - 已完成
- ✅ **Phase 4**: gRPC 反向注入 - 核心功能已完成
- ✅ **Phase 5**: 方法注册与容错机制 - 已完成

---

## 架构概览（TLS + Yamux）

1. **TLS Listener**  
   - `pkg/tunnel` 监听（默认）`0.0.0.0:8443`，加载自签或 CA 签发的证书文件。  
   - 当前默认使用单向 TLS，后续可扩展为 mTLS。  
   - 证书文件支持热更新：当 `config.listener.tls_cert_path` / `tls_key_path` 指向的文件变更时，监听器会自动重新加载，无需重启。

2. **Yamux Session**  
   - 每条 TLS 连接建立后，被包装成 `yamux.Server`，单 TCP 连接复用多个逻辑流（控制流、反向 gRPC、调试流等）。  
   - KeepAlive、窗口大小、写入超时等参数可通过配置文件调优。

3. **Control Stream & Handshake**  
   - 节点连上后必须立即打开首个 Yamux 流，发送 JSON Handshake：`node_id`、`token`、`version`、`supported_features`、`metadata`、`timestamp`。  
   - `pkg/control` 会验证时间戳、Token、白名单等规则，失败即拒绝注册。

4. **SessionManager**
   - `pkg/registry` 追踪活跃的 Yamux Session，记录握手信息、心跳时间、控制流。
   - 对外暴露 `OpenStream(ctx, node_id)`、`ControlChannel(node_id)` 等方法，用于发起反向调用或推送指令。
   - `pkg/tunnel.Server` 中的控制流心跳循环会强制刷新最后心跳时间，若在 `server.heartbeat_interval` + grace 窗口内未收到心跳则立即标记断开并触发清理。

5. **Reverse Dialer、Gateway 与 InvokeProxy**  
   - `pkg/reverse` 提供 `Dialer`（将 SessionManager 的 Yamux Session 暴露为 `grpc.WithContextDialer()`）和 `Gateway`（封装 InvokePlane 调用逻辑），并内建超时与连接重试。  
   - `pkg/server.InvokeProxy` 实现 `InvokePlane` gRPC 服务，实现公网调用入口与内网节点之间的流量桥接（将外部 gRPC 请求映射到指定 `node_id` 的 Yamux 流）。  
   - Go 控制面通过该链路在任意时刻向 Rust 节点发起反向 gRPC 请求，实现真正的“公网拉直”调用。

6. **Observability & Resilience**  
   - 隧道层记录连接日志、握手耗时、KeepAlive 状态，SessionManager 定时清理失联节点。  
   - 内建 Prometheus 指标导出器：配置 `server.metrics_address`（默认为 `:9090`）即可暴露 `/metrics`，若留空则自动关闭。  
   - 未来会叠加调用链路 tracing 以及节点级限流策略。

---

## 目录结构

```
grpc-mesh-server/
├── cmd/server/          # main.go，解析配置并启动服务
├── config/              # 示例配置与 TLS 证书占位
├── pkg/
│   ├── config/          # Viper 配置加载
│   ├── control/         # Handshake 校验 & AuthPolicy
│   ├── logging/         # Zap 日志封装
│   ├── registry/        # SessionManager，追踪 Yamux Session
│   ├── rpc/             # .proto 及生成文件（需自行生成）
│   ├── reverse/         # Reverse Dialer & Gateway，封装反向 gRPC 调用
│   ├── server/          # gRPC Server 装配
│   └── tunnel/          # TLS Listener + Yamux 接入
└── go.mod
```

---

## 配置说明

默认配置文件位于 `config/config.yaml`，同时支持环境变量覆盖（前缀 `WAEMU_`，点号自动转为 `_`）。核心字段如下：

```yaml
server:
  grpc_address: ":50051"          # 控制面 gRPC 监听地址
  metrics_address: ":9090"        # Prometheus 指标监听地址（留空则不启动）
  heartbeat_interval: "15s"
  invoke_timeout: "30s"

listener:
  address: ":8443"
  tls_cert_path: "./config/tls/server.crt"
  tls_key_path: "./config/tls/server.key"
  ca_file: "./config/tls/ca.crt"  # 可空，用于 mTLS 或自签 CA
  prefer_server_cipher_suites: true

tunnel:
  accept_backlog: 128
  enable_keepalive: true
  max_stream_window: 1048576
  keepalive_interval: "30s"
  keepalive_timeout: "90s"

security:
  require_token: true
  allowed_tokens:
    - "waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"  # 示例，详见 docs/token_policy.md
  node_whitelist: []              # 留空则允许任意 node_id
  handshake_deadline: "5s"

observability:
  log_level: "info"
```

`tunnel.enable_keepalive` 控制是否向所有 Yamux Session 注入 keepalive probing（默认开启）；`keepalive_interval` / `keepalive_timeout` 用于微调探测频率与写入超时，以便在不同网络质量下维持稳定连接。

> **提示**：Rust 端需要内置与 `listener` 对应的 CA 根证书，才能通过 TLS 验证。

> **Token 策略**：所有节点必须在握手时携带唯一 Token，生成与轮换流程请参考 `docs/token_policy.md`，并确保 `security.allowed_tokens` 与节点侧配置保持一致。

**TLS 证书热重载流程**

1. 更新或重新生成 `config/listener.tls_cert_path` 与 `tls_key_path` 指定的文件，例如替换新的服务端证书。  
2. 确保证书写入完成后落盘（避免临时文件未同步）。  
3. `pkg/tunnel` 内置的证书监听器会在数百毫秒内自动检测到变更，平滑刷新内存中的证书；正在建立或存量的 Yamux 连接无需中断。  
4. 若更新失败，可从日志关键字 `certificate reload failed` 排查原因（权限、格式或文件路径）。

**自签 CA / Server 证书一键生成**

1. 切换到仓库根目录：`cd grpc-mesh-server`。  
2. 执行 `./scripts/gen-dev-certs.sh`（依赖 `openssl`）。可通过环境变量 `CN` / `DAYS` 覆盖默认域名与有效期。  
3. 生成的 `ca.crt`, `ca.key`, `server.crt`, `server.key` 会写入 `config/tls/`，默认 `config/config.yaml` 已指向该路径，Rust 端只需同步 `ca.crt` 作为根证书。

---

## 快速开始

1. **准备环境**  
   - Go 1.21+  
   - `protoc` 或 `buf`（生成 RPC 代码）
   - OpenSSL（生成证书）

2. **生成开发证书**

   ```bash
   cd grpc-mesh-server
   make certs
   # 或手动运行：bash scripts/gen-dev-certs.sh
   ```

3. **构建服务**

   ```bash
   # 使用 Makefile（推荐）
   make build
   
   # 或直接使用 go build
   go build -o bin/grpc-mesh-server ./cmd/server
   ```

4. **查看版本**

   ```bash
   ./bin/grpc-mesh-server --version
   # 输出：
   # grpc-mesh-server
   #   Version:    dev
   #   Build Time: 2024-01-15_10:30:45
   #   Git Commit: 1a2b3c4
   ```

5. **启动服务**

   ```bash
   # 使用 Makefile
   make run
   
   # 或手动运行
   ./bin/grpc-mesh-server -config config/config.yaml
   ```

   启动后会监听：
   - `server.grpc_address`：外部控制面入口（暂未实现业务方法）
   - `listener.address`：TLS + Yamux 隧道入口，等待 Rust 节点上线

4. **运行测试**

   ```bash
   # 运行所有测试
   go test ./...

   # 运行控制流测试套件
   go test ./pkg/tunnel -run TestControlFlow -v

   # 运行带竞态检测的测试
   go test ./pkg/tunnel -race -v

   # 压测隧道（64 并发流）
   go test ./pkg/tunnel -run TestTunnelServerYamuxLoad -count=1
   ```

   测试覆盖：
   - ✅ 会话注册与生命周期
   - ✅ OpenStream 双向通信
   - ✅ 并发流处理（10+ streams）
   - ✅ Token 验证（正向/反向）
   - ✅ Handshake 校验与超时
   - ✅ 会话重连与清理
   - ✅ 心跳超时检测

   详细测试文档：[`docs/control_stream_tests.md`](docs/control_stream_tests.md)（测试用例说明）

---

## InvokePlane Proxy 接入说明

`InvokeProxy` 已集成在 `pkg/server` 中，并自动在 `server.Start()` 时注册到 gRPC Server：

- **对外暴露的服务**：`rpc.InvokePlane`。上游（CLI、自动化、其他控制面组件）可以直接通过 gRPC Dial 连接 `server.grpc_address`，使用标准的 `Invoke / InvokeStream` 方法。
- **调用流程**：上游请求 → InvokeProxy 校验 `peer_id`、补全字段 → Reverse Gateway `DialPeer()` → Yamux `OpenStream()` → Rust 节点上的 `reverse_gateway` Handler。
- **错误语义**：  
  - `codes.NotFound`：`peer_id` 不在线或未注册。  
  - `codes.InvalidArgument`：缺失 `peer_id`/`method` 等必填字段。  
  - `codes.Unavailable`：隧道拨号或 gRPC 调用失败（例如节点断链、方法内部错误）。

> **实践建议**：为 InvokeProxy 增加调用限流与 ACL，可在接入新业务 API 或 CLI 时优先完成；同时配合 Prometheus/Tracing 记录 `peer_id`、`method`、耗时等指标，便于运维排障。

---

## Rust 侧对接指南（摘要）

- **TLS**：使用 `tokio-rustls` 加载同一 CA，向服务器发起 `TcpStream -> TLS` 连接。
- **Yamux**：连接成功后构建 `yamux::Connection (Mode::Client)`，并维护重连逻辑。
- **控制流握手**：首个流发送 JSON Handshake，必须包含 node_id/token/timestamp 等字段。
- **反向 gRPC**：实现一个 `Incoming` 适配器，将 `yamux.accept_stream()` 暴露给 tonic Server。
- **心跳 & 指令**：通过控制流互换心跳、方法列表或调试信息（详见 `pkg/control` 设计）。

---

## Phase 3 完成情况

✅ **控制流、握手与注册** (已完成)

实现内容：
- **控制流协议**：Handshake + Heartbeat 消息帧编码/解码
- **SessionManager**：会话注册、心跳刷新、自动清理
- **认证授权**：Token 校验、NodeID 验证、时间戳防重放
- **事件系统**：会话生命周期事件发布（建立/关闭/超时）
- **全面测试**：12 个测试覆盖注册、OpenStream、认证、超时、清理等场景

关键文件：
- `pkg/control/` - 控制流消息协议
- `pkg/registry/` - SessionManager 实现
- `pkg/tunnel/server.go` - 控制循环与心跳处理
- `pkg/tunnel/controlflow_test.go` - 测试套件（700+ 行）

测试结果：
```bash
$ go test ./pkg/tunnel -v
PASS
ok      github.com/grpc-mesh/grpc-mesh-server/pkg/tunnel      1.781s
```

详细说明请参考：[`ROADMAP.md`](../ROADMAP.md) Phase 3 部分

---

## 后续路线

1. **Phase 4** (进行中)  
   - 完成 Go 侧反向 gRPC Dialer，真正打通调用链。  
   - 补充方法级 ACL 与限流策略。
   - 端到端 Invoke 测试。

2. **Phase 5**  
   - 动态方法注册与发现。
   - 容错机制（重试、熔断）。
   - 并发与限流策略。

3. **Phase 6**  
   - Prometheus 指标、OpenTelemetry tracing。
   - 压测与性能优化（100+ 节点）。
   - 安全审计、mTLS、灰度部署。

---

如需了解更详细的路线规划，请参考仓库根目录的 `ROADMAP.md`。欢迎按需扩展与贡献。
