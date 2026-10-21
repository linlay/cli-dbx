# 共享宿主与独立模块（实验分支）

本分支版本为 `v0.2.0-shared-host.10`。主程序内置 MySQL、SQLite，PostgreSQL、Oracle、达梦和 SQL Server 分别提供 `.dbmod`。四个原生模块直接加载到 CLI 主进程，一条命令内只初始化一次；安装目录只需要一份宿主，多个 terminal 各自独立运行。事务、整批导入仍在同一连接/事务中执行。没有常驻服务，现有命令和 driver_path 配置方式保持一致。

## Windows x64 安装

1. 解压 `dbx-shared-host_<version>_windows_amd64.zip`，保留宿主及许可证。
2. 按需解压同次构建的 `dbx-module-<engine>_<version>_windows_amd64.zip`。模块包平铺 `.dbmod` 与 `.dbmod.json` 两个文件，可放在任意专用目录。
3. 将连接 TOML 的 `driver_path` 指向模块。无需安装 Go、Python 或编译器。

Agent Platform 使用同次构建的 `builtin.dbx_<version>_windows_amd64.zip`，包含共享宿主与完整连接器资源；四种模块仍按需独立安装。

```toml
[connection]
engine = "postgres"
driver_path = "C:/dbx/drivers/dbx-driver-postgres-0.2.0-shared-host.10.dbmod"
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

普通 CLI、共享宿主与原生模块统一使用稳定的 Go 1.26 系列（最低 1.26.6，Oracle 依赖同样要求该最低版本），宿主、打包器和模块使用同一具体补丁版本，四库厂商依赖隔离在 `modules/go.mod`。共享构建通过忽略目录内的临时 workspace 统一宿主和模块依赖版本，不会让普通根构建链接厂商库。构建机需要 Go **1.26.6 或更新的 1.26 补丁版本**、Python 3；终端需能找到 `go` 和 `python`。脚本在忽略的 `dist/<实际 Go 版本>-dbmod`（例如 `dist/go1.26.6-dbmod`） 中准备私有 Go 源码树，不修改系统 Go。第三方 loader 已随源码提供，无需从 GitHub 再拉取。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-shared-host.ps1 -Test
```

产物位于 `dist/v0.2.0-shared-host.10/shared-host`，包含宿主、四种模块、各自 ZIP、摘要文件和实际安装字节数 `measurements.json`。`-Engine postgres` 可只重建一种模块，但宿主代码变更后应重建全部模块；打包器会拒绝与宿主摘要不符的旧模块。普通 `go build ./cmd/dbx` 仍构建独立进程版本；共享宿主必须使用此脚本，不能使用 `-s` 去除符号表。

打包前先用同一源码、私有 SDK、依赖图及参数进行规范宿主重建，逐字节确认一致；打包器另外检查工具链、目标平台及打包前后的宿主摘要。`-Test` 先构建真实模块，再构建一个单独的 SQLite 测试模块，验证主进程模块中的查询、中文路径与 BOM、精确整数/二进制、导入与超限回滚、事务提交/回滚、动作限制和超时退出，并检查模块 PID 等于 CLI PID、inspect schema 只初始化一次、初始化/连接准备忽略取消时仍有界退出、Ctrl+C 与 panic 脱敏。测试模块不会进入发布目录。另将四种真实模块各自复制到仅有宿主和自身模块的目录，执行加载、注册及本地断开连接测试，并确认实际建立了 TCP 连接；这不等同于真实数据库兼容性验证。

macOS Apple Silicon 的原生构建入口为 `scripts/build-shared-host.sh --test`，签名必须在生成模块前完成。旧 `.8` 在 macOS ARM64 / Go 1.26.6 完成 worker 路线的原生构建、签名、四个真实模块独立加载/TCP 初始化、SQLite fixture SQL 流程与重复打包验证，详见 [Mac 验证记录](./validation/macos-shared-host-20261014.json)。用户随后报告本机达梦可插拔手工验证可行；其他 Mac 厂商真库联调仍需对应数据库环境，自动化验证范围与用户报告在记录中分开列出。Linux、Intel Mac 和 Windows ARM64 不在此路线的验证范围内。

## Windows 真库回归

`.10` 在 2026-10-21 的历史 Windows 验证使用 Windows x64 / Go1.26.7 完成本机 PostgreSQL、SQL Server、达梦及 SQLite 的 223 项发布 ZIP 检查、26 项扩展检查和三库共 150 次真实只读初始化，全部通过，另验证了真实 Windows Ctrl+Break 可取消忽略 context 的模块调用。当时另用 Go1.22.12 测试过普通根模块；当前根模块已恢复最低 Go1.26.6，不再承诺 Go1.22 兼容，该历史结果不能替代修正后的 Windows 回归。详细范围见 [Windows 主进程模块历史验证](./validation/windows-inprocess-modules-20261021.json)。

源码在 D 盘、系统临时目录在 C 盘时也可直接运行 `-Test`；无需修改 `TEMP`。workspace 同盘保留相对路径，跨盘自动使用引用后的原生绝对路径。本机未安装 Oracle 服务，Oracle 的构建与 TCP 初始化不代表真库 SQL 验证。

2026-10-22 补充完成三库 30 项真实文件插拔检查：仅安装自身模块、实际移走模块、移走清单、恢复后读取原表及结构，以及移走一个模块时其他数据库继续查询，全部通过。检查使用隔离安装副本，每次启动新的 CLI；验证的是进程退出后的磁盘插拔，测试表已清理。详见 [Windows 模块插拔记录](./validation/windows-inprocess-modules-plug-20261022.json)。

以下是 `v0.2.0-shared-host.7` 的 Windows 历史验证记录；当前 `.10` 的原机结果见上文。测试从发布 ZIP 解压后的宿主和模块执行，包含 Platform ZIP。2026-10-14 按阶段通过 SQLite、PostgreSQL、SQL Server 和达梦的 223 项业务及安全边界检查，及 26 项扩展检查（含各库仅安装自身模块查询），以及三种外部数据库各 50 次真实只读启动（每库六个并发 worker）。补充检查覆盖宿主/工具链/API/平台不匹配、清单缺失或损坏、解码大小限制，以及 SQL Server、达梦超时和父进程终止后的回收。

本机测试需要相应数据库和当前用户 `~/Databases` 下的测试凭据。密码仅在进程内存中处理（兼容的独立驱动路线通过私有管道传递），连接 TOML 保存系统凭据库绑定的 v2 密文；脚本只创建、清理随机 `dbx_sh_` 前缀测试表，不自动重试失败写入。

```powershell
python scripts/test-driver-process-windows.py --shared-host --release-dir dist/v0.2.0-shared-host.10/shared-host --output dist/shared-host-real-tests
python scripts/test-shared-host-windows.py --release-dir dist/v0.2.0-shared-host.10/shared-host --output dist/shared-host-extra-tests
```

结果分别写入指定目录的 `results.json`，扩展测试另有 `summary.json`。脱敏范围记录见 [Windows 共享宿主测试记录](./validation/windows-shared-host-20261014.json)。当前脚本的共享路线检查模块执行期间没有驱动子进程、真实查询超时结束 CLI、终止 CLI 后可重新连接；独立驱动路线仍检查父退出后的子进程回收。当前 `.10` 的 Windows 原机回归已完成，历史记录不能替代其他版本的新验收。

## 更新和限制

放宽的是构建入口的版本限制；不能混用不同 Go 补丁版本生成的宿主和模块。模块清单绑定**宿主文件的 SHA-256**、精确 Go 版本、系统/架构、引擎、模块摘要和格式/API/协议版本。重新编译、修改或重新签名宿主后，必须重新生成模块；仅版本字符串相同不足以兼容。旧宿主与模块可整套并存。不要手改清单绕过检查。

`.dbmod` 是当前用户权限下的可信机器码，摘要用于完整性和版本配对，不是发布者身份签名，也不构成沙箱。模块使用 Go 内部 ABI 和动态代码映射，属于实验实现。宿主不在存活进程中卸载模块，命令退出后由操作系统回收；不提供常驻共享服务、跨调用事务或失败写入自动重试。

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

## 主进程运行的取消与故障边界

取消或 timeout 覆盖模块校验、装载、连接准备和 SQL 执行；正常清理最多等待 200ms，随后 CLI 返回错误并退出。这能结束忽略 context 的厂商调用，退出时不保证数据库写入已经回滚，失败写入不自动重试。模块后台 goroutine、全局状态和代码映射也在退出时回收。不要把此生命周期用于长驻服务。

普通模块调用 panic 会转成脱敏错误；不可恢复错误或其他 goroutine 的 panic 可能直接结束当前 CLI，其他 terminal 的 CLI 独立运行。模块保持磁盘可插拔，运行中的进程不卸载或切换同一引擎的模块版本。宿主更新后必须重新生成并配套分发四个模块。

## 当前 Mac 主进程模块尺寸和验收

2026-10-15 / macOS ARM64 / Go1.26.6 的 `.9` 主进程模块实测如下。安装大小指解压发布 ZIP 后的文件；模块自身仍以 gzip DBXMOD2 存储，解码大小不是 RSS。

| 文件 | 安装文件字节 | MiB | gzip 解码后的序列化字节 | 相比 .8 的字节变化 |
| --- | ---: | ---: | ---: | ---: |
| dbx | 12,071,104 | 11.51 | 不适用 | +288 |
| PostgreSQL | 1,796,423 | 1.71 | 5,908,532 | +3,548 |
| Oracle | 1,349,287 | 1.29 | 3,751,222 | +2,850 |
| 达梦 | 2,001,628 | 1.91 | 4,966,210 | +4,338 |
| SQL Server | 719,102 | 0.69 | 1,922,290 | +5,124 |

宿主、四模块及四份清单合计 17,939,330 字节（17.11 MiB），不含许可证/操作文档。相对旧版文件总量略增，主要涉及新增直接调用边界和弱调用跳板修复，不能把移除进程误解为必然减少安装文件。

验证记录见 [主进程模块 Mac 验证](./validation/macos-inprocess-modules-20261015.json)。完整原生 --test、普通根测试、四个真实模块隔离/TCP 初始化、主 PID/load-once、忽略 context 的初始化/Prepare、Ctrl+C、脱敏 panic、SQLite fixture 类型/JSON 导入/事务/超限和取消回滚、实际模块移除/恢复、loader/Python 回归、签名及重复打包通过。Windows x64 仅交叉编译；更新后的 Windows 脚本仅在 Mac 做语法检查，须原机验收。

本机已有达梦配置实际连接返回“Error 6001: 网络通信异常 / EOF”；同配置旧 `.8` worker 对照也返回相同网络 EOF。此次真库连接未通过，后续真库查询、元数据与事务未执行，不能沿用旧版用户报告冒充 `.9` 真库通过。私人配置和原始数据库输出不进 Git。模块查询等功能由 fixture 和四库 TCP 初始化分别提供限定范围的证据。

产物是在 `6081776` 基础上的未提交源码构建，宿主嵌入 base commit/time；当前已配对文件可使用，但另一机器提交后重新发布时需完整重建宿主和四模块。
