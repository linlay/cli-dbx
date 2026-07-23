# Beginner Guide

这份文档假设你第一次接触 `dbx`。

## 1. 先理解它

`dbx` 不是传统数据库 GUI，它更像是一个“有安全边界的数据库执行器”。

如果你想看这套边界为什么这样设计、`allow_actions` 和 `tx` 的约束是什么，可以继续看仓库根目录的 [AGENTS.md](../AGENTS.md)。

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

### allow_actions

`allow_actions` 决定这个连接允许做什么。

- 只查数据：`["query"]`
- 改数据：`["query", "update"]`
- 改表：`["query", "schema"]`
- 查、改数据、改表：`["query", "update", "schema"]`

### allow_tables

`allow_tables` 可以进一步限制这个连接能访问哪些表。

- 不写：不限制表
- 只允许用户表：`allow_tables = ["users"]`
- 允许多个表或前缀：`allow_tables = ["users", "orders", "audit_*"]`

## 3. 用 SQLite 入门最简单

先创建默认配置目录：

```bash
mkdir -p ~/.config/dbx
cat > ~/.config/dbx/local-sqlite.toml <<'EOF'
[connection]
engine = "sqlite"
path = "./demo.db"
allow_actions = ["query", "update", "schema"]
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
./dbx schema local-sqlite 'create table users (id integer primary key, name text)'
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
./dbx import file ./users.csv local-sqlite users
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

## 5. 连续动作用 `tx`

如果你只是做一条 SQL，继续用 `query`、`update`、`schema` 就够了。

如果你需要一组有顺序依赖、并且必须一起成功或一起回滚的动作，用 `tx`。它会把整个 plan 放在同一个连接、同一个事务里执行。

最小 `plan.json` 例子：

```json
{
  "steps": [
    {"action": "query", "sql": "select id from users where id = 1"},
    {"action": "update", "sql": "update users set active = 1 where id = 1", "max_rows_affected": 1}
  ]
}
```

执行：

```bash
./dbx tx local-pg --plan ./plan.json
./dbx query local-pg 'select id, active from users where id = 1'
```

记住这几个限制：

- `tx` 目前只支持 `query` 和 `update`
- 不支持把 `schema` 或 `admin` 放进事务计划
- 不支持跨连接事务，也不支持跨多次 CLI 调用保留事务会话

## 6. 如果你用 PostgreSQL 或 MySQL

### PostgreSQL

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
tags = ["dev"]
```

```bash
./dbx secret encrypt
./dbx conn test local-pg
```

无参数的 `secret encrypt` 会隐藏回显地读取数据库密码。也可以由智能体直接传入一次性位置参数：

```bash
dbx secret encrypt '<password>'
```

参数模式会立即返回 v2 TOML 密码行，不提示也不读取 stdin；密码以 `-` 开头时使用 `dbx secret encrypt -- '-password'`。这种方式会让明文进入智能体上下文、Shell history、命令审计和可能的进程参数，只应在接受一次明文暴露时使用。

加密密钥自动保存在当前用户的系统凭据库里，以后运行 `conn test` 或 `query` 不需要再输入口令。生成的 v2 密文只在当前机器和用户下可用，DBX 不提供解密、导出或显示密钥的命令。

在 agent-platform 的 Agent Terminal 中可先用 `which dbx`（Windows Command Prompt 使用 `where dbx`，PowerShell 使用 `Get-Command dbx`）定位后直接运行。通过 ZenMind Desktop 安装时，也可以在控制中心的 Agent Platform 服务详情中查看安装目录，DBX 位于其 `bin/dbx` 或 `bin\dbx.exe`。旧安装包需要在后续发布同步新版 DBX 后才支持位置参数。

### MySQL

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

```bash
./dbx secret encrypt
./dbx conn test local-mysql
```

## 7. 看不懂报错时先排这几个点

### 连接不存在

确认：

- 配置文件是不是 `~/.config/dbx/<name>.toml`
- 你传入的连接名是不是和文件名一致

### 动作不允许

你可能在只读连接上做了写操作，或者连接的 `allow_actions` 没放行当前动作。

### 密码没读到

检查：

- `password` 是否是 `dbx-aes-gcm:v2:...` 格式
- 配置是否从另一台机器复制而来；v2 需要在当前机器重新加密
- 系统凭据库是否可用并已解锁；Linux 需要 Secret Service 登录集合
- 如果是旧 `dbx-aes-gcm:v1:...`，输入的 master passphrase 是否和加密时一致
- 如果还在用明文 `password = "..."`，DBX 会允许但会提示不推荐

### SQLite 查不到库

检查 `path` 是不是你预期的文件位置。  
如果你用相对路径，它是相对于配置文件所在目录，不是相对于命令执行时的当前目录。

## 8. 推荐学习顺序

建议按这个顺序熟悉 `dbx`：

1. 先用 SQLite 跑通
2. 学会 `conn test`
3. 学会 `query` / `update` / `schema` 和 `inspect`
4. 再开始用 `import` 和 `export`
5. 最后再碰 `tx` 和更高权限的 `allow_actions`
