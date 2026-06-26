# Day 6：消息管理与对话压缩

::: warning 编写中
本篇正在编写中，敬请期待。
:::

## 本日目标

实现消息管理器，解决对话过长时的上下文窗口管理问题。

## 你将学到

- Token 估算：字符比例法 vs tiktoken-go
- ContextManager：维护消息列表、追踪 token 用量
- 压缩策略：保留 system prompt + 最近 N 轮，中间历史生成摘要
- 手动指令：/compact 触发压缩、/clear 清空历史
- 压缩对 Agent 行为的影响与权衡
