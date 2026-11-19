# 飞牛下载桥接 fnqbbridge

为 MoviePilot 提供飞牛 fnOS 原生下载器的 qBittorrent 兼容 API，附带下载管理界面。当前版本 **0.2.7**，面向 x86_64，属于个人测试项目，与飞牛官方无隶属关系。

## 功能

- 添加磁力链接和 BT 种子，查看进度、速度，暂停和继续。
- 支持飞牛目录选择器，保存目录偏好并可重新选择。
- MoviePilot 和管理界面共用原生任务删除接口，核验原生记录和引擎任务是否清理完成。
- 日志轮转、连接配置与卸载时自有数据清理。

## 安装与连接

从 Releases 下载 FPK，在飞牛应用中心手动安装或升级。升级无需先卸载。最低系统版本以 manifest 中的 1.2.0401 为准；依赖系统内部接口，其他版本需自行验证。

打开应用设置连接端口和密码。MoviePilot 下载器类型选择 qBittorrent，地址填写容器能够访问的 NAS 地址及桥接端口（默认7777），使用桥接配置的凭据。请勿将管理端直接暴露到公网。

下载器实际使用 NAS 路径；MoviePilot 的 Docker 挂载路径需对应同一份文件，不能仅凭容器内路径推断 NAS 目录。

## 删除行为与验证范围

通过当前下载引擎的所属 UID 和 Hash 查找唯一原生 ID，再调用原生 task.delete；数据库仅只读查询，不直接修改。不会回退为只删除 qB 任务。原生删除文件为异步操作，最长等待30秒核验；超时不自动重试。未知、歧义、跨用户 Hash 和 all 请求会拒绝。重复删除已不存在的任务会报错；不自动清理历史僵尸任务。

已在一台 NAS 上使用专用任务验证：保留文件删除和同时删除文件，原生记录与 qB 任务均消失，文件行为符合选择。正式 FPK 安装后的管理界面及 MoviePilot 端到端回归仍需继续验证。不能据此保证全部 fnOS 版本兼容。

## 构建

需要 Go 1.23+、Node.js/npm、飞牛 fnpack。Linux x86_64 使用 cc；其他平台需要 Zig。运行：

```bash
./build.sh
```

脚本从 npm 获取固定版本 `@trimjs/web-app@0.6.0`；该第三方 SDK 不纳入本仓库源码或本项目 MIT 授权范围。原生映射辅助程序源码位于 tools/native-map，依赖 NAS 系统 libsqlite3.so.0。

```bash
go test -race ./...
go vet ./...
node --test tests/*.test.mjs
```

原生删除实机自测会创建并删除专用任务，应先阅读 native_selftest.go，使用独立测试目录，避免在真实媒体任务上试验。

## 来源与许可

下载器底层连接部分基于 [xxxuuu/fnos-qb-proxy](https://github.com/xxxuuu/fnos-qb-proxy)，保留其 MIT 许可及版权声明，见 LICENSE。飞牛 SDK、系统组件及第三方商标归各自权利人所有。本仓库不包含飞牛系统二进制、数据库、账户配置、实际任务数据或 SSH 信息。
