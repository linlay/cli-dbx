# AGENTS.md

## 1. 项目概览

`dbx` 是一个带安全边界的 database CLI，不是任意 SQL 壳。它的目标是让人类和智能体都能在统一命令面下访问数据库，同时把高风险动作限制在 DBX 自身的控制面里。

当前核心能力：

- 连接管理：`conn list / show / test`
- 结构查看：`inspect schema / table / connection`
- 显式 SQL 动作：`query / update / schema / admin`
- 文件导入导出：`import` / `export`
- 批处理事务：`tx`

设计重点：

- 显式动作优先，减少大模型误用
- DBX 层能力白名单，独立于数据库账号权限
- 单语句默认、事务批处理明确、输出结构化

## 2. 技术栈

- 语言：Go
- CLI：标准库 `flag`
- 数据库访问：`database/sql`
- 驱动：
  - 主程序内置 MySQL：`go-sql-driver/mysql`，SQLite：`modernc.org/sqlite`
  - 独立进程：PostgreSQL、Oracle、达梦、SQL Server，各有 `drivers/<engine>/go.mod`
  - 构建支持 Go 1.26 系列，Oracle 依赖要求最低 1.26.6。厂商依赖隔离在 `modules/go.mod`；共享构建使用按实际版本区分的私有 SDK `dist/<go-version>-dbmod` 与临时 workspace。宿主和模块必须使用同一具体补丁版本及依赖图，清单和 DBXMOD2 解码仍校验精确 Go 版本与宿主摘要
- 配置格式：TOML
- 构建与测试：`go build`、`go test`
- 发布：仓库内 `scripts/release/build.sh`

## 3. 架构设计

模块分层大致如下：

- `cmd/dbx`
  CLI 入口，仅负责进程启动和顶层错误退出。
- `internal/cli`
  命令分发、参数解析、帮助信息、错误 envelope。
- `internal/config`
  单连接配置文件加载、路径归一化、密码来源解析。
- `internal/textfile`
  标准库 UTF-8 文件读取，移除文件开头至多一个 BOM；正文中的 BOM 保持原样。
- `internal/secret`
  v1/v2 密文处理、系统凭据库适配和运行时密钥解析。
- `internal/conn`
  把配置解析成可执行的连接规格 `Spec`。
- `internal/action`
  动作模型：`query / update / schema / admin`。
- `internal/sqlclass` / `internal/sqlanalyzer`
  SQL 分类、单语句校验、危险写入检测。
- `internal/sqllex`
  共享引号、注释和括号层级识别；方言语法不确定时拒绝执行。
- `internal/db`
  路由 MySQL/SQLite 到内置执行器，四种外部引擎到进程客户端。
- `internal/dbcore`
  共享 SQL 执行、元数据、导入与整批事务，不导入厂商驱动。
- `internal/driverprocess`
  驱动包校验、私有管道协议、进程取消与回收。
- `drivers/postgres`、`drivers/oracle`、`drivers/dm`、`drivers/sqlserver`
  独立模块及可执行入口，各自注册对应数据库驱动。
- `drivers/odbc`
  独立 ODBC 模块，按构建时固定的 oracle/dm/sqlserver 方言分别生成适配程序；只有此模块链接 Go ODBC 适配层。
- `internal/output`
  结构化输出、摘要、表格输出。

核心调用链：

1. CLI 解析命令与参数
2. 连接配置解析为 `conn.Spec`
3. SQL 被分类为 statement class，再映射到 action
4. DBX 先做 `allow_actions` 和安全策略校验
5. 校验通过后才进入数据库执行层；外部引擎此时才校验并启动独立驱动

## 4. 目录结构

- `cmd/dbx`
  可执行程序入口
- `internal/`
  核心实现
- `docs/`
  面向用户的补充文档
- `testdata/`
  配置示例
- `scripts/release/`
  打包与发布脚本

文档职责固定为：

- `README.md`
  操作手册、快速开始、简单验证
- `docs/beginner-guide.md`
  新手第一次上手
- `AGENTS.md`
  设计、重要功能、开发约定、维护流程

开发计划、历史手工测试资料、旧架构说明和下载目录清单仅在本地保留，按 `.gitignore` 排除，不作为另一台机器构建或测试的前提。跨机器协作所需的设计结论维护在本文件，当前用法维护在操作文档，保留最新验证记录和第三方许可证。不要强制添加被忽略的过程资料来扩大 PR。

## 5. 数据结构

### 连接配置

根结构是单连接文件：

```toml
[connection]
engine = "mysql|sqlite|postgres|oracle|dm|sqlserver"
allow_actions = ["query", "update", "schema", "admin"]
```

`allow_actions` 是 DBX 层保护，不依赖数据库账号本身的授权能力。

### 密码加密

- `dbx-aes-gcm:v2:<key-id>:...` 是默认格式。数据库密码保存在配置密文中，每条密文的随机 AES-256-GCM 密钥保存在当前用户的系统凭据库。
- 系统凭据库固定使用 macOS Keychain、Windows Credential Manager 或 Linux Secret Service，不允许降级到环境变量、命令或密钥文件。
- v2 运行时静默解密；密钥缺失、凭据库不可用或密文损坏都必须失败关闭。
- `dbx-aes-gcm:v1:...` 仅为兼容保留，读取时仍由 passphrase provider 获取旧 master passphrase。
- CLI 只提供 `dbx secret encrypt`，不得增加 decrypt、export、show-key 等明文或密钥输出入口。
- `dbx secret encrypt` 接受零个或一个位置参数：零参数隐藏回显地读取密码，一个参数直接加密且不得读取 stdin。位置参数必须原样处理，不得 trim 或写入 DBX 输出与错误。
- 位置参数模式只用于接受一次明文暴露的迁移场景；明文会进入调用智能体、Shell history、命令审计或进程参数，不能宣称该模式对模型保密。
- v2 的保护目标是避免配置文件直接泄露明文，不抵御同一操作系统用户主动读取系统凭据库或进程内存。

### 动作模型

- `query`
  只读 SQL
- `update`
  DML，如 `insert / update / delete / merge`
- `schema`
  DDL，如 `create / alter / drop / truncate / rename`
- `admin`
  管理动作，如 `grant / revoke / set / vacuum`

### 事务计划

`tx` 读取 JSON 计划文件：

```json
{
  "steps": [
    {"action":"query","sql":"select id from users where id = 1"},
    {"action":"update","sql":"update users set active = 1 where id = 1","max_rows_affected":1}
  ]
}
```

`tx` v1 只允许 `query` 和 `update` 步骤。

计划读取使用 `DisallowUnknownFields` 并检查第二次解码必须返回 `io.EOF`，在数据库执行前拒绝第二个 JSON 文档和尾随垃圾。JSON 导入保留 `UseNumber`、精确数字绑定和 EOF 检查；不得为了迁移文件编码修复而退回 `json.Unmarshal` 到浮点数字。

### 文件编码与 SQLite 路径

配置、SQL、事务 JSON、CSV 和 JSON 导入使用 `internal/textfile` 读取，支持带或不带 UTF-8 BOM，不自动转换 UTF-16。Windows 测试同时隔离 `HOME` 和 `USERPROFILE`，TOML 测试路径使用安全引用。

SQLite 普通文件路径编码为 `file:` URI 时，只编码文件名部分并保留原始查询参数。显式 URI 不重复编码；保留 `mode=ro`、内存库、共享缓存与 `_pragma` 行为。打包器的 `--executable` 以明确的暂存区相对普通文件路径指定可执行权限，不依赖构建主机的文件模式；无此参数的既有行为保持不变。

### 输出 Envelope

对外返回统一 envelope，关键字段包括：

- `kind`
- `action`
- `class`
- `summary`
- `data`
- `next`
- `warnings`

`action` 是稳定的外部语义；`statement_class` 仍然保留作兼容信息。

## 6. API 定义

`dbx` 的主要外部接口是 CLI，而不是 HTTP API。

核心命令面：

- `dbx conn ...`
- `dbx inspect ...`
- `dbx query <conn> '<sql>'`
- `dbx update <conn> '<sql>'`
- `dbx schema <conn> '<sql>'`
- `dbx admin <conn> '<sql>'`
- `dbx tx <conn> --plan <path.json>`
- `dbx import file ...`
- `dbx export table ...`
- `dbx secret encrypt`

接口约定：

- 默认只允许单语句 SQL
- `query/update/schema/admin` 要求 SQL 类型和命令动作匹配
- `tx` 为单连接、单次调用、单事务

错误语义重点：

- `action_blocked`
  当前连接未放行该动作
- `action_mismatch`
  命令动作和 SQL 类型不一致
- `multiple_statements_blocked`
  一次传入了多语句
- `missing_where`
  危险写入缺少 `WHERE`
- `tx_unsupported_action`
  事务步骤使用了 `schema` 或 `admin`

## 7. 开发要点

- 新增数据库能力时，先明确它属于哪个 action，再补 CLI 和帮助文案。
- 不要把设计解释重新堆进 `README.md`；设计说明统一维护在 `AGENTS.md`。
- `README.md` 只保留操作说明、配置示例、简单测试和排查。
- `docs/beginner-guide.md` 只讲新手视角，不承载架构事实。
- `allow_actions` 是 DBX 层最重要的能力边界之一，任何变更都必须补测试。
- `tx` 的边界优先保持简单和可验证，不要直接扩展到跨进程会话事务。
- 对外词汇优先使用 `query / update / schema / admin / tx`，保持动作边界清晰。
- 用户可见能力变更需要同步更新：
  - CLI help
  - `README.md`
  - 相关测试

## 8. 开发流程

本地开发常用命令：

```bash
go build -o ./dbx ./cmd/dbx
go test ./...
(cd drivers/postgres && go test ./...)
(cd drivers/oracle && go test ./...)
(cd drivers/dm && go test ./...)
(cd drivers/sqlserver && go test ./...)
```

修改命令或输出时，至少检查：

```bash
./dbx --help
./dbx query --help
./dbx tx --help
```

发布流程当前由维护者手工执行：

1. 将根目录 Git 跟踪的 `VERSION` 更新为计划发布的版本（格式如 `v0.1.0`），并与代码和文档一并提交
2. 运行 `go test ./...`
3. 创建并推送与 `VERSION` 相同的 tag，例如 `v0.1.0`
4. 执行 `scripts/release/build.sh` 构建核心包，另执行 `scripts/build-drivers.sh` 构建四种独立驱动包；本机验证可用 `--host --engine postgres`
5. 校验 `dist/<version>/..._checksums.txt` 与各独立驱动包的 `.sha256`
6. 在 GitHub Release 上传产物

如果文档分工变化：

- 设计和约定改 `AGENTS.md`
- 用户操作和快速验证改 `README.md`
- 新手入门体验改 `docs/beginner-guide.md`

## 9. 已知约束与注意事项

- `dbx` 不是数据库权限系统的替代品；它是在数据库账号之外再加一层 DBX 侧保护。
- `allow_actions` 只能收紧 DBX 的能力边界，不能放大数据库账号本身没有的权限。
- `tx` 不支持跨连接事务，也不支持跨多次 CLI 调用的事务会话。
- `tx` 不支持 `schema` 或 `admin`，主要是为了保持回滚语义可预期。
- 生产态连接默认更保守；带 `prod` 特征的连接会阻止高风险动作配置。
- v2 密文绑定创建它的机器和操作系统用户，复制配置到其他机器后必须重新加密。
- Linux v2 依赖可用且已解锁的 Secret Service 登录集合；不可用时不回退到其他密码来源。


## Platform 连接器发布包

项目位于 `agent-platform-connectors/dbx`，`connector/` 是 connector.json 模板、cli.json、skills 与全部资源的唯一源码。`VERSION` 同时控制 CLI 和连接器发布版本；模板不维护 version，构建时生成。任何 CLI、清单或技能改动均需发布新版本。

现有 Shell/PowerShell 发布脚本额外生成 `dist/<version>/builtin.dbx_<version>_<os>_<arch>.zip`，内容位于 `runtime/connectors/builtin.dbx/`，包含声明、完整 skills 和目标平台 `bin/dbx`。核心连接器只随主程序提供 MySQL、SQLite，四种外部驱动单独分发；不得向 `connector/` 添加驱动二进制。Platform 按完整包摘要消费，不修改包内内容。原 CLI 独立发布包继续提供。包生成器检查 VERSION 满足 cli.json.minVersion（包含 SemVer 预发布版本比较）。


## 独立驱动进程契约

- `connection.driver_path` 精确指定驱动可执行文件，支持 `~`，相对路径以连接 TOML 所在目录为准。包内平铺 `dbx-driver-<engine>-<version-without-v>[.exe]` 与完整程序文件名加 `.json` 的清单，多个版本互不覆盖。未指定路径时按 `driver_dir` > `DBX_DRIVER_DIR` > `~/.config/dbx/drivers` 选定根目录，只找平铺的无版本 `dbx-driver-<engine>[.exe]`。不搜索版本、不从 PATH 搜索、不自动下载或回退。旧的引擎子目录布局需重新安装。
- `<可执行文件名>.json` 必须包含 `protocol_version`（当前 1）、`engine`、`version`、`os`、`arch`、`executable` 和小写十六进制 `sha256`。清单内程序名必须与选定文件名完全一致，程序和清单必须为普通文件，拒绝目录逃逸和符号链接；启动前检查引擎、平台、协议及摘要。驱动包是当前用户权限下的可信代码，不是安全沙箱。
- 每次数据库操作使用一个进程；JSON 帧有长度限制，stdout 仅传协议，数据行分帧发送。标量显式保留 NULL、整数、浮点、字符串、二进制和时间，避免 JSON 浮点精度或时间格式变化。
- 凭据和 DSN 仅走私有 stdin，不能加入子进程参数、日志或直接转发 stderr。安全策略、密钥解析、审计和对外 envelope 仍在 DBX 主程序中。
- 事务全部步骤和整批导入必须在同一驱动进程内完成；影响行数在 commit 前校验，无法取得行数时不能假定为零。不要引入跨 CLI 调用的事务会话。
- 父进程取消或超时终止并回收子进程，子进程同时使用操作超时及管道关闭监听。写操作不自动重试；提交期间失联不能承诺已回滚，须提示查询确认结果。
- `cmd/dbx` 不得传递链接 pgx、Oracle、DM 或 SQL Server 的厂商客户端库。本分支 `modules/go.mod` 隔离厂商依赖；宿主只引用 loader，厂商代码独立编译成对象模块。原四个嵌套进程模块分别运行测试；根 `go test ./...` 不覆盖嵌套模块。
- 打包工具 `scripts/release/package-driver/` 仅依赖标准库。Shell/PowerShell 入口生成独立归档及 `.sha256`，固定目录、时间、权限和次序。独立驱动使用当前工具链，Oracle 要求 Go 1.26.6+，不运行全局 `go env -w`。
- 自动测试覆盖子进程执行、类型保真、清单/摘要/协议错误、取消回收、失败与超限回滚、策略拒绝时不启动驱动；真实厂商数据库未连接时，不能用编译或本地替身测试宣称数据库兼容性已验证。

用户安装与操作说明见 [docs/driver-process.md](./docs/driver-process.md)。

- `drivers/dm/fork` 固定保留 DM v1.8.22 源码及版权，修补 DECIMAL 文本绑定默认 64 位 `big.Float` 丢失小数的问题。只对 DECIMAL 按输入长度分配足够精度，不把 JSON 导入退回 float64；该 fork 需单独运行 `go test ./...`。

## 共享宿主对象模块契约（实验分支）

- `internal/dbmodule` 校验格式/API/协议版本、引擎、平台、Go 版本、模块大小与摘要以及宿主文件 SHA-256。清单和模块为普通文件，同次构建配套，拒绝错误绑定；父进程与 worker 均校验。
- `driver_path` 显式指向 `.dbmod` 时才启用对象模块。默认发现规则继续只查独立可执行驱动；不自动切换实现。ODBC 仍使用独立适配进程。
- `dbmod` 标签启用 loader，发布宿主同时使用 `dbmod_runtime` 精简符号读取和指令解码依赖；禁止 `-s` 剥离宿主符号表。开发机私有 Go 树只增加 `cmd/objfile`，不修改安装的 Go。供应 loader 固定来源和本地修补记录见 `third_party/goloader/README.dbx.md`。
- 每次调用执行同一宿主的隐藏 worker，单次仅加载一种引擎。凭据走原私有 stdin 协议，策略先行、事务/整批导入同 worker、取消后回收、不自动重试写入等契约继续适用。禁止在活跃进程中卸载模块或增加常驻跨调用事务。
- `modules/<engine>` 只保留厂商初始化及可选的 `Prepare(*conn.Spec)`，共享 SQL 执行由宿主提供。构建器只保留宿主缺少的对象代码与重定位数据，压缩后的对象文件就是安装的模块，不是完整驱动程序的下载压缩包。
- Windows x64 的 `.7` 已完成原生构建、SQL fixture 与 SQLite/PG/SQLServer/DM 真库 ZIP 回归；`.8` 在 macOS ARM64 / Go 1.26.6 完成原生构建、四个真实模块独立加载/TCP 初始化、SQLite fixture SQL 流程与重复打包。用户另报告本机达梦可插拔手工验证可行，具体证据范围见下文。其他 Mac 厂商真库及新版 Windows 仍需对应环境验收。Mac JIT 映射依赖 CGO，宿主签名须先于模块配对。Go 内部 ABI、动态映射和对象格式仍是实验风险，不能宣称通用插件 ABI 或跨工具链/平台复现。
- 本分支构建只使用 `scripts/build-shared-host.ps1` 或 `.sh`，与原进程发布入口分开。普通构建保留进程模式兼容。真实数据库兼容性必须由对应数据库环境验证。

对象格式版本 2 使用 `internal/dbmodformat` 的 DBXMOD2，保留运行时所需 `OpName`，外层 gzip。禁止丢失代码、GC/PC、类型、初始化和重定位数据；打包器逐个比较实际模块的完整图往返。离线预链接只处理与加载基址无关的重定位，不移动代码或 PC/GC 偏移，不宣称缩减运行内存。构建先验证规范宿主重建，再检查打包期间摘要未变；`-Test` 重打包四个模块并要求字节相同。Apple Silicon 加载期间必须固定 OS 线程以匹配线程 JIT 写保护。

共享 loader 回归包括比较指令宽度、零大小变量不得覆盖类型描述符，以及 Mac ARM64 接口方法指针先去除 chained-fixup 标记再重定位。私有 SDK 支持只读模块缓存中的官方工具链；不得修改硬链接指向的原始 SDK 文件。动态类型表先校验再按运行时类型字符串排序；父管道关闭不得受厂商阻塞调用拖延。扩展脚本每库执行 50 次真实只读初始化，失败即停止调度，不自动重试。

### 方案目的与公共代码归属

当前开发分支为 `codex/dbx-shared-host`，版本为 `v0.2.0-shared-host.8`。用户的目标是一份约 12 MiB 的主程序，加上按需安装的四种数据库增量文件，避免每个独立驱动程序重复携带约 5 MiB 的基础代码。不要把此前的独立可执行驱动尺寸当成当前对象模块尺寸。

共享宿主就是磁盘上的 `dbx`（Windows 为 `dbx.exe`），没有另一个名为 shared-host 的运行模块。它携带 Go runtime、CLI、配置/凭据/策略、私有管道协议、共享 SQL 执行层、内置 MySQL/SQLite，以及对象模块加载器。每个 `.dbmod` 保留厂商驱动及其依赖中宿主无法提供的代码、数据、类型、初始化和重定位信息；仍可能包含不能安全复用的公共符号副本，不能承诺每字节都与静态内嵌增量相同。

这是借鉴“宿主提供公共能力、扩展按需加载”的布局，使用供应的 Go 对象装载器实现。它不是 Go 标准 `plugin`、通用 `.so`/DLL ABI、解释器或 ODBC 客户端，也不是四个完整独立 Go 程序换了后缀。Native 达梦模块不依赖 unixODBC；旧 ODBC 程序的动态库错误不能据此判断新模块依赖。

“共享”主要减少重复的磁盘文件：多个 terminal 的 CLI 仍有各自的主进程和 worker、堆、GC 与数据库连接，不能据磁盘缩减推断进程内存同比缩减。一个 terminal 使用达梦不妨碍另一个使用 PostgreSQL；无需卸载前者。单个 CLI 命令/事务仍只面向一个连接，不提供跨库事务。

### 加载与调用过程

1. 用户继续使用现有 `conn`、`query`、`inspect`、`update`、`tx` 等命令，不新增面向用户的插件命令。
2. 配置解析得到 `engine` 和 `driver_path`。`driver` 只接受既有的 `native`/`odbc` 选择，不要写 `driver = "module"`；native 路径以 `.dbmod` 结尾时选择模块路线。
3. 主进程先做动作/SQL/表策略与凭据解析，验证模块及清单与当前宿主配对，再启动同一个宿主文件的内部 `--dbx-module-worker <path> <engine>` 入口。该入口不供用户手工调用，也不把凭据放进参数。
4. worker 再次验证并取得已检查摘要的不可变模块字节，解压/解码、动态映射与重定位、注册类型/GC 信息并运行初始化，然后使用原私有管道协议调用 `internal/dbcore`。不得校验后重新打开未验证的模块字节。
5. 整批导入、事务步骤在同一个 worker 内完成；操作结束或取消后回收 worker。下一条 CLI 命令重新加载所选模块，不存在在活跃进程中安全热卸载的承诺。

### 代码入口与分层

| 位置 | 职责 |
| --- | --- |
| `internal/driverprocess` | 原协议客户端、模块/独立进程路由、启动和取消回收 |
| `cmd/dbx/worker.go` | 宿主自身的隐藏模块 worker；`worker_disabled.go` 保留普通构建 |
| `internal/dbmodule` | 清单/宿主绑定验证、模块字节快照、装载及模块入口解析 |
| `internal/dbmodformat` | 有界 DBXMOD2 编解码；平台与精确 Go 版本校验 |
| `cmd/dbmod-pack` | 基于规范宿主构建厂商对象图、去重、预链接、压缩和生成清单 |
| `modules/go.mod`、`modules/<engine>/module.go` | 四库厂商依赖与注册入口，避免普通根构建链接厂商库 |
| `modules/postgres/fixture.go` | 仅 `dbmod_testfixture` 使用的 SQLite 替身，不发布 |
| `third_party/goloader` | 固定来源的 Go 内部对象装载器及本地 ABI/重定位修补 |
| `scripts/prepare-dbmod-toolchain.py`、`prepare-shared-workspace.py` | 私有 SDK 与统一依赖 workspace |
| `scripts/build-shared-host.sh`、`.ps1` | 原生构建、规范宿主检查、模块构建、测试和打包 |
| `scripts/package-shared-host.py` | 宿主/模块独立 ZIP、摘要、安装及解码尺寸记录 |

### 安装布局与可插拔验收

以 `.8` 的达梦为例，安装目录可只包含：

```text
dbx                            # Windows: dbx.exe
dbx-driver-dm-0.2.0-shared-host.8.dbmod
dbx-driver-dm-0.2.0-shared-host.8.dbmod.json
dameng-test.toml                # 用户的私有配置，不进 Git/发布包
```

`.dbmod.json` 必须放在对应模块旁边，`driver_path` 指向 `.dbmod`，不是 JSON 或 ZIP。同目录配置可写：

```toml
[connection]
engine = "dm"
driver = "native"
driver_path = "./dbx-driver-dm-0.2.0-shared-host.8.dbmod"
# 其余地址、账号、密码及 allow_actions 使用用户已有的私有配置
```

路径相对于 TOML 所在目录解析，与终端当前目录无关。模块也可以放在单独目录并使用绝对路径。主程序及模块包解压后即可使用，终端用户不需要 Go、Python、SDK 或编译器。添加 PostgreSQL/Oracle/SQL Server 只需同次构建的对应 `.dbmod` 和 `.json`，以及相应连接配置；四库不用一起安装。

可在独立测试目录验证，使用 `./dbx`（Windows 使用 `.\dbx.exe`），避免误调用 PATH 中旧版：

```bash
./dbx version
./dbx conn test dameng-test --config ./dameng-test.toml
module=dbx-driver-dm-0.2.0-shared-host.8.dbmod
mv "$module" "$module.disabled"
./dbx conn test dameng-test --config ./dameng-test.toml  # 预期模块缺失
./dbx version                                         # 宿主仍能启动
mv "$module.disabled" "$module"
./dbx conn test dameng-test --config ./dameng-test.toml  # 恢复连接
./dbx query dameng-test 'SELECT 1 AS N FROM DUAL' --config ./dameng-test.toml
```

这模拟下一次命令中的插拔，不测试运行中移除文件。实际分发和移除应以模块与清单两个文件为单位。测试目录/凭据只是本机临时资料，不应写成另一台机器上的固定前提。

### 构建、兼容与升级规则

- 支持稳定 Go 1.26 系列、最低 1.26.6；该最低值来自 Oracle 依赖。拒绝其他 minor、RC/devel/custom 工具链。构建入口设 `GOTOOLCHAIN=local`，不自动换到另一个补丁版本。
- 按实际 `GOVERSION` 准备 `dist/<go-version>-dbmod`，缓存身份同时绑定原 SDK 路径与完整版本；不同补丁不得混用缓存。只读模块缓存中的 SDK 可通过硬链接复制，但必须 unlink 后重写私有 `go.mod`/标记文件，绝不能截断或 chmod 原始硬链接文件。
- 临时 `dist/shared-host.go.work` 合并根模块和 `modules` 的依赖图。普通 `go build ./cmd/dbx` 不带加载器能力；必须使用共享宿主专用脚本生成模块宿主。宿主使用 `dbmod,dbmod_runtime`，打包器使用 `dbmod`；不能给宿主加 `-s`。
- macOS Apple Silicon 在原机执行 `bash scripts/build-shared-host.sh --test`；Windows x64 执行 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-shared-host.ps1 -Test`。Mac 开启 CGO/JIT 签名，Windows 使用 CGO=0。Linux、Intel Mac、Windows ARM64 未作为本方案支持目标验收。
- 签名先于规范宿主校验及模块打包。构建顺序是宿主 → Mac 签名 → 同参数规范重建并比对字节 → 打包器 → 四个模块 → 测试 → 独立组件 ZIP 和 Platform ZIP。
- 产物位于 `dist/<VERSION>/shared-host/`，宿主 ZIP 和四个模块 ZIP 分发独立。Platform ZIP 是同一宿主的另一种分发方式；内部打包器、私有 SDK、fixture、stats 不属于终端用户运行依赖。
- 清单校验格式/API/协议、引擎、文件名、平台、完整 Go 版本、宿主 SHA-256、模块 SHA-256 和解码尺寸。DBXMOD2 解码另有精确工具链/平台检查；模块上限 32 MiB，解码声明上限 128 MiB，编解码器还约束字段及累计分配。
- 放宽构建补丁版本不等于允许跨补丁混装。重新编译、重新签名、嵌入不同 commit/time 都可能改变宿主摘要，必须重建配套模块；只更新 VERSION 字符串或清单摘要不能产生 ABI 兼容性。
- 本机验证产物是在提交前构建，嵌入当时的 base commit。另一台机器在新 commit 上发布时应完整重建，不能把新宿主配本机旧模块。当前已配对的临时测试目录不会因为 Git 提交本身而失效。

### 当前尺寸与验证证据

以下为 `.8` / macOS ARM64 / Go 1.26.6 提交前构建的实测字节，完整哈希和归档尺寸见 [Mac 验证记录](./docs/validation/macos-shared-host-20261014.json)：

| 文件 | 安装文件字节 | gzip 解压后的序列化字节 |
| --- | ---: | ---: |
| dbx | 12,070,816 | 不适用 |
| PostgreSQL 模块 | 1,792,875 | 5,897,988 |
| Oracle 模块 | 1,346,437 | 3,738,233 |
| 达梦模块 | 1,997,290 | 4,955,711 |
| SQL Server 模块 | 713,978 | 1,912,126 |

安装文件大小不含旁边数百字节的清单；`.dbmod` 本身就是 gzip 压缩的安装格式，无需用户预先解压。解压后序列化大小不等于运行内存；worker 还要构造对象图和映射代码/数据，未测 RSS，不能给出内存结论。

2026-10-14 自动验证包括完整原生 `--test`、普通根测试、四个独立驱动子模块检查、DM fork、loader/Python 回归、四模块独立安装与 TCP 初始化、SQLite fixture SQL/事务/回滚/超时、模块重复打包字节一致、签名、ZIP 内容及宿主/模块绑定。Windows `.8` 本次只有交叉编译；Windows `.7` 的真库历史记录见 [Windows 验证记录](./docs/validation/windows-shared-host-20261014.json)，不能直接当作新版 Windows 实测。

用户随后在本机临时目录使用已有达梦配置报告“验证可行”，确认了本方案的达梦可插拔手工验收；未提供逐命令输出，不能扩大为所有 SQL 功能、压力测试或其余三库 Mac 真库联调已通过。私有配置、数据库地址、密码和临时目录内容不得提交。

后续维护时至少运行普通根测试及共享构建 `--test`，分别覆盖嵌套模块。若改 loader/编解码器/打包规则，保留规范宿主、完整对象图往返、四模块重打包一致与真实 worker 集成验证；不能只凭编译成功宣布可运行。保留本次 `TestInterfaceMethodPointerUsesMask`，它防止 Mach-O ARM64 接口方法切片指针漏做掩码后在打包阶段崩溃。

用户安装与验证见 [docs/shared-host.md](./docs/shared-host.md)。


## ODBC 独立进程

- 连接 `driver = "odbc"` 显式选择 ODBC；空值或 `native` 保持 Go 路线。当前 ODBC 仅开放 Oracle、达梦、SQL Server。
- `driver_path` 指向 DBX 的版本化 ODBC 适配程序；`odbc_driver` 指向另外安装的厂商库，两者支持 `~` 和 TOML 相对路径。Windows 按当前进程架构查找指向该 DLL 的已注册名称，不写注册表。
- ODBC 使用结构化字段和 `odbc_options`，禁止原始 DSN 和凭据/驱动/结构化目标字段覆盖。主程序解析密码后通过私有协议传给子进程，子进程组装 ODBC 连接串并加载库；输出不得序列化私有 ODBC 配置。
- `drivers/odbc/fork` 保留 ODBC 上游许可证和来源，维护数据类型及影响行数修补；它是嵌套模块，需单独运行测试。NUMERIC/DECIMAL 以精确文本传输，二进制与文本按 SQL 类型区分。
- ODBC 参数标记为 `?`，SQL 引号和元数据仍跟随 `engine`。事务、导入和影响行数保护与原有进程契约相同。厂商错误信息保守脱敏，同时保留不确定写入状态的提示。
- 清单 `adapter` 缺省或 `native` 代表 Go 路线，`odbc` 必须与连接的驱动选择一致。可选 `files` 记录同目录配套文件的 SHA-256；加载时验证普通文件、名称无目录跳转和摘要。资源不得重名覆盖程序或清单。
- `scripts/build-odbc-drivers.sh` / `.ps1` 单独构建 ODBC 变体。Mac 在匹配架构原生构建，复制 unixODBC/libltdl 并重定位、签名临时副本。配套库和许可证平铺且带发布版本，三个引擎同版本复用相同内容。主程序包不携带 ODBC 运行库。
- Linux ODBC 需本机 CGO/unixODBC；Windows 仅声明 x64 支持，使用系统管理器。厂商客户端不纳入 DBX 包，其资源和硬编码依赖沿用厂商安装要求。
- 所有 Go/ODBC 包保持不同文件名和清单；禁止因连接失败自动切换路线。协议暂保持 v1，新增连接字段向后兼容原有 Go 适配器。

安装和构建说明见 [ODBC 独立进程](./docs/odbc-driver-process.md)。
