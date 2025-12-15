# Phase 5 功能使用指南

## 概述

Phase 5 引入了三个核心功能，用于增强系统的可靠性和可观测性：

1. **动态方法发现**：节点动态注册和查询可用方法
2. **熔断器模式**：自动故障隔离和恢复
3. **重试策略**：智能重试失败的操作

---

## 1. 动态方法发现

### 功能说明

方法注册表允许 Rust 节点动态注册其提供的方法，Go 服务端可以查询和发现这些方法。

### 基本使用

#### 在 Go 端注册方法

```go
import "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"

// 创建方法注册表
methodRegistry := registry.NewMethodRegistry()

// 注册方法
peerID := registry.PeerID("node-123")
methods := []registry.MethodInfo{
    {
        Name:        "health.echo",
        Description: "Echo service for health checks",
        Tags:        []string{"health", "diagnostic"},
        Metadata: map[string]string{
            "timeout": "5s",
            "version": "1.0",
        },
    },
    {
        Name:        "whatsapp.send",
        Description: "Send WhatsApp message",
        Tags:        []string{"messaging"},
    },
}

version := methodRegistry.UpdateMethods(peerID, methods)
fmt.Printf("Registered methods, version: %d\n", version)
```

#### 查询方法

```go
// 获取节点的所有方法
catalog := methodRegistry.GetCatalog(peerID)
if catalog != nil {
    fmt.Printf("Node %s has %d methods\n", catalog.PeerID, len(catalog.Methods))
}

// 获取特定方法
method, err := methodRegistry.GetMethod(peerID, "health.echo")
if err == nil {
    fmt.Printf("Method: %s - %s\n", method.Name, method.Description)
}

// 查找所有提供特定方法的节点
peers := methodRegistry.FindPeersWithMethod("health.echo")
fmt.Printf("Found %d peers with health.echo\n", len(peers))

// 列出所有方法
allMethods := methodRegistry.ListAllMethods()
fmt.Printf("Total unique methods: %d\n", len(allMethods))
```

#### 模式匹配查询

```go
// 支持通配符查询
results := methodRegistry.MatchMethodPattern("health.*")     // 前缀匹配
results = methodRegistry.MatchMethodPattern("*.send")        // 后缀匹配
results = methodRegistry.MatchMethodPattern("*message*")     // 中缀匹配
results = methodRegistry.MatchMethodPattern("*")             // 匹配所有

for peerID, methods := range results {
    fmt.Printf("Peer %s: %d matching methods\n", peerID, len(methods))
}
```

#### 统计信息

```go
stats := methodRegistry.GetStats()
fmt.Printf("Total peers: %d\n", stats["total_peers"])
fmt.Printf("Total methods: %d\n", stats["total_methods"])
fmt.Printf("Unique method names: %d\n", stats["unique_method_names"])
fmt.Printf("Avg methods per peer: %.2f\n", stats["avg_methods_per_peer"])
```

### 控制流集成

#### UpdateMethods 消息格式

```go
import "github.com/grpc-mesh/grpc-mesh-server/pkg/control"

// 创建 UpdateMethods 消息
updateMsg := control.NewUpdateMethods("node-123", []control.MethodInfo{
    {Name: "health.echo", Description: "Echo service"},
    {Name: "health.ping", Description: "Ping service"},
})

// 包装为控制消息
ctrlMsg := control.NewUpdateMethodsMessage(updateMsg)

// 通过控制流发送（在实际代码中）
// controlStream.SendControlMessage(ctrlMsg)
```

#### 在服务器端处理

```go
// 当收到 UpdateMethods 消息时
func handleUpdateMethods(msg *control.UpdateMethods, sessionMgr *registry.ExtendedSessionManager) error {
    return sessionMgr.UpdateMethods(msg)
}
```

---

## 2. 熔断器模式

### 功能说明

熔断器在检测到故障时自动"断开"对故障服务的调用，避免级联失败，并在一段时间后自动尝试恢复。

### 三种状态

1. **Closed（关闭）**：正常状态，所有请求通过
2. **Open（打开）**：熔断状态，所有请求直接失败（不调用后端）
3. **HalfOpen（半开）**：探测状态，允许少量请求测试后端是否恢复

### 基本使用

```go
import (
    "context"
    "github.com/grpc-mesh/grpc-mesh-server/pkg/fault"
)

// 创建熔断器配置
config := fault.DefaultConfig()
config.MaxFailures = 5                // 5 次连续失败后打开
config.Timeout = 30 * time.Second     // 30 秒后尝试恢复
config.MaxHalfOpenRequests = 1        // 半开状态只允许 1 个请求
config.FailureRatio = 0.5             // 失败率 > 50% 时打开
config.MinSamples = 10                // 至少 10 次调用才计算失败率

// 创建熔断器
cb := fault.NewCircuitBreaker(config)

// 使用熔断器保护调用
ctx := context.Background()
err := cb.Execute(ctx, func() error {
    // 这里是实际的业务逻辑
    return someRemoteCall()
})

if err != nil {
    if errors.Is(err, fault.ErrCircuitOpen) {
        fmt.Println("Circuit breaker is open, request rejected")
    } else {
        fmt.Printf("Request failed: %v\n", err)
    }
}
```

### 状态监控

```go
// 获取熔断器状态
state := cb.State()
fmt.Printf("Circuit breaker state: %s\n", state) // closed, open, or half-open

// 获取详细统计
stats := cb.Stats()
fmt.Printf("Failures: %d\n", stats.Failures)
fmt.Printf("Successes: %d\n", stats.Successes)
fmt.Printf("Total calls: %d\n", stats.TotalCalls)
fmt.Printf("Failure ratio: %.2f%%\n", stats.FailureRatio * 100)

if stats.State == fault.StateOpen {
    fmt.Printf("Will retry in: %v\n", stats.TimeUntilHalfOpen)
}
```

### 状态变化回调

```go
config := fault.DefaultConfig()
config.OnStateChange = func(from, to fault.State) {
    log.Printf("[CircuitBreaker] State changed: %s -> %s", from, to)
    
    // 可以在这里发送告警
    if to == fault.StateOpen {
        alerting.SendAlert("Circuit breaker opened!")
    }
}

cb := fault.NewCircuitBreaker(config)
```

### 手动控制

```go
// 手动重置熔断器（强制关闭）
cb.Reset()

// 手动打开熔断器（维护模式）
cb.ForceOpen()
```

### 与 Session 集成

```go
import "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"

// 创建扩展会话管理器（自动为每个 peer 创建熔断器）
cbConfig := fault.DefaultConfig()
sessionMgr := registry.NewExtendedSessionManager(cbConfig)

// 执行调用时自动使用熔断器
peerID := registry.PeerID("node-123")
err := sessionMgr.ExecuteWithCircuitBreaker(ctx, peerID, func() error {
    // 调用远程方法
    return invokeRemoteMethod(peerID, "health.echo", payload)
})

// 查看熔断器状态
stats := sessionMgr.GetCircuitBreakerStats(peerID)
if stats != nil {
    fmt.Printf("Peer %s circuit: %s\n", peerID, stats.State)
}

// 手动重置某个 peer 的熔断器
sessionMgr.ResetCircuitBreaker(peerID)
```

---

## 3. 重试策略

### 功能说明

重试策略提供了智能的自动重试机制，支持指数退避、抖动和可配置的重试条件。

### 基本使用

```go
import "github.com/grpc-mesh/grpc-mesh-server/pkg/fault"

// 创建重试策略
policy := fault.DefaultRetryPolicy()
policy.MaxRetries = 3                        // 最多重试 3 次
policy.InitialDelay = 100 * time.Millisecond // 初始延迟 100ms
policy.MaxDelay = 10 * time.Second           // 最大延迟 10s
policy.Multiplier = 2.0                      // 指数倍数 2.0
policy.Jitter = true                         // 启用抖动

// 执行带重试的操作
ctx := context.Background()
err := policy.Execute(ctx, func() error {
    return someUnreliableOperation()
})

if errors.Is(err, fault.ErrMaxRetriesExceeded) {
    fmt.Println("All retries exhausted")
}
```

### 自定义可重试错误

```go
policy := fault.DefaultRetryPolicy()

// 自定义判断哪些错误可以重试
policy.RetryableErrors = func(err error) bool {
    // 不重试上下文错误
    if errors.Is(err, context.Canceled) {
        return false
    }
    
    // 不重试熔断器错误
    if errors.Is(err, fault.ErrCircuitOpen) {
        return false
    }
    
    // 不重试业务逻辑错误
    if errors.Is(err, ErrInvalidInput) {
        return false
    }
    
    // 其他错误都重试
    return true
}
```

### 重试回调

```go
policy := fault.DefaultRetryPolicy()

// 在每次重试前执行
policy.OnRetry = func(attempt int, err error, delay time.Duration) {
    log.Printf("Retry attempt %d after %v, previous error: %v", 
        attempt, delay, err)
}

err := policy.Execute(ctx, func() error {
    return doSomething()
})
```

### 带返回值的重试

```go
// 如果操作需要返回数据
result, err := policy.ExecuteWithData(ctx, func() (interface{}, error) {
    data, err := fetchDataFromRemote()
    return data, err
})

if err == nil {
    // 类型断言获取结果
    myData := result.(*MyDataType)
    fmt.Printf("Got data: %v\n", myData)
}
```

### 便捷函数

```go
// 使用预定义的指数退避策略
err := fault.ExponentialBackoff(ctx, 3, func() error {
    return doSomething()
})
```

### 创建可重用的 Retryer

```go
// 创建可重用的重试器
retryer := fault.NewRetryer(fault.DefaultRetryPolicy())

// 多次使用
err1 := retryer.Do(ctx, operation1)
err2 := retryer.Do(ctx, operation2)
```

---

## 4. 组合使用示例

### Session 调用with熔断器和重试

```go
import (
    "context"
    "github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
    "github.com/grpc-mesh/grpc-mesh-server/pkg/fault"
)

func InvokeWithFaultTolerance(
    sessionMgr *registry.ExtendedSessionManager,
    peerID registry.PeerID,
    method string,
    payload []byte,
) error {
    ctx := context.Background()
    
    // 创建重试策略
    retryPolicy := fault.DefaultRetryPolicy()
    retryPolicy.MaxRetries = 2
    retryPolicy.RetryableErrors = func(err error) bool {
        // 熔断器打开时不重试
        return !errors.Is(err, fault.ErrCircuitOpen)
    }
    
    // 带重试的熔断器保护调用
    return retryPolicy.Execute(ctx, func() error {
        return sessionMgr.ExecuteWithCircuitBreaker(ctx, peerID, func() error {
            // 实际的远程调用
            conn, err := sessionMgr.OpenStream(ctx, peerID)
            if err != nil {
                return err
            }
            defer conn.Close()
            
            // 执行 gRPC 调用
            return performGRPCCall(conn, method, payload)
        })
    })
}
```

### 健康检查与方法发现

```go
func PerformHealthCheck(sessionMgr *registry.ExtendedSessionManager) {
    // 获取所有会话的健康状态
    healthStatus := sessionMgr.HealthCheck(context.Background())
    
    for peerID, status := range healthStatus {
        fmt.Printf("Peer %s: %s\n", peerID, status)
        
        // 如果节点健康，列出其提供的方法
        if status == "healthy" {
            catalog := sessionMgr.GetMethodCatalog(peerID)
            if catalog != nil {
                fmt.Printf("  Methods: %v\n", len(catalog.Methods))
                for name, method := range catalog.Methods {
                    fmt.Printf("    - %s: %s\n", name, method.Description)
                }
            }
        }
    }
    
    // 获取综合统计
    stats := sessionMgr.GetExtendedStats()
    fmt.Printf("\nOverall stats:\n")
    fmt.Printf("  Sessions: %v\n", stats["sessions"])
    fmt.Printf("  Method stats: %v\n", stats["method_stats"])
    fmt.Printf("  Circuit breakers: %v\n", stats["circuit_breakers"])
}
```

### 智能方法路由

```go
func RouteMethodCall(
    sessionMgr *registry.ExtendedSessionManager,
    method string,
    payload []byte,
) error {
    // 查找提供该方法的所有节点
    peers := sessionMgr.FindPeersWithMethod(method)
    if len(peers) == 0 {
        return fmt.Errorf("no peers available for method %s", method)
    }
    
    // 过滤出健康的节点
    healthStatus := sessionMgr.HealthCheck(context.Background())
    healthyPeers := make([]registry.PeerID, 0)
    for _, peerID := range peers {
        if healthStatus[peerID] == "healthy" {
            healthyPeers = append(healthyPeers, peerID)
        }
    }
    
    if len(healthyPeers) == 0 {
        return fmt.Errorf("no healthy peers for method %s", method)
    }
    
    // 简单轮询选择（实际可以使用更复杂的负载均衡策略）
    selectedPeer := healthyPeers[rand.Intn(len(healthyPeers))]
    
    // 执行调用
    return InvokeWithFaultTolerance(sessionMgr, selectedPeer, method, payload)
}
```

---

## 5. 配置建议

### 开发环境

```go
// 熔断器配置（宽松）
cbConfig := fault.Config{
    MaxFailures:         10,              // 允许更多失败
    Timeout:             10 * time.Second, // 快速恢复
    MaxHalfOpenRequests: 3,
    FailureRatio:        0.7,              // 70% 失败率才熔断
    MinSamples:          5,
}

// 重试配置（激进）
retryPolicy := fault.RetryPolicy{
    MaxRetries:   5,
    InitialDelay: 50 * time.Millisecond,
    MaxDelay:     5 * time.Second,
    Multiplier:   1.5,
    Jitter:       true,
}
```

### 生产环境

```go
// 熔断器配置（严格）
cbConfig := fault.Config{
    MaxFailures:         3,                // 快速失败
    Timeout:             60 * time.Second, // 较长的冷却时间
    MaxHalfOpenRequests: 1,
    FailureRatio:        0.5,              // 50% 失败率即熔断
    MinSamples:          20,
    OnStateChange: func(from, to fault.State) {
        // 发送告警到监控系统
        sendAlert(fmt.Sprintf("Circuit breaker: %s -> %s", from, to))
    },
}

// 重试配置（保守）
retryPolicy := fault.RetryPolicy{
    MaxRetries:   2,
    InitialDelay: 200 * time.Millisecond,
    MaxDelay:     30 * time.Second,
    Multiplier:   3.0,
    Jitter:       true,
    OnRetry: func(attempt int, err error, delay time.Duration) {
        log.Warn("Retrying operation", 
            "attempt", attempt, 
            "delay", delay, 
            "error", err)
    },
}
```

---

## 6. 最佳实践

### 1. 方法注册

- ✅ 在节点启动时注册所有方法
- ✅ 方法名使用点号分隔的命名空间（如 `service.method`）
- ✅ 提供清晰的描述和标签
- ✅ 使用 Metadata 存储版本、超时等配置
- ❌ 避免频繁更新方法列表（每次更新会增加版本号）

### 2. 熔断器使用

- ✅ 为每个外部依赖使用独立的熔断器
- ✅ 根据服务特点调整 Timeout 和 MaxFailures
- ✅ 监控熔断器状态变化，及时告警
- ✅ 在维护时使用 ForceOpen 主动熔断
- ❌ 不要为快速失败的操作使用熔断器（如参数验证）

### 3. 重试策略

- ✅ 只对幂等操作使用自动重试
- ✅ 对非幂等操作使用显式的用户确认
- ✅ 合理设置 MaxRetries，避免雪崩
- ✅ 使用 Jitter 避免"雷鸣群效应"
- ❌ 不要重试明确的业务错误（如权限拒绝）

### 4. 监控与告警

```go
// 定期检查系统健康状态
func MonitorSystemHealth(sessionMgr *registry.ExtendedSessionManager) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        stats := sessionMgr.GetExtendedStats()
        
        // 检查熔断器状态
        cbStats := stats["circuit_breakers"].(map[string]interface{})
        openCircuits := 0
        for _, stat := range cbStats {
            if stat.(map[string]interface{})["state"] == "open" {
                openCircuits++
            }
        }
        
        if openCircuits > 0 {
            log.Warn("Open circuit breakers detected", "count", openCircuits)
        }
        
        // 导出指标到 Prometheus
        metricsOpenCircuits.Set(float64(openCircuits))
        metricsActiveSessions.Set(float64(stats["sessions"].(int)))
    }
}
```

---

## 7. 故障排查

### 熔断器一直打开

**可能原因：**
1. 后端服务确实不可用
2. MaxFailures 设置过低
3. Timeout 设置过短，没有足够时间恢复

**解决方案：**
```go
// 检查熔断器统计
stats := cb.Stats()
fmt.Printf("Failures: %d, Ratio: %.2f, Time until recovery: %v\n",
    stats.Failures, stats.FailureRatio, stats.TimeUntilHalfOpen)

// 如果确认后端已恢复，手动重置
cb.Reset()
```

### 重试次数过多

**可能原因：**
1. 后端响应慢，导致超时重试
2. 网络不稳定
3. MaxRetries 设置过高

**解决方案：**
```go
// 减少重试次数
policy.MaxRetries = 1

// 增加初始延迟
policy.InitialDelay = 500 * time.Millisecond

// 只对特定错误重试
policy.RetryableErrors = func(err error) bool {
    // 只对网络错误重试
    return isNetworkError(err)
}
```

### 方法找不到

**可能原因：**
1. 节点还未注册方法
2. UpdateMethods 消息丢失
3. 节点已断开连接

**解决方案：**
```go
// 检查节点是否在线
if _, ok := sessionMgr.Get(peerID); !ok {
    log.Error("Session not found", "peer", peerID)
}

// 检查方法注册
catalog := sessionMgr.GetMethodCatalog(peerID)
if catalog == nil {
    log.Error("No methods registered", "peer", peerID)
} else {
    log.Info("Registered methods", "peer", peerID, "count", len(catalog.Methods))
}
```

---

## 总结

Phase 5 提供的功能显著提升了系统的可靠性和可观测性：

- **动态方法发现**：实现了服务注册和发现，支持灵活的方法查询和路由
- **熔断器模式**：自动隔离故障服务，防止级联失败，支持自动恢复
- **重试策略**：智能重试失败操作，提高系统容错能力

这些功能相互配合，为构建高可用的分布式系统提供了坚实的基础。
