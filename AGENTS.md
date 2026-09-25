# AGENTS.md

## 1. 项目概览

`dbx` 是一个带安全边界的 database CLI，不是任意 SQL 壳。它的目标是让人类和智能体都能在统一命令面下访问数据库，同时把高风险动作限制在 DBX 自身的控制面里。

当前核心能力：

- 连接管理：`conn list / show / test`
- 结构查看：`inspect schema / table / connection`
- 显式 SQL 动作：`query / update / schema / admin`
- 文件导入导出：`import` / `export`
- 批处理事务：`tx`

设计重点：

- 显式动作优先，减少大模型误用
- DBX 层能力白名单，独立于数据库账号权限
- 单语句默认、事务批处理明确、输出结构化

## 2. 技术栈

- 语言：Go
- CLI：标准库 `flag`
- 数据库访问：`database/sql`
- 驱动：
  - PostgreSQL：`pgx`
  - MySQL：`go-sql-driver/mysql`
  - SQLite：`modernc.org/sqlite`
- 配置格式：TOML
- 构建与测试：`go build`、`go test`
- 发布：仓库内 `scripts/release/build.sh`

## 3. 架构设计

模块分层大致如下：

- `cmd/dbx`
  CLI 入口，仅负责进程启动和顶层错误退出。
- `internal/cli`
  命令分发、参数解析、帮助信息、错误 envelope。
- `internal/config`
  单连接配置文件加载、路径归一化、密码来源解析。
- `internal/secret`
  v1/v2 密文处理、系统凭据库适配和运行时密钥解析。
- `internal/conn`
  把配置解析成可执行的连接规格 `Spec`。
- `internal/action`
  动作模型：`query / update / schema / admin`。
- `internal/sqlclass` / `internal/sqlanalyzer`
  SQL 分类、单语句校验、危险写入检测。
- `internal/db`
  具体数据库执行、导入导出、事务计划执行。
- `internal/output`
  结构化输出、摘要、表格输出。

核心调用链：

1. CLI 解析命令与参数
2. 连接配置解析为 `conn.Spec`
3. SQL 被分类为 statement class，再映射到 action
4. DBX 先做 `allow_actions` 和安全策略校验
5. 校验通过后才进入数据库执行层

## 4. 目录结构

- `cmd/dbx`
  可执行程序入口
- `internal/`
  核心实现
- `docs/`
  面向用户的补充文档
- `testdata/`
  配置示例
- `scripts/release/`
  打包与发布脚本

文档职责固定为：

- `README.md`
  操作手册、快速开始、简单验证
- `docs/beginner-guide.md`
  新手第一次上手
- `AGENTS.md`
  设计、重要功能、开发约定、维护流程

## 5. 数据结构

### 连接配置

根结构是单连接文件：

```toml
[connection]
engine = "postgres|mysql|sqlite"
allow_actions = ["query", "update", "schema", "admin"]
```

`allow_actions` 是 DBX 层保护，不依赖数据库账号本身的授权能力。

### 密码加密

- `dbx-aes-gcm:v2:<key-id>:...` 是默认格式。数据库密码保存在配置密文中，每条密文的随机 AES-256-GCM 密钥保存在当前用户的系统凭据库。
- 系统凭据库固定使用 macOS Keychain、Windows Credential Manager 或 Linux Secret Service，不允许降级到环境变量、命令或密钥文件。
- v2 运行时静默解密；密钥缺失、凭据库不可用或密文损坏都必须失败关闭。
- `dbx-aes-gcm:v1:...` 仅为兼容保留，读取时仍由 passphrase provider 获取旧 master passphrase。
- CLI 只提供 `dbx secret encrypt`，不得增加 decrypt、export、show-key 等明文或密钥输出入口。
- `dbx secret encrypt` 接受零个或一个位置参数：零参数隐藏回显地读取密码，一个参数直接加密且不得读取 stdin。位置参数必须原样处理，不得 trim 或写入 DBX 输出与错误。
- 位置参数模式只用于接受一次明文暴露的迁移场景；明文会进入调用智能体、Shell history、命令审计或进程参数，不能宣称该模式对模型保密。
- v2 的保护目标是避免配置文件直接泄露明文，不抵御同一操作系统用户主动读取系统凭据库或进程内存。

### 动作模型

- `query`
  只读 SQL
- `update`
  DML，如 `insert / update / delete / merge`
- `schema`
  DDL，如 `create / alter / drop / truncate / rename`
- `admin`
  管理动作，如 `grant / revoke / set / vacuum`

### 事务计划

`tx` 读取 JSON 计划文件：

```json
{
  "steps": [
    {"action":"query","sql":"select id from users where id = 1"},
    {"action":"update","sql":"update users set active = 1 where id = 1","max_rows_affected":1}
  ]
}
```

`tx` v1 只允许 `query` 和 `update` 步骤。

### 输出 Envelope

对外返回统一 envelope，关键字段包括：

- `kind`
- `action`
- `class`
- `summary`
- `data`
- `next`
- `warnings`

`action` 是稳定的外部语义；`statement_class` 仍然保留作兼容信息。

## 6. API 定义

`dbx` 的主要外部接口是 CLI，而不是 HTTP API。

核心命令面：

- `dbx conn ...`
- `dbx inspect ...`
- `dbx query <conn> '<sql>'`
- `dbx update <conn> '<sql>'`
- `dbx schema <conn> '<sql>'`
- `dbx admin <conn> '<sql>'`
- `dbx tx <conn> --plan <path.json>`
- `dbx import file ...`
- `dbx export table ...`
- `dbx secret encrypt`

接口约定：

- 默认只允许单语句 SQL
- `query/update/schema/admin` 要求 SQL 类型和命令动作匹配
- `tx` 为单连接、单次调用、单事务

错误语义重点：

- `action_blocked`
  当前连接未放行该动作
- `action_mismatch`
  命令动作和 SQL 类型不一致
- `multiple_statements_blocked`
  一次传入了多语句
- `missing_where`
  危险写入缺少 `WHERE`
- `tx_unsupported_action`
  事务步骤使用了 `schema` 或 `admin`

## 7. 开发要点

- 新增数据库能力时，先明确它属于哪个 action，再补 CLI 和帮助文案。
- 不要把设计解释重新堆进 `README.md`；设计说明统一维护在 `AGENTS.md`。
- `README.md` 只保留操作说明、配置示例、简单测试和排查。
- `docs/beginner-guide.md` 只讲新手视角，不承载架构事实。
- `allow_actions` 是 DBX 层最重要的能力边界之一，任何变更都必须补测试。
- `tx` 的边界优先保持简单和可验证，不要直接扩展到跨进程会话事务。
- 对外词汇优先使用 `query / update / schema / admin / tx`，保持动作边界清晰。
- 用户可见能力变更需要同步更新：
  - CLI help
  - `README.md`
  - 相关测试

## 8. 开发流程

本地开发常用命令：

```bash
go build -o ./dbx ./cmd/dbx
go test ./...
```

修改命令或输出时，至少检查：

```bash
./dbx --help
./dbx query --help
./dbx tx --help
```

发布流程当前由维护者手工执行：

1. 将根目录 Git 跟踪的 `VERSION` 更新为计划发布的版本（格式如 `v0.1.0`），并与代码和文档一并提交
2. 运行 `go test ./...`
3. 创建并推送与 `VERSION` 相同的 tag，例如 `v0.1.0`
4. 执行 `scripts/release/build.sh`
5. 校验 `dist/<version>/..._checksums.txt`
6. 在 GitHub Release 上传产物

如果文档分工变化：

- 设计和约定改 `AGENTS.md`
- 用户操作和快速验证改 `README.md`
- 新手入门体验改 `docs/beginner-guide.md`

## 9. 已知约束与注意事项

- `dbx` 不是数据库权限系统的替代品；它是在数据库账号之外再加一层 DBX 侧保护。
- `allow_actions` 只能收紧 DBX 的能力边界，不能放大数据库账号本身没有的权限。
- `tx` 不支持跨连接事务，也不支持跨多次 CLI 调用的事务会话。
- `tx` 不支持 `schema` 或 `admin`，主要是为了保持回滚语义可预期。
- 生产态连接默认更保守；带 `prod` 特征的连接会阻止高风险动作配置。
- v2 密文绑定创建它的机器和操作系统用户，复制配置到其他机器后必须重新加密。
- Linux v2 依赖可用且已解锁的 Secret Service 登录集合；不可用时不回退到其他密码来源。


## Platform 连接器发布包

项目位于 `agent-platform-connectors/dbx`，`connector/` 是 connector.json 模板、cli.json、skills 与全部资源的唯一源码。`VERSION` 同时控制 CLI 和连接器发布版本；模板不维护 version，构建时生成。任何 CLI、清单或技能改动均需发布新版本。

现有 Shell/PowerShell 发布脚本额外生成 `dist/<version>/builtin.dbx_<version>_<os>_<arch>.zip`，内容位于 `runtime/connectors/builtin.dbx/`，包含声明、完整 skills 和目标平台 bin。Platform 按完整包摘要消费，不修改包内内容。原 CLI 独立发布包继续提供。包生成器检查 VERSION 满足 cli.json.minVersion（包含 SemVer 预发布版本比较）。
