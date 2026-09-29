# dbx

## 1. 项目简介

`dbx` 是一个给人类和智能体都能用的 database CLI。MySQL、PostgreSQL、SQLite、SQL Server、Oracle、达梦驱动内置；兼容数据库按 TOML 的 `engine` 选择接入协议。其他数据库或需要替代原生驱动时，显式使用 ODBC。

- 用统一方式管理数据库连接
- 用显式命令执行查询、更新、DDL 和导入导出
- 用适合脚本和终端的格式返回结果

如果你要看设计目标、动作边界、事务模型和开发约定，请看 [AGENTS.md](./AGENTS.md)。

## 2. 快速开始

### 前置要求

- Go 1.26.6+，或直接下载 Release 二进制
- 本地可访问的 PostgreSQL / MySQL / SQLite

### 本地编译

```bash
go build -o ./dbx ./cmd/dbx
./dbx version
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
- 分页读取时继续传回同一条 SQL 和 `--cursor`

## 3. 配置说明

默认配置目录：

```bash
~/.config/dbx
```

每个连接一个文件，例如 `~/.config/dbx/local-pg.toml`。

### Agent Platform 专属配置

系统配置目录固定为 `~/.config/dbx`。当 Agent Platform 启动 dbx 时，可以设置公共的 `AP_AGENT_CONFIG_HOME` 指向当前 agent 的私有配置根目录；dbx 会优先读取 `$AP_AGENT_CONFIG_HOME/dbx/<connection>.toml`，agent 未定义该连接时才读取 `~/.config/dbx/<connection>.toml`。`conn list` 合并两侧连接，重名连接以 agent 配置为准。旧的 `DBX_AGENT_CONFIG_HOME` 不再识别。

显式传入 `--config <path>` 时只读取该文件或目录，不使用 agent 或系统回退。路径不存在、连接不存在、名称不匹配或配置无法解析时都会直接报错。agent 中已经存在但无法解析的同名连接也会直接报错，避免意外访问系统连接。连接文件可能包含数据库访问资料，应放在私有运行时目录且不得提交。

最小 PostgreSQL 例子：

```toml
[connection]
engine = "postgres"
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

### 内置与 ODBC 驱动

默认内置 MySQL、PostgreSQL、SQLite、SQL Server、Oracle、达梦，无需额外安装数据库客户端。SQL Server 使用微软 `go-mssqldb`，Oracle 使用官方 `go-oracledb/v26 v26.0.1-beta`，达梦使用社区维护的 `gitee.com/chunanyong/dm`。

Oracle 官方支持范围为 19c+，DBX 不主动拦截旧版本，但不保证旧版本兼容。Oracle 使用 `database` 指定服务名；Oracle、达梦的 `schema` 用于 inspect，业务 SQL 显式限定 schema。三个新增内置引擎均使用结构化连接字段。

`driver` 省略或为 `"native"` 时使用内置驱动；设为 `"odbc"` 时使用外部厂商库。不根据连接失败自动切换。已有 ODBC 配置需明确加上 `driver = "odbc"`。DBX 不下载或安装驱动。

通过环境变量 `DBX_DRIVER_DIR` 指定驱动目录，也可在连接配置中设置 `driver_dir` 覆盖它；未设置时沿用 `~/.config/dbx/drivers`。目录中的厂商文件不需要改名。

```toml
[connection]
engine = "sqlserver"
driver = "odbc"
driver_dir = "/path/to/vendor/lib"
odbc_driver = "libmsodbcsql.18.dylib" # 可省略；存在多个版本时必须明确选择
host = "127.0.0.1"
port = 1433
user = "app"
database = "appdb"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]

[connection.odbc_options]
Encrypt = "yes"
```

`odbc_driver` 是目录内的相对文件路径，可以包含厂商包的子目录。未设置时仅查找目录第一层的厂商常见文件名；不会递归扫描其他目录。`odbc_options` 允许额外厂商连接属性，不能覆盖驱动、凭据或已设置的结构化字段。ODBC 连接使用上述结构化配置，不接受 `dsn` / `dsn_env` / `--dsn` 绕过目录选择。

```bash
export DBX_DRIVER_DIR="/path/to/vendor/lib"
dbx odbc dir
dbx odbc list
```

Windows PowerShell 示例：

```powershell
$env:DBX_DRIVER_DIR = "C:\path\to\vendor\driver"
dbx odbc list
```

`odbc dir/list` 查看环境变量对应目录，不读取某个连接的 `driver_dir`。列表只表示找到了候选库文件，连接时由 ODBC 检查能否加载。缺失时返回 `driver_missing`，包含 `engine`、`driver_dir` 和市场检索标识 `market_package`；后者不代表云端一定已经发布对应包。

OceanBase、TDSQL 等兼容数据库由用户在 TOML 中选择实际协议：MySQL 兼容连接填写 `engine = "mysql"`，PostgreSQL 兼容连接填写 `engine = "postgres"`。DBX 不识别品牌、不推断模式；端口、用户名及数据库名按实际连接填写。模板只是配置示例，不代表所有数据库版本都已验证。

`engine` 填数据库类型或兼容方言，`driver` 选择接入方式。其他数据库填写实际类型，显式配置 `driver = "odbc"`、`odbc_driver`，并在 `odbc_options` 填写厂商要求的连接属性；凭据仍使用 `user` / `password`。未知厂商文件名必须指定 `odbc_driver`，`odbc list` 仍只自动识别已知厂商。

**macOS M 系列发布包已包含 unixODBC 运行库；Windows x64 使用系统 ODBC 管理器。** 用户无需额外安装驱动管理器，厂商数据库驱动仍按需安装。Linux ODBC 的依赖及自定义构建方式见 [ODBC 构建与安装说明](docs/odbc.md)。

交互生成加密密码（推荐给人类使用）：

```bash
./dbx secret encrypt
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

### 连接配置示例

配置示例统一位于 `examples/`，源码仓库和独立 CLI 发布包使用相同目录。

| 数据库 | 示例文件 |
|---|---|
| MySQL | [config.mysql.toml](./examples/config.mysql.toml) |
| PostgreSQL | [config.postgres.toml](./examples/config.postgres.toml) |
| SQLite | [config.sqlite.toml](./examples/config.sqlite.toml) |
| 达梦 | [config.dm.toml](./examples/config.dm.toml) |
| Oracle | [config.oracle.toml](./examples/config.oracle.toml) |
| SQL Server | [config.sqlserver.toml](./examples/config.sqlserver.toml) |
| 通用 ODBC | [config.odbc.toml](./examples/config.odbc.toml) |

解压 CLI 包后，以 MySQL 为例：

```bash
mkdir -p connections
cp examples/config.mysql.toml connections/mysql.toml
# 编辑 connections/mysql.toml，填写实际地址、端口、账号和库名。
./dbx secret encrypt >> connections/mysql.toml
./dbx conn test --config ./connections mysql
./dbx query --config ./connections mysql 'SELECT 1 AS ok'
```

加密命令交互读取密码并追加配置，仅在文件没有 `password` 字段时执行一次；后续修改密码应替换原字段。网络数据库示例不含真实密码，默认仅开放 `query`。

## 4. 发布与分发

如果你是普通使用者，优先从 GitHub Releases 下载对应平台压缩包：

Windows ARM64 和 macOS Intel 暂不纳入支持及发布范围。macOS 解压后须保留 `dbx` 旁的 `lib/` 和 `licenses/`，不要只复制可执行文件；最低 macOS 版本取决于发布时所用运行库。

- macOS Apple Silicon：`dbx_vX.Y.Z_darwin_arm64.tar.gz`
- Linux ARM64：`dbx_vX.Y.Z_linux_arm64.tar.gz`
- Linux AMD64：`dbx_vX.Y.Z_linux_amd64.tar.gz`
- Windows AMD64：`dbx_vX.Y.Z_windows_amd64.zip`

解压后可直接验证：

```bash
tar -xzf dbx_v0.1.0_darwin_arm64.tar.gz
./dbx version
./dbx conn --help
```

Windows 使用 `Expand-Archive` 或其他 zip 工具解压后运行 `dbx.exe version`。

维护者打包时，正式版本由 Git 跟踪的仓库根目录 [`VERSION`](./VERSION) 统一管理。运行 `scripts/release/build.sh`。

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

例如：

```bash
./dbx query local-sqlite 'select * from users' --format json
./dbx query local-sqlite 'select * from users' --format table
./dbx query local-sqlite 'select * from users order by id' --cursor 100 --page-size 200
```

### 常见排查

- 连接不存在：确认配置文件名和连接名一致
- 动作不允许：检查 `allow_actions`
- v2 密钥不存在：配置可能来自另一台机器；在当前机器重新运行 `dbx secret encrypt`
- 系统凭据库不可用：解锁 macOS/Windows 凭据库，或确认 Linux Secret Service 的登录集合可用
- 旧 v1 密码解不开：确认输入的是加密时使用的 master passphrase
- 密码来源被禁用：改用 `password = "dbx-aes-gcm:v2:..."`
- SQLite 路径不对：相对路径是相对于配置文件目录，不是当前工作目录
- `driver_missing`：按 `market_package` 查找厂商 ODBC 包，通过外部工具安装、校验，再设置 `driver_dir` 或 `DBX_DRIVER_DIR` 指向实际驱动目录；Windows 还需厂商安装器完成 ODBC 注册

## 6. 进一步阅读

- [AGENTS.md](./AGENTS.md)
  设计与开发约定
- [Beginner Guide](./docs/beginner-guide.md)
  第一次上手
- [config.postgres.toml](./examples/config.postgres.toml)
  配置示例
- [config.sqlite.toml](./examples/config.sqlite.toml)
  SQLite 示例


## Platform 连接器发布包

项目位于 `agent-platform-connectors/dbx`，`connector/` 是 connector.json 模板、cli.json、skills 与全部资源的唯一源码。`VERSION` 同时控制 CLI 和连接器发布版本；模板不维护 version，构建时生成。任何 CLI、清单或技能改动均需发布新版本。

现有 Shell/PowerShell 发布脚本额外生成 `dist/<version>/builtin.dbx_<version>_<os>_<arch>.zip`，内容位于 `runtime/connectors/builtin.dbx/`，包含声明、完整 skills 和目标平台 bin。Platform 按完整包摘要消费，不修改包内内容。原 CLI 独立发布包继续提供。包生成器检查 VERSION 满足 cli.json.minVersion（包含 SemVer 预发布版本比较）。

### 可复现发布打包

发布打包需要 Python 3（Unix 命令 `python3`，Windows 命令 `python`）。
`scripts/reproducible-package.py` 固定归档顺序、时间、权限和压缩头，保留文件内容与符号链接；
sidecar 额外规范 Cargo 本机路径和 SBOM 可变元数据，保留依赖与许可证事实。
复验必须使用相同源码、依赖、目标平台、编译器、Python/zlib 和 Syft 版本；不保证跨工具链逐字节相同。
历史已发布归档不能重新打包覆盖。
