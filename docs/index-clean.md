# Infinite Canvas 文档索引

## 项目介绍

- [快速开始](/docs/overview/quick-start)
- [功能介绍](/docs/overview/features)
- [Render 部署](/docs/overview/render)
- [Docker 部署](/docs/overview/docker)
- [第三方 GitHub 提示词仓库](/docs/overview/third-party-prompt-repositories)

## 部署方案

- [方案二：前端直连 Render + URL 结果返回](/docs/deployment/solution-2-direct-render-url-results)
- [方案三：图片异步任务 + 前端轮询](/docs/deployment/solution-3-async-task-polling)

## 画布操作

- [画布节点操作手册](/docs/canvas/canvas-node-manual)
- [画布快捷键](/docs/canvas/canvas-shortcuts)

## 后端与开发

- [本地开发](/docs/backend/local-development)
- [接口响应约定](/docs/backend/api-response)
- [系统配置数据结构](/docs/backend/system-settings)
- [后端数据库说明](/docs/backend/backend-database)
- [画布数据结构](/docs/backend/canvas-data-structure)

## 项目进度

- [待测项](/docs/progress/pending-test)
- [TODO](/docs/progress/todo)

## 说明

- 当前画布项目和“我的素材”主要保存在浏览器本地，默认不做云同步。
- 本地直连模式下，AI API Key 保存在浏览器本地并由前端直接请求 OpenAI 兼容接口。
- 远程部署推荐使用前端静态托管 + Render Go API + Postgres / Storage 的拆分架构。
