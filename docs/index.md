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
  - icon: 01
    title: Agent Loop
    details: 从零实现 think → act → observe 循环，理解模型如何在多轮执行中自主完成任务。
    link: /day1
    linkText: 学习执行循环
  - icon: 02
    title: Tool System
    details: 设计统一 Tool 接口与注册表，接入 Bash、文件读写等可插拔工具。
    link: /day2
    linkText: 构建工具系统
  - icon: 03
    title: Prompt Engineering
    details: 动态组装身份、规则、能力与工作上下文，让 System Prompt 成为运行时的一部分。
    link: /day3
    linkText: 设计系统提示词
  - icon: 04
    title: Skill & MCP
    details: 通过 Skill 渐进披露知识，通过 MCP 标准协议连接外部工具服务。
    link: /day4
    linkText: 扩展 Agent 能力
  - icon: 05
    title: Context Management
    details: 估算 Token、校准真实用量并自动摘要压缩，让长任务保持关键记忆。
    link: /day6
    linkText: 管理上下文
  - icon: 06
    title: Production Assembly
    details: 将配置、日志、会话和所有模块组装为一个真正可运行的命令行 Agent。
    link: /day7
    linkText: 完成最终组装
---

<div class="home-intro">
  <div class="home-intro__eyebrow">WHY GULL</div>
  <h2>不把 Agent 当作黑盒</h2>
  <p>模型只是发动机，Harness 才是底盘、方向盘、刹车与导航。本教程绕过大型框架，从最小可运行实现出发，逐步解释一次请求如何演化成自主执行系统。</p>
</div>

<div class="learning-path">
  <a class="learning-step" href="./day0">
    <span class="learning-step__day">DAY 0</span>
    <strong>建立正确心智模型</strong>
    <small>Harness、Tool Calling 与环境准备</small>
  </a>
  <a class="learning-step" href="./day1">
    <span class="learning-step__day">DAY 1–3</span>
    <strong>搭起 Agent 骨架</strong>
    <small>执行循环、工具系统与 Prompt</small>
  </a>
  <a class="learning-step" href="./day4">
    <span class="learning-step__day">DAY 4–6</span>
    <strong>获得可扩展能力</strong>
    <small>Skill、MCP 与上下文压缩</small>
  </a>
  <a class="learning-step" href="./day7">
    <span class="learning-step__day">DAY 7</span>
    <strong>组装完整运行时</strong>
    <small>配置、日志、会话与 CLI</small>
  </a>
</div>



<div class="home-quote">
  <span>核心公式</span>
  <code>Agent = Model + Harness</code>
  <p>用可以读懂的 Go 代码，掌握模型之外真正决定 Agent 能力上限的工程系统。</p>
</div>
