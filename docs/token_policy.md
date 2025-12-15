# 令牌策略规范（grpc-mesh-server）
本文档定义了节点令牌的需求与操作流程，所有`@grpc-mesh-node`客户端在建立控制流握手时均须提供该令牌。其目标是在后续升级为双向TLS（mTLS）或签名认证机制前，提供一套简洁、可审计的节点身份验证方案。

---

## 目标
1.  **唯一性**——每个已注册节点均拥有专属令牌。
2.  **可撤销性**——无需改动正在运行的服务端程序，即可完成令牌的轮换或吊销。
3.  **纵深防御**——令牌校验作为TLS协议的补充机制；即便证书颁发机构（CA）被攻破，攻击者也无法直接获取控制平面的访问权限。
4.  **运维简便性**——令牌为短文本数据块，可通过持续集成（CI）密钥管理工具、机密信息存储服务或手动脚本完成部署配置。

---

## 令牌格式

| 字段       | 要求                                          |
|------------|-----------------------------------------------|
| 长度       | 32-64个可打印字符（采用Base64URL或十六进制编码） |
| 字符集     | `A-Za-z0-9-_`（符合Base64URL编码规范）|
| 示例       | `waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9`      |

令牌**禁止**包含空白字符。若采用Base64编码方式，建议优先使用URL安全字符集（即`-`和`_`），避免出现字符转义问题。

---

## 生命周期与轮换流程
1.  **生成**
    - 使用提供的脚本生成令牌：
      ```bash
      # 生成单个 token
      bash scripts/gen-token.sh
      
      # 生成多个 token（例如：5 个）
      bash scripts/gen-token.sh -c 5
      
      # 自定义长度和前缀
      bash scripts/gen-token.sh -l 32 -p "myapp_"
      ```
    - 或手动使用 OpenSSL：`openssl rand -base64 48 | tr '+/' '-_' | cut -c1-40`，然后添加前缀 `waemu_`。
    - 将令牌存储至安全的密钥管理服务中（如Vault、AWS Secrets Manager等）。

2.  **分发到 Go 服务端**
    - 在 `config/config.yaml` 配置文件中更新 `security.allowed_tokens` 字段：
      ```yaml
      security:
        require_token: true
        allowed_tokens:
          - "waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"
          - "waemu_TmpNextRotationToken"  # 轮换期间的新 token
      ```
    - 或通过环境变量覆盖该配置（如果支持）。

3.  **分发到 Rust 客户端**
    - **方式 1：配置文件**（推荐用于测试环境）
      在 `grpc-mesh-node/config/reverse_gateway_config.json` 中设置：
      ```json
      {
        "node": {
          "token": "waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"
        }
      }
      ```
      ⚠️ **重要**：修改配置文件后需要重新编译以嵌入新 token：
      ```bash
      cd grpc-mesh-node
      cargo build --release --bin reverse_gateway
      ```
    
    - **方式 2：环境变量**（推荐用于生产环境）
      ```bash
      export WA_NODE_TOKEN="waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"
      ./reverse_gateway
      ```
      环境变量优先级高于配置文件，无需重新编译。
4.  **激活**
    - 重启 `grpc-mesh-server` 服务使新令牌列表生效。
    - 未来将支持热重载（通过 SIGHUP 信号）。
5.  **轮换**
    - 在至少一个重连周期内，将新令牌与旧令牌并存（即允许列表中同时保留新旧令牌）。
    - 将所有节点逐步切换至使用新令牌。
    - 从允许列表中移除旧令牌。
6.  **吊销**
    - 立即从配置中移除受损令牌，并重启服务或刷新配置。
    - 可选操作：将受影响节点的`node_id`添加至`security.node_whitelist`白名单中，实现额外的访问管控。

---

## 快速开始指南

### 1. 生成 Token

```bash
cd grpc-mesh-server

# 生成一个 token
bash scripts/gen-token.sh

# 输出示例：
# waemu_a4Fu1Kjz5nxtxeR8udKxTYiHhdfL9rAyI3429ESv
```

### 2. 配置 Go 服务端

编辑 `grpc-mesh-server/config/config.yaml`：

```yaml
security:
  require_token: true
  allowed_tokens:
    - "waemu_a4Fu1Kjz5nxtxeR8udKxTYiHhdfL9rAyI3429ESv"  # 使用生成的 token
    - "waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"         # 示例 token
  node_whitelist: []  # 留空允许任意 node_id
  handshake_deadline: "5s"
```

### 3. 配置 Rust 客户端

**方式 A：环境变量（推荐生产环境）**

```bash
export WA_NODE_TOKEN="waemu_a4Fu1Kjz5nxtxeR8udKxTYiHhdfL9rAyI3429ESv"
./reverse_gateway
```

**方式 B：配置文件（测试环境）**

编辑 `grpc-mesh-node/config/reverse_gateway_config.json`：

```json
{
  "node": {
    "token": "waemu_a4Fu1Kjz5nxtxeR8udKxTYiHhdfL9rAyI3429ESv"
  }
}
```

然后重新编译：

```bash
cd grpc-mesh-node
cargo build --release --bin reverse_gateway
```

### 4. 验证

启动服务后，Rust 客户端应该能成功连接。如果 token 不匹配，将看到类似错误：

```
ERROR Handshake rejected: invalid token
```

---

## 配置映射参考

### Go 服务端配置

```yaml
security:
  require_token: true
  allowed_tokens:
    - "waemu_7RCx4i4T6gU3O9Gqcx4-SvHMRN1V8dJ9"
    - "waemu_TmpNextRotationToken"
  node_whitelist:
    - "node-lab-001"
    - "node-prod-042"
  handshake_deadline: "5s"
```

**注意事项**
- 仅在本地/开发环境中，允许将`require_token`设置为`false`。
- `node_whitelist`为可选配置项；若配置该列表，令牌校验将在白名单校验**之后**执行。
- 若使用环境变量配置，需遵循`WAEMU_SECURITY_ALLOWED_TOKENS`的命名规范，多个令牌之间用英文逗号分隔。

---

## 校验流程
1.  建立TLS会话连接。
2.  Rust节点开启控制流，发送包含`token`（令牌）、`node_id`（节点ID）、`timestamp`（时间戳）的握手消息。
3.  服务端执行以下校验步骤：
    - 时间戳新鲜度校验（依据`handshake_deadline`配置的超时时间）。
    - 白名单校验（若已配置）。
    - 令牌存在性校验与允许列表归属校验。

伪代码如下：
```
if cfg.require_token && token == "" -> reject
if cfg.allowed_tokens empty -> accept any non-empty token
if token ∉ allowed_tokens -> reject
```

---

## 运维检查清单
- [ ] 令牌通过密码学安全伪随机数生成器生成，并已安全存储。
- [ ] 令牌已添加至`allowed_tokens`配置项（或通过环境变量覆盖配置）。
- [ ] 节点配置已完成更新。
- [ ] 服务端已完成配置重载或重启。
- [ ] 握手日志中显示`token accepted`（令牌已接受）的信息级日志。
- [ ] 令牌轮换周期结束后，已移除旧令牌。

---

## 未来增强计划
1.  **签名令牌（JWT/HMAC）**——在令牌中嵌入过期时间与节点元数据。
2.  **双向TLS（mTLS）**——将令牌与证书身份进行绑定。
3.  **控制平面API**——提供令牌的增删改查（CRUD）接口，并生成审计日志。

在上述功能落地前，请严格遵循本手册的操作流程，防止未授权节点注册会话。
