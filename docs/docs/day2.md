# Day 2：内置工具与注册表

::: warning 编写中
本篇正在编写中，敬请期待。
:::

## 本日目标

实现三个内置工具（bash / file_read / file_write）和可插拔的工具注册表。

## 你将学到

- Tool interface 的设计：Name / Description / Parameters / Execute
- bash 工具：subprocess 执行、超时控制、输出截断
- file_read 工具：分片读取、行号标注
- file_write 工具：自动创建目录、安全写入
- ToolRegistry：注册、查找、调度、错误处理
