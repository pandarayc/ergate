# Ergate 长期迭代规范

> 本文档定义 ergate 的路径规范、代码约定、功能优先级和长期演进策略。
> 每个新功能或重构都应符合本文档的标准。

---

## 1. 目录结构与路径规范

### 1.1 项目顶层

```
ergate/
├── cmd/ergate/           # 入口（仅 main.go，极其薄）
├── internal/             # 所有业务逻辑
├── .ergate/              # 运行时数据（file-history, sessions, tool-results）
├── .planning/            # 设计文档、intel、roadmap
├── .worktrees/           # git worktree 隔离环境
├── .vscode/              # IDE 配置
├── ARCHITECTURE.md       # 架构总览（与代码同步）
├── CONTRIBUTING.md       # ← 本文件
├── config.example.yaml   # 完整配置示例
├── go.mod / go.sum
└── README.md
```

### 1.2 `internal/` 包命名规范

| 路径 | 职责 | 依赖方向 |
|------|------|----------|
| `internal/engine/` | 核心循环：chat → tools → chat | 依赖 tool, llm, prompt, compact, config |
| `internal/llm/` | Provider 抽象 + API 客户端 | 无 internal 依赖 |
| `internal/tool/` | 工具接口 + 注册表 + 内置工具 | 无 internal 依赖 |
| `internal/task/` | 后台任务（bash + sub-agent） | 依赖 tool, engine |
| `internal/prompt/` | 系统 prompt 组装 | 依赖 memory, skill, config |
| `internal/compact/` | 上下文压缩 | 依赖 llm, config |
| `internal/mcp/` | MCP 协议客户端 | 依赖 tool |
| `internal/memory/` | 持久记忆 | 无 internal 依赖 |
| `internal/skill/` | 技能系统 | 无 internal 依赖 |
| `internal/config/` | YAML + 环境变量配置 | 无 internal 依赖 |
| `internal/hooks/` | 工具生命周期回调 | 依赖 tool |
| `internal/filehistory/` | 文件修改自动备份 | 无 internal 依赖 |
| `internal/worktree/` | Git worktree 管理 | 无 internal 依赖 |
| `internal/session/` | 会话持久化 | 依赖 llm |
| `internal/planmode/` | Plan/Implement 状态机 | 依赖 tool |
| `internal/cachestability/` | Prefix Cache 指纹 | 无 internal 依赖 |
| `internal/tui/` | Bubbletea 终端 UI | 依赖 engine（通过 Event channel） |
| `internal/cli/` | CLI 层 + REPL 命令 | 依赖所有其他 internal |
| `internal/util/` | Markdown 终端渲染等 | 无 internal 依赖 |

**规则**：
- 禁止循环依赖。上层（cli）可依赖下层，下层不可依赖上层。
- `internal/` 内包之间通过接口依赖，而非具体类型。
- Engine 是唯一"编排者"——它持有对其他包的引用，其他包不应反向引用 engine。

### 1.3 文件命名规范

```
internal/<package>/
├── <name>.go            # 核心类型与接口定义
├── <name>_test.go       # 单元测试
├── <submodule>.go       # 子模块拆分
└── <submodule>_test.go  # 子模块测试
```

**命名规则**：
- Go 文件：小写蛇形（`chat_render.go`, `commands_help.go`）
- 测试文件：`_test.go` 后缀
- 一个 package 内按职责拆文件，而非按类型拆
- 文件头 3-5 行注释描述该文件职责

---

## 2. 代码规范

### 2.1 通用 Go 风格

- 遵循 **[Go 官方 Code Review Comments](https://go.dev/wiki/CodeReviewComments)**
- 使用 `gofmt` / `goimports` 格式化（CI 检查）
- 错误处理：始终检查 `err`，不吞错误
- 私有命名：`camelCase`，导出：`CamelCase`
- 接口命名：`-er` 后缀（`Reader`, `Logger`, `ProviderAdapter`）

### 2.2 包内组织

每个包的公开 API 应遵循：
1. **类型定义**（结构体 + 接口）
2. **构造函数**（`New*`）
3. **方法实现**
4. **内部辅助函数**

```go
// Package tool defines the Tool interface and built-in tool implementations.
package tool

// Tool is the interface every tool must implement.
type Tool interface { ... }

// BaseTool provides a partial implementation of Tool.
type BaseTool struct { ... }

// NewBaseTool creates a new BaseTool.
func NewBaseTool(...) BaseTool { ... }

// Schema builds a JSON Schema from properties.
func Schema(...) json.RawMessage { ... }
```

### 2.3 测试规范

**测试文件位置**：与源码同目录，`<name>_test.go`

**测试类型**：

| 类型 | 命名约定 | 用途 | 示例 |
|------|----------|------|------|
| 单元测试 | `TestXxx` | 隔离测试单个函数/方法 | `TestEngineTextOnlyResponse` |
| Golden 测试 | `TestXxx_Golden` | 快照比对 | `TestChatModel_Golden` |
| 集成测试 | `TestXxx_Integration` | 跨包协作 | `TestToolIntegration` |
| 表驱动测试 | `TestXxx` (table) | 多 case 场景 | `TestRead_OffsetLimit` |

**规则**：
- 每个 package 至少有一个 `TestXxx` 覆盖核心逻辑
- Mock 接口而非具体实现（见 `internal/engine/engine_test.go` 的 `mockLLMClient`）
- 测试不依赖外部 API（禁止调用真实 LLM）
- 使用 `t.Run()` 子测试组织 case
- Golden 文件路径：`internal/<pkg>/testdata/<name>.golden`

### 2.4 错误处理

- 工具执行错误：返回 `ToolResult{Success: false, Content: "..."}`，而非 `error`
- 引擎级错误：通过 `EventError` 事件通知
- `panic` 必须 recover（`safeExecute` 模式）

### 2.5 日志规范

- 使用 `log/slog`（标准库结构化日志）
- 级别约定：
  - `slog.Info` — 用户可见的重要事件（session start/end, tool executed）
  - `slog.Warn` — 可恢复的异常（retry, fallback）
  - `slog.Error` — 不可恢复的错误
  - `slog.Debug` — 调试信息（受 `ERGATE_DEBUG` 控制）
- 日志结构：键值对，key 使用蛇形（`tool_name`, `duration_ms`）

---

## 3. 功能迭代优先级

### 3.1 当前状态评估

```
稳定性:  ████████░░  70% (核心循环可用，边角有已知 bug)
测试覆盖: ██░░░░░░░░  20% (仅9个测试文件)
TUI体验:  ██████░░░░  60% (基础可用，交互待增强)
可观测性: █░░░░░░░░░  10% (slog 已引入但未正式使用)
CI:       ░░░░░░░░░░   0% (无 CI)
```

### 3.2 建议实施路线

#### Phase A — 基础设施巩固（当前优先，2-4 周）

```
P-A1: 已知 Bug 修复
├── A.2 HTTP 400 — tool result content encoding（HEADLESS 阻塞项）
├── 调试文件清理（/tmp/ergate_req_body.json, /tmp/ergate_task_*.out）
└── 硬编码模型 cost 前缀修复（TUI cost estimate）

P-A2: 可观测性（Phase 4 from roadmap）
├── Turn Metrics（engine/singleTurn 前后埋点）
├── Tool Tracing（executeTools 前后埋点）
└── Session 摘要（Run() defer 输出）

P-A3: TUI Phase 2 重构
├── 文件拆分（chat.go → view + update + events）
└── 2.2 布局修复（header 固定，viewport 仅滚动 messages）

P-A4: 测试覆盖
├── engine：核心循环 + 事件流
├── llm：mock provider adapter
├── tool：每个内置工具至少一个测试
└── config：加载 + 合并 + 默认值
```

#### Phase B — 质量建设（2-3 周）

```
P-B1: CI 流水线
├── GitHub Actions：go build + go vet + go test
├── golangci-lint（集成到 CI + 编辑器）
├── 测试覆盖率阈值（≥40%）
└── golden test 自动更新（`go test -update`）

P-B2: 功能增强
├── /diff 和 /commit 命令（自动 commit message 生成）
├── Sub-agent 独立上下文（不污染主 agent 消息历史）
├── Tool 展开/折叠（Enter 展开 tool 完整输出）
└── MCP 多 server 管理

P-B3: 大仓库性能
├── 1000+ 文件 repo 性能基线
├── Context compaction 策略验证（500K/800K 阈值）
└── Token 预算优化（cache 命中率提升）
```

#### Phase C — 差异化能力（按需推进）

```
P-C1: 开发体验增强
├── LSP 集成（goToDefinition / findReferences）
├── Vim 键绑定（可选 modal editing）
├── Go 专用工具（go vet, golangci-lint runner）
└── Session 管理（list, search, import/export）

P-C2: Engine 解耦（Phase 5）
├── Event channel 泛化（不绑定 TUI 类型）
├── Engine 生命周期独立（支持 headless 完整流程）
├── InputSource / OutputSink 接口化
└── HTTP API（ergate daemon 子命令）

P-C3: Long-Running Agent
├── 多事件源（Webhook、文件监听、MCP）
├── 持久化任务队列
├── 定时任务（cron-like）
└── 断线重连
```

### 3.3 功能优先级矩阵

| 功能 | 价值 | 成本 | 优先级 | 备注 |
|------|------|------|--------|------|
| HTTP 400 修复 | 🔥高 | ⭐低 | P0 | HEADLESS 阻塞项 |
| Test 覆盖 | 🔥高 | ⭐低 | P0 | 质量基石 |
| CI 流水线 | 🔥高 | ⭐低 | P0 | 增量质量保证 |
| Turn Metrics | 🔥高 | ⭐低 | P1 | 问题排查基础 |
| /diff + /commit | 🔥高 | ⭐⭐中 | P1 | 高频率使用 |
| Sub-agent 独立上下文 | 🔥高 | ⭐⭐中 | P1 | 核心差异能力 |
| Tool 展开/折叠 | ⭐中 | ⭐低 | P1 | TUI 体验 |
| LSP 集成 | ⭐中 | ⭐⭐⭐高 | P2 | Claude Code 对标 |
| Vim 键绑定 | ⭐中 | ⭐⭐中 | P2 | 个人偏好 |
| HTTP API (daemon) | ⭐中 | ⭐⭐⭐高 | P2 | 需要明确需求 |
| Long-Running Agent | ⭐⭐高 | ⭐⭐⭐⭐极高 | P3 | 需要彻底设计 |

---

## 4. 配置管理规范

### 4.1 配置层级（优先级递减）

```
1. 命令行参数（--flag）
2. 环境变量（ERGATE_*）
3. 项目级配置（.ergate/config.yaml）
4. 用户级配置（~/.config/ergate/config.yaml）
5. 默认值（config.DefaultConfig()）
```

### 4.2 新增配置项流程

```go
// 1. 在 config/defaults.go 中添加默认值
const DefaultMaxTokens = 8192

// 2. 在 config/config.go 中添加结构体字段（带 yaml + mapstructure tag）
type Config struct {
    MaxTokens int `yaml:"max_tokens" mapstructure:"max_tokens"`
}

// 3. 在 config.example.yaml 中添加文档示例
// 4. 如果有环境变量，在 loader.go 中添加绑定
```

### 4.3 环境变量命名

```
ERGATE_<SECTION>_<KEY>    # 全大写，下划线分隔
ERGATE_API_KEY            # ✅
ERGATE_PROVIDER_API_KEY   # 如果有多 provider
ERGATE_DEBUG              # ✅ 已存在
```

---

## 5. Git 工作流

### 5.1 分支策略

```
main          — 稳定分支，只接受 PR merge
<feature>     — 功能分支（源自 main）
fix/<bug>     — 修复分支
refactor/*    — 重构分支
worktree/*    — 使用 EnterWorktree 工具创建的实验分支
```

### 5.2 Commit 规范

```
<type>: <简短描述>

<详细说明（可选）>
```

**类型**：
- `feat:` — 新功能
- `fix:` — Bug 修复
- `refactor:` — 重构（功能不变）
- `test:` — 测试相关
- `docs:` — 文档
- `chore:` — 构建/工具/CI
- `perf:` — 性能优化

**示例**：
```
feat: add /resume command, fix indentation in existing commands
fix: visualLineCount now uses ansi.Wordwrap to match prewrapContent
refactor: extract FoldToggle component, unify fold bar across chat and toolbar
```

### 5.3 Worktree 使用场景

- 涉及时 EnterWorktree 工具自动创建
- 实验性/高风险修改
- 频繁在 TUI 开发和 ergate 自身开发间切换时

---

## 6. 测试策略

### 6.1 各层测试要求

| 层 | 单元测试 | 集成测试 | Golden 测试 |
|----|----------|----------|-------------|
| `tool/` | ✅ 每个工具 | ✅ Execute + registry | ❌ |
| `engine/` | ✅ 核心循环 | ✅ 多 turn + tool use | ❌ |
| `llm/` | ✅ 每个 adapter | ❌（需 mock） | ❌ |
| `config/` | ✅ 加载与合并 | ❌ | ❌ |
| `tui/` | ✅ 每个组件 | ❌ | ✅ 快照 |
| `cli/` | ✅ 每个 command | ✅ 跨包 | ❌ |
| `compact/` | ✅ 压缩逻辑 | ❌ | ❌ |
| `session/` | ✅ 存储与恢复 | ❌ | ❌ |
| `task/` | ✅ 任务管理 | ✅ bash subprocess | ❌ |

### 6.2 Mock 策略

```
LLM 层  → mockLLMClient（已存在 engine_test.go 典范）
工具层  → echoTool（已存在典范）
文件系统 → os.ReadFile/WriteFile 直测（工具层职责）
配置层  → config.DefaultConfig() + 覆盖
```

### 6.3 测试命令

```bash
# 所有测试
go test ./...

# 带覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# golden 测试更新
go test ./internal/tui/ -update

# race 检测
go test -race ./...
```

---

## 7. 新增功能流程

1. **讨论**：在 Issue 或 .planning 中记录需求
2. **设计**：更新 ARCHITECTURE.md 或新增 .planning/ 文档
3. **接口先行**：定义接口，再实现
4. **测试先行**：编写测试用例
5. **实现**：功能开发
6. **文档**：更新 README.md + config.example.yaml（如涉及配置）
7. **清理**：删除调试代码，检查 /tmp/ 写入

### 新增工具流程

```go
// 1. internal/tool/<name>.go — 实现 Tool 接口
// 2. internal/tool/builtins.go — Register() 注册
// 3. internal/tool/<name>_test.go — 测试
// 4. internal/llm/message.go — 确认 ToolUse 类型兼容
// 5. README.md + 工具列表更新
```

### 新增 Provider 流程

```go
// 1. internal/llm/<provider>.go — 实现 LLMClient 接口
// 2. internal/llm/<provider>_adapter.go — 实现 ProviderAdapter
// 3. internal/config/config.go — 配置结构体扩展
// 4. config.example.yaml — 配置示例
// 5. internal/cli/bridge.go — provider 工厂注册
```

---

## 8. 已知技术债务

| # | 债务 | 影响 | 建议解决时机 |
|---|------|------|--------------|
| 1 | `/tmp/ergate_req_body.json` 调试文件残留 | 安全性、磁盘污染 | Phase A-1 |
| 2 | model cost 硬编码前缀匹配 | 自定义模型 cost 不准确 | Phase A-1 |
| 3 | TUI `chat.go` 文件过大（~500+ 行） | 可维护性 | Phase A-3 |
| 4 | 无 CI | 合并风险 | Phase B-1 |
| 5 | 无 lint 检查 | 代码风格不一致 | Phase B-1 |
| 6 | Engine 与 TUI 紧耦合（Event channel 类型绑定） | 扩展受限 | Phase C-2 |
| 7 | `.planning/` 与代码不同步风险 | 文档腐化 | 持续维护 |

---

## 9. 发布流程

```
1. 确认 main 分支所有测试通过
2. 更新 ARCHITECTURE.md 和 README.md（如功能变更）
3. 更新版本号（git tag）
4. 构建：
   go build -ldflags="-X main.Version=v0.x.y" ./cmd/ergate/
5. Release notes 记录：
   - 新增功能
   - 修复的 bug
   - 破坏性变更
   - 升级说明
```

---

## 10. 长期愿景

```
2026-Q2: 稳定期
├── 核心循环稳定，已知 bug 清零
├── CI 绿，测试覆盖率 ≥50%
└── TUI 体验完整（展开/折叠/选择/复制）

2026-Q3: 差异化
├── /diff + /commit 高频使用
├── Sub-agent 独立上下文
├── LSP 集成基础能力
└── 大仓库（1000+ 文件）性能达标

2026-Q4: 架构演进
├── Engine 解耦（可 headless 完整运行）
├── HTTP API 层（可选激活）
└── 多事件源（Webhook / 文件监听）

2027+: 虫群意识
├── 上层规划层（高维任务分解）
├── 子智能体池（并发协作）
└── 自评估/自修复循环
```

---

*本文档随项目迭代持续更新。每次新增功能或重构后，检查是否需要对本文档进行同步更新。*
