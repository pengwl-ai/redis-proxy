# SPEC.md — Redis Proxy（Task2：全命令覆盖）

> 设计日期：2026-07-13
> 目标：基于 Task1 的 RESP 协议代理骨架，覆盖 xaas-bbc / xaas-bbc-proxy 业务使用的全部 Redis 命令，并提供功能测试与性能压测客户端。

---

## 1. 目标

基于 Phase 1 已实现的 RESP2 协议代理（支持 GET/SET/DEL/AUTH + 主备路由 + 自动重连），扩展为**全命令透明代理**，覆盖业务线 `xaas-bbc` 与 `xaas-bbc-proxy` 两个模块使用的全部 Redis 命令（约 50+ 个），并提供测试客户端用于功能验证与性能基准测试。

**目标用户**：后端应用团队，通过标准 RESP 客户端连接代理，获得与直连 Redis 一致的使用体验。

---

## 2. 核心功能与验收标准

### 2.1 命令覆盖范围

基于 `docs/task/xaas-bbc与xaas-bbc-proxy-Redis使用分析.md` 的静态分析结果，代理须支持以下全部命令：

#### String 类（18 个）

| 命令 | 说明 |
|------|------|
| `GET` | 读取键值 |
| `SET` | 设置键值 |
| `SETEX` | 设置键值并指定过期秒数 |
| `SETNX` | 键不存在时设置 |
| `GETSET` | 获取旧值并设置新值 |
| `GETDEL` | 获取值并删除键 |
| `MGET` | 批量获取 |
| `MSET` | 批量设置 |
| `INCR` | 自增 1 |
| `INCRBY` | 按步长自增 |
| `DECR` | 自减 1 |
| `DECRBY` | 按步长自减 |
| `EXISTS` | 判断键是否存在 |
| `DEL` | 删除键 |
| `TTL` | 获取剩余生存时间（秒） |
| `PTTL` | 获取剩余生存时间（毫秒） |
| `EXPIRE` | 设置过期时间（秒） |
| `PEXPIRE` | 设置过期时间（毫秒） |
| `EXPIREAT` | 设置到期时间戳 |
| `RENAME` | 重命名键 |

#### Hash 类（10 个）

| 命令 | 说明 |
|------|------|
| `HGET` | 读取 hash 字段 |
| `HSET` | 设置 hash 字段 |
| `HMSET` | 批量设置 hash 字段 |
| `HMGET` | 批量读取 hash 字段 |
| `HGETALL` | 读取全部 hash 字段 |
| `HDEL` | 删除 hash 字段 |
| `HEXISTS` | 判断 hash 字段是否存在 |
| `HKEYS` | 获取 hash 全部字段名 |
| `HSETNX` | hash 字段不存在时设置 |
| `HINCRBY` | hash 字段按步长自增 |

#### List 类（4 个）

| 命令 | 说明 |
|------|------|
| `RPUSH` | 列表右侧插入 |
| `LPUSH` | 列表左侧插入 |
| `LLEN` | 列表长度 |
| `LRANGE` | 获取列表范围元素 |

#### Set 类（6 个）

| 命令 | 说明 |
|------|------|
| `SADD` | 集合添加元素 |
| `SREM` | 集合移除元素 |
| `SMEMBERS` | 获取集合全部成员 |
| `SISMEMBER` | 判断是否为集合成员 |
| `SCARD` | 集合元素数量 |
| `SMISMEMBER` | 批量判断集合成员 |

#### Key/Scan 类（4 个）

| 命令 | 说明 |
|------|------|
| `KEYS` | 模式匹配查找键 |
| `SCAN` | 游标迭代遍历键 |
| `SSCAN` | 游标迭代集合成员 |
| `MEMORY USAGE` | 估算键的内存占用 |

#### Server 类（3 个）

| 命令 | 说明 |
|------|------|
| `PING` | 服务探活 |
| `SELECT` | 切换数据库 |
| `AUTH` | 身份认证 |

#### BitMap 类（2 个）

| 命令 | 说明 |
|------|------|
| `SETBIT` | 设置位值 |
| `GETBIT` | 获取位值 |

#### Scripting 类（2 个）

| 命令 | 说明 |
|------|------|
| `EVAL` | 执行 Lua 脚本 |
| `SCRIPT LOAD` | 预加载 Lua 脚本 |

#### Transaction 类（5 个）

| 命令 | 说明 |
|------|------|
| `MULTI` | 开启事务 |
| `EXEC` | 执行事务 |
| `DISCARD` | 放弃事务 |
| `WATCH` | 监视键 |
| `UNWATCH` | 取消监视 |

**总计：54 个命令**

### 2.2 多词命令处理

`MEMORY USAGE` 和 `SCRIPT LOAD` 为多词命令（子命令模式）。当前 `resp.ReadCommand` 仅提取第一个数组元素作为命令名。须扩展为：若第一个元素属于已知子命令前缀（`MEMORY`、`SCRIPT`），则将前两个元素拼合为完整命令名（如 `MEMORY USAGE`、`SCRIPT LOAD`）。

**验收**：`redis-cli MEMORY USAGE mykey` 和 `redis-cli SCRIPT LOAD "return 1"` 能正确转发并返回结果。

### 2.3 Pipeline 支持

Pipeline 是 `xaas-bbc` 和 `xaas-bbc-proxy` 的高频使用模式（go-redis 的 `Pipeline()`/`Pipelined()`/`TxPipeline()`）。

当前代理串行 read-forward-write 可正确响应 pipeline：客户端批量发送的多个 RESP 命令在 `bufio.Reader` 中缓冲，代理逐一读取、转发、回复。

**处理策略**：
- 串行处理 pipeline 命令（正确性优先，避免并发引入乱序）
- 不在代理层做命令聚合/批转发到后端

**验收**：使用 go-redis `Pipelined()` 发送 100 条 SET 命令，全部成功返回 `+OK`，顺序正确。

### 2.4 MULTI/EXEC 事务支持

代理以透明方式转发事务命令。MULTI/EXEC/DISCARD/WATCH/UNWATCH 作为普通 RESP 命令被转发到后端 Redis。事务内的命令（返回 `+QUEUED`）同样透传。

- 代理**不解析**事务状态，不缓存/排队命令
- 整个事务的所有命令在同一个后端连接上执行
- 事务中的写命令**不**额外转发到 standby（避免 MULTI 上下文外执行）

**验收**：`MULTI → SET k v → EXEC` 正确返回 `[OK, QUEUED, [OK]]`。

### 2.5 SCAN 系列命令支持

`SCAN`/`SSCAN` 返回游标+数组的 RESP 结构。代理已在 `resp` 包中实现完整的 Array/BulkString 解析，无需额外处理。

**验收**：`SCAN 0 MATCH * COUNT 10` 正确返回游标和键列表。

### 2.6 主备路由规则扩展

核心路由规则：

| 命令类型 | 路由行为 |
|----------|----------|
| 写命令（SET、DEL、SETEX、SETNX、HSET、SADD、LPUSH、RPUSH、INCR、DECR、EXPIRE、SETBIT、EVAL 等） | 主节点 → 成功返回客户端 → 异步 best-effort 写 standby |
| 读命令（GET、MGET、HGET、HGETALL、SMEMBERS、LRANGE、TTL、EXISTS、SCAN、KEYS 等） | 仅主节点 |
| 管理命令（PING、SELECT） | 仅主节点 |
| 事务命令（MULTI、EXEC、DISCARD、WATCH、UNWATCH） | 仅主节点（不转发 standby） |
| AUTH | 主节点 → 异步转发 standby |
| MEMORY USAGE | 仅主节点 |
| SCRIPT LOAD | 主节点 → 异步转发 standby |

**写命令判定规则**：命令修改了 Redis 中的数据状态即为写命令。只读命令仅查询不修改。

### 2.7 自动重连（已在 Task1 实现）

- Redis 重启后，`Backend.Forward()` 检测到连接断开时自动触发后台重连
- 重连使用指数退避（100ms → 5s 上限）
- 重连成功后，若 backend 仍为 primary 角色则自动重新注册到池
- `atomic.Bool` 防重入保护，同一时刻仅一条重连 goroutine

### 2.8 测试客户端

独立目录 `cmd/bench/` 下实现一个 Go 测试客户端，支持：

#### 功能测试（`-mode=func`）
- 逐条发送每个受支持命令，验证代理返回正确的 RESP 回复
- 覆盖所有 54 个命令的基本调用路径
- 验证 Pipeline 场景（批量命令→批量回复）
- 验证 MULTI/EXEC 事务场景

#### 性能压测（`-mode=perf`）
- 可配置并发连接数（`-c`）、每连接命令数（`-n`）、Pipeline 批量大小（`-pipeline`）
- 输出 QPS、P50/P95/P99 延迟
- 支持混合读写场景（`-rw-ratio`）
- 命令类型权重可配（String/Hash/Set 混合比例）

#### 使用方式
```bash
# 功能测试
go run ./cmd/bench -addr=127.0.0.1:6379 -mode=func

# 性能压测
go run ./cmd/bench -addr=127.0.0.1:6379 -mode=perf -c=50 -n=10000 -pipeline=10
```

---

## 3. 项目结构

```
redis-proxy/
├── main.go                         # 入口、配置解析、启动
├── config.yaml                     # 示例配置
├── go.mod / go.sum
├── SPEC.md                         # 本文件
├── internal/
│   ├── proxy/
│   │   ├── server.go               # TCP 监听、accept 循环
│   │   └── session.go              # 单客户端会话：RESP 读、命令路由、回复写
│   ├── resp/
│   │   ├── reader.go               # RESP2 协议解析（含 ReadCommand 多词命令支持）
│   │   ├── writer.go               # RESP2 协议序列化
│   │   └── message.go              # 消息类型定义
│   ├── backend/
│   │   ├── pool.go                 # 后端连接池、角色管理、重连调度
│   │   └── backend.go              # 单后端：TCP 连接、角色、转发、断开回调
│   ├── api/
│   │   └── handler.go              # Gin HTTP 管理 API
│   └── config/
│       └── config.go               # YAML 配置解析与校验
├── cmd/
│   └── bench/
│       └── main.go                 # 功能测试 + 性能压测客户端
└── docs/
    └── task/
        ├── task1.md
        ├── task2.md
        └── xaas-bbc与xaas-bbc-proxy-Redis使用分析.md
```

---

## 4. 代码风格

- 标准 Go 惯例：`gofmt`、`go vet`、effective Go
- 仅跨包需要时导出标识符
- 错误处理：返回 error，不 panic（`main` 启动阶段 fatal 除外）
- 日志：`log/slog` 结构化日志（DEBUG 逐命令、INFO 生命周期、ERROR 故障）
- Context 传播：所有网络操作接受 `context.Context` 并尊重取消
- 不使用第三方 RESP 库 — 协议解析器自建
- 使用 `net.Conn` 直连 Redis 后端（不依赖 go-redis 等客户端库）
- 设计文档使用中文

---

## 5. 测试策略

### 单元测试
- RESP 解析器：每种消息类型 round-trip 测试，多词命令提取测试
- Backend 连接池：重连回调触发、primary 重注册、防重入
- 配置解析：全命令白名单校验

### 集成测试
- 使用 `miniredis` 或真实 Redis 实例
- 启动代理 → 发送 54 个命令 → 验证回复正确
- Pipeline 多命令批量测试
- MULTI/EXEC 事务透传测试
- 主备路由：写命令应同时到达 primary 和 standby

### 端到端测试（测试客户端）
- 使用 `cmd/bench -mode=func` 运行全部功能用例
- 使用 `cmd/bench -mode=perf` 获取基线性能数据

### 运行方式
```bash
go test ./...                    # 单元 + 集成测试
go run ./cmd/bench -mode=func    # 功能验证
go run ./cmd/bench -mode=perf    # 性能压测
```

---

## 6. 边界

### Always Do
- 使用 `slog` 结构化日志，合理分 DEBUG/INFO/WARN/ERROR 级别
- 启动时校验配置，不合法则 fail fast
- 优雅关闭：SIGINT/SIGTERM → 停 accept → 排空现有连接 → 关闭后端连接
- 后端连接错误透传给客户端 `-ERR` 回复
- 代理本身不认证客户端（AUTH 透传）

### Ask First
- 新增任何第三方依赖（当前仅依赖 Gin 和 YAML 解析）
- 改变配置文件格式
- 添加 TLS 支持
- 添加 RESP3 支持
- 实现连接池（当前每后端单连接）

### Never Do
- 在代理层缓存或重放命令
- 日志中输出认证凭据（AUTH 密码）
- 篡改后端 Redis 回复内容
- 在代理层实施访问控制
- 修改 Redis 后端的数据

---

## 7. Task3：性能优化 — 后端连接池 + Pipeline 批转发

> 基线：代理 2,241 QPS vs 直连 Redis 88,245 QPS（~40x 差距）
> 根因：每 Backend 单连接 + 全局互斥锁，所有并发客户端串行排队

### 7.1 后端连接池

当前每个 Backend 只有 1 个 `net.Conn`，`Forward()` 用 `b.mu.Lock()` 串行化所有访问。改为 per-Backend 连接池。

#### 数据结构

```go
type pooledConn struct {
    conn   net.Conn
    reader *bufio.Reader
}

type Backend struct {
    Name    string
    Addr    string
    Role    Role

    pool       chan *pooledConn   // 可用连接
    poolSize   int                // 池大小上限
    maxPool    int                // 最大连接数
    sem        chan struct{}      // 信号量（控制并发获取）
    
    onDisconnect func()
    reconnecting atomic.Bool
}
```

#### 连接获取/归还

```
acquire(ctx):
  1. 从 pool channel 非阻塞取 → 拿到直接返回
  2. pool 空 → 尝试 sem 获取槽位 → 创建新连接 → 返回
  3. sem 满 → 阻塞等待 pool channel 或 ctx 取消

release(pc):
  pc 健康 → pool <- pc
  pc 断开 → close(pc.conn), sem <- {}（释放槽位），触发 tryReconnect
```

#### Pool 大小配置

| 层级 | 默认值 | 说明 |
|------|--------|------|
| `pool_size` | 20 | 预建/常驻连接数，对标 go-redis `PoolSize` |
| `max_pool_size` | 100 | 连接数上限，对标 go-redis `PoolSize` 硬上限 |
| `min_idle` | 5 | 最低保活连接数 |

配置项添加到 `config.yaml` 的 backend 条目和全局默认值。

#### 重连逻辑适配

连接断开时：
1. 坏连接从池中移除（不归还）
2. 异步创建新连接补充到池中
3. 若所有连接断开 → 触发全局 reconnect loop（现有逻辑复用）

### 7.2 Pipeline 批转发

当前 session 逐条 read-forward-write，即使客户端 pipeline 了 10 条命令，代理也是逐条等待后端回复。

优化：session 检测到 reader buffer 中有多个完整命令时，批量转发到后端。

#### 实现

```
Run() 主循环改为：
  1. ReadCommand() → 读取第一条
  2. 检测 reader.Buffered() > 0 → 批量读取剩余命令
  3. 将 N 条命令的 raw bytes 拼接 → 调用 backend.ForwardBatch(raws)
  4. ForwardBatch 通过连接池获取一条连接，连续写入 N 条，再连续读取 N 条回复
  5. 将 N 条回复按序写回客户端
```

#### Backend.ForwardBatch

```go
func (b *Backend) ForwardBatch(ctx context.Context, raws [][]byte) ([][]byte, error) {
    pc, err := b.acquire(ctx)
    if err != nil {
        return nil, err
    }
    defer b.release(pc)
    
    // Pipeline write
    for _, raw := range raws {
        pc.conn.Write(raw)
    }
    // Pipeline read (顺序对应)
    replies := make([][]byte, len(raws))
    for i := range raws {
        msg, err := resp.ReadMessage(ctx, pc.reader)
        if err != nil {
            b.removeConn(pc)
            return nil, err
        }
        replies[i] = msg.Bytes()
    }
    return replies, nil
}
```

### 7.3 验收标准

- `redis-benchmark -t set -n 100000 -c 100 -q` 通过代理应达到直连的 **50% 以上** QPS
- 所有已有测试通过，无回归
- Pool 配置可通过 `config.yaml` 调整

### 7.4 变更文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/backend/backend.go` | 重写 | 单连接 → 连接池 + Forward/ForwardBatch |
| `internal/backend/pool.go` | 修改 | 适配新 Backend 接口 |
| `internal/proxy/session.go` | 修改 | 支持批量读取 + batch 转发 |
| `internal/config/config.go` | 修改 | 新增 pool_size / max_pool_size 配置字段 |
| `config.yaml` | 修改 | 新增连接池配置示例 |
