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
  - 普通根模块、共享宿主与原生模块统一使用 Go 1.26 系列，最低 1.26.6；不维护 Go 1.22 兼容路线。厂商依赖隔离在 `modules/go.mod`，Oracle 依赖同样要求最低 1.26.6；共享构建使用按实际版本区分的私有 SDK `dist/<go-version>-dbmod` 与临时 workspace。宿主和模块必须使用同一具体补丁版本及依赖图，清单和 DBXMOD2 解码仍校验精确 Go 版本与宿主摘要
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
  路由 MySQL/SQLite 到内置执行器；四种外部引擎按 driver_path 选择主进程对象模块或独立驱动进程。
- `internal/dbcore`
  共享 SQL 执行、元数据、导入与整批事务，不导入厂商驱动。
- `internal/driverprocess`
  对象模块直接调用与生命周期，以及可执行驱动的包校验、私有管道协议、进程取消与回收。
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
5. 校验通过后才进入数据库执行层；外部引擎此时才校验并加载对象模块或启动独立驱动

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

当前开发分支为 `codex/dbx-inprocess-modules`，版本 `v0.2.0-shared-host.10`，从 `6081776` 新建。`.9` 将四个原生 `.dbmod` 从隐藏 worker 移到 CLI 主进程；`.10` 修复 Windows 跨盘 workspace 并补充实际控制台中断验证。普通根模块最低版本保持 Go 1.26.6，与模块工具链要求一致；不得为兼容旧 Go 而将根 `go.mod` 降到 1.22。独立可执行驱动和 ODBC 仍走原进程协议。不要继续沿用 `.8` 的模块 worker 架构描述。

Windows workspace 同盘使用相对路径，跨盘使用 JSON 引用的原生绝对路径。控制台回归在隐藏的独立控制台内，仅向测试 CLI 的新进程组发送 Ctrl+Break，不能广播信号或改变运行测试者的控制台。普通根测试使用 Go 1.26.6 或更新的 1.26 补丁版本；模块与装载器测试使用对应版本的私有 SDK。历史验证记录中的 Go 1.22 测试只描述当时的源码，不作为当前兼容性承诺。

### 目的与运行边界

用户需要一份约 12 MiB 的 `dbx`，加上按需提供的 PostgreSQL、Oracle、达梦、SQL Server 增量文件，避免每库重复携带独立 Go 程序的基础代码。主程序仍内置 MySQL、SQLite；四个厂商库隔离在 `modules/go.mod`，不得静态链接到 `cmd/dbx`。

共享宿主是磁盘上的 `dbx`（Windows 为 `dbx.exe`），没有额外 shared-host 文件或后台服务。宿主携带 Go runtime、CLI、配置/凭据/策略、共享 SQL 执行层、对象装载器，以及兼容独立驱动/ODBC 的协议代码。`.dbmod` 保留宿主无法提供的厂商代码、数据、类型、初始化及重定位；相对偏移目标等仍可能要求公共符号副本，不能承诺与静态内嵌增量逐字节相同。

这是 Go 对象装载器布局，不是 Go 标准 plugin、通用 .so/DLL ABI、解释器或四个完整程序改后缀。Native 达梦不依赖 unixODBC。终端用户只需宿主和模块文件，不需 Go、Python、SDK 或编译器。

生命周期以“一条 CLI 命令执行完退出”为前提：

- 四个原生对象模块直接在当前 CLI 进程运行，不启动 `--dbx-module-worker`，该隐藏入口已删除。
- 多个 terminal 启动的是互相独立的 CLI；一个使用达梦不影响另一个使用 PostgreSQL，无需卸载其他模块。
- 同一 CLI 中每个引擎只初始化一次，保持 `Loaded` 引用直到进程退出，不进行热卸载。同一活跃进程不能为已加载引擎切换另一个模块路径/版本。
- `inspect schema` 依次执行 schemas/tables/relations，复用同一个已加载模块，每次操作仍分别创建和关闭数据库连接；这不是跨调用连接池或事务会话。
- 模块 globals、后台 goroutine 和映射代码随整个 CLI 退出回收。单个命令仍面向一个连接，不提供跨库事务。
- 去掉 worker 主要减少重复进程的运行时状态、启动、协议编解码及结果副本。旧 worker 本来执行同一个宿主文件，不能据此宣称磁盘再节省一整份宿主或每库 5 MiB；文件大小以本次构建实测为准。

### 加载与执行流程

1. 现有 `conn`、`query`、`inspect`、`update`、`tx`、`import`、`export` 命令照常解析参数、连接、凭据和策略；不新增用户侧插件命令。
2. `driver` 只接受既有 native/odbc 选择。native 的显式 `driver_path` 以 `.dbmod` 结尾才启用对象模块，不添加 `driver = "module"`。默认发现继续只查独立可执行驱动，不从 PATH 搜索、不下载、不自动切换路线。
3. `internal/db` 进入公共调用边界 `internal/driverprocess.Call`；这里为整个数据库操作建立 timeout，再选择对象模块直接调用或独立进程。包名为兼容历史沿用，不代表 `.dbmod` 仍走进程。
4. `module.go` 按引擎缓存 `sync.Once` 条目；第一次调用验证清单、平台、精确 Go 版本、宿主摘要及模块摘要，取得不可变字节快照后调用 `dbmodule.Load`。禁止校验后重新打开未验证的模块字节。缓存包含失败状态，避免部分初始化后重复注册驱动。
5. 装载器解压/解码 DBXMOD2，映射与重定位，注册类型/GC 信息并运行厂商初始化。每次操作再调用可选的 `Prepare(*conn.Spec)`；Oracle 在这里创建 connector。主入口先保存 CLI 参数再清空 `os.Args[1:]`，Cobra 使用保存的显式参数，避免 Oracle 的全局 `flag.Parse()` 消费 DBX 参数。
6. 直接调用既有 `dispatch` / `dbcore`，查询结果原样返回，不经过内部 stdin/stdout 或 JSON 帧。整批导入使用原始行；CLI 在文件解析阶段已把 json.Number 转成 int64/float64/精确文本，不能丢失此步骤。独立进程仍使用类型标记协议，保持整数、二进制、时间及小数保真。
7. 对外 envelope、错误脱敏、动作/SQL/表保护留在 CLI。模块普通 panic 转成无 panic 值/堆栈的通用错误；runtime fatal、不可恢复信号、其他后台 goroutine 的 panic 仍可能结束整个 CLI。可选达梦日志/统计文件创建失败不得直接打印到 stdout。

### 取消、事务及故障处理

每个模块操作运行在有界等待的调用 goroutine 中；主调用方独立监听 context。操作 timeout 包括校验、加载、Initialize、Prepare、Open 和 SQL 执行。取消后最多等待 200ms 允许正常 rollback/Close，随后返回取消错误，CLI 输出错误并退出整个进程；不会等待忽略 context 的厂商调用无限阻塞。Ctrl+C 使用相同路径。加载后、Prepare 后均检查 context，避免已取消请求继续进入 SQL 分发。

这不是“终止某个 Go goroutine”，也不支持将该返回行为当作长驻服务的安全复用契约。库调用方若取消后仍保留进程，厂商 goroutine 可能继续运行；必须遵守 single-shot 退出边界，不要将同进程嵌入服务/循环任务。多个终端的隔离仍由操作系统进程提供。

事务步骤、整批导入、影响行数保护仍由同一 `dbcore` 连接/事务完成。只在实际 SQL 分发后为写入失败附加“重试前确认数据库状态”；强制退出或提交失联不能保证数据库已经回滚，不自动重试写入。原独立可执行驱动/ODBC 的私有管道、父退出保护和 Windows Job 保留。

### 代码入口与分层

| 位置 | 职责 |
| --- | --- |
| `cmd/dbx/main.go` | signal context、保存 CLI 参数、隔离厂商全局 flag、命令退出 |
| `internal/db` | 内置数据库与可选模块/进程的统一路由 |
| `internal/driverprocess/client.go` | timeout、模块/进程选择、写入不确定提示、原始导入行 |
| `internal/driverprocess/module.go`、`module_disabled.go` | dbmod 构建的 load-once/Prepare/直接执行/有界取消；普通构建拒绝对象模块 |
| `internal/driverprocess/server.go` | 共享操作 dispatch；仅独立驱动调用 Serve 和协议编解码 |
| `internal/dbcore` | 公共 SQL、元数据、整批导入与事务，不导入厂商驱动 |
| `internal/dbmodule`、`internal/dbmodformat` | 清单与宿主绑定、字节快照、装载、DBXMOD2 有界编解码 |
| `modules/go.mod`、`modules/<engine>/module.go` | 四库厂商依赖、注册及可选 Prepare |
| `modules/postgres/fixture.go` | 仅 dbmod_testfixture：SQLite、PID/次数跟踪、阻塞/panic 替身，不发布 |
| `cmd/dbmod-pack`、`third_party/goloader` | 对象图构建、宿主去重、保守预链接及供应装载器 |
| `scripts/build-shared-host.sh`、`.ps1` | 原生宿主、规范重建、四模块、测试和组件打包 |
| `scripts/test-*-windows.py` | 真库 ZIP 回归；共享路线检查无驱动子进程，进程路线仍检查子进程回收 |

### 安装与插拔

目录可只包含 `dbx`、`dbx-driver-dm-0.2.0-shared-host.9.dbmod`、同名加 `.json` 的清单和用户私有 TOML：

```toml
[connection]
engine = "dm"
driver = "native"
driver_path = "./dbx-driver-dm-0.2.0-shared-host.9.dbmod"
# 地址、账号、密码和 allow_actions 使用用户自己的配置
```

路径相对于 TOML 所在目录。模块可放在专用目录或用绝对路径；`.dbmod.json` 必须紧邻模块，driver_path 指向模块本身。新增其他库只需同次构建的对应模块与清单，不用安装全部四库。拔掉模块后，下一次相应连接命令失败，宿主 version/help 和其他数据库仍能运行；恢复两个文件后即可连接。使用 `./dbx`（Windows `.\dbx.exe`）避免误调用 PATH 中旧版。

### 构建、配对与装载器约束

- 稳定 Go 1.26 系列、最低 1.26.6（Oracle 要求），拒绝其他 minor、RC/devel/custom；入口设 GOTOOLCHAIN=local。宿主/打包器/模块使用同一完整补丁版本和依赖图。
- `scripts/prepare-dbmod-toolchain.py` 按实际版本准备忽略目录的私有 SDK；缓存绑定源 SDK 路径及版本。只读模块缓存可硬链接复制，但写私有文件前必须 unlink，不能修改原 SDK。
- `prepare-shared-workspace.py` 合并 root/modules 依赖图。宿主标签为 dbmod,dbmod_runtime；打包器为 dbmod。普通 go build 不具备模块装载能力；宿主禁止 -s 剥离符号表。
- Mac Apple Silicon 原生运行 `bash scripts/build-shared-host.sh --test`，CGO/JIT 签名先于模块生成。Windows x64 运行 PowerShell 同名脚本 -Test，CGO=0。其他平台/架构未验收。
- 顺序固定为宿主 → Mac 签名 → 同参数规范重建逐字节比对 → 打包器 → 四模块 → 测试 → 各组件/Platform ZIP。宿主源码、签名、commit/time 等改变后必须重建配套模块，不得手改清单绕过绑定。
- 清单仍校验格式 2/API 1/协议 1、引擎、文件名、平台、完整 Go 版本、宿主 SHA-256、模块 SHA-256 和解码尺寸。协议字段为格式兼容保留，不代表对象模块运行时仍经 IPC。模块上限 32MiB、解码上限 128MiB，编解码另有限额。
- `.dbmod` 本身是 gzip 压缩的 DBXMOD2 安装格式，不是完整驱动程序的下载压缩包。对象图完整往返验证；预链接只处理加载基址无关的重定位，不移动代码或 PC/GC 偏移。不允许删除代码、类型、初始化、重定位或 GC/PC 信息来缩减文件。
- 保留供应 loader 的零大小符号、CMP 宽度、相对目标、类型表排序、Mac chained-pointer 掩码及 JIT 线程固定修补。`.9` 增补弱调用修补：扩展跳板和 ARM/ARM64 指令选择必须忽略 R_WEAK 标记；否则远 ARM64 弱调用会错误写入 ARM32 指令或覆盖未预留代码。对应两项回归加入两个构建脚本。
- 平台和精确 Go 内部 ABI 风险不会因移除 worker 消失；这不是通用或跨补丁插件 ABI。四个模块必须跟随新宿主整套重建。

### 尺寸和证据口径

安装文件大小以发布目录 measurements.json 为准；`.dbmod` 已压缩，旁边清单仅数百字节。gzip 解压后的序列化字节不等于 RSS，不能据此宣称运行内存或峰值节省量。

`.8` 的 Mac ARM64/Go1.26.6 旧 worker 基线：宿主 12,070,816 字节，PostgreSQL 1,792,875、Oracle 1,346,437、达梦 1,997,290、SQL Server 713,978 字节。其验证记录见 [Mac 历史记录](./docs/validation/macos-shared-host-20261014.json)。Windows `.7` 真库记录仍为历史证据，不能当作 `.9` Windows 原生运行已经验证。

2026-10-15 / macOS ARM64 / Go1.26.6 的 `.9` 主进程模块实测如下。安装大小指解压发布 ZIP 后的文件；模块自身仍以 gzip DBXMOD2 存储，解码大小不是 RSS。

| 文件 | 安装文件字节 | MiB | gzip 解码后的序列化字节 | 相比 .8 的字节变化 |
| --- | ---: | ---: | ---: | ---: |
| dbx | 12,071,104 | 11.51 | 不适用 | +288 |
| PostgreSQL | 1,796,423 | 1.71 | 5,908,532 | +3,548 |
| Oracle | 1,349,287 | 1.29 | 3,751,222 | +2,850 |
| 达梦 | 2,001,628 | 1.91 | 4,966,210 | +4,338 |
| SQL Server | 719,102 | 0.69 | 1,922,290 | +5,124 |

宿主、四模块及四份清单合计 17,939,330 字节（17.11 MiB），不含许可证/操作文档。相对旧版文件总量略增，主要涉及新增直接调用边界和弱调用跳板修复，不能把移除进程误解为必然减少安装文件。

验证记录见 [主进程模块 Mac 验证](./docs/validation/macos-inprocess-modules-20261015.json)。完整原生 --test、普通根测试、四个真实模块隔离/TCP 初始化、主 PID/load-once、忽略 context 的初始化/Prepare、Ctrl+C、脱敏 panic、SQLite fixture 类型/JSON 导入/事务/超限和取消回滚、实际模块移除/恢复、loader/Python 回归、签名及重复打包通过。Windows x64 仅交叉编译；更新后的 Windows 脚本仅在 Mac 做语法检查，须原机验收。

本机已有达梦配置实际连接返回“Error 6001: 网络通信异常 / EOF”；同配置旧 `.8` worker 对照也返回相同网络 EOF。此次真库连接未通过，后续真库查询、元数据与事务未执行，不能沿用旧版用户报告冒充 `.9` 真库通过。私人配置和原始数据库输出不进 Git。模块查询等功能由 fixture 和四库 TCP 初始化分别提供限定范围的证据。

产物是在 `6081776` 基础上的未提交源码构建，宿主嵌入 base commit/time；当前已配对文件可使用，但另一机器提交后重新发布时需完整重建宿主和四模块。

后续维护至少运行普通根测试和共享构建 --test，分别覆盖嵌套模块。共享验收包括四库独立安装/TCP 初始化、真实宿主 PID 与单次 Initialize、同 CLI 多操作复用、忽略 context 的 Initialize/Prepare 取消、Ctrl+C、脱敏 panic、SQLite fixture 的类型/导入/事务/策略，以及规范宿主/模块重复打包。真实数据库兼容性另用相应厂商环境证明，不能用 fixture 或编译代替。

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
