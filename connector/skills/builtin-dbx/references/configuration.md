# Configuration

只在创建连接、覆盖配置来源或排查配置错误时阅读本文件。正常数据库操作直接使用 DBX 命令。

## 配置位置

在 Agent Platform 中，DBX 先读取当前 agent 的 `$AP_AGENT_CONFIG_HOME/dbx`，再读取 `~/.config/dbx`；同名连接以 agent 私有配置为准。正常操作不要展开变量、列目录或读取 raw config。

每个连接对应一个 `<name>.toml`：

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
tags = ["dev"]
```

`--config <path>` 可以指定单个 TOML 文件或配置目录；使用后只读取该来源。

Windows 默认目录为 `%USERPROFILE%\.config\dbx`。配置使用 UTF-8，可带 BOM；PowerShell 写入或追加时显式指定 `-Encoding UTF8`。TOML 路径可用单引号 `path = 'C:\data\demo.db'`，双引号中的反斜杠需要转义。SQL、事务 JSON、导入 CSV/JSON 同样支持 UTF-8 BOM，不自动转换 UTF-16。

## Engine

主程序内置 `mysql`、`sqlite` / `sqlite3`。以下引擎需要独立驱动包：

- `postgres` / `postgresql`
- `oracle`
- `dm`
- `sqlserver`

驱动平铺安装：带版本的可执行文件与 `<完整程序文件名>.json` 放在同一目录，Windows 为 `.exe` 与 `.exe.json`。连接通过 `driver_path` 精确选择文件，支持 `~`，相对路径以连接 TOML 所在目录为准。未设置时，在 `driver_dir` > `DBX_DRIVER_DIR` > `~/.config/dbx/drivers` 选定的根目录只查找无版本的 `dbx-driver-<engine>[.exe]`，不自动搜索版本。安装包和平台需匹配，DBX 启动前校验清单与 SHA-256；选中目录缺包或损坏时不回退、不自动下载。

Oracle、达梦、SQL Server 支持 `driver = "odbc"`：`driver_path` 选择带 `-odbc-` 的独立程序，`odbc_driver` 指定厂商库，`odbc_options` 仅提供非凭据选项。ODBC 不接受原始 DSN，不允许选项覆盖结构化字段；Windows 需要厂商 DLL 已注册。Mac ODBC 包的运行库也必须完整解压。

共享宿主包支持将 `driver_path` 指向 `dbx-driver-<engine>-0.2.0-shared-host.1.dbmod`，与同目录 `.dbmod.json` 配套安装。模块必须匹配当前宿主的文件摘要；更新宿主后同时安装其配套模块。普通构建不支持模块，默认目录也不自动发现模块。

正常数据库操作不用检查驱动目录；只有驱动加载错误或明确的安装任务才排查。Platform 核心连接器不含外部驱动包。

SQLite 示例：

```toml
[connection]
engine = "sqlite"
path = "./demo.db"
allow_actions = ["query"]
allow_tables = ["users"]
```

SQLite 普通相对 `path` 以配置文件目录为基准；显式 `file:` URI 中的相对路径以命令当前目录为基准。
普通文件名中的空格、中文、`#`、`%` 按字面保留。路径后的 `?mode=ro`、内存库/共享缓存及 `_pragma` 参数继续生效；显式 `file:` URI 保持原语义，其文件名需按 URI 规则编码。

## 当前字段

- 连接：`engine`、`dsn`、`dsn_env`、`host`、`port`、`user`、`password`、`database`、`schema`、`path`、`sslmode`
- 行为：`readonly`、`timeout`、`role`、`tags`
- 驱动路线：`driver`（`native` 或 `odbc`），ODBC 厂商库：`odbc_driver`，附加参数：`odbc_options`
- 外部驱动文件：`driver_path`（推荐，精确选择已安装的版本）
- 无版本驱动默认目录：`driver_dir`（仅未设置 `driver_path` 时生效，推荐绝对路径）
- 权限：`allow_actions`、`allow_tables`

`allow_actions` 必填，可选值为 `query`、`update`、`schema`、`admin`。`allow_tables` 可省略；配置后支持 `*` 通配符。

## 密码

运行：

```bash
dbx secret encrypt
```

把输出的 `dbx-aes-gcm:v2:...` 写入 `password`。密钥保存在当前操作系统用户的凭据库中，密文绑定当前机器和用户。不得输出、解密或提交密码与密文。

`dsn_env` 可用于从环境变量读取 DSN；若 DSN 内含密码，优先改用结构化连接字段和加密 `password`。
