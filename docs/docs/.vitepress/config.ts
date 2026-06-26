import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'

export default withMermaid(
  defineConfig({
    title: 'Agent Harness 实战',
    description: '七天用 Go 从零手写 Agent Harness 框架',
    lang: 'zh-CN',

    themeConfig: {
      logo: '/logo.svg',

      nav: [
        { text: '教程', link: '/day0' },
        { text: 'GitHub', link: 'https://github.com/yourname/agent-harness' }
      ],

      sidebar: [
        {
          text: '七天构建 Agent Harness',
          items: [
            { text: 'Day 0 · 起步', link: '/day0' },
            { text: 'Day 1 · Agent Loop', link: '/day1' },
            { text: 'Day 2 · 内置工具与注册表', link: '/day2' },
            { text: 'Day 3 · System Prompt 工程', link: '/day3' },
            { text: 'Day 4 · Skill 注册与加载', link: '/day4' },
            { text: 'Day 4+ 阶段总结', link: '/summary-01' },
            { text: 'Day 5 · MCP 客户端', link: '/day5' },
            { text: 'Day 6 · 消息管理与对话压缩', link: '/day6' },
            { text: 'Day 7 · 整合与 CLI 交互', link: '/day7' },
          ]
        }
      ],

      outline: {
        label: '本页目录',
        level: [ 2, 3 ]
      },

      footer: {
        message: '用 Go 从零理解 Agent Harness 的每一行代码',
      },

      search: {
        provider: 'local',
        options: {
          translations: {
            button: { buttonText: '搜索', buttonAriaLabel: '搜索' },
            modal: {
              noResultsText: '没有找到结果',
              resetButtonTitle: '清除查询',
              footer: { selectText: '选择', navigateText: '导航', closeText: '关闭' }
            }
          }
        }
      },

      docFooter: {
        prev: '上一篇',
        next: '下一篇'
      },

      lastUpdated: {
        text: '最后更新于'
      }
    }
  }))
