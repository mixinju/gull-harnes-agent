# Day 5：MCP 客户端

::: warning 编写中
本篇正在编写中，敬请期待。
:::

## 本日目标

实现 MCP（Model Context Protocol）客户端，对接外部工具服务。

## 你将学到

- MCP 协议基础：JSON-RPC 2.0 over stdio
- stdio transport：通过 exec.Command 启动子进程通信
- 协议握手：initialize / initialized 流程
- 工具发现与桥接：tools/list → 注册到 ToolRegistry → tools/call 转发
- goroutine 管理子进程的读写与生命周期
