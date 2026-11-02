# 亲邻个人开门服务

Go 单服务，面向自己的亲邻账号和已有门禁权限。管理页完成短信登录、门禁选择和令牌管理；iPhone 快捷指令通过一个 POST 请求开门，无需打开亲邻 App。

**当前状态：模拟 HTTP 流程、自动化安装和升级验证已完成；本地已有账号的定时只读检测已收到成功响应。实际开门效果和 iPhone 体验仍需现场验证。** 亲邻协议依据公开项目移植，非官方 API；没有实现未经证实的自动续期，亲邻会话失效后，在管理页重新短信登录。

## Linux 服务器一键安装

支持使用 systemd 的 Debian/Ubuntu、Rocky/AlmaLinux 等常见 Linux 发行版。无需域名，默认使用 HTTP 和服务器 IP，默认端口为 8080。

```bash
curl -fsSL https://raw.githubusercontent.com/bunnya33/qinlin_server/main/bootstrap.sh | sudo bash
```

如果需要指定公网 IP 和端口，可在首次安装时传入：

```bash
curl -fsSL https://raw.githubusercontent.com/bunnya33/qinlin_server/main/bootstrap.sh \
  | sudo bash -s -- --port 8080 --public-url http://你的服务器IP:8080
```

也可通过 Git 克隆安装，两种方式共用同一安装逻辑：

```bash
git clone https://github.com/bunnya33/qinlin_server.git
cd qinlin_server
sudo bash install.sh --public-url http://你的服务器IP:8080
```

安装脚本会补齐必要依赖，优先使用已有 Go 1.26+，否则从 Go 官方下载并核验校验和，再编译程序、注册 systemd 服务、启动并开启开机自启。首次安装会显示访问地址、随机管理员账号和强随机密码，请记录。重复安装保留既有配置、管理员凭据、加密密钥、亲邻会话、门禁及快捷指令令牌；首次安装参数不会覆盖已有配置。

安装后打开中文数字菜单：

```bash
sudo qinlin
```

菜单支持查看状态、启动、停止、重启、修改端口、修改管理员账号密码、随机重置管理员账号密码，以及卸载。修改端口会校验范围和占用，同时更新访问地址并重启；修改配置后若启动失败会恢复原配置。管理员密码输入隐藏，配置只保存 bcrypt 密码哈希；随机重置后显示新凭据，普通状态界面不显示密码。

| 安装位置 | 用途 |
| --- | --- |
| `/opt/qinlin` | 程序、管理脚本，以及按需下载的项目专用 Go 工具链 |
| `/etc/qinlin/qinlin.env` | 配置和密码哈希，root 管理，服务用户只读 |
| `/var/lib/qinlin` | SQLite 业务数据和会话密文 |
| `/etc/systemd/system/qinlin.service` | systemd 服务定义 |
| `/usr/local/bin/qinlin` | 管理菜单命令 |

服务以无登录权限的 `qinlin` 系统用户运行。日志可通过 `sudo journalctl -u qinlin -n 50` 查看。卸载需要明确确认，默认保留配置和业务数据；彻底删除必须再次输入 `DELETE`。卸载不会移除共享系统依赖，系统服务账号保留，以便保留数据后重新安装继续使用。保留数据重新安装会沿用原凭据和密钥。

没有外网 Go 下载条件时，可在匹配架构的机器编译 Linux 程序，再指定已有程序安装：

```bash
sudo QINLIN_PREBUILT_BINARY=/绝对路径/qinlin-linux-amd64 bash install.sh
```

## 账号过期检测与版本更新

账号关联后，服务默认每 **15 分钟**只读查询一次门禁权限，浏览器关闭后也继续执行。在「亲邻账号与检测设置」可设为 **5～1440 分钟**，填写 **0** 关闭；顶部「立即检测」可手动执行。设置保存在 SQLite，重启后保留。页面显示最近检测结果和下次检测时间，每 30 秒读取服务器保存的状态。

亲邻明确返回登录失效时，账号标记为已过期，暂停定时查询并禁用开门，重新短信登录后恢复。断网、超时或未知响应显示「暂无法确认」，保留账号与门禁配置，按原间隔再检测。检测不会发短信或开门，也不承诺延长会话或推算剩余有效期；查询成功会同步权限，已失去权限的门和对应令牌按原规则停用。

Linux 安装版在后台「服务更新」显示当前版本、最新正式版本、发布说明和下载进度。默认每 **6 小时**检查 GitHub Releases，启动后首次检查在约 1 分钟后执行；默认发现新版本时自动下载，可分别关闭自动检查或自动下载，也可手动检查、下载。

下载只使用本仓库正式发布的、匹配服务器架构的程序，并校验 `SHA256SUMS`。下载期间继续运行原版本；点击「重启使用新版本」并确认后，才停止原进程并启动新程序。有开门或检测任务正在处理时会拒绝立即重启。新版本必须通过本地健康检查和版本核对，失败时恢复旧版；成功后重启服务器仍使用已选版本。账号、密钥、门禁及快捷指令令牌保留，后台网页需要重新登录。

网页更新适用于 **Linux amd64/arm64 的 systemd 安装版**。已有旧安装需要先重新运行一次 `bootstrap.sh` 或 `install.sh`，以安装新版进程管理方式，之后可在网页下载和重启更新。Windows 独立运行及 Docker 可查看正式版本，更新方式分别为替换程序、重新构建或更新镜像。

维护者发布时使用新的 `v主版本.次版本.修订号` Git 标签；`.github/workflows/release.yml` 会测试并发布 Linux amd64、Linux arm64、Windows amd64 程序和校验文件，并实际验证从旧版本下载、重启升级及保留数据。升级中的数据库变更必须保持与上一版兼容，确保程序回滚仍可读取原数据。

## 本地源码启动

需要 Go 1.26 或更高版本，SQLite 不需要另外安装，也不需要 C 编译器。

```powershell
# 仅首次执行；如果工作目录已有 .env，不要再次初始化。
go run ./cmd/qinlin init

go run ./cmd/qinlin
```

打开 **http://localhost:8080**，用 `.env` 中的 `QINLIN_ADMIN_TOKEN` 进入管理页。初始化会自动生成管理密钥、会话加密密钥和固定设备标识；不会输出密钥，不会发送短信，不会连接亲邻或开门。

如需改用管理员账号密码，可先运行以下任一命令，再启动或重启服务：

```bash
go run ./cmd/qinlin admin set       # 手动设置，密码输入隐藏
go run ./cmd/qinlin admin reset     # 随机重置并显示新凭据
go run ./cmd/qinlin admin show      # 只显示非敏感配置，不显示密码或管理 Token
```

设置或重置后台凭据会同时轮换管理 Token，重启后旧管理 Token 和旧网页登录失效。亲邻账号会话、门禁和 iPhone 快捷指令的专用令牌继续保留。旧的纯 Token 配置可直接继续使用；无需迁移数据库。

请将服务地址复制到 Chrome、Edge 或 Safari 地址栏中打开。如果应用内预览提示无法连接接口，使用页面上的「直接打开服务」地址；静态预览不能代替运行中的 Go 服务。

也可以直接编译运行：

```powershell
go build -o bin/qinlin.exe ./cmd/qinlin
.\bin\qinlin.exe
```

在管理页：

1. 展开「关联亲邻账号」，输入自己的手机号，主动获取短信验证码并登录。
2. 在「我的门禁」启用需要的门。服务只接受从当前账号查询到的门禁。
3. 点击该门的「配置快捷指令」，复制一次性显示的 URL 和 Authorization 请求头。
4. 在 iPhone 配置快捷指令。网页上的「开门」按钮也可直接使用。

网页开门时，门禁卡片会显示「正在处理」，随后显示亲邻返回成功、请求被拒绝或结果不确定，以及本次请求时间。已保存的最近一次结果在刷新页面后仍可查看；页面下方「最近开门结果」可查看原因和请求编号，点击「刷新结果」只读取记录，不会再次开门。成功表示亲邻接受了请求，暂未接入门锁实际状态反馈；模拟模式会单独注明未发送真实命令。

同一手机号重新登录后，仍有权限的门和快捷指令继续有效。换账号、断开账号、禁用门或某扇门从权限列表消失时，相关令牌会被撤销。重新启用门后需要重新生成令牌。

## iPhone 快捷指令

手机需要能访问服务的 **HTTP 或 HTTPS 地址**，可以直接使用服务器 IP，无需域名。iPhone 的 `localhost` 指向手机自身，不能使用电脑的 localhost 地址。先按下方部署说明配置服务器地址和监听端口。

在「快捷指令」App 新建快捷指令，添加「获取 URL 内容」：

| 项目 | 设置 |
| --- | --- |
| URL | 管理页为该门生成的地址，例如 `http://服务器IP:8080/api/v1/doors/本地门ID/open` |
| 方法 | `POST` |
| 请求头 `Authorization` | `Bearer ql_...`，复制管理页生成的完整内容 |
| 请求头 `Content-Type` | `application/json` |
| 请求体 | 选择 JSON，保留空字典 `{}` |

之后添加「获取字典值」，键设为 `message`，再添加「显示结果」即可。可以把这个快捷指令添加到主屏幕、操作按钮或 Siri。不同 iOS 版本的操作名称可能略有不同。

可选：在请求前添加「生成 UUID」，将 UUID 作为 `Idempotency-Key` 请求头。**每次主动开门生成新 UUID；同一次请求重试必须使用同一个 UUID。** 不要把固定 UUID 永久写在快捷指令里，也不要增加失败自动重试或离线排队。

等效 HTTP 请求：

```bash
curl -X POST 'http://203.0.113.10:8080/api/v1/doors/LOCAL_DOOR_ID/open' \
  -H 'Authorization: Bearer YOUR_DOOR_TOKEN' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: NEW_UUID_FOR_THIS_ACTION' \
  --data '{}'
```

返回示例：

```json
{
  "ok": true,
  "mock": false,
  "replayed": false,
  "operation": {
    "request_id": "NEW_UUID_FOR_THIS_ACTION",
    "door_id": "LOCAL_DOOR_ID",
    "state": "accepted",
    "code": "upstream_accepted",
    "created_at": 1793491200000,
    "updated_at": 1793491200200
  },
  "message": "亲邻服务端已接受请求，请确认门的实际状态。"
}
```

`accepted` 表示亲邻接口确认接收，不表示已获得门磁反馈。`unknown` 表示超时、断网、响应不兼容或结果保存异常，必须先观察门的实际状态。`rejected` 表示明确拒绝；`pending` 表示已有请求在处理中。所有状态均不会触发后台自动重试。

## 先用模拟模式体验

在 `.env` 中改为：

```dotenv
QINLIN_MOCK=true
QINLIN_DB=data/demo.db
```

启动后输入任意符合格式的手机号，例如 `13800138000`，验证码固定为 `123456`。模拟短信、门禁查询和开门全部在本地完成，没有真实网络请求。页面和开门结果会明确标注模拟模式。

结束后把 `QINLIN_MOCK=false`、`QINLIN_DB=data/qinlin.db` 改回并重启。若在同一数据库切换模拟/真实模式，会清除账号关联、禁用门禁并撤销全部旧令牌，防止混用。

## 部署到常开服务器

先将源码复制到服务器，在服务器初始化自己的 `.env`。使用 IP 和 HTTP 直接访问时设置（将示例 IP 换成服务器实际地址）：

```dotenv
QINLIN_ADDR=0.0.0.0:8080
QINLIN_PUBLIC_URL=http://203.0.113.10:8080
```

`QINLIN_PUBLIC_URL` 必须与浏览器实际打开的来源一致，包含协议和端口，不带路径。HTTP 和 HTTPS 均可使用，不要求域名或备案。直接运行 Go 服务即可通过设置的地址访问；需要服务器网络允许相应端口。

也可使用 Docker 配置：

```bash
docker compose up -d --build
```

Compose 默认将应用发布到服务器的 `127.0.0.1:8080`。通过 IP 直接访问 Docker 服务时，把 `compose.yaml` 中的端口映射改为 `"8080:8080"`。数据保存在命名卷 `qinlin_data`，容器以非 root 用户运行。`.env` 通过环境变量注入，不会进入构建镜像。首次启动需要联网下载 Go 依赖和镜像。

如使用已有私有网络、Caddy/Nginx 或 HTTPS 网关，设置对应访问来源即可；可选的 [Caddy 示例](deploy/Caddyfile) 假设反向代理运行在同一宿主机。反向代理须保留 `Origin`、`Authorization`、`X-CSRF-Token` 和 `Idempotency-Key`；不要缓存 API、记录请求头/请求体或为开门请求设置重试。`GET /healthz` 只检查本地存储和进程，不请求亲邻。

## 配置

默认读取工作目录的 `.env`，可用 `QINLIN_CONFIG` 指定其他配置文件。进程环境变量优先于配置文件。配置只支持简单的 `KEY=value`，不使用 shell 引号或变量展开。命令行管理操作可通过 `--config /路径/文件` 指定目标配置；systemd 安装由 `sudo qinlin` 自动管理对应配置和重启。

| 变量 | 含义 |
| --- | --- |
| `QINLIN_ADDR` | 默认 `127.0.0.1:8080`；Compose 内覆盖为 `0.0.0.0:8080` |
| `QINLIN_PUBLIC_URL` | 管理页来源和生成的快捷指令 URL |
| `QINLIN_DB` | 默认 `data/qinlin.db` |
| `QINLIN_ADMIN_TOKEN` | 初始化生成的管理密钥，至少 32 字符 |
| `QINLIN_ADMIN_USERNAME` | 可选的管理员用户名，通过管理菜单或 `admin set/reset` 设置 |
| `QINLIN_ADMIN_PASSWORD_HASH` | 可选 bcrypt 密码哈希；CLI 使用 `bcrypt:` 加 Base64 编码，兼容 Compose，无明文密码 |
| `QINLIN_CONFIG` | 配置文件路径，默认 `.env`；systemd 使用 `/etc/qinlin/qinlin.env` |
| `QINLIN_ENCRYPTION_KEY` | Base64 编码的 32 字节 AES-GCM 主密钥 |
| `QINLIN_DEVICE_ID` | 固定设备标识，不要每次重启都变更 |
| `QINLIN_COOLDOWN` | 默认 `5s`，从上次请求结束后计算；至少 `1s` |
| `QINLIN_UPSTREAM_TIMEOUT` | 单次亲邻请求超时，默认 `8s`，范围 `1s`～`30s` |
| `QINLIN_MOCK` | `true` 为完全本地模拟，默认 `false` |

不要提交 `.env`、数据库及数据卷；仓库已配置忽略。Linux 文件使用受限权限；Windows 上仍应使用账户和 NTFS 权限限制访问。

## API

除浏览器登录与健康检查外，所有接口都要求认证。管理 API 可用管理密钥的 Bearer 请求头，网页则使用 HttpOnly Cookie 和 CSRF。快捷指令令牌只允许查看自己的门和开启该门，不可调用管理 API。令牌有效期为 90 天，只保存 SHA-256 摘要。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | 登录：`{"username":"账号","password":"密码"}`，或旧版 `{"token":"管理密钥"}`；要求正确 Origin |
| `GET` | `/api/v1/auth/session` | 浏览器会话和 CSRF |
| `POST` | `/api/v1/auth/logout` | 撤销当前浏览器会话 |
| `GET` | `/api/v1/status` | 账号状态；不返回上游凭据 |
| `POST` | `/api/v1/account/sms` | 发送短信：`{"phone":"..."}` |
| `POST` | `/api/v1/account/login` | 登录并查门：`{"phone":"...","code":"..."}` |
| `DELETE` | `/api/v1/account` | 清除关联并撤销令牌 |
| `GET` | `/api/v1/doors` | 门禁列表，仅返回本地标识和名称等展示字段 |
| `POST` | `/api/v1/doors/refresh` | 手动刷新当前账号权限 |
| `PATCH` | `/api/v1/doors/{id}` | 白名单开关：`{"enabled":true}` |
| `POST` | `/api/v1/doors/{id}/open` | 明确指定门开门，请求体为 `{}` |
| `GET / POST` | `/api/v1/tokens` | 查看令牌元数据 / 创建：`{"name":"iPhone","door_id":"..."}` |
| `DELETE` | `/api/v1/tokens/{id}` | 撤销指定令牌 |
| `GET` | `/api/v1/operations` | 最近 50 条请求 |
| `GET` | `/healthz` | 无开门副作用的健康检查 |

状态码：`200` 已完成；`202` 同一请求处理中；`400/415` 请求格式错误；`401/403` 未认证或无权；`409` 登录失效、明确拒绝或操作编号冲突；`429` 限流/冷却；`502` 结果不确定或上游异常。API 不接收查询串，不提供 GET 开门或默认门回退。

## 请求去重、恢复与备份

- 同一调用身份 + 同一 `Idempotency-Key` + 同一门，在 7 天保留期内只派发一次；更换门复用编号返回冲突。缺少编号时服务生成一个并返回，此时只能依靠门级冷却防止连续误触。
- 每扇门同一时刻只允许一个在途请求。调用亲邻时不会转发本地认证头或去重头，不跟随重定向，不复用连接，也不进行应用层重试。
- 派发前先写 SQLite，重启把所有未完成操作标为 `unknown`，绝不重放；数据库文件锁禁止两个服务实例同时使用一个数据库。单实例部署，SQLite 应使用本地磁盘。
- Cookie 为 HttpOnly、SameSite=Strict，HTTPS 下带 Secure；空闲 1 小时或绝对 12 小时失效，重启后需重新进入管理页。修改管理密钥并重启可撤销全部网页登录；快捷指令令牌独立管理。
- 只有会话和账户指纹保存在 AES-GCM 密文中，不保存手机号和验证码。日志只记录操作编号及结果类别，不记录上游 URL、签名原文、手机号或凭据。
- 请求记录只保留 7 天，每次启动和每小时清理。账号权限通过登录、定时只读检测、手动刷新及每次亲邻实际开门响应校验，不发送保活命令。

备份时先停止应用，再备份 SQLite 数据目录/数据卷；`QINLIN_ENCRYPTION_KEY` 通过独立的受保护渠道备份。缺少原主密钥无法解密；错误密钥会导致启动失败。不要直接复制正在写入的单个 SQLite 文件。恢复数据后先手动刷新门禁权限再使用；历史未完成请求不会自动重新发送。

## 开发验证

```bash
go test ./...
go vet ./...
node --test web/request-id.test.cjs
shellcheck bootstrap.sh install.sh deploy/manage.sh
go test -race ./...  # 需要支持 race 的工具链及 C 编译器；Linux CI 已配置
go test ./internal/qinlin -fuzz=FuzzParse -fuzztime=10s
```

测试使用临时 SQLite 和本地 HTTP 测试服务器，覆盖协议签名、手机号加密、响应分类、重定向/超时不重试、授权、CSRF、JSON 校验、并发去重、冷却、令牌撤销、账号切换、会话密文及重启恢复。不向真实亲邻服务发送请求。

`tests/linux-smoke.py` 提供实际 systemd 安装和菜单验收：仅能在无现有亲邻安装、无 `qinlin` 系统用户的可弃置 Linux 环境以 root 运行，需要预先编译 `bin/qinlin-linux-amd64` 或设置 `QINLIN_PREBUILT_BINARY`。测试使用模拟门禁，覆盖安装、服务操作、端口检查、隐藏密码输入、凭据重置、重复安装、保留数据卸载、重装和彻底卸载，结束后清理测试安装。

协议来源和 MIT 许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，本次实际执行的检查及环境限制见 [VALIDATION.md](VALIDATION.md)。尚未验证的主要部分是实际账号兼容性、上游会话寿命和 iPhone 端体验；本服务不会据公开 Issue 猜测自动续期机制。
