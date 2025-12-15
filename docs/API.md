# grpc-mesh-server API 文档

## 概述

`grpc-mesh-server` 是一个 Go 实现的反向网关控制平面框架，用于管理内网节点连接、路由 gRPC 调用并提供统一的服务发现和访问控制。

**核心特性：**
- 🔐 TLS 1.3 加密通信
- 🚀 Yamux 多路复用管理
- 🔄 会话生命周期管理
- 📡 反向 gRPC 调用
- 🔑 Token 认证 + ACL 授权
- 🚦 灵活的限流策略
- 🛡️ 熔断器容错机制
- 📊 Prometheus 指标导出
- 🔍 服务发现与方法注册表

---

## 架构概览

```
┌─────────────────────────────────────────────────────────┐
│  外部调用方（业务系统）                                    │
│                                                         │
│  ┌──────────────┐    ┌──────────────┐                 │
│  │  gRPC Client │───▶│  REST API    │                 │
│  └──────────────┘    └──────────────┘                 │
└──────────────────────────┬──────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────┐
│  grpc-mesh-server (公网控制平面)                            │
│                                                         │
│  ┌──────────────┐    ┌──────────────┐                 │
│  │  InvokeProxy │───▶│   ACL +      │                 │
│  │  (gRPC)      │    │  RateLimit   │                 │
│  └──────────────┘    └──────┬───────┘                 │
│                              │                          │
│                              ▼                          │
│                       ┌─────────────┐                  │
│                       │   Session   │                  │
│                       │   Manager   │                  │
│                       └──────┬──────┘                  │
│                              │                          │
│                              ▼                          │
│                       ┌─────────────┐                  │
│                       │   Reverse   │                  │
│                       │   Dialer    │                  │
│                       └──────┬──────┘                  │
│                              │                          │
│                              ▼                          │
│                       ┌─────────────┐                  │
│                       │   Yamux     │                  │
│                       │   Session   │                  │
│                       └──────┬──────┘                  │
└──────────────────────────────┼──────────────────────────┘
                               │ TLS 1.3
                               ▼
┌─────────────────────────────────────────────────────────┐
│  grpc-mesh-node (内网节点)                                    │
│                                                         │
│  ┌──────────────┐    ┌──────────────┐                 │
│  │  gRPC Server │◀───│  Transport   │                 │
│  │  (业务服务)   │    │  Connector   │                 │
│  └──────────────┘    └──────────────┘                 │
└─────────────────────────────────────────────────────────┘
```

---

## 快速开始

### 1. 安装

```bash
go get github.com/grpc-mesh/grpc-mesh-server
```

### 2. 基础用法

```go
package main

import (
    "context"
    "log"
    
    waemu "github.com/grpc-mesh/grpc-mesh-server/pkg/server"
    "github.com/grpc-mesh/grpc-mesh-server/pkg/config"
)

func main() {
    // 1. 加载配置
    cfg, err := config.Load("config.yaml")
    if err != nil {
        log.Fatal(err)
    }
    
    // 2. 创建服务器
    server, err := waemu.NewServer(cfg)
    if err != nil {
        log.Fatal(err)
    }
    
    // 3. 启动服务
    if err := server.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
    
    // 4. 等待停止信号
    server.Wait()
}
```

### 3. 配置文件示例

```yaml
server:
  listen_address: ":50051"
  metrics_address: ":9090"

listener:
  address: ":8443"
  tls_cert_path: "./config/tls/server.crt"
  tls_key_path: "./config/tls/server.key"

security:
  require_token: true
  allowed_tokens:
    - "waemu_your_token_here"

session:
  heartbeat_interval: "30s"
  heartbeat_timeout: "90s"
  cleanup_interval: "1m"
  inactive_timeout: "5m"

acl:
  enabled: true
  default_policy: "deny"
  rules:
    - peer_pattern: "*"
      method_pattern: "health.*"
      action: "allow"
    - peer_pattern: "prod-*"
      method_pattern: "*.Get*"
      action: "allow"

rate_limit:
  enabled: true
  default_rate: 100
  default_burst: 200
```

---

## 核心 API

### 1. Server

服务器主对象，负责协调所有组件。

#### 创建服务器

```go
import (
    waemu "github.com/grpc-mesh/grpc-mesh-server/pkg/server"
    "github.com/grpc-mesh/grpc-mesh-server/pkg/config"
)

// 从配置创建
cfg, _ := config.Load("config.yaml")
server, err := waemu.NewServer(cfg)

// 使用 Builder 模式
server, err := waemu.NewServerBuilder().
    WithConfig(cfg).
    WithLogger(logger).
    WithMetrics(metrics).
    Build()
```

#### 启动和停止

```go
// 启动服务器
ctx := context.Background()
if err := server.Start(ctx); err != nil {
    log.Fatal(err)
}

// 优雅关闭
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := server.Shutdown(ctx); err != nil {
    log.Printf("Shutdown error: %v", err)
}

// 阻塞等待停止信号
server.Wait()
```

#### 健康检查

```go
// 检查服务器状态
if server.IsHealthy() {
    log.Println("Server is healthy")
}

// 获取详细状态
status := server.Status()
log.Printf("Active sessions: %d", status.ActiveSessions)
log.Printf("Total requests: %d", status.TotalRequests)
```

---

### 2. SessionManager

会话管理器，负责维护节点连接状态。

#### 导入

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
)
```

#### 创建会话管理器

```go
config := &registry.SessionConfig{
    HeartbeatInterval: 30 * time.Second,
    HeartbeatTimeout:  90 * time.Second,
    CleanupInterval:   1 * time.Minute,
    InactiveTimeout:   5 * time.Minute,
}

manager := registry.NewSessionManager(config, logger)
```

#### 注册会话

```go
// 注册新会话
session := &registry.Session{
    NodeID:      "node-001",
    YamuxConn:   yamuxConn,
    Metadata:    metadata,
    ConnectedAt: time.Now(),
}

if err := manager.Register(ctx, session); err != nil {
    log.Printf("Failed to register session: %v", err)
}
```

#### 查询会话

```go
// 根据 NodeID 获取会话
session, exists := manager.Get("node-001")
if exists {
    log.Printf("Session uptime: %v", time.Since(session.ConnectedAt))
}

// 列出所有会话
sessions := manager.ListAll()
for _, s := range sessions {
    log.Printf("Node: %s, Status: %s", s.NodeID, s.Status)
}

// 获取统计信息
stats := manager.Stats()
log.Printf("Total sessions: %d", stats.TotalSessions)
log.Printf("Active sessions: %d", stats.ActiveSessions)
```

#### 会话操作

```go
// 更新心跳
if err := manager.Heartbeat(ctx, "node-001"); err != nil {
    log.Printf("Heartbeat failed: %v", err)
}

// 打开 Yamux 流
stream, err := manager.OpenStream(ctx, "node-001")
if err != nil {
    log.Printf("Failed to open stream: %v", err)
}
defer stream.Close()

// 移除会话
if err := manager.Remove("node-001"); err != nil {
    log.Printf("Failed to remove session: %v", err)
}
```

#### 监听会话事件

```go
// 订阅会话事件
eventCh := manager.Subscribe()

go func() {
    for event := range eventCh {
        switch event.Type {
        case registry.SessionEstablished:
            log.Printf("Session established: %s", event.NodeID)
        case registry.SessionClosed:
            log.Printf("Session closed: %s", event.NodeID)
        case registry.SessionTimeout:
            log.Printf("Session timeout: %s", event.NodeID)
        case registry.SessionError:
            log.Printf("Session error: %s - %v", event.NodeID, event.Error)
        }
    }
}()

// 取消订阅
manager.Unsubscribe(eventCh)
```

---

### 3. MethodRegistry

方法注册表，实现服务发现功能。

#### 创建注册表

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
)

methodRegistry := registry.NewMethodRegistry()
```

#### 注册方法

```go
methods := []*registry.MethodDescriptor{
    {
        Name:        "greeter.SayHello",
        Description: "Greet a user",
        Tags:        []string{"greeting"},
        Metadata: map[string]string{
            "timeout": "5s",
        },
    },
    {
        Name:        "calculator.Add",
        Description: "Add two numbers",
        Tags:        []string{"math"},
    },
}

// 更新节点的方法列表
version, err := methodRegistry.UpdateMethods("node-001", methods)
if err != nil {
    log.Printf("Failed to update methods: %v", err)
}
log.Printf("Methods updated, version: %d", version)
```

#### 查询方法

```go
// 获取节点的所有方法
methods, exists := methodRegistry.GetPeerMethods("node-001")
if exists {
    for _, m := range methods {
        log.Printf("Method: %s - %s", m.Name, m.Description)
    }
}

// 获取方法详情
method, exists := methodRegistry.GetMethod("node-001", "greeter.SayHello")
if exists {
    log.Printf("Found method: %s", method.Name)
}

// 查找提供指定方法的所有节点
peers := methodRegistry.FindPeersWithMethod("greeter.SayHello")
log.Printf("Peers with SayHello: %v", peers)
```

#### 模式匹配查询

```go
// 支持通配符查询
methods := methodRegistry.FindMethods("greeter.*")
for nodeID, methodList := range methods {
    log.Printf("Node %s has %d greeter methods", nodeID, len(methodList))
}

// 获取完整目录
catalog := methodRegistry.GetCatalog()
log.Printf("Total peers: %d", len(catalog))
```

#### 统计信息

```go
stats := methodRegistry.Stats()
log.Printf("Total peers: %d", stats.TotalPeers)
log.Printf("Total methods: %d", stats.TotalMethods)
log.Printf("Average methods per peer: %.2f", stats.AverageMethodsPerPeer)
```

---

### 4. InvokeProxy

代理服务，处理外部调用并路由到内网节点。

#### 创建代理

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/server"
)

proxy := server.NewInvokeProxy(
    sessionManager,
    aclPolicy,
    rateLimiter,
    logger,
)
```

#### 执行调用

```go
import (
    pb "github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
)

// 构造请求
req := &pb.InvokeRequest{
    PeerId:        "node-001",
    Method:        "greeter.SayHello",
    Payload:       payload,
    CorrelationId: uuid.New().String(),
    TimeoutMs:     5000,
}

// 执行调用
resp, err := proxy.Invoke(ctx, req)
if err != nil {
    log.Printf("Invoke failed: %v", err)
    return
}

if resp.Success {
    log.Printf("Result: %s", string(resp.Result))
} else {
    log.Printf("Error: %s - %s", resp.Error.Code, resp.Error.Message)
}
```

#### gRPC 服务端实现

```go
type InvokeService struct {
    pb.UnimplementedInvokePlaneServer
    proxy *server.InvokeProxy
}

func (s *InvokeService) Invoke(
    ctx context.Context,
    req *pb.InvokeRequest,
) (*pb.InvokeResponse, error) {
    return s.proxy.Invoke(ctx, req)
}

// 注册服务
grpcServer := grpc.NewServer()
pb.RegisterInvokePlaneServer(grpcServer, &InvokeService{proxy: proxy})
```

---

### 5. ACL (访问控制)

方法级访问控制，支持灵活的规则配置。

#### 定义 ACL 策略

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/server"
)

// 创建静态 ACL
rules := []server.ACLRule{
    {
        PeerPattern:   "*",
        MethodPattern: "health.*",
        Action:        server.ActionAllow,
        Priority:      100,
    },
    {
        PeerPattern:   "prod-*",
        MethodPattern: "*.Get*",
        Action:        server.ActionAllow,
        Priority:      200,
    },
    {
        PeerPattern:   "*",
        MethodPattern: "*.Delete*",
        Action:        server.ActionDeny,
        Priority:      300,
    },
}

acl := server.NewStaticACL(rules)
```

#### 检查权限

```go
// 检查是否允许调用
allowed := acl.Check("prod-node-001", "user.GetProfile")
if !allowed {
    log.Println("Access denied")
    return
}

// 获取匹配的规则
rule, matched := acl.Match("prod-node-001", "user.DeleteAccount")
if matched {
    log.Printf("Matched rule: %s %s -> %s",
        rule.PeerPattern, rule.MethodPattern, rule.Action)
}
```

#### 预定义策略

```go
// 允许所有（开发环境）
acl := server.NewPermissiveACL()

// 拒绝所有（锁定模式）
acl := server.NewRestrictiveACL()

// 组合策略（AND 逻辑）
acl := server.NewCompositeACL(acl1, acl2, acl3)
```

#### 动态更新规则

```go
staticACL := server.NewStaticACL(initialRules)

// 运行时更新
newRules := []server.ACLRule{
    // ... 新规则
}
staticACL.UpdateRules(newRules)
```

#### 从配置文件加载

```go
// 配置文件格式（每行一条规则）
// peer_pattern method_pattern action priority
// * health.* allow 100
// prod-* *.Get* allow 200

acl, err := server.LoadACLFromFile("acl.txt")
if err != nil {
    log.Fatal(err)
}
```

---

### 6. RateLimiter (限流器)

灵活的限流策略，支持多种算法。

#### 令牌桶限流器

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/server"
)

// 创建令牌桶限流器
// 100 请求/秒，突发容量 200
limiter := server.NewTokenBucketLimiter(100, 200)

// 检查是否允许
if !limiter.Allow(ctx, "node-001", "method") {
    return status.Error(codes.ResourceExhausted, "rate limit exceeded")
}
```

#### 滑动窗口限流器

```go
// 创建滑动窗口限流器
// 1000 请求/分钟
limiter := server.NewSlidingWindowLimiter(1000, time.Minute)

if !limiter.Allow(ctx, "node-001", "method") {
    return status.Error(codes.ResourceExhausted, "rate limit exceeded")
}
```

#### 并发数限流器

```go
// 限制最大并发数为 50
limiter := server.NewConcurrencyLimiter(50)

if !limiter.Allow(ctx, "node-001", "method") {
    return status.Error(codes.ResourceExhausted, "too many concurrent requests")
}

// 完成时释放
defer limiter.Release(ctx, "node-001", "method")
```

#### 组合限流器

```go
// 同时应用多个限流策略
limiter := server.NewCompositeLimiter(
    server.NewTokenBucketLimiter(100, 200),       // QPS 限制
    server.NewConcurrencyLimiter(50),              // 并发限制
    server.NewSlidingWindowLimiter(1000, time.Minute), // 分钟级限制
)
```

#### 按节点/方法配置

```go
// 为不同节点配置不同的限流策略
limiterMap := map[string]server.RateLimiter{
    "prod-*":  server.NewTokenBucketLimiter(100, 200),
    "dev-*":   server.NewTokenBucketLimiter(10, 20),
    "default": server.NewTokenBucketLimiter(50, 100),
}

// 根据模式匹配选择限流器
func selectLimiter(nodeID string) server.RateLimiter {
    for pattern, limiter := range limiterMap {
        if matchPattern(pattern, nodeID) {
            return limiter
        }
    }
    return limiterMap["default"]
}
```

---

### 7. CircuitBreaker (熔断器)

熔断器模式，防止故障扩散。

#### 创建熔断器

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/fault"
)

config := &fault.CircuitBreakerConfig{
    MaxFailures:     5,              // 最大连续失败次数
    Timeout:         10 * time.Second, // 熔断超时
    FailureRatio:    0.5,            // 失败率阈值
    HalfOpenMaxAttempts: 3,          // 半开状态最大尝试次数
}

cb := fault.NewCircuitBreaker(config)
```

#### 使用熔断器

```go
// 执行操作，带熔断保护
err := cb.Execute(ctx, func() error {
    // 调用可能失败的操作
    return callRemoteService()
})

if err == fault.ErrCircuitOpen {
    log.Println("Circuit breaker is open")
    return
}
```

#### 监听状态变化

```go
cb.OnStateChange(func(from, to fault.State) {
    log.Printf("Circuit breaker state: %s -> %s", from, to)
    
    if to == fault.StateOpen {
        // 触发告警
        alerting.SendAlert("Circuit breaker opened")
    }
})
```

#### 手动控制

```go
// 手动重置
cb.Reset()

// 强制打开（维护模式）
cb.ForceOpen()

// 强制关闭（恢复服务）
cb.ForceClose()
```

#### 与 SessionManager 集成

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
)

// 创建扩展会话管理器（内置熔断器）
extManager := registry.NewExtendedSessionManager(
    sessionManager,
    methodRegistry,
    circuitBreakerConfig,
    logger,
)

// 健康检查（包含熔断器状态）
health := extManager.HealthCheck("node-001")
log.Printf("Session healthy: %t", health.SessionHealthy)
log.Printf("Circuit open: %t", health.CircuitOpen)
```

---

### 8. Metrics (指标收集)

Prometheus 指标导出。

#### 启用指标

```go
import (
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "net/http"
)

// 启动 metrics 端点
go func() {
    http.Handle("/metrics", promhttp.Handler())
    log.Fatal(http.ListenAndServe(":9090", nil))
}()
```

#### 内置指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `tunnel_active_sessions` | Gauge | 当前活跃会话数 |
| `tunnel_handshakes_total` | Counter | 握手总数 |
| `tunnel_heartbeats_total` | Counter | 心跳总数 |
| `tunnel_session_duration_seconds` | Histogram | 会话持续时间 |
| `invoke_requests_total` | Counter | 调用请求总数 |
| `invoke_requests_failed_total` | Counter | 调用失败总数 |
| `invoke_duration_seconds` | Histogram | 调用延迟 |
| `acl_denials_total` | Counter | ACL 拒绝次数 |
| `ratelimit_rejections_total` | Counter | 限流拒绝次数 |
| `circuit_breaker_state` | Gauge | 熔断器状态（0=closed, 1=open, 2=half-open） |

#### 自定义指标

```go
import (
    "github.com/prometheus/client_golang/prometheus"
)

var (
    customCounter = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "my_custom_counter",
            Help: "A custom counter",
        },
    )
)

func init() {
    prometheus.MustRegister(customCounter)
}

// 使用
customCounter.Inc()
```

---

## 高级用法

### 1. 自定义认证

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/auth"
)

type CustomAuthenticator struct{}

func (a *CustomAuthenticator) Authenticate(
    ctx context.Context,
    nodeID, token string,
) error {
    // 实现自定义认证逻辑
    // 例如：JWT 验证、数据库查询等
    if !isValidToken(token) {
        return auth.ErrInvalidToken
    }
    return nil
}

// 使用自定义认证器
server := waemu.NewServerBuilder().
    WithConfig(cfg).
    WithAuthenticator(&CustomAuthenticator{}).
    Build()
```

### 2. 请求拦截器

```go
import (
    "google.golang.org/grpc"
)

func loggingInterceptor(
    ctx context.Context,
    req interface{},
    info *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler,
) (interface{}, error) {
    start := time.Now()
    
    // 调用前
    log.Printf("Request: %s", info.FullMethod)
    
    // 执行调用
    resp, err := handler(ctx, req)
    
    // 调用后
    duration := time.Since(start)
    log.Printf("Response: %s (took %v)", info.FullMethod, duration)
    
    return resp, err
}

// 注册拦截器
server := waemu.NewServerBuilder().
    WithConfig(cfg).
    WithUnaryInterceptor(loggingInterceptor).
    Build()
```

### 3. 会话状态持久化

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
    "encoding/json"
)

// 保存会话状态
func saveSessionState(manager *registry.SessionManager) error {
    snapshot := manager.Snapshot()
    data, err := json.Marshal(snapshot)
    if err != nil {
        return err
    }
    return os.WriteFile("sessions.json", data, 0644)
}

// 恢复会话状态
func restoreSessionState(manager *registry.SessionManager) error {
    data, err := os.ReadFile("sessions.json")
    if err != nil {
        return err
    }
    
    var snapshot registry.SessionSnapshot
    if err := json.Unmarshal(data, &snapshot); err != nil {
        return err
    }
    
    return manager.Restore(snapshot)
}
```

### 4. 负载均衡

```go
import (
    "github.com/grpc-mesh/grpc-mesh-server/pkg/loadbalancer"
)

// 轮询策略
lb := loadbalancer.NewRoundRobin()

// 根据方法查找节点
peers := methodRegistry.FindPeersWithMethod("greeter.SayHello")
if len(peers) == 0 {
    return errors.New("no available peers")
}

// 选择节点
selectedPeer := lb.Select(peers)

// 执行调用
resp, err := proxy.Invoke(ctx, &pb.InvokeRequest{
    PeerId: selectedPeer,
    Method: "greeter.SayHello",
    Payload: payload,
})
```

#### 其他负载均衡策略

```go
// 加权轮询
weights := map[string]int{
    "node-001": 3,
    "node-002": 2,
    "node-003": 1,
}
lb := loadbalancer.NewWeightedRoundRobin(weights)

// 最少连接
lb := loadbalancer.NewLeastConnections(sessionManager)

// 随机选择
lb := loadbalancer.NewRandom()

// 一致性哈希
lb := loadbalancer.NewConsistentHash()
selectedPeer := lb.SelectByKey(peers, "user-12345")
```

### 5. 动态配置重载

```go
import (
    "github.com/fsnotify/fsnotify"
)

func watchConfig(configPath string, server *waemu.Server) error {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return err
    }
    defer watcher.Close()
    
    if err := watcher.Add(configPath); err != nil {
        return err
    }
    
    for {
        select {
        case event := <-watcher.Events:
            if event.Op&fsnotify.Write == fsnotify.Write {
                log.Println("Config file changed, reloading...")
                
                cfg, err := config.Load(configPath)
                if err != nil {
                    log.Printf("Failed to reload config: %v", err)
                    continue
                }
                
                if err := server.UpdateConfig(cfg); err != nil {
                    log.Printf("Failed to apply config: %v", err)
                }
            }
        case err := <-watcher.Errors:
            log.Printf("Watcher error: %v", err)
        }
    }
}

// 启动配置监听
go watchConfig("config.yaml", server)
```

---

## 部署指南

### 1. 生成证书

```bash
cd grpc-mesh-server

# 开发环境（localhost）
make certs

# 局域网部署
SAN_IPS="192.168.1.100,192.168.1.101" make certs-lan

# 生产环境
SAN_DNS="gateway.example.com" \
SAN_IPS="203.0.113.10" \
make certs-prod
```

### 2. 生成 Token

```bash
# 生成单个 Token
make token

# 生成多个 Token
make tokens COUNT=10
```

### 3. 编译

```bash
# 开发构建
make build

# 生产构建（静态链接）
make build-release
```

### 4. 运行

```bash
# 使用配置文件
./bin/grpc-mesh-server -config config.yaml

# 使用环境变量
export WA_SERVER_LISTEN=":50051"
export WA_LISTENER_ADDRESS=":8443"
./bin/grpc-mesh-server
```

### 5. Docker 部署

```dockerfile
FROM golang:1.21 AS builder
WORKDIR /app
COPY . .
RUN make build-release

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/bin/grpc-mesh-server /usr/local/bin/
COPY --from=builder /app/config /etc/wa-emu/

EXPOSE 50051 8443 9090
CMD ["grpc-mesh-server", "-config", "/etc/wa-emu/config.yaml"]
```

```bash
# 构建镜像
docker build -t grpc-mesh-server:latest .

# 运行容器
docker run -d \
  -p 50051:50051 \
  -p 8443:8443 \
  -p 9090:9090 \
  -v $(pwd)/config:/etc/wa-emu \
  -e WA_NODE_TOKEN=waemu_your_token \
  grpc-mesh-server:latest
```

### 6. Kubernetes 部署

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: grpc-mesh-server
spec:
  replicas: 3
  selector:
    matchLabels:
      app: grpc-mesh-server
  template:
    metadata:
      labels:
        app: grpc-mesh-server
    spec:
      containers:
      - name: grpc-mesh-server
        image: grpc-mesh-server:latest
        ports:
        - containerPort: 50051
          name: grpc
        - containerPort: 8443
          name: tunnel
        - containerPort: 9090
          name: metrics
        env:
        - name: WA_NODE_TOKEN
          valueFrom:
            secretKeyRef:
              name: wa-emu-secrets
              key: token
        volumeMounts:
        - name: config
          mountPath: /etc/wa-emu
        - name: tls
          mountPath: /etc/wa-emu/tls
        livenessProbe:
          httpGet:
            path: /healthz
            port: 9090
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: 9090
          initialDelaySeconds: 5
          periodSeconds: 10
      volumes:
      - name: config
        configMap:
          name: wa-emu-config
      - name: tls
        secret:
          secretName: wa-emu-tls
---
apiVersion: v1
kind: Service
metadata:
  name: grpc-mesh-server
spec:
  selector:
    app: grpc-mesh-server
  ports:
  - name: grpc
    port: 50051
    targetPort: 50051
  - name: tunnel
    port: 8443
    targetPort: 8443
  - name: metrics
    port: 9090
    targetPort: 9090
  type: LoadBalancer
```

---

## 监控和运维

### 1. Prometheus 配置

```yaml
scrape_configs:
  - job_name: 'grpc-mesh-server'
    static_configs:
      - targets: ['localhost:9090']
    metrics_path: '/metrics'
    scrape_interval: 15s
```

### 2. Grafana Dashboard

导入预定义的 Dashboard JSON：

```bash
curl -O https://wa-emu.example.com/dashboards/grpc-mesh-server.json
```

**关键面板：**
- 活跃会话数趋势
- 调用 QPS 和延迟
- 错误率
- 熔断器状态
- 限流统计

### 3. 日志分析

```bash
# 查看实时日志
tail -f /var/log/grpc-mesh-server.log | jq .

# 过滤错误日志
tail -f /var/log/grpc-mesh-server.log | jq 'select(.level=="error")'

# 统计调用次数
tail -f /var/log/grpc-mesh-server.log | \
  jq 'select(.msg=="invoke_request") | .method' | \
  sort | uniq -c
```

### 4. 健康检查端点

```go
// 实现健康检查 HTTP 端点
http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    if server.IsHealthy() {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    } else {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte("Unhealthy"))
    }
})

http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
    if server.IsReady() {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("Ready"))
    } else {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte("Not Ready"))
    }
})
```

---

## 故障排查

### 问题 1：节点无法连接

**症状：**
```
ERROR Failed to accept connection: tls: bad certificate
```

**检查清单：**
1. 证书是否过期
2. CA 证书是否匹配
3. 服务端域名是否正确

```bash
# 验证证书
openssl x509 -in config/tls/server.crt -noout -dates

# 测试 TLS 连接
openssl s_client -connect localhost:8443 \
  -CAfile config/tls/ca.crt
```

### 问题 2：Token 认证失败

**症状：**
```
ERROR Handshake rejected: invalid token
```

**解决方案：**
1. 检查 Token 是否在允许列表中
2. 确认 Token 格式正确（`waemu_` 前缀）
3. 验证配置文件是否正确加载

```bash
# 查看当前配置
./bin/grpc-mesh-server -config config.yaml -check-config
```

### 问题 3：会话频繁超时

**症状：**
```
WARN Session timeout: node-001
```

**可能原因：**
1. 心跳间隔设置过短
2. 网络不稳定
3. 节点负载过高

**调整配置：**
```yaml
session:
  heartbeat_interval: "60s"  # 增加心跳间隔
  heartbeat_timeout: "180s"  # 增加超时时间
```

### 问题 4：内存占用过高

**排查步骤：**

```bash
# 启用 pprof
go tool pprof http://localhost:9090/debug/pprof/heap

# 查看内存分配
go tool pprof -alloc_space http://localhost:9090/debug/pprof/alloc

# 查看 goroutine 泄漏
curl http://localhost:9090/debug/pprof/goroutine?debug=2
```

**常见原因：**
- 会话泄漏（未正确清理）
- Yamux 流未关闭
- 事件订阅未取消

---

## 性能优化

### 1. 连接池配置

```yaml
listener:
  max_connections: 10000
  connection_timeout: "30s"
  
yamux:
  max_streams_per_connection: 256
  window_size: 262144  # 256KB
  keep_alive_interval: "30s"
```

### 2. gRPC 优化

```go
grpcServer := grpc.NewServer(
    grpc.MaxConcurrentStreams(1000),
    grpc.MaxRecvMsgSize(16 * 1024 * 1024),  // 16MB
    grpc.MaxSendMsgSize(16 * 1024 * 1024),
    grpc.KeepaliveParams(keepalive.ServerParameters{
        Time:    30 * time.Second,
        Timeout: 10 * time.Second,
    }),
)
```

### 3. 日志级别

```yaml
logging:
  level: "info"  # 生产环境使用 info 或 warn
  format: "json"
  output: "/var/log/grpc-mesh-server.log"
```

---

## 示例代码

完整示例请参考：
- `examples/basic_server.go` - 基础服务器
- `examples/custom_acl.go` - 自定义 ACL
- `examples/metrics_export.go` - 指标导出
- `examples/load_balancer.go` - 负载均衡

---

## API 参考

完整 Go API 文档：

```bash
godoc -http=:6060
# 访问 http://localhost:6060/pkg/github.com/grpc-mesh/grpc-mesh-server/
```

---

## 更新日志

### v0.1.0 (2024-01-15)
- ✅ 初始版本发布
- ✅ TLS + Yamux 传输层
- ✅ 会话管理
- ✅ 控制流（握手、心跳）
- ✅ 反向 gRPC 调用
- ✅ 方法注册表
- ✅ ACL 授权
- ✅ 限流器
- ✅ 熔断器
- ✅ Prometheus 指标

---

## 许可证

[待定]

---

## 技术支持

- 📧 Email: support@example.com
- 💬 Issues: https://github.com/grpc-mesh/grpc-mesh-server/issues
- 📖 文档: https://wa-emu.example.com/docs
