# dbx

## 1. 项目简介

`dbx` 是一个给人类和智能体都能用的 database CLI。主程序内置 MySQL、SQLite；PostgreSQL、Oracle、达梦和 SQL Server 通过单独安装的驱动进程连接。重点解决三件事：

- 用统一方式管理数据库连接
- 用显式命令执行查询、更新、DDL 和导入导出
- 用适合脚本和终端的格式返回结果

本分支另提供实验性的[共享宿主与独立模块](./docs/shared-host.md)：Windows x64、macOS Apple Silicon 的驱动使用 `.dbmod`，安装、构建和验证见该文档。使用模块需下载同次构建的共享宿主与模块包，并在 `driver_path` 中指定模块路径。

如果你要看设计目标、动作边界、事务模型和开发约定，请看 [AGENTS.md](./AGENTS.md)。

## 2. 快速开始

### 前置要求

- Go 1.26 系列（最低 1.26.6），或直接下载 Release 二进制
- 本地可访问的数据库；MySQL、SQLite 开箱即用
- PostgreSQL、Oracle、达梦、SQL Server 需先安装与本机平台匹配的[独立驱动包](./docs/driver-process.md)

- 达梦 `inspect schema` 使用普通账号可访问的 `ALL_OBJECTS` 视图列举可见 schema，无需额外授予 `SYS.SYSOBJECTS` 查询权限。

- `inspect table/schema` 按定义顺序返回复合主键、唯一键和外键列，外键两侧逐列对应；不同表上的同名约束分别返回。

- 达梦 Native 驱动在 CSV/JSON 导入时保留高精度小数文本；目标列仍需具有足够的 `DECIMAL` 精度和小数位数。

- Windows 主进程退出时由系统回收外部驱动进程，即使厂商调用未响应取消；写操作中断后仍须查询确认数据库状态。

共享宿主 Windows ZIP 真库与并发回归见 [测试说明](./docs/shared-host.md#windows-真库回归)，运行 `scripts/test-driver-process-windows.py --shared-host` 与 `scripts/test-shared-host-windows.py`；测试结果写入指定输出目录。

### 本地编译

```bash
go build -o ./dbx ./cmd/dbx
./dbx version
```

Windows PowerShell：

```powershell
go build -o .\dbx.exe .\cmd\dbx
.\dbx.exe version
```

### 5 分钟跑起来

先创建默认配置目录：

```bash
mkdir -p ~/.config/dbx
```

创建一个最小 SQLite 连接 `~/.config/dbx/local-sqlite.toml`：

```toml
[connection]
engine = "sqlite"
path = "./demo.db"
allow_actions = ["query", "update", "schema"]
tags = ["local"]
```

准备环境并执行最小流程：

```bash
./dbx conn list
./dbx conn test local-sqlite
./dbx schema local-sqlite 'create table users (id integer primary key, name text)'
./dbx import file ./users.csv local-sqlite users
./dbx query local-sqlite 'select * from users order by id'
./dbx inspect table local-sqlite users
./dbx export table users local-sqlite ./users-export.csv --format csv
```

常用命令：

```bash
./dbx query local-sqlite 'select * from users'
./dbx update local-sqlite 'update users set name = "Ada" where id = 1'
./dbx schema local-sqlite 'alter table users add column email text'
./dbx query file local-sqlite ./query.sql
./dbx query dsn postgres 'postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable' 'select 1'
```

如果你需要一组必须一起成功或一起回滚的连续动作，用 `tx`：

```json
{
  "steps": [
    {"action": "query", "sql": "select id from users where id = 1"},
    {"action": "update", "sql": "update users set active = 1 where id = 1", "max_rows_affected": 1}
  ]
}
```

```bash
./dbx tx local-pg --plan ./plan.json
./dbx query local-pg 'select id, active from users where id = 1'
```

说明：

- 优先使用 `query` / `update` / `schema` / `admin`
- 需要原子性的多步动作时，用 `tx`
- `tx` 只支持 `query` / `update`，不支持 `schema` / `admin`
- 事务计划必须是一个完整 JSON 文档；未知字段、第二个 JSON 文档和尾随垃圾会在执行前被拒绝
- 分页读取时继续传回同一条 SQL 和 `--cursor`
- `update/delete` 的 `WHERE` 必须属于最外层写语句，子查询中的条件不能替代它
- 写入型 CTE 和对写语句的 `EXPLAIN` 会被拒绝；修改配置的 SQLite `PRAGMA` 使用 `admin`
- `admin` 不由 DBX 包装事务，支持 SQLite `VACUUM`；`--max-rows-affected` 不适用于管理动作

## 3. 配置说明

默认配置目录：

```bash
~/.config/dbx
```

每个连接一个文件，例如 `~/.config/dbx/local-pg.toml`。

Windows 默认目录是 `$env:USERPROFILE\.config\dbx`。TOML 中的 Windows 路径可以写成 `path = 'C:\data\demo.db'`，或使用正斜杠 `path = "C:/data/demo.db"`；双引号中的反斜杠需要转义。

配置、SQL 文件、事务 JSON、导入 CSV 和导入 JSON 支持带或不带 UTF-8 BOM。UTF-16 文件需要先另存为 UTF-8。Windows PowerShell 5.1 的 `>` / `>>` 默认写入 UTF-16，建议明确指定 UTF-8，例如：

```powershell
$configDir = Join-Path $env:USERPROFILE '.config\dbx'
New-Item -ItemType Directory -Force $configDir | Out-Null
@'
[connection]
engine = "sqlite"
path = './demo.db'
allow_actions = ["query", "update", "schema"]
'@ | Set-Content -Encoding UTF8 (Join-Path $configDir 'local-sqlite.toml')
.\dbx.exe conn test local-sqlite
```

SQLite 普通文件路径中的空格、中文、`#`、`%20` 等按原文件名处理，相对路径以配置文件目录为准。现有路径后的 `?mode=ro`、`?mode=memory&cache=shared` 和 `_pragma` 参数继续生效；也可显式使用 SQLite `file:` URI，URI 内的文件名需要百分号编码，例如 `file:./demo%23one.db?mode=ro`。显式 URI 由 SQLite 解析，其中的相对路径以命令当前目录为准。`:memory:` 和 `:memory:?cache=shared` 保持内存库形式。

### Agent Platform 专属配置

系统配置目录固定为 `~/.config/dbx`。当 Agent Platform 启动 dbx 时，可以设置公共的 `AP_AGENT_CONFIG_HOME` 指向当前 agent 的私有配置根目录；dbx 会优先读取 `$AP_AGENT_CONFIG_HOME/dbx/<connection>.toml`，agent 未定义该连接时才读取 `~/.config/dbx/<connection>.toml`。`conn list` 合并两侧连接，重名连接以 agent 配置为准。旧的 `DBX_AGENT_CONFIG_HOME` 不再识别。

显式传入 `--config <path>` 时只读取该文件或目录，不使用 agent 或系统回退。路径不存在、连接不存在、名称不匹配或配置无法解析时都会直接报错。agent 中已经存在但无法解析的同名连接也会直接报错，避免意外访问系统连接。连接文件可能包含数据库访问资料，应放在私有运行时目录且不得提交。

最小 PostgreSQL 例子（需先[安装 postgres 驱动](./docs/driver-process.md)）：

```toml
[connection]
engine = "postgres"
driver_path = "~/.config/dbx/drivers/dbx-driver-postgres-0.2.0-driver-process.3"
host = "127.0.0.1"
port = 5432
user = "app"
database = "appdb"
password = "dbx-aes-gcm:v2:..."
sslmode = "disable"
allow_actions = ["query"]
allow_tables = ["users", "public.audit_*"]
tags = ["dev", "local"]
```

上例引用现有已构建的 `.3` 独立驱动包；本次 `.4` 主程序仍使用协议 v1，可以继续搭配这些驱动。Windows 的程序文件名加 `.exe`，旁边保留对应的 `.exe.json` 清单。

最小 MySQL 例子：

```toml
[connection]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "appdb"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
```

交互生成加密密码（推荐给人类使用）：

```bash
./dbx secret encrypt
```

在 Windows PowerShell 中把生成的密码行追加到已有 UTF-8 连接文件时，明确指定编码，并只添加一次：

```powershell
.\dbx.exe secret encrypt | Add-Content -Encoding UTF8 (Join-Path $configDir 'local-pg.toml')
```

无参数模式只提示输入一次数据库密码，终端不会回显输入。需要由智能体或一次性迁移脚本直接调用时，也可以传入一个位置参数：

```bash
dbx secret encrypt '<password>'
```

密码以 `-` 开头时，用 `--` 结束选项解析：

```bash
dbx secret encrypt -- '-password'
```

参数模式不会提示，也不会读取 stdin；两种模式都只输出 `password = "dbx-aes-gcm:v2:..."`。注意，位置参数中的明文会暴露给调用它的智能体、Shell history、命令审计以及可能的进程参数查看工具，只适合接受这次明文暴露的迁移场景。DBX 自身不会把明文写到 stdout、stderr 或错误信息中。

v2 使用 AES-256-GCM，每条密文的随机密钥自动保存在当前操作系统用户的凭据库中：macOS Keychain、Windows Credential Manager，或 Linux Secret Service。DBX 运行时静默取回密钥，不再要求输入 master passphrase，也不提供解密、导出或显示密钥的 CLI 命令。

v2 密文与当前机器和操作系统用户绑定；复制到另一台机器后需要在那里重新运行 `dbx secret encrypt`。Linux 必须存在可用且已解锁的 Secret Service 登录集合，否则加密和运行时解密会明确失败。旧的 `dbx-aes-gcm:v1:...` 配置仍兼容，读取旧格式时才会继续提示原 master passphrase。

密码可以继续写成 `password = "明文"`，但 DBX 会输出 warning；`password.env`、`password.cmd`、`password.file` 默认禁用。`dsn_env` 仍可用，但如果 DSN 里带密码，也会提示改用结构化连接字段和 v2 加密密码。

在 agent-platform 发布包中，DBX 位于包根目录的 `bin/dbx`（Windows 为 `bin\dbx.exe`），并会进入 Agent Terminal 和 host Bash 的 `PATH`。可以先定位再运行：

```bash
# macOS / Linux
which dbx
dbx secret encrypt

# Windows Command Prompt
where dbx
dbx secret encrypt

# Windows PowerShell
Get-Command dbx
dbx secret encrypt
```

通过 ZenMind Desktop 安装 agent-platform 后，可在控制中心打开 Agent Platform 服务详情查看“安装目录”，然后在其 `bin/dbx` 或 `bin\dbx.exe` 找到同一个程序。当前已安装的旧服务包不会自动获得新命令语法，需要等待后续 Platform/Desktop 发布包同步新版 DBX。

操作层面可以先这样理解：

- `allow_actions` 控制这个连接允许哪些命令动作
- `allow_tables` 可选，控制这个连接允许访问哪些表；省略或留空表示不限制
- `allow_tables` 支持多个数组项和 `*` 通配符，例如 `["users", "orders", "public.audit_*"]`
- 推荐显式写出最小权限集合，不依赖隐式默认值

如果你要理解 `allow_actions`、`allow_tables` 和为什么 `tx` 只允许 `query/update`，请看 [AGENTS.md](./AGENTS.md)。

也可以直接参考：

- [config.example.toml](./testdata/config.example.toml)
- [config.sqlite.toml](./testdata/config.sqlite.toml)

## 4. 发布与分发

如果你是普通使用者，优先从 GitHub Releases 下载对应平台压缩包：

- macOS Apple Silicon：`dbx_vX.Y.Z_darwin_arm64.tar.gz`
- macOS Intel：`dbx_vX.Y.Z_darwin_amd64.tar.gz`
- Linux ARM64：`dbx_vX.Y.Z_linux_arm64.tar.gz`
- Linux AMD64：`dbx_vX.Y.Z_linux_amd64.tar.gz`
- Windows ARM64：`dbx_vX.Y.Z_windows_arm64.zip`
- Windows AMD64：`dbx_vX.Y.Z_windows_amd64.zip`

解压后可直接验证：

```bash
tar -xzf dbx_v0.1.0_darwin_arm64.tar.gz
./dbx version
./dbx conn --help
```

Windows 使用 `Expand-Archive` 或其他 zip 工具解压后运行 `dbx.exe version`。

维护者打包时，正式版本由 Git 跟踪的仓库根目录 [`VERSION`](./VERSION) 统一管理；更新该文件后运行 `scripts/release/build.sh`，无需传入版本号。

MySQL、SQLite 随主程序和 Platform 连接器提供。四种外部驱动分别发布为 `dbx-driver-<engine>_<version>_<os>_<arch>.tar.gz`（Windows 为 `.zip`），解压后直接平铺两个文件：带版本的可执行程序和 `<程序文件名>.json`（Windows 为 `.exe` 与 `.exe.json`）。连接用 `driver_path` 精确指定程序，支持 `~`，相对路径以连接 TOML 所在目录为准；多个版本可共存。未设置 `driver_path` 时，只在 `driver_dir`、`DBX_DRIVER_DIR`、`~/.config/dbx/drivers` 依次选定的根目录查找无版本的 `dbx-driver-<engine>[.exe]`，不自动选择版本。DBX 不从 PATH 找驱动，也不自动下载；清单、平台或摘要错误会阻止加载。安装和本机构建步骤见 [独立驱动](./docs/driver-process.md)。

Oracle、达梦、SQL Server 还可配置 `driver = "odbc"`，通过 `driver_path` 选择独立 ODBC 适配程序，`odbc_driver` 指向另行安装的厂商库。Mac 的 ODBC 包包含平铺的 unixODBC 运行库；Windows x64 使用系统 ODBC Driver Manager。详见 [ODBC 独立进程](./docs/odbc-driver-process.md)。

维护者的构建、打包、发布流程见 [AGENTS.md](./AGENTS.md)。

## 5. 简单验证与排查

### 简单测试

仓库级测试：

```bash
go test ./...
```

简单冒烟验证：

```bash
./dbx version
./dbx conn list
./dbx conn test local-sqlite
./dbx query local-sqlite 'select * from users' --format table
```

### 结果格式与分页

- `json`：默认格式，适合脚本和智能体
- `table`：适合人直接查看
- 不支持的 `--format` 会在数据库执行前报错；导出的 `csv/json` 格式也会在创建或覆盖文件前校验
- 重复结果列自动加唯一后缀，例如 `id/id` 返回 `id/id_2`；若 SQL 已有 `id_2`，后缀会跳过它。可用显式 `AS` 固定名称
- JSON 导入保留 int64 范围内的整数；更大的整数和高精度数字以原始数字文本绑定，最终存储精度取决于目标列类型

例如：

```bash
./dbx query local-sqlite 'select * from users' --format json
./dbx query local-sqlite 'select * from users' --format table
./dbx query local-sqlite 'select * from users order by id' --cursor 100 --page-size 200
```

### 常见排查

- 连接不存在：确认配置文件名和连接名一致
- 动作不允许：检查 `allow_actions`
- SQL 类型无法识别：不确定的转义和方言语法会被拒绝；普通引号内反斜线、MySQL `#` 行注释、Oracle/达梦 `q/nq` 替代引号暂不支持。可改用标准注释和普通引号；PostgreSQL 可用显式 `E'...'`，SQLite 可用 `char(92)` 拼接反斜线
- v2 密钥不存在：配置可能来自另一台机器；在当前机器重新运行 `dbx secret encrypt`
- 系统凭据库不可用：解锁 macOS/Windows 凭据库，或确认 Linux Secret Service 的登录集合可用
- 旧 v1 密码解不开：确认输入的是加密时使用的 master passphrase
- 密码来源被禁用：改用 `password = "dbx-aes-gcm:v2:..."`
- SQLite 路径不对：相对路径是相对于配置文件目录，不是当前工作目录
- 文件解析失败：保存为 UTF-8；支持 UTF-8 BOM，不自动转换 UTF-16。事务计划及 JSON 导入文件只接受一个 JSON 文档
- 外部驱动缺失：按[独立驱动安装说明](./docs/driver-process.md)检查 `driver_path` 指定的程序及同名 `.json` 清单，或无版本驱动的默认根目录
- 驱动清单、平台、协议或 SHA-256 不匹配：重新安装对应平台的完整可信驱动包，不修改摘要绕过校验
- 外部写操作因超时或进程中断而失败：不要自动重试；提交阶段中断可能无法确定结果，先用只读查询核验

## 6. 进一步阅读

- [独立驱动](./docs/driver-process.md)
  安装 PostgreSQL、Oracle、达梦和 SQL Server 驱动
- [AGENTS.md](./AGENTS.md)
  设计与开发约定
- [Beginner Guide](./docs/beginner-guide.md)
  第一次上手
- [config.example.toml](./testdata/config.example.toml)
  配置示例
- [config.sqlite.toml](./testdata/config.sqlite.toml)
  SQLite 示例


## Platform 连接器发布包

项目位于 `agent-platform-connectors/dbx`，`connector/` 是 connector.json 模板、cli.json、skills 与全部资源的唯一源码。`VERSION` 同时控制 CLI 和连接器发布版本；模板不维护 version，构建时生成。任何 CLI、清单或技能改动均需发布新版本。

现有 Shell/PowerShell 发布脚本额外生成 `dist/<version>/builtin.dbx_<version>_<os>_<arch>.zip`，内容位于 `runtime/connectors/builtin.dbx/`，包含声明、完整 skills 和目标平台的 `bin/dbx`；仅主程序内置 MySQL、SQLite，四种外部驱动包独立分发。Platform 按完整包摘要消费，不修改包内内容。原 CLI 独立发布包继续提供。包生成器检查 VERSION 满足 cli.json.minVersion（包含 SemVer 预发布版本比较）。

### 可复现发布打包

发布打包需要 Python 3（Unix 命令 `python3`，Windows 命令 `python`）。
`scripts/reproducible-package.py` 固定归档顺序、时间、权限和压缩头，保留文件内容与符号链接；
sidecar 额外规范 Cargo 本机路径和 SBOM 可变元数据，保留依赖与许可证事实。
复验必须使用相同源码、依赖、目标平台、编译器、Python/zlib 和 Syft 版本；不保证跨工具链逐字节相同。
历史已发布归档不能重新打包覆盖。
