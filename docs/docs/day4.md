# Day 4：Skill 注册与加载

::: warning 编写中
本篇正在编写中，敬请期待。
:::

## 本日目标

设计 Skill 规范，实现目录扫描和动态加载机制。

## 你将学到

- Skill 目录规范：SKILL.md 的 frontmatter 定义
- SkillLoader：扫描目录、解析元数据、注入 prompt
- 两级加载优先级：全局 skill vs 项目 skill
- Skill 附带工具的自动注册
