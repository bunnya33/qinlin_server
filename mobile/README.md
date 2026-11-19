# 亲邻开门 · React Native

手机端基础版使用 Expo SDK 56、React Native 0.85 和 TypeScript。内置本地模拟服务，方便在接入亲邻后台前体验和开发。

## 启动

从仓库根目录进入手机端，使用 Node.js 24 和 npm。

~~~sh
cd mobile
npm ci
npm run web
~~~

页面会预填演示手机号。点击「获取验证码」，输入 123456 后登录。可切换门禁、分享和我的，生成分享接口、暂停、启用、删除、选择小组件默认门禁，以及重新授权。演示授权时间、分享列表和小组件选门保存在本机。开门按钮返回模拟结果。

~~~sh
npm run typecheck
npm run export:web
npm run export:ios
~~~

iOS 导出命令生成 Metro 的 JavaScript/Hermes 产物。安装包、签名和 Widget 扩展在 macOS 的 Xcode 中构建。Windows 上也可以用 Web 版检查交互。

本次 npm 审计报告了 Expo CLI 传递依赖的已知问题，包括 Zod 的资源消耗告警。该依赖用于本机开发工具链。[GHSA-mrhx-c7xc-m3w5](https://github.com/advisories/GHSA-mrhx-c7xc-m3w5)

## 代码入口

- [App.tsx](App.tsx) 管理登录、门禁、授权、分享页面和小组件选门。
- [src/ui.tsx](src/ui.tsx) 包含底部滑动导航和通用控件。
- [src/service.ts](src/service.ts) 定义手机端服务契约、接口状态和授权时间的计算。
- [src/mockService.ts](src/mockService.ts) 提供本地演示数据。模拟接口使用保留域名，模拟凭据仅在生成结果中使用。

现有 Go 后台的亲邻短信和旧令牌接口属于管理员 API，使用单亲邻账号。正式版需要独立手机会话和按账号隔离的服务，再将 App 的模拟服务入口切换到真实适配器。[iOS 与 WidgetKit 交接](IOS-HANDOFF.md)
