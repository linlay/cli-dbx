# 共享宿主与独立模块（实验分支）

本分支版本为 `v0.2.0-shared-host.8`。主程序内置 MySQL、SQLite，PostgreSQL、Oracle、达梦和 SQL Server 分别提供 `.dbmod`。每次调用启动同一个 `dbx` 的短生命周期 worker，在 worker 中加载所选模块；安装目录只需要一份宿主。这里的“共享”指复用宿主文件和 Go runtime，不是跨调用常驻的服务。事务、整批导入仍在单个 worker 中执行。

## Windows x64 安装

1. 解压 `dbx-shared-host_<version>_windows_amd64.zip`，保留宿主及许可证。
2. 按需解压同次构建的 `dbx-module-<engine>_<version>_windows_amd64.zip`。模块包平铺 `.dbmod` 与 `.dbmod.json` 两个文件，可放在任意专用目录。
3. 将连接 TOML 的 `driver_path` 指向模块。无需安装 Go、Python 或编译器。

Agent Platform 使用同次构建的 `builtin.dbx_<version>_windows_amd64.zip`，包含共享宿主与完整连接器资源；四种模块仍按需独立安装。

```toml
[connection]
engine = "postgres"
driver_path = "C:/dbx/drivers/dbx-driver-postgres-0.2.0-shared-host.8.dbmod"
host = "127.0.0.1"
port = 5432
user = "app"
database = "app"
password = "dbx-aes-gcm:v2:..." # 替换为本机 dbx secret encrypt 的实际输出
sslmode = "disable"
allow_actions = ["query"]
```

```powershell
.\dbx.exe conn test local-pg --config C:/dbx/local-pg.toml
.\dbx.exe query local-pg 'select 1' --config C:/dbx/local-pg.toml
```

相对路径以 TOML 所在目录为准，支持 `~`。模块必须显式指定；默认目录仍只查找无版本独立驱动程序。原有独立进程协议 v1 和显式 ODBC 适配程序继续可用，厂商 ODBC 客户端另行安装。

## 构建和验证

构建支持稳定的 Go 1.26 系列（最低 1.26.6，由 Oracle 依赖决定），宿主、打包器和模块使用同一具体补丁版本，四库厂商依赖隔离在 `modules/go.mod`。共享构建通过忽略目录内的临时 workspace 统一宿主和模块依赖版本，不会让普通根构建链接厂商库。构建机需要 Go **1.26.6 或更新的 1.26 补丁版本**、Python 3；终端需能找到 `go` 和 `python`。脚本在忽略的 `dist/<实际 Go 版本>-dbmod`（例如 `dist/go1.26.6-dbmod`） 中准备私有 Go 源码树，不修改系统 Go。第三方 loader 已随源码提供，无需从 GitHub 再拉取。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-shared-host.ps1 -Test
```

产物位于 `dist/v0.2.0-shared-host.8/shared-host`，包含宿主、四种模块、各自 ZIP、摘要文件和实际安装字节数 `measurements.json`。`-Engine postgres` 可只重建一种模块，但宿主代码变更后应重建全部模块；打包器会拒绝与宿主摘要不符的旧模块。普通 `go build ./cmd/dbx` 仍构建独立进程版本；共享宿主必须使用此脚本，不能使用 `-s` 去除符号表。

打包前先用同一源码、私有 SDK、依赖图及参数进行规范宿主重建，逐字节确认一致；打包器另外检查工具链、目标平台及打包前后的宿主摘要。`-Test` 先构建真实模块，再构建一个单独的 SQLite 测试模块，验证真实 worker 中的查询、中文路径与 BOM、精确整数/二进制、导入与超限回滚、事务提交/回滚、动作限制和超时回收。测试模块不会进入发布目录。另将四种真实模块各自复制到仅有宿主和自身模块的目录，执行加载、注册及本地断开连接测试，并确认实际建立了 TCP 连接；这不等同于真实数据库兼容性验证。

macOS Apple Silicon 的原生构建入口为 `scripts/build-shared-host.sh --test`，签名必须在生成模块前完成。本次在 macOS ARM64 / Go 1.26.6 完成原生构建、签名、四个真实模块独立加载/TCP 初始化、SQLite fixture SQL 流程与重复打包验证，详见 [Mac 验证记录](./validation/macos-shared-host-20261014.json)。用户随后报告本机达梦可插拔手工验证可行；其他 Mac 厂商真库联调仍需对应数据库环境，自动化验证范围与用户报告在记录中分开列出。Linux、Intel Mac 和 Windows ARM64 不在此路线的验证范围内。

## Windows 真库回归

以下是 `v0.2.0-shared-host.7` 的 Windows 历史验证记录；新版 Windows 仍需原机回归。测试从发布 ZIP 解压后的宿主和模块执行，包含 Platform ZIP。2026-10-14 按阶段通过 SQLite、PostgreSQL、SQL Server 和达梦的 223 项业务及安全边界检查，及 26 项扩展检查（含各库仅安装自身模块查询），以及三种外部数据库各 50 次真实只读启动（每库六个并发 worker）。补充检查覆盖宿主/工具链/API/平台不匹配、清单缺失或损坏、解码大小限制，以及 SQL Server、达梦超时和父进程终止后的回收。

本机测试需要相应数据库和当前用户 `~/Databases` 下的测试凭据。密码只在私有管道和内存中处理，连接 TOML 保存系统凭据库绑定的 v2 密文；脚本只创建、清理随机 `dbx_sh_` 前缀测试表，不自动重试失败写入。

```powershell
python scripts/test-driver-process-windows.py --shared-host --release-dir dist/v0.2.0-shared-host.8/shared-host --output dist/shared-host-real-tests
python scripts/test-shared-host-windows.py --release-dir dist/v0.2.0-shared-host.8/shared-host --output dist/shared-host-extra-tests
```

结果分别写入指定目录的 `results.json`，扩展测试另有 `summary.json`。脱敏范围记录见 [Windows 共享宿主测试记录](./validation/windows-shared-host-20261014.json)。完整回归包含三种外部引擎的真实查询超时、父进程终止回收及恢复连接检查。

## 更新和限制

放宽的是构建入口的版本限制；不能混用不同 Go 补丁版本生成的宿主和模块。模块清单绑定**宿主文件的 SHA-256**、精确 Go 版本、系统/架构、引擎、模块摘要和格式/API/协议版本。重新编译、修改或重新签名宿主后，必须重新生成模块；仅版本字符串相同不足以兼容。旧宿主与模块可整套并存。不要手改清单绕过检查。

`.dbmod` 是当前用户权限下的可信机器码，摘要用于完整性和版本配对，不是发布者身份签名，也不构成沙箱。模块使用 Go 内部 ABI 和动态代码映射，属于实验实现。宿主不在存活进程中卸载模块，每次调用后由操作系统回收；不提供常驻共享服务、跨调用事务或失败写入自动重试。

Windows x64 `v0.2.0-shared-host.7` 历史实测宿主 **12.42 MiB**。四个模块独立安装大小如下（模块文件，不含几百字节的清单）：

| 模块 | MiB |
| --- | ---: |
| PostgreSQL | 2.22 |
| Oracle | 1.77 |
| 达梦 | 2.30 |
| SQL Server | 0.80 |

一份宿主加四个模块及清单约 **19.51 MiB**，主程序和四个模块的下载 ZIP 合计约 **11.88 MiB**；Platform ZIP 是另一种主程序分发方式，不重复计入总量。相比本次第一阶段 `.4` 的同口径安装量约 23.63 MiB，减少约 **17.5%**。按需安装时只累加所选模块；许可证、操作文档和 ZIP 元数据另占少量空间，精确字节以 `measurements.json` 与实际归档为准。模块在磁盘上已压缩，运行时解码并映射，所以这些数字不代表内存占用。

构建固定文件次序、时间和权限，并要求四个模块分别在另一进程重新打包后字节完全一致；这是同一源码、SDK、宿主与依赖图的复现检查，不承诺跨 Go 版本或平台兼容。Windows 已完成 PostgreSQL、SQL Server、达梦及内置 SQLite 的真实 ZIP 回归。Oracle 仅验证独立加载、GC/TCP 连接初始化；Oracle、MySQL 真库、ODBC 真库及其他 Mac 厂商真库仍需对应环境验收；本机达梦手工确认不代表全量 SQL/压力回归。

## DBXMOD2 格式

格式版本 2 使用前缀字典、变长整数、偏移差值、零 BSS、PC 函数名引用及 ARM64 NOP 游程编码；安装文件继续使用 gzip 压缩。编码保留指令名，运行时不携带 x86 解码器。每个真实模块在打包时都做完整图往返比对，解码限制输入、字典、字段与累计分配量，拒绝截断及尾随数据。格式 1 的旧宿主与模块可整套保留，不能与格式 2 交叉配对。
