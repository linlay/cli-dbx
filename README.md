# dbx

## 1. 项目简介

`dbx` 是一个给人类和智能体都能用的 database CLI。它面向 MySQL、PostgreSQL、SQLite，重点解决三件事：

- 用统一方式管理数据库连接
- 用显式命令执行查询、更新、DDL 和导入导出
- 用适合脚本和终端的格式返回结果

如果你要看设计目标、动作边界、事务模型和开发约定，请看 [AGENTS.md](./AGENTS.md)。

## 2. 快速开始

### 前置要求

- Go 1.22+，或直接下载 Release 二进制
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

## 6. 进一步阅读

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

现有 Shell/PowerShell 发布脚本额外生成 `dist/<version>/builtin.dbx_<version>_<os>_<arch>.zip`，内容位于 `runtime/connectors/builtin.dbx/`，包含声明、完整 skills 和目标平台 bin。Platform 按完整包摘要消费，不修改包内内容。原 CLI 独立发布包继续提供。包生成器检查 VERSION 满足 cli.json.minVersion（包含 SemVer 预发布版本比较）。
