# Ergate

Go 实现的 AI 编程助手 CLI，单二进制分发，持续迭代中。

核心目标是攒一套自己趁手的工具链基础，能稳定跑起来、方便改、逐步长出新能力。

多 provider 抽象层（Anthropic / OpenAI / DeepSeek）的初衷是为了适配 DeepSeek——结果为了这碟醋包了盘饺子，但也顺手搭好了可扩展的 ProviderAdapter 架构。

## 名字

Ergate = 工蚁。蚁群中数量最多、埋头干活的个体。

设想的架构分两层：

- **底层（ergate，工蚁）** — 接收信息素信号，按调用工作流执行具体任务。就是现在这个项目。
- **上层（虫群意识）** — 感知全局上下文，做高维的任务分解与分发，把复杂意图拆成信息素路径派给工蚁执行。

现在是先把工蚁层做好——能稳定干活、方便扩展、逐步长出新能力。

## 安装

```bash
go install github.com/raydraw/ergate/cmd/ergate@latest
```

## 快速开始

```bash
# 设置 API Key
export ERGATE_API_KEY="sk-ant-..."

# 启动 TUI
ergate

# 或使用 headless 模式（一次性任务）
ergate --headless -p "帮我 Review 这个项目"
```

## 配置

复制示例配置并按需修改：

```bash
mkdir -p ~/.config/ergate
cp config.example.yaml ~/.config/ergate/config.yaml
```

配置采用 **多 provider 独立配置** 结构，每个 provider 可独立设置 API Key、Base URL、模型列表及模型专属参数。

### 全局设置

| Key | Default | 说明 |
|-----|---------|------|
| `api_provider` | `anthropic` | 当前使用的 provider 标签 |
| `model` | `claude-sonnet-4-20250514` | 当前使用的模型 ID |
| `max_turns` | `25` | 每轮最大工具调用次数 |
| `max_tokens` | `8192` | 单次响应最大 token |
| `temperature` | `0` | 采样温度 |
| `permission_mode` | `normal` | normal / always / bypass |
| `theme` | `dark` | dark / light |
| `headless` | `false` | 无 TUI 模式 |
| `enable_mcp` | `false` | 启用 MCP 协议支持 |

### Provider 配置

```yaml
providers:
  anthropic:
    compat: "anthropic"
    api_key: "sk-ant-..."
    models:
      claude-sonnet-4-20250514:
        thinking_budget: 16000    # Claude extended thinking

  deepseek:
    compat: "openai"              # DeepSeek 使用 OpenAI 协议
    api_key: "sk-..."
    base_url: "https://api.deepseek.com"
    models:
      deepseek-chat: {}
      deepseek-reasoner:
        reasoning_effort: "max"
```

完整配置项见 `config.example.yaml`。

## 功能

**核心循环**
- 多轮对话 + 工具调用循环，事件驱动的 SSE 流式渲染
- 上下文自动压缩（MicroCompact + LLM 摘要）
- Prompt 分层组装（system prompt / memory / skills / planmode），cache_control 锚定稳定前缀

**工具系统（11 个内置工具）**
- `Bash` — 执行 shell 命令，支持后台运行
- `Read` — 读取文件内容
- `Write` — 创建 / 覆写文件
- `Edit` — 精确字符串替换编辑
- `Glob` — 文件路径模式匹配
- `Grep` — 内容搜索
- `WebFetch` — HTTP 请求 + 内容提取
- `WebSearch` — 网络搜索
- `TodoWrite` — 任务列表管理
- `Task` — 后台任务（Bash subprocess + Agent sub-LLM）
- `Agent` — 子 agent 分发

**扩展能力**
- MCP 协议支持（stdio / sse / http transport）
- 持久记忆系统（`.ergate/memory/`）
- 技能系统（SKILL.md + 条件触发 frontmatter）
- 工具生命周期 Hook（pre / post / onStop）
- Git worktree 隔离
- 文件修改自动备份（filehistory）
- 会话 JSON 持久化

**TUI**
- 基于 Bubbletea 的终端 UI
- 多行输入（textarea）、自动换行、视口跟随
- 增量渲染 + 缓存输出优化
- 暗色 / 亮色主题

**CLI**
- REPL 命令系统（`/help`、`/clear`、`/compact` 等）
- Headless 模式（`--headless -p "prompt"`）
- 断点续传（`-r` 恢复上次会话）

## 架构

```
cmd/ergate/main.go          — 入口
internal/
  engine/                    — 核心循环：chat → tools → chat
  llm/                       — Provider 抽象 + API 适配器
  tool/                      — 工具接口、注册表、内置工具
  task/                      — 后台任务管理
  prompt/                    — 系统提示组装
  compact/                   — 上下文压缩
  mcp/                       — MCP 协议
  memory/                    — 持久记忆
  skill/                     — 技能系统
  config/                    — YAML + 环境变量配置
  cli/                       — CLI 层 + REPL 命令
  tui/                       — Bubbletea 终端 UI
  hooks/                     — 工具生命周期回调
  filehistory/               — 文件备份
  worktree/                  — Git worktree 管理
  session/                   — 会话持久化
  planmode/                  — Plan/Implement 状态机
  util/                      — Markdown 终端渲染
```

详见 [ARCHITECTURE.md](ARCHITECTURE.md)。
