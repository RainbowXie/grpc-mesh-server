# Makefile 使用指南

## 常用命令

```bash
# 查看所有命令
make help

# 构建和运行
make build          # 开发构建
make build-release  # 生产构建（优化）
make run            # 构建并运行
make clean          # 清理构建产物

# 测试
make test           # 运行测试
make check          # 完整检查（fmt + vet + lint + test）

# 证书生成
make certs                                    # 本地开发证书
make certs-lan IP=192.168.1.100              # 局域网证书
make certs-prod DOMAIN=example.com IPS=...   # 生产证书

# Token 生成
make token              # 生成单个 token
make tokens COUNT=5     # 生成多个 token

# 完整设置
make setup              # 依赖 + 证书 + token

# 版本信息
make version            # 显示版本
make info               # 显示完整构建信息
```

## 版本信息

版本号自动从 Git 获取：

```bash
make build
./bin/grpc-mesh-server --version
```

输出示例：
```
grpc-mesh-server
  Version:    v1.0.0
  Build Time: 2024-01-15_10:30:45
  Git Commit: 1a2b3c4
```

## 生产构建

生产构建使用静态链接，无 CGO 依赖：

```bash
make build-release
```

特点：
- ✅ 优化编译（`-trimpath`）
- ✅ 静态链接（`CGO_ENABLED=0`）
- ✅ 独立可执行文件

## 证书生成示例

```bash
# 本地开发
make certs

# 局域网部署
make certs-lan IP=192.168.1.100

# 生产环境（2年有效期）
make certs-prod DOMAIN=prod.example.com IPS=203.0.113.10
```

## 参考

- 完整命令列表：`make help`
- 证书生成详细说明：`scripts/README.md`
- Token 策略：`docs/token_policy.md`
