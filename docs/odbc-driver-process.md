# 独立进程 ODBC 路线

Oracle、达梦、SQL Server 可以选择原有 Go 驱动或独立 ODBC 适配程序。主程序仍只内置 MySQL、SQLite；PostgreSQL 保持独立 Go 驱动。ODBC 路线不会把 CGO 或 unixODBC 链接进主程序。

## 安装和选择

先安装匹配操作系统、CPU 架构的 ODBC 适配包，例如 M 系列 Mac 的 `dbx-driver-oracle-odbc_v0.2.0-driver-process.3_darwin_arm64.tar.gz`。Windows x64 为 `..._windows_amd64.zip`。校验旁边的 `.sha256` 后，将全部内容解压到同一个 `drivers/` 目录，不要只取出可执行文件。

```text
~/.config/dbx/drivers/
  dbx-driver-oracle-0.2.0-driver-process.3          # 原有 Go 适配程序，可选
  dbx-driver-oracle-0.2.0-driver-process.3.json
  dbx-driver-oracle-odbc-0.2.0-driver-process.3     # ODBC 适配程序
  dbx-driver-oracle-odbc-0.2.0-driver-process.3.json
  dbx-odbc-runtime-0.2.0-driver-process.3-libodbc.2.dylib
  dbx-odbc-runtime-0.2.0-driver-process.3-libodbcinst.2.dylib
  dbx-odbc-runtime-0.2.0-driver-process.3-libodbccr.2.dylib
  dbx-odbc-runtime-0.2.0-driver-process.3-libltdl.7.dylib
  ...配套的许可证、作者和来源说明文件
```

Mac 的 unixODBC 运行库随 ODBC 包分发；使用者无需为适配程序单独安装 Homebrew unixODBC。运行库和许可证均平铺，文件名带发布版本，避免不同版本覆盖。Windows 包只有适配程序 `.exe` 和 `.exe.json`，使用系统 ODBC Driver Manager。Linux 用户需要自行准备 unixODBC 运行库。

此外，需要按厂商说明安装对应的 ODBC 动态库和依赖。Oracle 的 Instant Client、微软的资源文件等仍保留厂商要求的布局；不要把厂商包强行拆成单个文件。DBX 不下载、注册或改写厂商客户端。厂商库硬编码的其他依赖路径仍须按厂商要求配置，随包 unixODBC 不等于已解决所有厂商客户端依赖。

## 连接配置

Oracle 示例，密码应替换为 `dbx secret encrypt` 的结果：

```toml
[connection]
engine = "oracle"
driver = "odbc"
driver_path = "~/.config/dbx/drivers/dbx-driver-oracle-odbc-0.2.0-driver-process.3"
odbc_driver = "/opt/oracle/instantclient/libsqora.dylib.23.1"
host = "127.0.0.1"
port = 1521
user = "app"
database = "FREEPDB1"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
```

SQL Server 可使用：

```toml
[connection]
engine = "sqlserver"
driver = "odbc"
driver_path = "~/.config/dbx/drivers/dbx-driver-sqlserver-odbc-0.2.0-driver-process.3"
odbc_driver = "/opt/microsoft/lib/libmsodbcsql.18.dylib"
host = "db.example.com"
port = 1433
user = "app"
database = "appdb"
password = "dbx-aes-gcm:v2:..."
allow_actions = ["query"]
odbc_options = { Encrypt = "yes", TrustServerCertificate = "no" }
```

达梦配置 `engine = "dm"`，选择 `dbx-driver-dm-odbc-...`，`odbc_driver` 指向厂商提供的库，通常使用端口 5236。厂商对具体系统和架构的支持需自行确认；适配程序可编译不代表厂商提供了对应库。

Windows 的 `driver_path` 指向 `.exe`，`odbc_driver` 指向已通过厂商安装器注册的 DLL，建议用 TOML 单引号保存 Windows 路径。DBX 按当前架构读取注册信息，要求注册项指向所选文件，不写注册表。

两个路径都支持 `~` 和相对于连接 TOML 的路径。`driver_path` 选择 DBX 的 ODBC 适配程序；`odbc_driver` 选择厂商库。清单会校验引擎、适配类型、平台、协议和配套文件摘要；不会因连接失败自动切换到 Go 驱动或另一版本。

省略 `driver` 或设置为 `native` 使用原有 Go 路线。ODBC 配置禁止原始 `dsn`、`dsn_env`，`odbc_options` 不允许覆盖密码、用户名、驱动选择或已定义的结构化连接字段。`Address`、`Addr` 等目标别名和自动语句重试选项 `RetryExec` 同样禁止。只把非凭据选项放入 `odbc_options`。

```bash
dbx conn test local-oracle
dbx query local-oracle 'select 1 from dual'
```

ODBC 同样遵守动作/表权限、单语句限制、事务和导入影响行数限制。超时可终止子进程；写操作中断仍需查询确认提交结果，不自动重试。NUMERIC/DECIMAL 以字符串返回以保留精度，二进制列在 JSON 中按 Base64 输出。原始厂商报错可能带凭据，因此对外保守隐藏 ODBC 错误细节。

## 构建

```bash
# macOS/Linux 原生宿主；Mac 构建机需有 unixODBC 和 libtool
scripts/build-odbc-drivers.sh --host
scripts/build-odbc-drivers.sh --host --engine oracle

# Windows x64 可交叉编译，不需要 CGO
scripts/build-odbc-drivers.sh --os windows --arch amd64
```

Mac 可用 `DBX_UNIXODBC_PREFIX` 和 `DBX_LIBTOOL_PREFIX` 指定构建依赖前缀；脚本只修改临时打包副本，保留原始安装库。Linux 需要 unixODBC 开发头文件和库，运行时使用系统库。Windows 本机构建：

```powershell
.\scripts\build-odbc-drivers.ps1 -Engine oracle
```

Native Go 包仍用 `scripts/build-drivers.sh` / `.ps1` 单独构建。ODBC 包和 Native 包有不同文件名，可以安装在同一目录。

实现使用 [alexbrainman/odbc](https://github.com/alexbrainman/odbc)，Mac/Linux 通过 CGO 调用 [unixODBC](https://www.unixodbc.org/)。发布包保留 unixODBC/libltdl 许可证及来源说明，发行者需随发布提供对应源码。真实厂商数据库、服务版本、认证和 TLS 兼容性仍需实际环境验收。
