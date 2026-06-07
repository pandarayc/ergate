# TUI Roadmap

## Phase 1 — 快速修复（体验立即可感知） ✅

- [x] **1.1 Status Bar 增强** — 加入上下文占比%、花费估算、plan mode 标记、session ID
- [x] **1.2 Spinner 上下文** — 显示当前工具名："⠹ ReadTool..."
- [x] **1.3 权限对话框修复** — lipgloss 动态宽度 + 加"Always Deny"选项
- [x] **1.4 消息分隔** — user 消息前加空行，assistant 块加左边框色条
- [x] **1.5 Diff 渲染** — Edit 结果用绿色+/红色-（已有 DiffStyles 直接用）
- [x] **1.6 欢迎页** — 启动显示版本 + 快捷键 + 当前模型
- [x] **1.7 输入历史** — Ctrl+P/Ctrl+N 上下切换历史输入
- [x] **1.8 Tool 截断改进** — 加"(expand with Enter)"提示

## Phase 2 — 结构重构

- [ ] **2.1 文件拆分** — model.go(517行) → model.go + view.go + update.go + commands.go
- [ ] **2.2 布局修复** — Header 固定顶部，viewport 仅滚动 messages
- [ ] **2.3 命令统一** — TUI 复用 cli.CommandRegistry，删除重复 switch-case
- [ ] **2.4 流式优化** — strings.Builder 替代 += 拼接

## Phase 3 — 交互增强

- [ ] **3.1 Tool 展开/折叠** — Enter 键 expand 完整 tool 输出
- [ ] **3.2 多行输入** — Alt+Enter 换行，Enter 发送
- [ ] **3.3 消息虚拟化** — 大量消息时只渲染可见区域

## Phase 4 — 可观测性

### 4.1 Turn 级 Metrics（slog 结构化日志）

每轮 `singleTurn` 结束时输出一行结构化日志：

```
[engine] turn=3 model=deepseek-v4-pro tokens_in=1234 tokens_out=567
         latency=2.3s ttft=0.8s cache_hit=true tools=2 compact=false
```

需要埋点的位置：
- `singleTurn` 开始/结束 → 记录 turn latency、token delta
- `ChatStream` 调用 → 记录 TTFT（首个 StreamEvent 到达时间）
- `EventMessageDelta` → 记录 usage delta
- `maybeCompact` → 记录压缩触发前后 token 数

实现：在 Engine 中新增 `TurnMetrics` 结构体，`singleTurn` 里收集，turn 结束时 `slog.Info` 输出。

### 4.2 Tool 级 Tracing

每个工具调用记录耗时和结果：

```
[tool] name=Read file=main.go duration=45ms success=true size=2048
[tool] name=Bash cmd="go test ./..." duration=3.2s success=false exit_code=1
```

需要埋点的位置：
- `executeTools` 循环里，每个 `safeExecute` 前后加 `time.Now()` 计时
- 记录 input 摘要（工具名 + 关键参数）、output size、success/error

实现：在 `executeTools` 中加 `defer` 计时，结果注入 slog。可选输出到独立 trace 文件。

### 4.3 LLM API 调用明细

在 AnthropicClient 层记录每次 HTTP 调用：

```
[llm] provider=anthropic status=200 latency=2.1s
      endpoint=/messages model=deepseek-v4-pro
      retries=0 cache_read=1200 cache_create=3400
```

需要埋点的位置：
- `ChatStream` 请求前后 → 记录 HTTP status、latency
- `readSSEStream` → 记录 `message_start` 事件中的 cache_read/cache_create token
- `RetryWithBackoff` → 记录重试次数和间隔

实现：在 `AnthropicClient.ChatStream` 里加 `time.Since` + slog。

### 4.4 Session 级摘要（会话结束时）

`Run()` 结束时（`defer` 里）输出会话级摘要：

```
[session] id=session_xxx turns=5 total_in=15000 total_out=3200
          duration=45s tools_executed=12 compact_count=1
          cache_ratio=85% cost=$0.042
```

实现：在 Engine `Run()` 的 `defer` 块里，汇总已有的 `usage`、`turn`、`tools` 计数输出。

### 4.5 /status 命令增强

扩展现有 `/status` 命令，显示本轮会话的详细 metrics：

```
Model:    deepseek-v4-pro
Turns:    5
Tokens:   in:15000 out:3200 total:18200
Latency:  avg 2.1s/turn (ttft 0.8s)
Tools:    12 executed (Bash:5 Read:4 Edit:3)
Cache:    85% stable
Compact:  1 time
Cost:     $0.042
Session:  session_xxx
```

需要：Engine 暴露 `SessionMetrics()` 方法，返回当前会话汇总数据。

### 4.6 Debug 日志开关

- `ERGATE_DEBUG=1` → 开启所有 slog 输出到 stderr
- `ERGATE_TRACE_FILE=/path/to/trace.json` → 输出 JSON trace 文件（可被 Jaeger/Perfetto 导入）
- 默认只输出 WARNING+ 级别日志

### 优先级

| 子项 | 优先级 | 复杂度 | 依赖 |
|------|--------|--------|------|
| 4.1 Turn Metrics | 高 | 低 | 无 |
| 4.2 Tool Tracing | 高 | 低 | 无 |
| 4.3 LLM API 明细 | 中 | 低 | 无 |
| 4.4 Session 摘要 | 中 | 低 | 4.1 |
| 4.5 /status 增强 | 中 | 中 | 4.1 |
| 4.6 Debug 开关 | 低 | 低 | 无 |

建议实施顺序：4.1 → 4.2 → 4.3 → 4.4 → 4.5 → 4.6

## Phase 5 — 架构演进（Engine 解耦 → Long-Running Agent）

### 设计原则

**不拆双进程**（对比 CodeWhale 的 `codewhale` + `codewhale-tui` 双二进制架构）。
Go 单二进制 + 子命令模式足够，避免双进程带来的启动/调试/日志分散问题。

```
CodeWhale（双进程）：               ergate（单进程演进）：
codewhale → spawn codewhale-tui    ergate         → TUI 直连 LLM（当前行为）
         → IPC 通道建立             ergate daemon  → Engine + HTTP API（后台服务）
         → 两个进程调试             ergate run "x" → 向 daemon 发一次性指令
                                   → 一个进程，一个日志流，go tool pprof 直接用
```

### 5.1 Engine 逻辑独立化（基础）

**目标**：Engine 不依赖 TUI 的任何类型，成为纯状态机。

- [ ] `eventChan` 泛化：Engine 的 Event channel 不绑定 TUI 类型，任何消费者（TUI / HTTP handler / CLI）都能消费
- [ ] Engine 生命周期独立于 TUI：`Engine.Run()` 可以在没有 TUI 的情况下完整执行
- [ ] Engine 输入接口化：定义 `InputSource` 接口，TUI 键盘输入只是其中一个实现
- [ ] Engine 输出接口化：定义 `OutputSink` 接口，TUI 渲染只是其中一个实现

**验收标准**：`ergate -p "hello"`（one-shot 模式）走 Engine 完整流程，不依赖 TUI 代码。

### 5.2 Engine 可观测性内建

**目标**：引擎自带 metrics/tracing，Phase 4 的日志系统成为引擎的内建能力。

- [ ] `TurnMetrics` 结构体作为 Engine 的一等公民
- [ ] `SessionMetrics()` 方法暴露给所有消费者
- [ ] slog 输出不依赖 TUI 的 debugf

### 5.3 HTTP API 层（可选激活）

**目标**：Engine 可通过 HTTP/SSE 访问，TUI 变成可选客户端。

- [ ] `ergate daemon` 子命令：启动 Engine + HTTP API（绑定 localhost）
- [ ] `POST /v1/chat` — 发送用户消息，触发 Engine.Run
- [ ] `GET /v1/events` — SSE 流，接收 Engine 事件（text/thinking/tool_use/done）
- [ ] `GET /v1/status` — 当前会话 metrics
- [ ] `POST /v1/sessions/:id/resume` — 恢复 session
- [ ] TUI 支持连接到 daemon 模式（`ergate --connect localhost:PORT`）

**依赖**：5.1 完成后才有意义。

### 5.4 Long-Running Agent 能力

**目标**：Engine 7×24 运行，响应外部事件而非仅用户键盘输入。

- [ ] 事件源扩展：Engine 支持多种 InputSource
  - TUI 键盘（当前）
  - HTTP API（5.3）
  - Webhook 回调（外部系统触发）
  - 文件监听（文件变更触发任务）
  - MCP 事件（MCP server 推送）
- [ ] 持久化任务队列：任务跨重启存活
- [ ] 定时任务：cron-like 调度器
- [ ] 子智能体并发池：独立 goroutine 池，不阻塞主 Engine
- [ ] 断线重连：TUI 断开后 Engine 继续运行，TUI 重连后恢复视图

**依赖**：5.3 完成后。

### 实施路径

```
5.1 Engine 独立化 ← 当前可做，与 Phase 2/3/4 并行
  ↓
5.2 可观测性内建 ← 与 Phase 4 结合
  ↓
5.3 HTTP API     ← 用户明确需要远程访问时启动
  ↓
5.4 Long-Running ← 用户明确需要 7×24 agent 时启动
```

**启动条件**：每个子阶段在用户明确需要对应能力时才启动开发，不提前实现。

## Phase 6 — 自举迭代（用 ergate 开发 ergate）

### 工作流原则

```
每个迭代 = 一个独立的 session
  │
  ├── 1. 从 session 面板选一个迭代任务（或新建）
  ├── 2. ergate 在当前代码库上工作
  ├── 3. commit 产出，关闭 session
  └── 4. 下一个迭代从新的 session 开始
```

核心：**每个 session 上下文干净，产出通过 git 传递，不依赖长对话记忆。**

### 迭代编排

```
It-1: 基础可用        It-2: Diff + 编辑     It-3: 自举尝试
  ┌──────────┐        ┌──────────┐          ┌──────────┐
  │恢复折叠   │   →    │Diff 渲染  │    →    │用 ergate │
  │Tool 2行   │        │Edit 审计  │         │改 ergate │
  │cache 验证 │        │LSP 反馈   │         │找到瓶颈  │
  └──────────┘        └──────────┘          └──────────┘
```

### Iteration 1: 基础可用性

**目标**：恢复折叠 + tool 输出简洁化 + 验证 cache 效果

**具体任务**：
- [ ] 重新启用折叠（maxToolOutputLines=8, maxThinkingLines=3）
- [ ] 修复折叠 hit-test bug（之前关闭折叠的原因）
- [ ] Tool 结果默认显示 2 行 + "click to expand"
- [ ] 验证 OpenAI 协议 + 新阈值后的 cache 命中率
- [ ] 如果命中率仍 < 50%，调研 DeepSeek OpenAI 端点的缓存行为

**可观测性要求**：
- 需要 API 返回的 `prompt_cache_hit/miss_tokens` 在 status bar 实时可见
- 需要知道每轮 tokens_in/out 用于评估 cache 效果

### Iteration 2: Diff + 编辑可靠性

**目标**：Edit 结果可视化 + LSP 诊断反馈

**具体任务**：
- [ ] Edit 结果渲染为 unified diff（+/- 颜色，参考 detail.go 已有的 diff 着色）
- [ ] Edit 操作加入文件备份验证（写入后读取确认）
- [ ] LSP 诊断接入：编辑 Go 文件后自动跑 `go build`，错误注入会话
- [ ] 可选：`/diff` 命令查看当前变更

### Iteration 3: 自举尝试

**目标**：用 ergate 给 ergate 加一个功能，从中找到瓶颈

**验证标准**：完成一个完整的 "需求 → 实现 → 测试 → commit" 循环

**观察点**：
- 哪些步骤 ergate 做对了？哪些卡住？
- 长对话（10+ 轮）是否退化？
- 工具调用成功率？权限弹窗是否拖慢？
- cache 命中率实际表现？

### 后续迭代方向

- It-4: 子智能体（并发工具执行 + background task）
- It-5: Vim 模式
- It-6: 远程 daemon 模式（Phase 5）

### 每个迭代的 Prompt 模板

```
你在开发 ergate 项目。当前任务是 [任务描述]。

1. 先理解代码，找到相关文件
2. 实现改动
3. 编译验证（go build ./...）
4. 提交（如果成功）或报告问题

注意：
- 每个工具调用后等待结果再继续
- 如果遇到编译错误，自己分析和修复
- 完成后用 /status 汇报进度
```

---

## 提问：第一个迭代确认

在启动 It-1 之前，需要确认几个问题：

1. 当前的 cache 命中率是多少？OpenAI 协议切换后是否改善？
   （可以在 status bar 看到 `cache:XX%`）
   
2. Tool 输出现在显示全量（折叠已关闭），是否严重影响体验？
   如果是，It-1 优先恢复折叠

3. ergate 现在作为编辑工具，最大的不方便是什么？
   （比如：看不到 diff、不知道改了什么、编辑容易出错）

回答这些可以帮助确定 It-1 的具体优先级。
