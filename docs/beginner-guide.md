# Beginner Guide

这份文档假设你第一次接触 `dbx`。

## 1. 先理解它

`dbx` 不是传统数据库 GUI，它更像是一个“有安全边界的数据库执行器”。

如果你想看这套边界为什么这样设计、`allow_actions` 和 `tx run` 的约束是什么，可以继续看仓库根目录的 [CLAUDE.md](../CLAUDE.md)。

你可以把它想象成：

- 会连数据库
- 会执行 SQL
- 会拦住明显危险的动作
- 会把结果整理成适合脚本或人类查看的格式

## 2. 你最需要记住的两个概念

### 连接 profile

不要把它想复杂。  
连接 profile 就是一个起了名字的数据库连接，比如：

- `local-sqlite`
- `dev-pg`
- `report-mysql`

这样你以后执行命令只要写：

```bash
./dbx query local-sqlite 'select 1'
```

### mode

`mode` 决定你可以做什么。

- 只查数据：`Lantern`
- 改数据：`Tweezers`
- 改表：`Chisel`

如果你刚开始用，记一个规则就够了：

- 查数据用 `Lantern`
- 导入数据用 `Tweezers`
- 建表改字段用 `Chisel`

## 3. 用 SQLite 入门最简单

先创建默认配置目录：

```bash
mkdir -p ~/.config/dbx
cat > ~/.config/dbx/local-sqlite.toml <<'EOF'
[connection]
engine = "sqlite"
path = "./demo.db"
mode = "Lantern"
allow_actions = ["query"]
tags = ["local"]
EOF
```

测试：

```bash
./dbx conn list
./dbx conn test local-sqlite
```

建表：

```bash
./dbx schema local-sqlite 'create table users (id integer primary key, name text)' --mode Chisel
```

查询：

```bash
./dbx query local-sqlite 'select * from users'
./dbx query local-sqlite 'select * from users order by id' --page-size 100
```

## 4. 导入一个 CSV

准备：

```csv
id,name
1,Ada
2,Linus
```

导入：

```bash
./dbx import file ./users.csv local-sqlite users --mode Tweezers
```

再查一下：

```bash
./dbx query local-sqlite 'select * from users order by id'
```

如果结果超过默认 `100` 行，输出里会给 `data.next_cursor`。继续读下一页时，保持同一条 SQL：

```bash
./dbx query local-sqlite 'select * from users order by id'
./dbx query local-sqlite 'select * from users order by id' --cursor 100
```

## 5. 如果你用 PostgreSQL 或 MySQL

### PostgreSQL

```toml
[connection]
engine = "postgres"
dsn_env = "LOCAL_PG_DSN"
mode = "Lantern"
allow_actions = ["query"]
tags = ["dev"]
```

```bash
export LOCAL_PG_DSN='postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable'
./dbx conn test local-pg
```

### MySQL

```toml
[connection]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "appdb"
password.env = "MYSQL_PASSWORD"
mode = "Lantern"
allow_actions = ["query"]
```

```bash
export MYSQL_PASSWORD='secret'
./dbx conn test local-mysql
```

## 6. 看不懂报错时先排这几个点

### 连接不存在

确认：

- 配置文件是不是 `~/.config/dbx/<name>.toml`
- 你传入的连接名是不是和文件名一致

### 动作不允许

你可能在 `Lantern` 模式下做了写操作，或者连接的 `allow_actions` 没放行当前动作。

### 密码没读到

检查：

- `password.env` 对应的环境变量是否真的存在
- `password.cmd` 是否写成数组
- 文件路径是否可读

### SQLite 查不到库

检查 `path` 是不是你预期的文件位置。  
如果你用相对路径，它是相对于配置文件所在目录，不是相对于命令执行时的当前目录。

## 7. 推荐学习顺序

建议按这个顺序熟悉 `dbx`：

1. 先用 SQLite 跑通
2. 学会 `conn test`
3. 学会 `query` / `update` / `schema` 和 `inspect`
4. 再开始用 `import` 和 `export`
5. 最后再碰 `tx run` 和高权限 mode
