# Configuration

只在创建连接、覆盖配置来源或排查配置错误时阅读本文件。正常数据库操作直接使用 DBX 命令。

## 配置位置

在 Agent Platform 中，DBX 先读取当前 agent 的 `$AP_AGENT_CONFIG_HOME/dbx`，再读取 `~/.config/dbx`；同名连接以 agent 私有配置为准。正常操作不要展开变量、列目录或读取 raw config。

每个连接对应一个 `<name>.toml`：

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
tags = ["dev"]
```

`--config <path>` 可以指定单个 TOML 文件或配置目录；使用后只读取该来源。

## Engine

`engine` 表示数据库类型或兼容方言，内置支持：

- `postgres` / `postgresql`
- `mysql`
- `sqlite` / `sqlite3`
- `oracle`
- `dm` / `dameng`
- `sqlserver` / `mssql`

SQLite 示例：

```toml
[connection]
engine = "sqlite"
path = "./demo.db"
allow_actions = ["query"]
allow_tables = ["users"]
```

SQLite 相对 `path` 以配置文件目录为基准。

Oracle 的 `database` 填服务名；Oracle、达梦的 `schema` 用于 inspect，不改变普通 SQL 的默认 schema，跨 schema 查询需显式写 `SCHEMA.TABLE`。

## ODBC

- `driver` 省略或为 `"native"` 时使用内置驱动；`driver = "odbc"` 使用厂商 ODBC 库，不因连接失败自动切换。
- 其他数据库的 `engine` 填实际类型，并显式选择 `driver = "odbc"`。
- 驱动目录优先级：`driver_dir` → `DBX_DRIVER_DIR` → `~/.config/dbx/drivers`。
- `odbc_driver` 为目录内相对库路径；已知厂商可省略，存在多个版本或未知厂商需指定。
- ODBC 连接属性通过 `odbc_options` 提供，凭据用 `user` / `password`。

## 当前字段

- 连接：`engine`、`driver`、`dsn`、`dsn_env`、`host`、`port`、`user`、`password`、`database`、`schema`、`path`、`sslmode`
- ODBC：`driver_dir`、`odbc_driver`、`odbc_options`
- 行为：`readonly`、`timeout`、`role`、`tags`
- 权限：`allow_actions`、`allow_tables`

`allow_actions` 必填，可选值为 `query`、`update`、`schema`、`admin`。`allow_tables` 可省略；配置后支持 `*` 通配符。

## 密码

运行：

```bash
dbx secret encrypt
```

把输出的 `dbx-aes-gcm:v2:...` 写入 `password`。密钥保存在当前操作系统用户的凭据库中，密文绑定当前机器和用户。不得输出、解密或提交密码与密文。

`dsn_env` 可用于从环境变量读取 DSN；若 DSN 内含密码，优先改用结构化连接字段和加密 `password`。
