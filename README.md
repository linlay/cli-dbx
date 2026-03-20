# dbx

`dbx` 是一个给智能体和人类都能用的 database CLI。  
第一版重点支持 MySQL、PostgreSQL、SQLite，强调 4 件事：

- 连接配置清晰
- 执行边界明确
- 输出对大模型友好
- 新手也能快速跑起来

## 1. 它能做什么

当前已经支持：

- `conn`：查看、解析、测试连接
- `inspect`：查看 schema、表结构、连接信息
- `query`：执行 SQL
- `export`：导出表数据
- `import`：导入 CSV / JSON
- 模式系统：`Lantern`、`Tweezers`、`Chisel`、`Forge`、`Crown`、`Wildfire`
- 输出格式：`table`、`json`、`jsonl`、`llm`

## 2. 先编译

如果你已经装好了 Go，可以直接在仓库根目录运行：

```bash
go build -o ./dbx ./cmd/dbx
```

运行测试：

```bash
go test ./...
```

如果你当前网络连不上 `proxy.golang.org`，可以临时这样跑：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn go test ./...
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn go build -o ./dbx ./cmd/dbx
```

## 3. 5 分钟跑起来

最简单的体验方式是先用 SQLite。

### 第一步：创建配置目录

```bash
mkdir -p ~/.dbx
```

### 第二步：写一个最小配置

把下面内容写到 `~/.dbx/config.toml`：

```toml
default_connection = "local-sqlite"

[connections.local-sqlite]
engine = "sqlite"
path = "./demo.db"
mode = "Lantern"
tags = ["local"]
```

也可以直接参考现成示例：

- [config.example.toml](/Users/linlay/Server/zenmind/testdata/config.example.toml)
- [config.sqlite.toml](/Users/linlay/Server/zenmind/testdata/config.sqlite.toml)

### 第三步：测试连接

```bash
./dbx conn list
./dbx conn test local-sqlite
```

### 第四步：建表

`Lantern` 是只读模式，不能改表，所以这里要切到 `Chisel`：

```bash
./dbx query \
  --conn local-sqlite \
  --mode Chisel \
  --require-ack \
  --sql 'create table users (id integer primary key, name text)'
```

### 第五步：导入 CSV

先准备一个文件 `users.csv`：

```csv
id,name
1,Ada
2,Linus
```

再导入：

```bash
./dbx import file users.csv \
  --conn local-sqlite \
  --into users \
  --mode Tweezers
```

### 第六步：查询

```bash
./dbx query --conn local-sqlite --sql 'select * from users order by id'
./dbx query --conn local-sqlite --sql 'select * from users order by id' --format llm
./dbx inspect table --conn local-sqlite users
```

### 第七步：导出

`export` 会把真正的数据写到文件里，所以必须带 `--out`：

```bash
./dbx export table users \
  --conn local-sqlite \
  --format csv \
  --out users-export.csv
```

## 4. 配置文件怎么写

默认配置文件位置：

```bash
~/.dbx/config.toml
```

最常见的结构是：

```toml
default_connection = "local-pg"

[connections.local-pg]
engine = "postgres"
dsn_env = "LOCAL_PG_DSN"
mode = "Lantern"
tags = ["dev", "local"]
```

一个连接 profile 至少需要这些字段：

- `engine`
- `mode`
- 一组可用的连接信息

连接信息有两种写法。

### 写法 1：直接给 DSN

```toml
[connections.local-pg]
engine = "postgres"
dsn = "postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable"
mode = "Lantern"
```

### 写法 2：结构化字段

```toml
[connections.local-mysql]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "appdb"
password.env = "MYSQL_PASSWORD"
mode = "Lantern"
```

SQLite 也用同一套 profile：

```toml
[connections.local-sqlite]
engine = "sqlite"
path = "./demo.db"
mode = "Lantern"
```

## 5. 密码怎么放

推荐顺序：

1. `env`
2. `file`
3. `cmd`
4. 明文值

### 环境变量

```toml
password.env = "MYSQL_PASSWORD"
```

### 文件

```toml
password.file = "~/.secrets/mysql_password"
```

### 命令输出

```toml
password.cmd = ["printenv", "MYSQL_PASSWORD"]
```

注意：

- `cmd` 必须是数组形式，不能写成一整段 shell
- `dbx` 只会读取标准输出
- 命令失败会直接报错

## 6. 模式怎么选

如果你把模式理解成“安全开关”，就很容易上手。

| 模式 | 适合做什么 | 是否可写 |
| --- | --- | --- |
| `Lantern` | 查结构、查数据、做分析 | 否 |
| `Tweezers` | 精细改数据 | 是，不能改表 |
| `Chisel` | 改表结构 | 是，主要是 DDL |
| `Forge` | 迁移、批量施工 | 是 |
| `Crown` | 库级治理、角色、配置 | 是，高风险 |
| `Wildfire` | 不受限 | 是，最高风险 |

最常见用法：

- 日常查询：`Lantern`
- 导入数据：`Tweezers`
- 建表改字段：`Chisel`

如果是高风险动作，通常要显式带上：

```bash
--require-ack
```

## 7. 最常用命令

### 看有哪些连接

```bash
./dbx conn list
```

### 看某个连接解析后的结果

```bash
./dbx conn show local-sqlite
./dbx conn resolve local-sqlite
```

### 测试连接是否可用

```bash
./dbx conn test local-sqlite
```

### 执行 SQL

```bash
./dbx query --conn local-sqlite --sql 'select * from users'
```

也可以从文件读 SQL：

```bash
./dbx query --conn local-sqlite --file ./query.sql
```

### 查看表结构

```bash
./dbx inspect table --conn local-sqlite users
```

### 查看 schema 和表列表

```bash
./dbx inspect schema --conn local-sqlite
```

### 导入 CSV

```bash
./dbx import file ./users.csv --conn local-sqlite --into users --mode Tweezers
```

### 导出 CSV

```bash
./dbx export table users --conn local-sqlite --format csv --out ./users.csv
```

## 8. 输出格式怎么选

- `table`：给人快速看
- `json`：最通用
- `jsonl`：一行一个 JSON，适合流处理
- `llm`：适合智能体和大模型消费

例如：

```bash
./dbx query --conn local-sqlite --sql 'select * from users' --format table
./dbx query --conn local-sqlite --sql 'select * from users' --format json
./dbx query --conn local-sqlite --sql 'select * from users' --format llm
```

`llm` 输出会尽量保留：

- 列信息
- 结果摘要
- 样本值
- 风险等级
- 审计 ID

## 9. 环境变量支持

除了配置文件，也支持环境变量：

- `DBX_DSN`
- `DBX_ENGINE`
- `DBX_CONN`

优先级大致是：

1. 命令行显式参数
2. 环境变量
3. 配置文件默认连接

例如：

```bash
DBX_CONN=local-sqlite ./dbx query --sql 'select 1'
```

## 10. 新手常见问题

### 为什么提示 mode 不允许？

因为默认模式通常是 `Lantern`，它只能读，不能写。  
如果你在建表、导入、更新数据，需要显式切到更高模式。

### 为什么导出一定要 `--out`？

因为 `dbx` 会同时输出审计 envelope。  
如果导出内容也直接打到标准输出，二者会混在一起，不方便后续处理。

### 为什么生产连接更严格？

连接名或标签里带 `prod` 时，`dbx` 会默认启用更保守的行为，避免误操作。

### 为什么我的密码配置不生效？

先优先检查：

- 环境变量名是否真的存在
- `password.cmd` 是否写成了数组
- 文件路径是否正确

## 11. 进一步阅读

- [Beginner Guide](/Users/linlay/Server/zenmind/docs/beginner-guide.md)
- [config.example.toml](/Users/linlay/Server/zenmind/testdata/config.example.toml)
- [config.sqlite.toml](/Users/linlay/Server/zenmind/testdata/config.sqlite.toml)

