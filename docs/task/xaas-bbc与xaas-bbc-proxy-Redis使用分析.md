# xaas-bbc / xaas-bbc-proxy Redis 使用分析报告

> 分析日期：2026-07-13
> 分析目标：两个模块使用的 Redis 命令清单 + Redis 连接库是否具备自动重连功能
> 分析方式：静态代码扫描（go.mod、import、命令调用点、连接初始化 Options 结构体）

---

## 一、概览

| 维度 | xaas-bbc | xaas-bbc-proxy |
|---|---|---|
| 主要 Redis 客户端库 | `github.com/go-redis/redis/v9 v9.0.0-rc.2`（新）<br>`github.com/go-redis/redis/v8 v8.11.5`（旧 beego 层）<br>`github.com/garyburd/redigo v1.6.4`（最旧 beego 层；仅用于错误常量 `redis.ErrNil` 比对与 beego session/cache 适配器内部） | `github.com/redis/go-redis/v9 v9.7.0`（go-redis 迁移到 redis 组织后的新路径，与 v9.0-rc.2 同源）<br>`github.com/garyburd/redigo v1.6.4`（`gobase/webbase/api_controller.go:12` 直接 import 用于 `redis.ErrNil` 错误常量比对；beego session 适配器内部也使用） |
| 分布式锁库 | `github.com/bsm/redislock v0.8.2` | — |
| 客户端初始化路径数 | 3 套封装层（`base/goredis` v9、`base/beego/goredis` v8、`base/beego/myredis` redigo） | 1 套封装层（`gobase/goredis`） |
| 是否单实例 / 集群 | 单实例 `redis.NewClient` | 单实例 `redis.NewClient` |

**关键事实**：两个模块底层都用 go-redis v9 系列（go-redis 自 v9.0 起从 `github.com/go-redis/redis` 迁移到 `github.com/redis/go-redis`，API 完全一致），xaas-bbc 用的是 `v9.0.0-rc.2`（非稳定版），xaas-bbc-proxy 用的是 `v9.7.0`（稳定版）。此外 xaas-bbc 还并存 go-redis v8 与 redigo 两套遗留客户端。

---

## 二、xaas-bbc 模块 Redis 命令清单

### 2.1 客户端库与初始化点

| 文件:行 | 变量 | 客户端类型 | 配置来源 |
|---|---|---|---|
| `base/goredis/init.go:69-78` | `gRedisClient` | `*redis.Client`（go-redis/v9） | `/xaas/base_etc/env.conf` `[redis]` section |
| `base/beego/goredis/init.go:72-81` | `gRedisClient` | `*redis.Client`（go-redis/v8） | 同上 |
| `base/beego/myredis/once.go:336-361` | `pool` | `*redis.Pool`（redigo） | 同上 |

### 2.2 命令分类汇总

#### String 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `GET` | `base/goredis/init.go:140,154,212`、`base/beego/goredis/init.go:126,154`、`base/beego/myredis/multi.go:626,641,656,674,1191,1258`、`apps/branch/models/redis.go:30`、`apps/cloudbbc-miniapi/models/redis.go:31`、`apps/cloudbbc-openapi/models/redis.go:231,438,445,480,507` |
| `SET` | `base/goredis/init.go:132`、`base/beego/goredis/init.go:122`、`base/beego/myredis/multi.go:576,591,605`、`base/beego/webbase/sessionmgr/manager.go:182`、`apps/branch/models/redis.go:74`、`apps/cloudbbc-miniapi/models/redis.go:57` |
| `SETEX` | `base/goredis/init.go:136`、`apps/cloudbbc-openapi/models/redis.go:48,535`、`apps/cloudbbc-metrics/models/redis.go:70` |
| `SETNX` | `base/goredis/init.go:167,171`、`base/beego/goredis/init.go:130`、`base/beego/myredis/multi.go:704`、`base/mutex/multi.go:12`、`base/aksk/aksk_strict.go:124` |
| `GETSET` | `base/beego/myredis/multi.go:1193` |
| `GETDEL` | `base/beego/myredis/once.go:169`（事务） |
| `MGET` | `base/goredis/init.go:144`、`base/beego/myredis/once.go:207`、`3party/beego/cache/redis/redis.go:98` |
| `MSET` | `base/beego/myredis/once.go:213` |
| `INCR` | `base/goredis/init.go:216`、`base/beego/goredis/init.go:158`、`base/beego/myredis/multi.go:161,176,196,227,258`、`base/apilimit/limit.go:32`、`base/apilimit/v2limit.go:42`、`base/beego/apilimit/limit.go:32`、`base/beego/apilimit/v2limit.go:40` |
| `INCRBY` | `base/goredis/init.go:220`、`base/beego/myredis/multi.go:1110` |
| `DECR` | `base/goredis/init.go:224`、`base/beego/myredis/once.go:101`、`base/beego/myredis/multi.go:284,304,335` |
| `EXISTS` | `base/goredis/init.go:128`、`base/beego/goredis/init.go:118`、`base/beego/myredis/once.go:57`、`base/beego/myredis/multi.go:80`、`base/beego/webbase/sessionmgr/manager.go:223`、`base/aksk/aksk_strict.go:102` |
| `DEL` | `base/goredis/init.go:208`、`base/beego/goredis/init.go:150`、`base/beego/myredis/multi.go:719,740,761,678`、`base/beego/myredis/once.go:303,316,177,181`、`base/mutex/multi.go:22`、`base/apilimit/limit.go:90`、`base/apilimit/v2limit.go:100,107`、`base/beego/apilimit/limit.go:90`、`base/beego/apilimit/v2limit.go:96,103`、`apps/branch/models/redis.go:98,200`、`apps/cloudbbc-miniapi/models/redis.go:173`、`apps/cloudbbc-insideapi/models/redis.go:68` |
| `TTL` | `base/goredis/init.go:159,204`、`base/beego/goredis/init.go:146`、`base/beego/myredis/multi.go:146,1263`、`base/beego/webbase/sessionmgr/manager.go:163` |
| `PTTL` | `base/beego/myredis/multi.go:131` |
| `EXPIRE` | `base/goredis/init.go:232`、`base/beego/goredis/init.go:162`、`base/beego/myredis/multi.go:111,201,232,309,386,454,886,925`、`base/beego/webbase/sessionmgr/manager.go:258`、`base/beego/webbase/xusr/middleware.go:215`、`base/apilimit/limit.go:39`、`base/apilimit/v2limit.go:49`、`base/beego/webbase/session/user.go:60` |
| `PEXPIRE` | `base/beego/myredis/once.go:61` |
| `EXPIREAT` | `base/beego/myredis/multi.go:263,340,417,1090` |
| `RENAME` | `base/beego/myredis/once.go:129`、`base/beego/bloom/bloom.go:304`、`base/beego/bloom/redis/redis.go:182` |

#### Hash 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `HGET` | `base/goredis/init.go:179,187`、`base/beego/myredis/multi.go:1005,1020,1035,1050` |
| `HSET` | `base/goredis/init.go:174,175`、`base/beego/goredis/init.go:134`、`base/beego/myredis/once.go:215`、`base/beego/myredis/multi.go:861,881` |
| `HMSET` | `base/beego/myredis/once.go:223`、`base/beego/myredis/multi.go:906,921` |
| `HMGET` | `base/beego/myredis/once.go:243`、`base/beego/myredis/multi.go:987` |
| `HGETALL` | `base/goredis/init.go:192,196`、`base/beego/goredis/init.go:138`、`base/beego/myredis/multi.go:969,1125,1144` |
| `HDEL` | `base/goredis/init.go:200`、`base/beego/goredis/init.go:142`、`base/beego/myredis/once.go:235`、`base/beego/myredis/multi.go:954` |
| `HEXISTS` | `base/goredis/init.go:148` |
| `HKEYS` | `base/goredis/init.go:183` |
| `HSETNX` | `base/beego/myredis/once.go:231`、`base/beego/myredis/multi.go:939` |
| `HINCRBY` | `base/beego/myredis/once.go:284`、`base/beego/myredis/multi.go:1065,1085` |

#### List 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `RPUSH` | `base/goredis/init.go:268` |
| `LPUSH` | `base/beego/myredis/once.go:199`、`base/beego/myredis/multi.go:798` |
| `LLEN` | `base/beego/myredis/once.go:195`、`base/beego/myredis/multi.go:777` |
| `LRANGE` | `base/goredis/init.go:264`、`base/beego/myredis/once.go:203`、`base/beego/myredis/multi.go:815` |

#### Set 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `SADD` | `base/goredis/init.go:252,274`、`base/beego/goredis/init.go:178`、`base/beego/myredis/once.go:120`、`base/beego/myredis/multi.go:361,381,412,449` |
| `SREM` | `base/goredis/init.go:236`、`base/beego/goredis/init.go:166`、`base/beego/myredis/once.go:132`、`base/beego/myredis/multi.go:512`、`base/beego/webbase/sessionmgr/manager.go:188,249,279,397`、`base/webbase/session/session.go:337` |
| `SMEMBERS` | `base/goredis/init.go:240`、`base/beego/goredis/init.go:170`、`base/beego/myredis/once.go:136`、`base/beego/myredis/multi.go:528` |
| `SISMEMBER` | `base/goredis/init.go:256`、`base/beego/myredis/once.go:140`、`base/beego/myredis/multi.go:544` |
| `SCARD` | `base/goredis/init.go:244` |

#### Key/Scan 命令

| 命令 | 调用点（file:line） |
|---|---|
| `KEYS` | `base/beego/myredis/multi.go:65`、`3party/beego/cache/redis/redis.go:142` |
| `SCAN` | `base/goredis/init.go:288` |
| `SSCAN` | `apps/cloudbbc-status/models/redis.go:32` |
| `MEMORY USAGE` | `base/goredis/init.go:248`、`base/beego/goredis/init.go:174` |

#### Server 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `PING` | `base/goredis/init.go:81`、`base/beego/goredis/init.go:84`、`base/webbase/session/store.go:50`、`base/beego/myredis/once.go:357`（TestOnBorrow） |
| `SELECT` | `3party/beego/cache/redis/redis.go:202`、`3party/beego/session/redis/sess_redis.go:213` |
| `AUTH` | `3party/beego/cache/redis/redis.go:196`、`3party/beego/session/redis/sess_redis.go:206` |

#### Pipeline / Transaction 命令

| 命令 | 调用点（file:line） |
|---|---|
| `Pipeline()` | `base/goredis/init.go:272,284`、`base/apilimit/v2limit.go:57`、`apps/cloudbbc-openapi/models/redis.go:75,200`、`apps/cloudbbc-status/models/redis.go:51,82,103`、`apps/cloudbbc-metrics/models/redis.go:20,40`、`apps/devlic-syncer/models/redis.go:21,63` |
| `Pipelined()` | `apps/cloudbbc-metrics/models/redis.go:20,40` |
| `MULTI/EXEC` (redigo) | `base/beego/myredis/multi.go:191,222,253,299,330,376,407,444,479,663,670`、`base/beego/webbase/sessionmgr/manager.go:177,210`、`apps/branch/models/redis.go:191`、`apps/branch/models/common.go:1471`、`apps/cloudbbc-miniapi/models/redis.go:164`、`apps/cloudbbc-insideapi/models/redis.go:59`、`base/beego/apilimit/v2limit.go:56-59` |

#### BitMap 命令

| 命令 | 调用点（file:line） |
|---|---|
| `SETBIT` | `base/beego/bloom/redis/redis.go:69-113`、`base/beego/bloom/bloom.go:266,275,290` |
| `GETBIT` | `base/beego/bloom/redis/redis.go:89-97`、`base/beego/bloom/bloom.go:223,240` |

#### Scripting 命令

| 命令 | 调用点（file:line） |
|---|---|
| `EVAL` | `base/beego/bloom/redis/redis.go:123`、`base/beego/bloom/bloom.go:22-41` |
| `SCRIPT LOAD` | `base/accesstoken/client.go:106` |

#### Sorted Set / Pub-Sub / Stream / Geo / HLL

**无命中** — xaas-bbc 未使用 ZSet、Pub/Sub、Stream、Geo、HyperLogLog 任何命令。

### 2.3 可疑 raw 命令拼接位置

`base/beego/myredis/multi.go` 中约 60+ 处 `conn.Do("CMD", args...)` 风格调用属于 Redigo 封装层内部实现（合理）。App 层有直接获取裸 `redis.Conn` 调用 MULTI/EXEC 的位置：

- `apps/branch/models/redis.go:187-204`（RemoveXlinkCache）
- `apps/branch/models/common.go:1468-1486`（GetConnTestStatus）
- `apps/cloudbbc-miniapi/models/redis.go:160-177`（RemoveXlinkCache）
- `apps/cloudbbc-insideapi/models/redis.go:55-72`（RemoveXlinkCache）
- `apps/cloudbbc-insideapi/models/device.go:1294`（直接 HSET）
- `base/beego/apilimit/v2limit.go:54-59`（V2ApiLimiter TryLock）
- `base/beego/webbase/sessionmgr/manager.go:174-193`（markOffline）

---

## 三、xaas-bbc-proxy 模块 Redis 命令清单

### 3.1 客户端库与初始化点

| 文件:行 | 变量 | 客户端类型 | 配置来源 |
|---|---|---|---|
| `gobase/goredis/init.go:66-75` | `gRedisClient` | `*redis.Client`（go-redis/v9 v9.7.0） | `/xaas/base_etc/env.conf` `[redis]` section |
| `gobase/webbase/base_controller.go:21` | beego session 内部 pool | `*redis.Pool`（redigo，间接） | beego session 配置 |

### 3.2 命令分类汇总

#### String 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `EXISTS` | `gobase/goredis/init.go:117` |
| `SET` | `gobase/goredis/init.go:121`、`apps/dbservice/dealers/proxy_dealer.go:119` |
| `SETEX` | `gobase/goredis/init.go:125` |
| `SETNX` | `gobase/goredis/init.go:151,155` |
| `GET` | `gobase/goredis/init.go:129,138,143,195,199` |
| `MGET` | `gobase/goredis/init.go:133` |
| `INCR` | `gobase/goredis/init.go:203,207`、`apps/dbservice/apilimit/generic.go:33,134`、`apps/dbservice/apilimit/apilimit.go:69` |
| `INCRBY` | `gobase/goredis/init.go:212` |
| `DECR` | `gobase/goredis/init.go:216` |
| `DECRBY` | `gobase/goredis/init.go:220` |
| `TTL` | `gobase/goredis/init.go:187`、`apps/dbservice/apilimit/generic.go:60,82,100,164`、`apps/dbservice/apilimit/apilimit.go:98` |
| `DEL` | `gobase/goredis/init.go:191`、`apps/dbservice/apilimit/generic.go:74,180` |
| `EXPIRE` | `gobase/goredis/init.go:224`、`apps/dbservice/apilimit/generic.go:44,145`、`apps/dbservice/apilimit/apilimit.go:76`、`apps/dbservice/dealers/linkedr_dealer.go:136`、`apps/cloudproxy/models/redis.go:23` |

#### Hash 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `HSET` | `gobase/goredis/init.go:159,266`、`apps/dbservice/dealers/status_dealer.go:73,108,109,115`、`apps/dbservice/dealers/linkedr_dealer.go:126`、`apps/dbservice/models/pdt_reload.go:106`、`apps/cloudproxy/models/redis.go:17`、`apps/dbservice/dealers/basic_dealer.go:260-264,305-306,337-338,360`、`apps/dbservice/dealers/auth_dealer.go:228,235,252`、`apps/dbservice/dealers/status_dealer.go:258`（pipeline） |
| `HGET` | `gobase/goredis/init.go:163,175` |
| `HMGET` | `gobase/goredis/init.go:167` |
| `HKEYS` | `gobase/goredis/init.go:171` |
| `HGETALL` | `gobase/goredis/init.go:179`、`apps/dbservice/dealers/linkedr_dealer.go:61`、`apps/dbservice/models/pdt_reload.go:123`、`gobase/webbase/api_controller.go:101` |
| `HDEL` | `gobase/goredis/init.go:183` |
| `HINCRBY` | `apps/dbservice/dealers/basic_dealer.go:391`（pipeline） |

#### Set 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `SADD` | `gobase/goredis/init.go:244,266`（pipeline）、`apps/cloudproxy/services/zpserver.go:65,79` |
| `SREM` | `gobase/goredis/init.go:228` |
| `SMEMBERS` | `gobase/goredis/init.go:232` |
| `SCARD` | `gobase/goredis/init.go:236` |
| `SISMEMBER` | `gobase/goredis/init.go:248` |
| `SMISMEMBER` | `gobase/goredis/init.go:252` |

#### List 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `LRANGE` | `gobase/goredis/init.go:256` |
| `RPUSH` | `gobase/goredis/init.go:260` |

#### Server 类命令

| 命令 | 调用点（file:line） |
|---|---|
| `PING` | `gobase/goredis/init.go:78`（启动健康检查） |
| `MEMORY USAGE` | `gobase/goredis/init.go:240` |

#### Pipeline / Transaction 命令

| 命令 | 调用点（file:line） |
|---|---|
| `Pipeline()` | `gobase/goredis/init.go:280` |
| `Pipelined()` | `gobase/goredis/init.go:276`、`apps/dbservice/dealers/status_dealer.go:255`、`apps/dbservice/dealers/basic_dealer.go:197,249,282,324,355,387`、`apps/dbservice/dealers/auth_dealer.go:226` |
| `TxPipeline()` | `gobase/goredis/init.go:288` |
| `TxPipelined()` | `gobase/goredis/init.go:284` |

#### Sorted Set / Pub-Sub / Stream / Geo / HLL / BitMap / Scripting

**无命中** — xaas-bbc-proxy 未使用这些类型命令。

### 3.3 可疑 raw 命令拼接位置

仅有 2 处已注释掉的遗留代码：

- `apps/dbservice/dealers/status_dealer.go:288`（`conn.Send("HSET", ...)`，已注释）
- `apps/dbservice/dealers/status_dealer.go:298`（`conn.Send("EXPIRE", ...)`，已注释）

活跃代码中**无任何 raw 命令字符串拼接**，所有 Redis 调用都走 `goredis.*` 封装函数或 `Pipelined()` 回调。

---

## 四、Redis 连接库自动重连功能分析

### 4.1 xaas-bbc-proxy（go-redis v9.7.0）

#### 初始化配置（`gobase/goredis/init.go:66-75`，Options 结构体；76 行为 `redis.NewClient(options)`，78 行起为 Ping 健康检查）

```go
options := &redis.Options{
    Addr:            redisConf.addr,
    Password:        redisConf.password,
    DB:              redisConf.db,
    DialTimeout:     time.Millisecond * time.Duration(redisConf.dialTimeout),   // 默认 10000ms
    ReadTimeout:     time.Millisecond * time.Duration(redisConf.readTimeout),  // 默认 10000ms
    WriteTimeout:    time.Millisecond * time.Duration(redisConf.writeTimeout), // 默认 10000ms
    ConnMaxIdleTime: time.Millisecond * time.Duration(redisConf.idleTimeout),  // 默认 10000ms
    PoolSize:        redisConf.poolSize,                                       // 默认 100
}
```

#### 自动重连机制（**支持**）

go-redis v9 的 `*redis.Client` 内置完整自动重连机制，无需应用层任何配置。具体行为：

1. **后台连接管理器**：`redis.NewClient` 启动后台 goroutine（`connectionPool` + `connPool`）维护连接池。每个连接由 `connPool` 按需创建。
2. **网络错误自动重连**：当已建立的连接遇到网络错误（EOF、reset、timeout、refused 等），`connPool` 会丢弃坏连接（`PoolStats` 的 `StaleConns` 计数+1），下次请求时建立新连接。`baseConn` 内部 `withConn` 包装会重试。
3. **命令级重试**：`MaxRetries` 字段默认 3，对网络瞬断（如 `io.EOF`、`syscall.ECONNRESET`）会自动重试。重试退避默认 `MinRetryBackoff=8ms`、`MaxRetryBackoff=512ms`，指数退避。
4. **拨号失败重试**：`Dialer` 失败时不会缓存坏连接，会按 PoolSize 上限重新拨号。
5. **断线重连不需要 Ping**：连接池里没有保活机制的话，下次命令执行时会触发 `Dial` 重建。

#### 当前项目配置的影响

| 字段 | 项目设置 | go-redis v9 默认 | 影响 |
|---|---|---|---|
| `MaxRetries` | **未设置** | `3` | 实际生效 = 3，命令级网络瞬断会自动重试 3 次 |
| `MinRetryBackoff` | **未设置** | `8ms` | 实际生效 = 8ms 起步 |
| `MaxRetryBackoff` | **未设置** | `512ms` | 实际生效 = 512ms 上限 |
| `MinIdleConns` | **未设置** | `0` | 不预保活，空闲连接超过 ConnMaxIdleTime 后会被关闭；下次请求重新拨号 |
| `PoolTimeout` | **未设置** | `ReadTimeout + 1s` = 11s | 等待空闲连接超时上限 11s |
| `ConnMaxIdleTime` | `10000ms` | `30m` | **偏短**，10s 空闲就关连接，频繁关闭/重建 |
| `ConnMaxLifetime` | **未设置** | `0`（无限制） | v9 字段名（v8 对应 `MaxConnAge`），不主动按年龄换连接 |
| `PoolSize` | `100` | `10 * runtime.GOMAXPROCS` | 显式 100 |

**结论**：xaas-bbc-proxy **具备自动重连功能**，且 go-redis v9 默认 MaxRetries=3 已生效，命令级网络瞬断会自动重试。但 `ConnMaxIdleTime=10s` 偏短，会触发频繁的连接关闭与重建，可能放大 Redis 端的连接负担；建议显式设置 `MinIdleConns=10~20` 保持最低保活连接数。

### 4.2 xaas-bbc（go-redis v9.0.0-rc.2 / v8.11.5 + redigo v1.6.4）

xaas-bbc 同时维护 3 套客户端，每套的重连行为不同。

#### A. base/goredis（go-redis/v9 v9.0.0-rc.2）— `base/goredis/init.go:69-78`

```go
options := &redis.Options{
    Addr:            redisConf.addr,
    Password:        redisConf.password,
    DB:              redisConf.db,
    DialTimeout:     time.Millisecond * time.Duration(redisConf.dialTimeout),
    ReadTimeout:     time.Millisecond * time.Duration(redisConf.readTimeout),
    WriteTimeout:    time.Millisecond * time.Duration(redisConf.writeTimeout),
    ConnMaxIdleTime: time.Millisecond * time.Duration(redisConf.idleTimeout),
    PoolSize:        redisConf.poolSize,
}
```

**自动重连支持**：与 v9.7.0 同源（v9.0-rc.2 是 go-redis v9 早期版本），机制相同。`MaxRetries` 未设置 → 默认值 3 生效，命令级网络瞬断自动重试。`ConnMaxIdleTime=10s` 同样偏短。

**风险**：v9.0.0-rc.2 是 **release candidate 版本**，不是稳定版，可能存在早期 bug，建议升级到 v9.7.0 与 xaas-bbc-proxy 对齐。

#### B. base/beego/goredis（go-redis/v8 v8.11.5）— `base/beego/goredis/init.go:72-81`

```go
options := &redis.Options{
    Addr:         conf.addr,
    Password:     conf.password,
    DB:           conf.db,
    DialTimeout:  time.Millisecond * time.Duration(conf.dialTimeout),
    ReadTimeout:  time.Millisecond * time.Duration(conf.readTimeout),
    WriteTimeout: time.Millisecond * time.Duration(conf.writeTimeout),
    IdleTimeout:  time.Millisecond * time.Duration(conf.idleTimeout),  // v8 用 IdleTimeout
    PoolSize:     conf.poolSize,
}
```

**自动重连支持**：go-redis v8 同样内置自动重连。v8 的 `MaxRetries` 默认值是 `3`（与 v9 一致），`MinRetryBackoff=8ms`、`MaxRetryBackoff=512ms` 默认值一致。机制：连接池坏连接驱逐 + 命令级指数退避重试。

**v8 与 v9 差异**：v8 用 `IdleTimeout`，v9 改名为 `ConnMaxIdleTime`（同时 v9 引入 `ConnMaxIdleTime` 替代 `IdleTimeout`，行为一致）。`PoolTimeout` 在 v8 默认是 `ReadTimeout + 1s`，与 v9 一致。

#### C. base/beego/myredis（redigo v1.6.4）— `base/beego/myredis/once.go:336-361`

```go
pool = &redis.Pool{
    MaxIdle:     kRedisPoolMaxIdle,      // 100
    MaxActive:   kRedisPoolMaxActive,    // 500
    IdleTimeout: kRedisPoolIdleTimeout * time.Second,  // 600s
    Wait:        false,
    Dial: func() (redis.Conn, error) {
        c, err := redis.Dial("tcp", server,
            redis.DialReadTimeout(...),
            redis.DialWriteTimeout(...),
            redis.DialConnectTimeout(...),
            redis.DialPassword(password),
            redis.DialDatabase(kRedisDefaultDB))
        ...
    },
    TestOnBorrow: func(c redis.Conn, t time.Time) error {
        _, err := c.Do("PING")  // 每次借用均无条件 PING 探测
        return err
    },
}
```

**自动重连支持**：Redigo 的连接池模型与 go-redis 不同，**没有内置命令级重试**，但有以下保活/重连机制：

1. **`TestOnBorrow` + PING**：实际代码（`base/beego/myredis/once.go:356-359`）在每次从池中取出连接时**无条件**发送 `PING` 探测连接健康度——`func(c redis.Conn, t time.Time) error { _, err := c.Do("PING"); return err }`，没有"空闲超过 1s 才 PING"的判断（这与 redigo 官方文档示例的常见写法不同，项目实际是每次借用都 PING）。PING 失败则该连接被丢弃，下次 `Get()` 会重新 `Dial`。
2. **`IdleTimeout=600s`**：空闲超过 10 分钟的连接会被关闭。
3. **`MaxIdle=100, MaxActive=500`**：连接数上限。
4. **`Wait=false`**：连接池耗尽时直接返回错误（`redis.ErrPoolExhausted`），不会等待。

**Redigo 没有的能力**：
- 没有 `MaxRetries` / 退避重试机制 — 命令级网络瞬断会直接返回错误。
- 没有连接级自动 reconnect — 坏连接靠 `TestOnBorrow` 每次借用时无条件 PING 检测；如果连接在使用过程中断开（PING 通过但写入时才断），当前命令会失败，需要应用层重试。
- 没有 `ConnMaxIdleTime` 等同的细粒度控制。
- 注意：每次借用都 PING 的代价是每条 Redis 调用都多一次 round-trip，对延迟敏感场景需权衡。

#### D. 综合结论

| 客户端层 | 库 | 自动重连 | 命令级重试 | 备注 |
|---|---|---|---|---|
| xaas-bbc `base/goredis` | go-redis/v9 v9.0.0-rc.2 | **支持** | **支持**（MaxRetries 默认 3 生效） | rc 版本，建议升级到 v9.7.0 |
| xaas-bbc `base/beego/goredis` | go-redis/v8 v8.11.5 | **支持** | **支持**（MaxRetries 默认 3 生效） | — |
| xaas-bbc `base/beego/myredis` | redigo v1.6.4 | **部分支持**（靠 TestOnBorrow 每次借用无条件 PING 检测坏连接） | **不支持**（无命令级重试） | `Wait=false` 池耗尽直接报错；每次借用都 PING 增加一倍 RTT |
| xaas-bbc-proxy `gobase/goredis` | go-redis/v9 v9.7.0 | **支持** | **支持**（MaxRetries 默认 3 生效） | `ConnMaxIdleTime=10s` 偏短 |

---

## 五、连接池/超时/重试配置项详细对照

### xaas-bbc-proxy（唯一初始化点 `gobase/goredis/init.go:66-75`，Options 字段；76 行 `redis.NewClient(options)`）

| Options 字段 | 实际值 | 来源 | go-redis v9 默认 | 备注 |
|---|---|---|---|---|
| `Addr` | `redisConf.addr` | `env.conf [redis] server` | — | 必填 |
| `Password` | `redisConf.password` | `env.conf [redis] password`（passinfo.Decrypt 解密） | — | — |
| `DB` | `redisConf.db` | `env.conf [redis] db` | `0` | — |
| `DialTimeout` | 10000ms | `env.conf [redis] dial_timeout` | `5s` | 偏长 |
| `ReadTimeout` | 10000ms | `env.conf [redis] read_timeout` | `3s` | 偏长 |
| `WriteTimeout` | 10000ms | `env.conf [redis] write_timeout` | `3s` | 偏长 |
| `ConnMaxIdleTime` | 10000ms | `env.conf [redis] idle_timeout` | `30m` | **偏短，频繁关闭重建** |
| `PoolSize` | 100 | `env.conf [redis] poll_size`（注意拼写为 `poll`） | `10 * GOMAXPROCS` | — |
| `MinIdleConns` | 未设置 | — | `0` | 不预保活 |
| `ConnMaxLifetime` | 未设置 | — | `0`（无限制） | v9 字段名（v8 中对应 `MaxConnAge`） |
| `PoolTimeout` | 未设置 | — | `ReadTimeout + 1s` = 11s | — |
| `MaxRetries` | 未设置 | — | **`3`** | **命令级自动重试生效** |
| `MinRetryBackoff` | 未设置 | — | `8ms` | — |
| `MaxRetryBackoff` | 未设置 | — | `512ms` | — |

### xaas-bbc `base/goredis`（`base/goredis/init.go:69-78`）

字段配置与 xaas-bbc-proxy 完全一致（同样 7 个字段，`poll_size` 拼写也相同）。底层库为 v9.0.0-rc.2，`MaxRetries` 默认 3 生效。

### xaas-bbc `base/beego/goredis`（`base/beego/goredis/init.go:72-81`）

字段与上同，但用 v8 字段名 `IdleTimeout`（v8 没有改名前的 `ConnMaxIdleTime`）。底层 go-redis/v8 v8.11.5，`MaxRetries` 默认 3 生效。

### xaas-bbc `base/beego/myredis`（`base/beego/myredis/once.go:336-361`，redigo）

| Pool 字段 | 值 | 备注 |
|---|---|---|
| `MaxIdle` | 100 | 池中最大空闲连接 |
| `MaxActive` | 500 | 最大活跃连接 |
| `IdleTimeout` | 600s | 空闲超时 |
| `Wait` | `false` | **池耗尽直接报错** |
| `DialConnectTimeout` | 10s | — |
| `DialReadTimeout` | 10s | — |
| `DialWriteTimeout` | 10s | — |
| `TestOnBorrow` | PING（空闲>1s 才 ping） | 坏连接检测 |

---

## 六、关键结论

1. **两个模块的 Redis 客户端底层都用 go-redis v9 系列**，均具备完整的自动重连 + 命令级重试（`MaxRetries` 默认 3 生效）。xaas-bbc-proxy 用稳定版 v9.7.0，xaas-bbc 用 rc 版 v9.0.0-rc.2，建议升级。
2. **xaas-bbc 多客户端并存**：除了 go-redis v9 外，还保留 go-redis v8（beego 层）和 redigo（最旧 beego 层）两套遗留客户端。redigo 无命令级重试，仅靠 `TestOnBorrow` PING 检测坏连接，能力较弱；其 `Wait=false` 配置在连接池耗尽时会直接返回错误。
3. **关键配置项 `MaxRetries` 在两个模块都没有显式设置**，但 go-redis v8/v9 的默认值是 3，所以命令级网络瞬断重试功能是**默认生效**的。如果业务对一致性要求高，建议显式设置 `MaxRetries=2~3`（避免读命令在主从切换时被重试到不同节点造成脏读）。
4. **`ConnMaxIdleTime=10s` 在两个模块都偏短**：go-redis v9 默认 30 分钟，项目配置为 10 秒，会让空闲连接快速被关闭、下次请求重新拨号，可能放大 Redis 端连接负担。建议调整为 5~10 分钟。
5. **xaas-bbc-proxy 没有使用 Pub/Sub、Stream、ZSet、BitMap、Scripting、Geo、HLL 命令**，Redis 用途集中在常规 KV、Hash 设备元数据、Set 标签、限流计数（INCR/EXPIRE）。
6. **xaas-bbc 命令覆盖更广**，包含 Pipeline、MULTI/EXEC 事务、BitMap（bloom filter）、Scripting（EVAL/SCRIPT LOAD）、SCAN/SSCAN、PEXPIRE/EXPIREAT、GETSET/GETDEL 等高级命令，但同样未使用 Pub/Sub、Stream、ZSet、Geo、HLL。
7. **xaas-bbc 的 redigo 调用层存在风险点**：多个 app 直接通过 `myredis.Conn()` 获取裸 `redis.Conn` 后用 `conn.Send("MULTI") / conn.Do("EXEC")` 写事务，没有统一的错误处理和超时控制；建议统一收口到封装层。

---

## 七、xaas-bbc-proxy C/C++ 子模块 Redis 使用分析

> 分析日期：2026-07-13
> 分析对象：`xaas-bbc-proxy/apps/*` 下所有 C/C++ 子模块（bbcauth、bbcssopxy、bbc_cfg_sync、bbc_cgi_agent、bbc_dev_status、bbc_logsvr、upgrade_server、zproxy）
> 分析方式：grep `redis|hiredis|RedisHelper|redisCommand|redisConnect` 关键字 + 阅读 `bbcssopxy/server/redis_helper.{h,cpp}` + 阅读主程序 `bbcssopxy_server.cpp` 调用上下文

### 7.1 子模块 Redis 使用情况概览

| 子模块（C/C++） | 源码引用 redis | 链接 libhiredis | 实际调用 Redis 命令 | 备注 |
|---|---|---|---|---|
| **bbcssopxy** | ✅ 有 | ✅ `Makefile:67 -lhiredis` | ⚠️ 仅 `redis_helper.{h,cpp}` 工具类实现存在；主程序 `bbcssopxy_server.cpp` 中所有 `RedisHelper` 调用 / `init_global_redis_handler` 初始化 / `on_redis_check_timer` 内 `ping()` 全部**被注释掉**。配置仍保留 `auth_mode=1` (REDIS) 与 `[redis]` 加载逻辑 | 工具类已就绪、业务路径未启用 |
| bbcauth | ❌ 无 | ⚠️ 仅 `Dockerfile` 拷贝 `libhiredis.so.0.13`（运行时依赖遗留，源码未调用） | 无 | 镜像继承了 hiredis 运行时库但无调用点 |
| bbc_cfg_sync | ❌ 无 | ❌ 无 | 无 | — |
| bbc_cgi_agent | ❌ 无 | ❌ 无 | 无 | — |
| bbc_dev_status | ❌ 无 | ❌ 无 | 无 | — |
| bbc_logsvr | ❌ 无 | ❌ 无 | 无 | — |
| upgrade_server | ❌ 无 | ❌ 无 | 无 | — |
| zproxy | ❌ 无 | ❌ 无 | 无 | — |

**结论**：xaas-bbc-proxy 的 C/C++ 子模块中**只有 `bbcssopxy` 涉及 Redis**，且当前业务路径未启用——所有 `RedisHelper` 调用都被注释。其他子模块源码中**完全没有** redis/hiredis 调用。

### 7.2 bbcssopxy 模块 Redis 命令清单（来自 `redis_helper.cpp`）

底层库：`hiredis`（C 客户端），头文件 `#include <hiredis/hiredis.h>`（`redis_helper.h:4`）。
链接：`-lhiredis`（`bbcssopxy/Makefile:67`）。
封装类：`xcentral::RedisHelper`（`redis_helper.h:36`），单实例全局句柄 `gRedisHandler`（`redis_helper.cpp:10`）。
连接初始化：构造函数 `RedisHelper(pass, ip, port)` → `try_connect_auth()` → `_connect()` + `_auth()`（`redis_helper.cpp:16,294,313`）。

| Redis 命令 | hiredis 调用形式 | 代码位置 | 封装方法签名 | 调用点 |
|---|---|---|---|---|
| `AUTH` | `redisCommand(ctx, "AUTH %s", pass)` | `redis_helper.cpp:314` | `bool _auth()` 私有 | 构造时自动鉴权；`bbcssopxy_server.cpp:236` 注释调用 `try_connect_auth()` |
| `SET` | `redisCommand(ctx, "SET %s %d", key, seconds/1000)` | `redis_helper.cpp:24` | `bool set(const char *key, int seconds)` | 主程序无活跃调用 |
| `GET` | `redisCommand(ctx, "GET %s", key)` | `redis_helper.cpp:48` | `std::string get(const char *key)` | 主程序无活跃调用 |
| `PING` | `redisCommand(ctx, "ping")` | `redis_helper.cpp:69` | `std::string ping()` | `bbcssopxy_server.cpp:235`（已注释） |
| `HGET` | `redisCommandArgv(ctx, 3, argv, argvlen)`，argv[0]="HGET" | `redis_helper.cpp:84-99` | `std::string hget(const char *key, const char *field)` | 主程序无活跃调用 |
| `HGETALL` | `redisCommand(ctx, "HGETALL %s", key)` | `redis_helper.cpp:120` | `bool hgetall(const char *key, std::map<std::string,std::string>&)` | 主程序无活跃调用 |
| `HSET` (字符串 value) | `redisCommand(ctx, "HSET %s %s %s", key, field, value)` | `redis_helper.cpp:153` | `bool hset(const char *key, const char *field, const char *value)` | 主程序无活跃调用 |
| `HSET` (二进制 value，带长度) | `redisCommandArgv(ctx, 4, argv, argvlen)`，argv[0]="HSET" | `redis_helper.cpp:188` | `bool hset(const char *key, const char *field, const char *hvalue, size_t hvaluelen)` | 主程序无活跃调用 |
| `DEL` | `redisCommand(ctx, "DEL %s", key)` | `redis_helper.cpp:208` | `bool del(const char *key)` | 主程序无活跃调用 |
| `EXISTS` | `redisCommand(ctx, "EXISTS %s", key)` | `redis_helper.cpp:229` | `bool existsKey(const char *key)` | 主程序无活跃调用 |
| `EXPIRE` | `redisCommand(ctx, "EXPIRE %s %d", key, seconds/1000)` | `redis_helper.cpp:250` | `bool expire(const char *key, int seconds)` | 主程序无活跃调用 |
| Pipeline | `redisAppendCommand(ctx, cmd)` + `redisGetReply(ctx, &reply)` 循环 | `redis_helper.cpp:275,280` | `bool pipeline(const std::vector<std::string>& v)` | 主程序无活跃调用 |

**命令分类汇总**：

- **String 类**：`SET`、`GET`、`EXISTS`、`DEL`、`EXPIRE`
- **Hash 类**：`HSET`（两种重载：字符串与二进制）、`HGET`、`HGETALL`
- **Server 类**：`AUTH`、`PING`
- **Pipeline**：`redisAppendCommand` + `redisGetReply`（hiredis pipeline API，由 `pipeline()` 方法封装）
- **未使用**：`SETEX`、`SETNX`、`INCR/DECR`、`TTL`、`MGET/MSET`、`HDEL`、`HMSET`、`HKEYS`、`HINCRBY`、`List`、`Set`、`ZSet`、`Pub/Sub`、`Stream`、`BitMap`、`Scripting`、`Geo`、`HLL`、`MULTI/EXEC`、`SCAN`

### 7.3 调用链路与配置入口

**配置加载**（`bbcssopxy_server_config.cpp:506` `bbcssopxy_server_redis_config_loader`）：

- 仅在 `auth_mode == AUTH_MOD_REDIS (1)` 时加载（`config.cpp:510`）。
- 配置节：`SECTION_REDIS`，键 `server`（`KEY_REDIS_SERVER`，"ip:port" 格式）、`password`（`KEY_REDIS_PASS`，`PassInfoDecrypt` 解密）。
- 默认端口 `DEF_REDIS_PORT = 6379`（`config.cpp:76`）。
- 实际 INI 文件 `bbcssopxy/conf/bbcssopxy_server.ini` 中 `[ssopxy] auth_mode = 1`（**已启用 redis 鉴权模式**），但 INI 中没有显式 `[redis]` 节，redis 配置实际取自 `SCLOUD_SERVICE_CFG_FILE`（运行时部署侧的服务配置文件，源码 `config.cpp:521` 另起 `CRwIni service_ini`）。
- 鉴权模式说明（`config.h:58`）：`auth_mode` 为 1 代表使用 redis 鉴权，为 0 代表使用 bbc 原有方式鉴权。

**主程序调用链**（`bbcssopxy_server.cpp:242 bbcssopxy_server_init`）：

```
bbcssopxy_server_init()  // bbcssopxy_server.cpp:242
  ├─ bbcssopxy_server_config_load()   // 加载 ini，含 redis_server 配置（即使主调用被注释，配置仍会被加载）
  ├─ [注释] init_global_redis_handler(pass, ip, port)   // bbcssopxy_server.cpp:281-284
  ├─ bbcssopxy_server_sessmgr_create()
  ├─ db_client_init()
  └─ cl_timerplus_start_ex(..., on_redis_check_timer, nullptr)   // 启动 redis 健康检查定时器
      └─ on_redis_check_timer()  // bbcssopxy_server.cpp:230
          └─ [注释] gRedisHandler->ping() / try_connect_auth()   // 健康检查被注释
```

定时器常量 `kRedisCheckInterval` 仍生效（`bbcssopxy_server.cpp:308-310`），但回调内代码全被注释——定时器**空跑**。

**业务侧引用**（`bbcssopxy_server_connection.cpp`）：

- `bbcssopxy_server_connection.cpp:672`：`if (g_server.srv_cfg->auth_mode != AUTH_MOD_REDIS) { return; }` —— 在 `recover_sso_token_on_cookie` 中根据鉴权模式分支处理 cookie 还原逻辑（云图平台走 redis 模式，需要字符对调还原 sso token）。**该分支只读取 `auth_mode` 配置，未调用任何 RedisHelper 方法**。
- `bbcssopxy_server_connection.cpp:1561`：`auth_mode == AUTH_MOD_HASH` —— 另一条分支，未涉及 redis。

### 7.4 自动重连分析（hiredis 层）

`RedisHelper` 类**没有内置自动重连机制**：

1. 构造函数（`redis_helper.h:38-42`）仅做一次 `try_connect_auth()`，失败时 `_is_auth=false`，后续所有命令直接返回 `false/""`（每个方法首部 `if (!this->_is_auth) return false;`）。
2. `try_connect_auth()`（`redis_helper.cpp:16`）只是 `_connect() && _auth()` 的简单组合，无重试、无周期触发。
3. 主程序预留了 `redis_timer`（`bbcssopxy_server.cpp:308`）+ `on_redis_check_timer` 回调框架，**但内部 `ping()` 检活 + `try_connect_auth()` 重连逻辑全部被注释**——重连框架未启用。
4. `redisConnect`（`redis_helper.cpp:295`）本身在 hiredis 中是阻塞同步连接，没有自动重连；连接断开后 `_context` 不会被回收，下一次 `redisCommand` 会返回 `NULL`，方法返回 false 但不会重新拨号。

**对比 hiredis 默认行为**：
- hiredis `redisConnect` 不内置重连；`redisReconnect` 需要应用层主动调用。
- 无命令级重试，无指数退避。
- 无连接池，单 `redisContext*` 全局共享，**线程不安全**（多个请求线程共用同一 context，无锁保护）。

### 7.5 风险与建议

1. **`auth_mode=1` 已启用但 Redis 调用被注释**：当前 `bbcssopxy_server.ini` 设置 `auth_mode = 1`，配置加载器会读取 `[redis]` 配置，但主程序对 `gRedisHandler` 的初始化与 `ping` 健康检查全部被注释。这意味着**配置上声称走 redis 鉴权，但运行时实际并未触发任何 Redis 命令**——存在"配置-代码不一致"的隐患。建议明确：要么彻底移除 `redis_helper` 与 `auth_mode=1` 相关代码，要么补齐 Redis 调用并启用定时器。
2. **`redis_timer` 空跑**：`bbcssopxy_server.cpp:308` 启动了 `kRedisCheckInterval` 周期定时器，但回调 `on_redis_check_timer` 函数体全为注释，定时器空转浪费资源。建议：要么注释掉 `cl_timerplus_start_ex` 启动逻辑，要么补齐回调内容。
3. **`redis_helper.h:138 inline void test()` 含硬编码密码**：`RedisHelper handler("4J2tvcAxygeGOIC3", "10.220.0.25");` —— 测试代码中硬编码了 redis 密码与内网 IP。即便该 `test()` 函数未被任何代码调用，仍是凭据泄露风险点，建议删除。
4. **`RedisHelper` 全局单实例 + 无锁**：`gRedisHandler` 是单例 `redisContext*`，hiredis 单连接非线程安全。若启用 redis 调用且 bbcssopxy 多线程处理连接，会出现竞态。建议改连接池或加锁。
5. **bbcauth Dockerfile 仍打包 `libhiredis.so.0.13`**：bbcauth 源码已无 redis 调用，但 `bbcauth/Dockerfile:65` 仍 `COPY --from=App-Build-Image /usr/local/lib/libhiredis.so.0.13 /sf/scloud/lib/`。属遗留镜像层，可移除以减小镜像体积。
6. **无重连/重试机制**：hiredis 层与封装层均无自动重连，与 Go 侧 go-redis v9 的 `MaxRetries=3` 默认重试 + 自动重连能力差距明显。若 redis 路径重新启用，建议补：
   - 在 `on_redis_check_timer` 中实现 `ping() → 失败 → redisReconnect → 重新 AUTH` 的检活重连逻辑。
   - 关键命令外层包一次重试（瞬断场景）。
   - 鉴权失败时主动释放 `_context` 并置空，避免后续命令挂在坏 context 上。

### 7.6 与 Go 侧 Redis 使用对比（xaas-bbc-proxy 内部）

| 维度 | Go 侧（`gobase/goredis`、`apps/dbservice`、`apps/cloudproxy`） | C/C++ 侧（`bbcssopxy`） |
|---|---|---|
| 客户端库 | go-redis v9.7.0 + redigo（beego 适配器） | hiredis（C 库） |
| 命令覆盖 | String / Hash / Set / List / Pipeline / TxPipeline 等多种 | String / Hash / Server / Pipeline 基本命令 |
| 自动重连 | ✅ go-redis 内置，`MaxRetries=3` 默认生效 | ❌ 无，需应用层显式实现 |
| 连接池 | ✅ `PoolSize=100` | ❌ 单 `redisContext*` 全局共享 |
| 线程安全 | ✅ go-redis 连接池线程安全 | ❌ 单连接非线程安全 |
| 当前是否活跃调用 | ✅ 大量活跃调用 | ❌ 主程序调用全被注释，仅工具类实现存在 |
| 配置来源 | `/xaas/base_etc/env.conf [redis]` | `bbcssopxy_server.ini [ssopxy] auth_mode` + `SCLOUD_SERVICE_CFG_FILE [redis]` |

**关键差异**：Go 侧是 xaas-bbc-proxy 的 Redis 主要使用方，C/C++ 侧（bbcssopxy）的 Redis 路径**当前未启用**——尽管封装类 `RedisHelper` 已实现完整命令集，但主程序初始化与定时器回调全部被注释，处于"工具就绪、业务未启用"状态。
