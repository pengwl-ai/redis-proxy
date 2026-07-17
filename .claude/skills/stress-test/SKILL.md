---
name: stress-test
description: redis proxy 性能压力测试
metadata:
  version: "1.0.0"
  triggers:
    - 编译
    - 压力测试
    - 性能测试
  agents:
    - general-purpose
  tools: ["Read", "Write", "Edit", "Grep", "Glob", "Bash"]
  model: sonnet
---

# redis proxy 性能压力测试技能

## 功能说明

执行完整的 redis proxy 测试环境部署验收流程，包括：编译、部署、验证。

## 使用方法

调用 `/stress-test` 启动交互式部署流程。

## 完整流程

### 1. 构建镜像

```bash
export GOOS=linux
go build
```

### 2. 上传文件到服务器

```bash
# 检查是否有服务在运行
ssh -p 22 root@192.168.31.200 "ps aux |grep redis-proxy"
# 有服务运行，则杀死旧的进程
ssh -p 22 root@192.168.31.200 "ps aux |grep redis-proxy |grep -v grep | awk '{print $2}' |xargs kill -9"

服务器地址为 192.168.31.200，当前已经配置免密登录
使用以下命令 scp.exe redis-proxy root@192.168.31.200:/root/redis-proxy/
```

### 3. 启动服务

```bash
# 启动服务（必须在 redis-proxy 目录下执行，因为需要 config.yaml）
ssh -p 22 root@192.168.31.200 "cd /root/redis-proxy && setsid ./redis-proxy > redis-proxy.log 2>&1 < /dev/null &"
# 验证服务运行
sleep 1 && ssh -p 22 root@192.168.31.200 "redis-cli -h 127.0.0.1 -p 6666 PING"
```

### 4. 执行压力测试

注意：
- 禁止同时运行，分开执行避免误差
- 压测前确认 pprof 端点正常：`curl http://192.168.31.200:8080/debug/pprof/`

#### 4a. 走代理测试（同步采集 pprof）

```bash
# 终端1：启动 30s CPU profile 采集（在压测开始前执行）
ssh -p 22 root@192.168.31.200 "curl -o /root/redis-proxy/cpu.prof 'http://127.0.0.1:8080/debug/pprof/profile?seconds=30'" &

# 终端2：立即启动走代理压测
# 单线程验证
ssh -p 22 root@192.168.31.200 "memtier_benchmark -h 127.0.0.1 -p 6666 \
  --ratio=8:2 --threads=1 --clients=1 \
  -n 10000 --data-size=1024 --key-pattern=S:R"
# 并发验证
ssh -p 22 root@192.168.31.200 "memtier_benchmark -h 127.0.0.1 -p 6666 \
  --ratio=8:2 --threads=10 --clients=50 \
  -n 10000 --data-size=1024 --key-pattern=S:R"

# 等待 cpu.prof 采集完成（profile 命令会阻塞 30s）
wait
```

#### 4b. 直连 redis 测试（基线对比）

```bash
# 单线程验证
ssh -p 22 root@192.168.31.200 "memtier_benchmark -h 127.0.0.1 -p 6379 \
  --ratio=8:2 --threads=1 --clients=1 \
  -n 10000 --data-size=1024 --key-pattern=S:R"
  
# 并发验证
ssh -p 22 root@192.168.31.200 "memtier_benchmark -h 127.0.0.1 -p 6379 \
  --ratio=8:2 --threads=10 --clients=50 \
  -n 10000 --data-size=1024 --key-pattern=S:R"
```

#### 4c. redis-benchmark 走代理测试

```bash
# 单线程验证
ssh -p 22 root@192.168.31.200 "redis-benchmark -h 127.0.0.1 -p 6666 \
  -t set,get -d 1024 -n 1000000 -c 1 --threads 1 --csv"

# 并发验证
ssh -p 22 root@192.168.31.200 "redis-benchmark -h 127.0.0.1 -p 6666 \
  -t set,get -d 1024 -n 5000000 -c 50 --threads 10 --csv"
```

#### 4d. redis-benchmark 直连 redis 测试（基线对比）

```bash
# 单线程验证
ssh -p 22 root@192.168.31.200 "redis-benchmark -h 127.0.0.1 -p 6379 \
  -t set,get -d 1024 -n 1000000 -c 1 --threads 1 --csv"

# 并发验证
ssh -p 22 root@192.168.31.200 "redis-benchmark -h 127.0.0.1 -p 6379 \
  -t set,get -d 1024 -n 5000000 -c 50 --threads 10 --csv"
```

#### 4e. 采集 heap 和 goroutine 快照（压测结束后）

```bash
# Heap profile（内存分配热点）
ssh -p 22 root@192.168.31.200 "curl -o /root/redis-proxy/heap.prof 'http://127.0.0.1:8080/debug/pprof/heap'"

# Goroutine 快照
ssh -p 22 root@192.168.31.200 "curl -o /root/redis-proxy/goroutine.txt 'http://127.0.0.1:8080/debug/pprof/goroutine?debug=1'"
```

#### 4f. 回传 profile 文件到本地分析

```bash
scp.exe root@192.168.31.200:/root/redis-proxy/cpu.prof .
scp.exe root@192.168.31.200:/root/redis-proxy/heap.prof .
scp.exe root@192.168.31.200:/root/redis-proxy/goroutine.txt .
```

### 5. 分析测试结果

#### 5a. 吞吐量/延迟对比

基于直连 redis 与走代理的方式，分析性能差异（吞吐量下降比例、延迟增加绝对值、尾部延迟放大倍数）。

**memtier_benchmark** 侧重自定义读写比例和随机 key 模式，关注平均/p50/p99/p99.9 延迟分布。

**redis-benchmark** 侧重纯 SET/GET 命令的最大吞吐量（无 miss 干扰），关注 requests/sec 绝对值，以及单线程 vs 多线程下的并行效率。

#### 5b. pprof CPU profile 分析

```bash
# 交互式火焰图分析
go tool pprof -http=:9090 cpu.prof

# 命令行 top 查看热点函数
go tool pprof -top cpu.prof

# 重点关注的维度：
# - resp.ReadCommand / resp.ReadRawReply 的 CPU 占比（协议解析开销）
# - backend.(*Backend).Forward 的 CPU 占比（网络 I/O 开销）
# - backend.(*Backend).acquire/release 的 CPU 占比（连接池开销）
# - syscall.Read/Write 的占比（系统调用开销）
# - runtime.mallocgc 的占比（内存分配开销）
```

#### 5c. Heap profile 分析

```bash
go tool pprof -http=:9090 heap.prof

# 重点关注：
# - bufio.Reader/Writer buffer 的内存占用
# - RESP 消息解析产生的临时对象
# - []byte 切片的分配量和逃逸情况
```

#### 5d. Goroutine 分析

```bash
# 查看 goroutine 数量和状态分布
cat goroutine.txt | head -1  # 总数
cat goroutine.txt | grep -E '^goroutine' | wc -l  # 详细计数
# 检查是否有大量 goroutine 阻塞在 channel send/receive 或 I/O wait
```

## 环境配置

| 环境 | 服务器 | 用途 |
|------|--------|------|
| 测试机 | build-server (192.168.31.200) | 构建镜像和 pkg 包 |


## 强制要求（防止历史问题重现）

### 编码规范（所有配置文件）
- **禁止添加 BOM**：必须是纯 UTF-8 编码，禁止 `\xEF\xBB\xBF`，会导致 kubectl 显示乱码
- **必须使用 LF 换行**：禁止 CRLF（`\r\n`），会导致 K8s ConfigMap 解析异常

### 部署流程（必须严格遵守）
1. **切换分支前**：必须先执行 `git clean -xdf && git reset --hard && git pull` 拉取最新代码
2. **提交代码**：构建前必须先 `git add` + `git commit` + `git push` 确保本地改动已同步到远端仓库，否则打包机拉不到最新代码

### 常见问题处理
- 确定服务正常运行后，在执行压力测试

## Quality Gates

- [ ] 确定服务正常运行（`redis-cli PING`）
- [ ] 确定 pprof 端点可访问（`curl /debug/pprof/`）