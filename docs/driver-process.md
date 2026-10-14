# 独立驱动安装与开发

DBX 主程序内置 MySQL 和 SQLite。PostgreSQL、Oracle、达梦和 SQL Server 使用独立驱动包；四种驱动分别编译、安装和升级，不需要重编主程序。CLI 的 `conn`、`inspect`、`query`、`update`、`schema`、`admin`、`import`、`export` 和 `tx` 语法保持一致。

| 数据库 | 配置 engine | 分发方式 | 驱动程序前缀 |
| --- | --- | --- | --- |
| MySQL | `mysql` | 主程序内置 | 无需安装 |
| SQLite | `sqlite` | 主程序内置 | 无需安装 |
| PostgreSQL | `postgres` | 独立驱动 | `dbx-driver-postgres-` |
| Oracle | `oracle` | 独立驱动 | `dbx-driver-oracle-` |
| 达梦 | `dm` | 独立驱动 | `dbx-driver-dm-` |
| SQL Server | `sqlserver` | 独立驱动 | `dbx-driver-sqlserver-` |

Oracle、达梦、SQL Server 也可以使用独立的 [ODBC 路线](./odbc-driver-process.md)，显式设置 `driver = "odbc"` 并选择 `-odbc-` 程序。本文默认介绍原有 Go 驱动包。

## 安装

选择与操作系统、CPU 架构匹配的可信驱动包。包名为 `dbx-driver-<engine>_<version>_<os>_<arch>.tar.gz`；Windows 为 `.zip`。`darwin` 表示 macOS，M 系列芯片使用 `arm64`，Intel 使用 `amd64`。每个包同时提供 `.sha256` 文件。

例如，在 Apple Silicon Mac 安装 PostgreSQL 驱动：

```bash
shasum -a 256 -c dbx-driver-postgres_v0.2.0-driver-process.3_darwin_arm64.tar.gz.sha256
mkdir -p ~/.config/dbx/drivers
tar -xzf dbx-driver-postgres_v0.2.0-driver-process.3_darwin_arm64.tar.gz -C ~/.config/dbx/drivers
```

Linux 可用 `sha256sum -c <文件>.sha256` 校验。Windows PowerShell：

```powershell
Get-FileHash -Algorithm SHA256 .\dbx-driver-postgres_v0.2.0-driver-process.3_windows_amd64.zip
# 将输出与同名 .sha256 文件核对后解压。
Expand-Archive .\dbx-driver-postgres_v0.2.0-driver-process.3_windows_amd64.zip "$HOME/.config/dbx/drivers"
```

归档内只有两个文件，没有引擎或版本子目录。以 Mac 为例：

```text
~/.config/dbx/drivers/
  dbx-driver-postgres-0.2.0-driver-process.3
  dbx-driver-postgres-0.2.0-driver-process.3.json
  dbx-driver-oracle-0.2.0-driver-process.3
  dbx-driver-oracle-0.2.0-driver-process.3.json
```

Windows 程序名为 `dbx-driver-postgres-0.2.0-driver-process.3.exe`，对应清单为 `dbx-driver-postgres-0.2.0-driver-process.3.exe.json`。保持程序和清单成对存放，Mac/Linux 程序需有执行权限。SHA-256 校验用于发现包内容不一致，不证明发布者身份；驱动以当前用户身份执行，必须来自可信来源。

## 为连接选择驱动文件

在连接 TOML 中用 `driver_path` 指定解压后的**可执行文件**，无需添加目录层级。支持 `~`，相对路径以该连接 TOML 所在目录为准。示例为 Mac/Linux 路径，Windows 在程序名末尾加 `.exe`：

```toml
[connection]
engine = "postgres"
driver_path = "~/.config/dbx/drivers/dbx-driver-postgres-0.2.0-driver-process.3"
host = "127.0.0.1"
port = 5432
user = "app"
database = "appdb"
password = "dbx-aes-gcm:v2:..."
sslmode = "require"
allow_actions = ["query"]
```

保存连接（例如 `local-pg.toml`）后验证：

```bash
dbx conn test local-pg
dbx query local-pg 'select version()'
```

多个版本可以直接放在同一目录。不同连接各自指定不同的文件；升级时安装新版本后修改 `driver_path`，回退时指回保留的旧版本。文件名中的版本是驱动包版本，不是数据库服务版本。

`driver_path` 优先于 `driver_dir` 和环境变量。未设置它时，DBX 按连接 `driver_dir` > 环境变量 `DBX_DRIVER_DIR` > `~/.config/dbx/drivers` 选择一个根目录，只查找平铺的无版本 `dbx-driver-<engine>`（Windows 加 `.exe`）及其同名 `.json`。不会自动扫描或选择版本；上面的带版本发布包应显式设置 `driver_path`。

DBX 读取 `<完整可执行文件路径>.json`，核对清单中的程序名与选定文件名完全相同，然后校验引擎、系统、CPU 架构、协议和摘要。缺失或损坏时直接报错，不回退到其他版本、目录或 PATH，不自动下载。

此前实验版的 `<engine>/driver.json` 布局不再加载。升级时重新安装新的平铺驱动包，并在连接配置中填写 `driver_path`；不要直接重命名旧包文件绕过清单校验。

## 其他数据库连接

密码先用 `dbx secret encrypt` 生成，下面的占位值需要替换。Oracle 的 `database` 是服务名；SQL Server 的 `database` 是数据库名。达梦使用 `schema` 标识对象所属模式，不将 `database` 拼入连接地址。

```toml
# local-oracle.toml
[connection]
engine = "oracle"
driver_path = "~/.config/dbx/drivers/dbx-driver-oracle-0.2.0-driver-process.3"
host = "127.0.0.1"
port = 1521
user = "app"
database = "FREEPDB1"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
```

```toml
# local-dm.toml
[connection]
engine = "dm"
driver_path = "~/.config/dbx/drivers/dbx-driver-dm-0.2.0-driver-process.3"
host = "127.0.0.1"
port = 5236
user = "APP"
schema = "APP"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
```

```toml
# local-sqlserver.toml
[connection]
engine = "sqlserver"
driver_path = "~/.config/dbx/drivers/dbx-driver-sqlserver-0.2.0-driver-process.3"
host = "127.0.0.1"
port = 1433
user = "app"
database = "appdb"
schema = "dbo"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
```

可依次使用 `dbx conn test <name>`、`dbx inspect schema <name>`、`dbx query <name> '<sql>'` 验证自己的服务。SQL 仍遵循目标数据库方言；Oracle 和达梦的简单探测可使用 `select 1 from dual`。构建成功和本地协议测试不代表已验证所有厂商服务版本、认证方式或元数据行为。

## 运行与故障处理

主程序先完成动作、表权限、单语句和危险写入检查，再启动驱动。DSN 和解密后的密码经私有 stdin 管道传输，不加入子进程命令行；stdout 用于协议，子进程 stderr 不直接回显。

SQL Server 中没有分号分隔的多条命令也会被拒绝；`SELECT INTO` 按建表动作处理，需要使用 `schema` 命令和对应权限。该检查是 DBX 的保守检查，不能代替数据库账号权限。

一次操作对应一次驱动进程。`tx` 的全部步骤与整批导入各自在同一个驱动进程内执行；影响行数限制在提交前检查。超时或取消会终止并回收进程。写操作失败不会自动重试；如果提交阶段连接或进程中断，结果可能无法确定，应先只读查询确认，不能把通信失败当作已经回滚。

`import` 同样遵守 `--max-rows-affected`（默认 1000）；超限或驱动无法报告影响行数时整批回滚。确需导入更多行时，明确提高该上限。

遇到加载错误时检查 `driver_path` 指向的程序、旁边的同名 JSON 及安装包平台。清单校验失败应重新安装完整可信包，不手工修改摘要绕过检查。遇到数据库错误时用现有 `conn` / `inspect` 命令排查，不打印完整 DSN 或原始管道内容。

## 本地构建和发布

本实验分支主程序及各维护的驱动模块最低要求 Go 1.26.6；各驱动仍拥有独立 `go.mod`。脚本使用当前工具链，不再为 Oracle 强制切换到某个补丁版本，不会修改全局 Go 配置。共享宿主构建限定在 Go 1.26 系列，宿主与模块仍需使用同一具体补丁版本。首次构建可能需要下载模块依赖；离线环境需要提前缓存。

```bash
# 仅打包本机平台的四种驱动。
scripts/build-drivers.sh --host

# 只构建 PostgreSQL，便于开发冒烟验证。
scripts/build-drivers.sh --host --engine postgres

# 指定平台，或不加平台参数构建全部六个平台。
scripts/build-drivers.sh --os linux --arch amd64
scripts/build-drivers.sh
```

Windows PowerShell：

```powershell
.\scripts\build-drivers.ps1 -HostOnly
.\scripts\build-drivers.ps1 -HostOnly -Engine postgres
.\scripts\build-drivers.ps1 -TargetOS linux -TargetArch amd64
```

脚本只构建到临时目录并生成 `dist/<VERSION>/` 下的驱动归档及各自的 `.sha256` 文件，不安装或上传。可用 `--output-dir <目录>` / `-OutputDir <目录>` 更改输出目录。归档工具只使用 Go 标准库，固定条目次序、时间、权限与所有者；相同编译产物和打包工具链可得到相同包字节。

核心发布仍使用 `scripts/release/build.sh` 或 `.ps1`，生成主程序包和只含主程序的 Platform 连接器包。完整发行需另运行独立驱动构建脚本；不要将驱动程序放入 `connector/` 资源目录。

根模块和四个嵌套模块分别测试：

```bash
go test ./...
(cd drivers/postgres && go test ./...)
(cd drivers/oracle && go test ./...)
(cd drivers/dm && go test ./...)
(cd drivers/sqlserver && go test ./...)
```

根目录的 `go test ./...` 不会自动进入嵌套模块。协议、清单和加载约定见 [AGENTS.md](../AGENTS.md)。
