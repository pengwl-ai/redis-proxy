# 背景
  前面实现了基本的 redis 命令的代理，现在需要完善支持业务所有的 redis 命令，完整的 redis 命令在文档 xaas-bbc与xaas-bbc-proxy-Redis使用分析.md 中
# 方案
 - 保证 xaas-bbc与xaas-bbc-proxy-Redis使用分析.md  所有 redis 指令覆盖
 - 需要保证性能，并发性能，以及延迟
 - 保证可靠性，失败重连等
 - 现实一个完整的客户端，用于个功能测试与性能压力测试
 - 设计文档使用中文