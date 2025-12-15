# 脚本使用指南

## gen-dev-certs.sh

生成自签名 TLS 证书。

### 基本用法

```bash
# 本地开发（默认）
bash scripts/gen-dev-certs.sh

# 局域网部署
SAN_IPS="192.168.1.100" bash scripts/gen-dev-certs.sh

# 多个 IP
SAN_IPS="192.168.1.100,192.168.1.101" bash scripts/gen-dev-certs.sh

# 公网部署
SAN_DNS="prod.example.com" SAN_IPS="203.0.113.10" bash scripts/gen-dev-certs.sh

# 生产环境（2年有效期 + 自定义域名 + 多IP）
DAYS=730 CN=prod.example.com \
SAN_DNS="prod.example.com,*.prod.example.com" \
SAN_IPS="192.168.1.100,203.0.113.10" \
bash scripts/gen-dev-certs.sh
```

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DAYS` | `365` | 证书有效期（天） |
| `CN` | `grpc-mesh-server.local` | Common Name |
| `SAN_DNS` | 空 | 额外的域名（逗号分隔） |
| `SAN_IPS` | 空 | 额外的 IP 地址（逗号分隔） |

### 自动分发

脚本会自动将 CA 证书复制到 `grpc-mesh-node/config/ca.crt`。

### 验证证书

```bash
# 查看证书信息
openssl x509 -in config/tls/server.crt -text -noout

# 验证证书链
openssl verify -CAfile config/tls/ca.crt config/tls/server.crt
```

---

## gen-token.sh

生成认证 Token。

### 基本用法

```bash
# 生成单个 token
bash scripts/gen-token.sh

# 生成多个 token
bash scripts/gen-token.sh -c 5

# 自定义长度和前缀
bash scripts/gen-token.sh -l 32 -p "myapp_"
```

### 选项

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `-c, --count` | `1` | 生成数量 |
| `-l, --length` | `40` | Token 长度（不含前缀） |
| `-p, --prefix` | `waemu_` | Token 前缀 |

### Token 配置

生成后需要配置到：
1. `grpc-mesh-server/config/config.yaml` 的 `security.allowed_tokens`
2. `grpc-mesh-node` 通过环境变量 `WA_NODE_TOKEN` 或配置文件

详见 [Token 策略文档](../docs/token_policy.md)。

---

## 快速开始

```bash
# 1. 生成证书
bash scripts/gen-dev-certs.sh

# 2. 生成 token
bash scripts/gen-token.sh

# 3. 配置并启动
# 将 token 添加到 config.yaml，然后：
make run
```
