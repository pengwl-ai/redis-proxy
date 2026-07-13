# 背景
  业务需要支持主备数据中心 redis 切换，业务层需要修改代码，重新加载配置，重连 redis 才能实现，希望构建 redis 代理的方式
  代理服务通过实现 redis 的协议，转发请求到 redis 服务，再把结果返回给业务
  
# 实现方案
  - redis 的协议文档在子目录 docs/protocol-spec.md
  - 代理分阶段实现 redis 的命令，优先实现 get set del 命令
  - 代理需要实现 redis 认证协议，解析出账号密码后转发到 redis 服务认证
  - 代理支持转发请求到多个 redis 服务，redis 需要能标记为主备，主 redis 转发失败为失败，备用 redis 转发失败不影响
  - 代理提供 API 接口，用于redis 主备的修改
  - 编程语言使用 golang
  - api 使用 gin 框架
