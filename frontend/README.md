# Frontend

这是一个基于 Vue 3、Vite 和 TypeScript 的前端基础项目。

当前仅提供可启动的应用壳，不包含业务路由、状态管理、后端请求或 UI 组件库。真实业务需求明确后，再按垂直功能切片扩展模块。

## 环境要求

- Node.js 20 或更高版本
- npm

## 常用命令

在 `frontend/` 目录执行：

```bash
npm install
npm run dev
```

其他命令：

```bash
npm run type-check
npm run build
npm run preview
```

开发服务器默认地址为 `http://localhost:5173`。

## 目录结构

```text
frontend/
├── index.html       # 页面元信息和 Vue 挂载点
├── package.json      # 依赖和 npm scripts
├── vite.config.ts    # Vite 配置
├── tsconfig*.json    # TypeScript 配置
└── src/
    ├── main.ts       # 应用入口
    ├── App.vue       # 根视图
    └── style.css     # 全局基础样式
```

## 开发约定

- 使用 Vue 3 Composition API 和 TypeScript。
- `main.ts` 只负责应用组装，不在入口中放业务逻辑。
- 暂无 API client、路由、状态管理和 UI 框架；出现真实需求后再引入。
- 不将 `node_modules/`、`dist/` 或本地环境文件提交到仓库。
- 修改后至少执行 `npm run type-check` 和 `npm run build`。
