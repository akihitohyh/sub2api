# PR #1018 前后对比截图（第二组）

- before 构建：`a972dba33`（本 PR 的 base，production）
- after 构建：`d49838401`（PR 头；之后的提交只改后端测试/文档，前端未变）
- 渲染：本地 `vite` 开发服务器（仓库真实 CSS/组件），后端为只返回虚构空数据的本地假 API；无头 Chromium（playwright-core 1.55），deviceScaleFactor 2
- 步骤：预置登录态、语言、主题 → 打开 `/usage` → before：页签行滚到屏幕中部截图；after：把页签行滚到屏幕底部，点击「用量 API / Usage API」页签后截图（admin-tabs 不点击，只看页签行与「新功能」标记）

| 文件 | 角色 | 语言 | 主题 | 视口 |
|---|---|---|---|---|
| before/after-zh-dark-desktop | 普通用户 | zh | dark | 1440×900 |
| before/after-en-light-desktop | 普通用户 | en | light | 1440×900 |
| before/after-zh-dark-mobile | 普通用户 | zh | dark | 390×844 |
| before/after-zh-dark-admin-tabs | 管理员 | zh | dark | 1440×900（裁剪页签附近 520px） |
| after-zh-dark-full | 普通用户 | zh | dark | 1440×3600 整页 |
