# dbx

## 1. 项目简介

`dbx` 是一个给人类和智能体都能用的 database CLI。它面向 MySQL、PostgreSQL、SQLite，重点解决三件事：

- 用统一方式管理数据库连接
- 用显式命令执行查询、更新、DDL 和导入导出
- 用适合脚本和终端的格式返回结果

如果你要看设计目标、动作边界、事务模型和开发约定，请看 [CLAUDE.md](./CLAUDE.md)。

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

最小 PostgreSQL 例子：

```toml
[connection]
engine = "postgres"
dsn_env = "LOCAL_PG_DSN"
allow_actions = ["query"]
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
password.env = "MYSQL_PASSWORD"
allow_actions = ["query"]
```

密码来源支持：

- `password.env`
- `password.file`
- `password.cmd`
- 明文值

操作层面可以先这样理解：

- `allow_actions` 控制这个连接允许哪些命令动作
- 推荐显式写出最小权限集合，不依赖隐式默认值

如果你要理解 `allow_actions` 和为什么 `tx` 只允许 `query/update`，请看 [CLAUDE.md](./CLAUDE.md)。

也可以直接参考：

- [config.example.toml](./testdata/config.example.toml)
- [config.sqlite.toml](./testdata/config.sqlite.toml)

## 4. 发布与分发

如果你是普通使用者，优先从 GitHub Releases 下载对应平台压缩包：

- macOS Apple Silicon：`dbx_vX.Y.Z_darwin_arm64.tar.gz`
- macOS Intel：`dbx_vX.Y.Z_darwin_amd64.tar.gz`
- Linux ARM64：`dbx_vX.Y.Z_linux_arm64.tar.gz`
- Linux AMD64：`dbx_vX.Y.Z_linux_amd64.tar.gz`

解压后可直接验证：

```bash
tar -xzf dbx_v0.1.0_darwin_arm64.tar.gz
./dbx version
./dbx conn --help
```

维护者的构建、打包、发布流程见 [CLAUDE.md](./CLAUDE.md)。

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
- 密码没读到：检查 `password.env`、`password.file`、`password.cmd`
- SQLite 路径不对：相对路径是相对于配置文件目录，不是当前工作目录

## 6. 进一步阅读

- [CLAUDE.md](./CLAUDE.md)
  设计与开发约定
- [Beginner Guide](./docs/beginner-guide.md)
  第一次上手
- [config.example.toml](./testdata/config.example.toml)
  配置示例
- [config.sqlite.toml](./testdata/config.sqlite.toml)
  SQLite 示例
