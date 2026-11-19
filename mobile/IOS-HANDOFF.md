# 亲邻开门 · macOS 与 iOS 开发交接

React Native 基础版位于 mobile。已实现短信登录流程、门禁列表、开门结果、小组件选门、分享管理、重新授权和授权时长，业务使用本地模拟数据。下一阶段在 macOS 接入 iOS 原生扩展和真实手机 API。

## 在 Mac 启动

1. 安装 Node.js 24 与适配 Expo SDK 56 的 Xcode，安装所需 iOS Simulator runtime，完成 Xcode 初始设置。运行 xcodebuild -version 后检查系统能调用 Xcode 工具。
2. 从仓库根目录进入 mobile，执行下列命令。首次运行将生成 iOS 原生工程并安装 Pods。

~~~sh
cd mobile
npm ci
npm run typecheck
npx expo prebuild --platform ios
npx expo run:ios
~~~

3. 原生扩展需要自有开发构建。给 iPhone 安装时，在 Xcode 打开 ios/*.xcworkspace，选择已登录的 Apple Team，为主 App 和 Widget 扩展配置签名，连接并信任设备。com.qinlin.mobile 是交接用标识，发布前使用团队可用的唯一标识。
4. 当前 mobile/.gitignore 忽略预生成的原生工程。创建 Widget target 后，将手工修改纳入版本控制，或用 Expo config plugin 重现 target、entitlements 与源文件。采用手工 Xcode 工程时，先把它纳入版本控制再执行会重建工程的操作。
5. 模拟器先走一遍主 App。真机验证交互组件、锁屏认证、短信自动填充、Keychain、系统分享和签名。按 Expo 及所用 Xcode 版本核实宿主最低系统版本。交互式 Widget 以 iOS 17+ 为起点。

[Expo 本地构建](https://docs.expo.dev/guides/local-app-development/) · [Expo Prebuild](https://docs.expo.dev/workflow/continuous-native-generation/)

## WidgetKit 扩展

在 Xcode 新建 SwiftUI 的 Widget Extension target，先支持主屏幕 systemSmall。组件显示小区、所选门、当前状态和「开门」按钮。React Native 中「小组件用门」保存用户选择后，通过本地 Expo Module 或原生桥接写入共享数据，再调用 WidgetCenter 刷新。宿主和扩展的 App Group 标识使用同一个实际值，示例为 group.com.qinlin.mobile。

| 状态 | 小组件行为 |
| --- | --- |
| 已选门且授权可用 | 显示可操作的「开门」 |
| 处理中 | 显示「处理中」，拦截重复点击 |
| 亲邻明确接受 | 显示「请求已受理」 |
| 授权过期或门禁权限变化 | 显示「需重新授权」或「门无权限」，入口回到 App 的对应页面 |
| 超时或上游结果不明 | 显示「结果未确认」，引导用户确认门的实际状态 |

- App Group 只保存显示所需的字段，例如账号的匿名 ID、门的本地 ID、小区和门的显示名、配置版本、最近展示状态。退出账号、换账号或门被移除后清空或失效化该配置，并刷新 timeline。
- 组件开门使用 Button(intent:) 和 AppIntent。把网络入口和凭据访问放在 Swift 侧，支持 React Native JS 尚未启动的情况。每次用户主动点击用新的请求 ID，遇到超时只显示结果不明。Widget 的 timeline 提供显示内容，开门请求由用户的 AppIntent 触发。
- 为开门 Intent 配置适当的本机认证策略，使用真机确认锁屏、设备重启、锁屏小组件不可用时的行为。异常状态可通过深链交给宿主，深链路由只用于打开页面。现有 qinlin scheme 已在 Expo 配置中声明，业务路由需要在原生接入阶段接上。
- App 与 Widget 使用 HTTPS 访问服务。会话凭据保存在 iOS Keychain，Widget 如需独立认证，使用账号和所选门限定的短权限凭据。通过 Keychain Access Groups 共享该项或由扩展单独持有，由服务端按账号、门及设备范围校验并撤销。App Group、组件 timeline、公开 URL、系统日志中只使用非敏感字段。退出、取消小组件授权和账户切换均清除本机凭据。
- 主 App 首次成功保存选门并建立 Widget 授权后再展示可操作组件。授权过期、权限变化、服务禁用及分享开关均以服务端当前数据为准。

[Widget 交互](https://developer.apple.com/documentation/widgetkit/adding-interactivity-to-widgets-and-live-activities) · [App Intent 认证策略](https://developer.apple.com/documentation/appintents/appintent/authenticationpolicy) · [App Groups](https://developer.apple.com/documentation/xcode/configuring-app-groups) · [Keychain 共享](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps)

## 真实手机 API

现有 Go 项目在 internal/server/server.go 使用管理员保护亲邻短信、授权和令牌管理，在 internal/store/store.go 使用单账号表。先实现独立于管理员的手机会话，然后按 mobile/src/service.ts 的 MobileService 接口实现网络适配器。下面是可实施的契约，路径与载荷待后端落地。

| 客户端操作 | 服务端职责 |
| --- | --- |
| sendSms / verifySms | 对手机号、设备和 IP 限流。续授权请求由当前账号推导手机号。验证成功签发或维持手机会话，返回本轮成功授权的服务端时间。 |
| current / signOut | 根据用户会话返回本人账号、门禁和分享接口元数据。退出时撤销当前设备会话与关联的 Widget 凭据。 |
| open | 验证账号、门和调用身份。沿用 Go 后台的授权检查、门级冷却、请求去重和结果码。 |
| createShare | 每条接口绑定稳定的账号 ID 和一扇门。签发独立的接口地址和调用凭据，服务端保存凭据摘要。生成时一次性下发可复制凭据。 |
| setShareEnabled / deleteShare | 仅接口所属账号可启停或永久撤销。每次调用按账号授权、该门归属、门禁开关及分享状态重新判断。 |
| setWidgetDoor | 保存本设备选择的门，刷新或撤销旧的 Widget 授权。 |

- last_authorized_at 记录最近一次短信授权成功的服务端时间，使用 UTC ISO 8601。现有后台的 verified_at 用于检查状态，手机端用 last_authorized_at 计算「距上次授权」，避免定时检测重置计时。
- 同一账号重新授权只更新上游会话及 last_authorized_at。门权限仍在且启用的分享恢复使用原地址与原凭据。主动暂停保持暂停，删除永久失效，门权限移除立即阻止调用。
- iOS 上用 Keychain 存设备会话。分享凭据留在生成页面的内存中，复制或分享必须由用户主动触发。使用现有 Authorization 请求头的鉴权模式，地址本身只包含公开标识。
- 移动端开门结果区分 accepted、pending、rejected、unknown。accepted 对应上游已受理，门的物理状态需现场确认。后台短信验证码、管理员密钥、亲邻上游会话和 Widget 授权各自使用对应的凭证与生命周期。

## 真机验收

- 短信验证码自动填充、前后台恢复后更新授权时长、失败的验证码不改写授权时间。
- 当前账号只显示自己的门和分享。换账号后小组件不继续展示或调用之前账号的门。
- 暂停和删除的老凭据停止工作。暂停后可手动启用，删除后永久失效。续授权前后的已启用接口使用同一地址和凭据。
- 小组件在宿主未启动、退到后台、无网、超时、请求处理中、亲邻授权过期以及门权限移除的状态下给出正确反馈。
- 开门需从已解锁或已通过本机认证的设备发起。连点、宿主与组件同时操作时核对服务端记录只有一次实际请求。现场确认目标门的动作。
- 检查小尺寸 iPhone、动态字体、VoiceOver、减少动态效果、系统分享和返回前台。确认所有状态反馈能在系统字体放大时完整显示。

## 可直接交给 Mac 上 AI 的任务

> 阅读 mobile/README.md、mobile/IOS-HANDOFF.md 和 mobile/src/service.ts。在 Mac 用当前 Expo SDK 版本构建 React Native App。用 Xcode 添加 WidgetKit 扩展及 AppIntent，接收 App 的小组件选门。先用可验证的测试服务跑通 App Group 数据同步、Keychain、真机交互与锁屏认证，再实现独立的用户 API 和网络适配器。保留当前界面、底部动画和授权时间语义。确认分享地址及凭据在同账号续授权后稳定，按文档逐项验证，并记录 Xcode、iOS 和测试结果。
