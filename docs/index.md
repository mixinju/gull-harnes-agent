---
layout: home

hero:
  name: Gull Agent Harness
  text: 从模型调用到自主行动
  tagline: 七天、约千行 Go 代码，亲手构建一个能思考、会用工具、可持续扩展的 AI Agent 运行时。
  actions:
    - theme: brand
      text: 开始七日教程
      link: /day0
    - theme: alt
      text: 阅读 Agent Loop
      link: /day1
    - theme: alt
      text: 查看源码
      link: https://git.sankuai.com/~mixinju/gull-harness-agent

features:
  - icon:
      src: /icons/agent-loop.svg
      alt: Agent Loop
    title: Agent Loop
    details: 从零实现 think → act → observe 循环，理解模型如何在多轮执行中自主完成任务。
    link: /day1
    linkText: 学习执行循环
  - icon:
      src: /icons/tool-system.svg
      alt: Tool System
    title: Tool System
    details: 设计统一 Tool 接口与注册表，接入 Bash、文件读写等可插拔工具。
    link: /day2
    linkText: 构建工具系统
  - icon:
      src: /icons/prompt-engineering.svg
      alt: Prompt Engineering
    title: Prompt Engineering
    details: 动态组装身份、规则、能力与工作上下文，让 System Prompt 成为运行时的一部分。
    link: /day3
    linkText: 设计系统提示词
  - icon:
      src: /icons/skill-mcp.svg
      alt: Skill and MCP
    title: Skill & MCP
    details: 通过 Skill 渐进披露知识，通过 MCP 标准协议连接外部工具服务。
    link: /day4
    linkText: 扩展 Agent 能力
  - icon:
      src: /icons/context-management.svg
      alt: Context Management
    title: Context Management
    details: 估算 Token、校准真实用量并自动摘要压缩，让长任务保持关键记忆。
    link: /day6
    linkText: 管理上下文
  - icon:
      src: /icons/production-assembly.svg
      alt: Production Assembly
    title: Production Assembly
    details: 将配置、日志、会话和所有模块组装为一个真正可运行的命令行 Agent。
    link: /day7
    linkText: 完成最终组装
---

## 不把 Agent 当作黑盒

模型只是发动机，Harness 才是底盘、方向盘、刹车与导航。本教程绕过大型框架，从最小可运行实现出发，逐步解释一次请求如何演化成自主执行系统。

## 学习路径

| 阶段 | 学习目标 | 核心内容 |
| --- | --- | --- |
| [Day 0](/day0) | 建立正确心智模型 | Harness、Tool Calling 与环境准备 |
| [Day 1–3](/day1) | 搭起 Agent 骨架 | 执行循环、工具系统与 System Prompt |
| [Day 4–6](/day4) | 获得可扩展能力 | Skill、MCP 与上下文压缩 |
| [Day 7](/day7) | 组装完整运行时 | 配置、日志、会话与 CLI |

::: tip 核心公式

**Agent = Model + Harness**

用可以读懂的 Go 代码，掌握模型之外真正决定 Agent 能力上限的工程系统。

:::
