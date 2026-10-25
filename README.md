# ReClaude 代理实验与逆向记录

本仓库包含 Go 编写的本地 HTTPS 拦截代理、模拟/转发后端、带 SK 鉴权和 token 池的网关，以及 Windows 启动辅助工具。

现有代码来自 ReClaude v1.0.7 相关实验，**不等同于当前 ReClaude 客户端或服务端的完整实现**。提交说明中的历史运行结果也不代表当前客户端兼容性已经验证。

## 分析记录

- [2026-10-25：ReClaude v1.5.1 架构、隧道协议与登录机制完整报告](docs/research/2026-10-25-reclaude-v1.5.1-routing.md)
- [版本、函数地址与关键代码证据索引](docs/research/reclaude-v1.5.1-evidence.json)
- [登录流程与 Desktop OAuth 静态证据](docs/research/reclaude-v1.5.1-login-evidence.json)
- [Chat / Code 凭证关系的脱敏离线核验](docs/research/reclaude-v1.5.1-offline-login-audit.json)

本次记录基于安装包、官方 IDA MCP 静态分析、已有日志和本机目标档案的离线凭证比较。已确认该档案的 Chat sessionKey、Desktop Code 缓存 access token 和 CLI access token 均等于同一个 ReClaude SK；公开结果不含凭证原文。未启动客户端、安装证书、修改其运行配置或发送新的登录、账户及推理请求。服务端如何使用真实上游账号、是否按账号部署容器仍未确认。

## 代码入口

| 路径 | 用途 |
|---|---|
| `cmd/proxy`、`internal/proxy` | 本地 HTTPS 代理、路由及模拟接口 |
| `cmd/backend`、`internal/backend` | 模拟响应或注入凭证后转发 |
| `cmd/gateway`、`internal/gateway` | SK 鉴权、OAuth token 池、转发 |
| `cmd/launcher` | 关闭并重新启动 Desktop，设置代理环境 |
| `cmd/inject` | 写入实验用的模拟凭证 |
| `cmd/skctl` | 管理网关 SK |
| `start.ps1`、`install-ca.ps1` | 编译、启动及证书安装辅助 |

## 已确认的使用限制

这些是现有代码的静态发现，本次文档提交没有修复程序行为：

- `start.ps1` 没有传入 `-mode forward`，后端默认返回模拟响应；输入 API Key 不会自动切换模式。
- Backend 保存了 `-anthropic-url`，但实际转发使用 `X-Original-Host` 构造目标，该自定义地址没有生效。
- Gateway 的 `/stats` 未鉴权，并返回包含完整 SK 的列表。默认仅监听本机，但访问控制仍需完善。
- `config/gateway.json` 已跟踪一个完整 OAuth token 字符串，`data/sk_keys.json` 也存放 SK。未验证这些值是否有效；若曾用于真实服务，应撤销或轮换，不能仅靠后续删除文件处理历史暴露。
- 启动脚本涉及 CA 安装，launcher 会结束已有 Claude 进程，inject 会写凭证文件。阅读分析不需要运行这些入口。

当前实现对 `claude.ai` 使用透传；本次核对的 **ReClaude v1.5.1 内置拦截表包含 `claude.ai`**，这是两者的重要区别。
