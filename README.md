# ergate

Go 原生的 AI 软件工程 CLI，对标 Claude Code，单二进制分发。

支持 Anthropic / OpenAI / DeepSeek 三个 API provider。

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

# 或使用 headless 模式
ergate --headless -p "帮我 Review 这个项目"
```

## 配置

复制示例配置并根据需要修改：

```bash
mkdir -p ~/.config/ergate
cp config.example.yaml ~/.config/ergate/config.yaml
```

| Key | Default | 说明 |
|-----|---------|------|
| `api_provider` | `anthropic` | anthropic / openai / deepseek |
| `api_key` | — | API 密钥（也可用 `ERGATE_API_KEY` 环境变量） |
| `model` | `claude-sonnet-4-20250514` | 模型 ID |
| `base_url` | `https://api.anthropic.com/v1` | API 地址 |
| `max_turns` | `25` | 每轮最大工具调用次数 |
| `max_tokens` | `8192` | 单次响应最大 token |
| `temperature` | `0` | 采样温度 |
| `permission_mode` | `normal` | normal / always / bypass |

## 功能

- 多轮对话 + 工具调用循环
- 9 个内置工具（Bash、Read、Write、Edit、Glob、Grep 等）
- 后台任务（bash subprocess + agent sub-LLM）
- MCP 协议支持（stdio/sse/http）
- 持久记忆系统（`.ergate/memory/`）
- 技能系统（SKILL.md + 条件触发）
- 上下文自动压缩
- Git worktree 隔离
- Bubbletea TUI
