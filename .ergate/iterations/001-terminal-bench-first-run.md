# Iteration 001 — Terminal-Bench 首跑

**日期**: 2026-06-25
**Agent**: ergate v0.1.0  
**Model**: deepseek-v4-pro (via Anthropic protocol)  
**Benchmark**: terminal-bench/terminal-bench-2  
**配置**: max_turns=25, bypass permissions, api.deepseek.com/anthropic

## 结果

```
Score: 0.200 (2/10)
├─ ✅  2 passed
├─ ❌  5 failed  
└─ 💥 3 errors (timeout)
```

### 通过

| Task | 推测原因 |
|------|---------|
| break-filter-js-from-html | 安全审计类，Read→分析→Edit 模式，工具链短 |
| build-pov-ray | 编译构建类，./configure && make 标准流程 |

### 未通过

| Task | 根因 | 类别 |
|------|------|------|
| compile-compcert | `tls: failed to verify certificate` | TLS |
| overfull-hbox | `tls: failed to verify certificate` | TLS |
| distribution-search | 600s timeout | 超时 |
| path-tracing | 600s timeout | 超时 |
| video-processing | 600s timeout | 超时 |
| circuit-fibsqrt | agent 策略无效 | 能力 |
| make-mips-interpreter | 13次 Bash 后卡住 | 能力 |
| protein-assembly | 数据检索后无法推进 | 能力 |

### 失败分布

```
TLS证书      ██████████ 2
超时          ███████████████ 3  
工具策略      ███████████████ 3
```

## 改进项

### P0: 修 TLS 证书（预期 +2 pass）

容器内缺少 CA 证书。方案：
- Harbor agent setup 时 `apt-get install -y ca-certificates`
- 或 ergate 加 `--insecure` flag（跳过 TLS 验证）
- 或用 env var `ERGATE_INSECURE=true`

**影响**: 修复 compile-compcert, overfull-hbox

### P1: 增加 max_turns（预期 +1-2 pass）

当前 25 turns 不够。方案：
- 提升到 50 turns
- 或动态 max_turns（简单任务 10，复杂任务 50）
- Harbor agent 默认 max_turns=50

**影响**: 可能救回 circuit-fibsqrt, protein-assembly

### P2: 增加 agent timeout（预期 -3 errors）

当前 600s 超时。方案：
- 提升到 1200s（20min）
- 或 Harbor agent run 的 timeout_sec=1200

**影响**: 救回 3 个 timeout 任务

### P3: 端到端流程（预期 pass）

Tool chain 结构已就绪：Read→Bash→Edit 链路正常。后续可考虑：
- system prompt 加工具选择策略提示
- 错误恢复：Bash 失败后自动检查原因再重试

## 下次跑分配置

```bash
harbor run \
  --agent-import-path tasks.ergate_agent:ErgateAgent \
  --ae ERGATE_API_KEY=$ANTHROPIC_AUTH_TOKEN \
  --ae ERGATE_BASE_URL=$ANTHROPIC_BASE_URL \
  --ae ERGATE_MODEL=deepseek-v4-pro \
  --ae ERGATE_MAX_TURNS=50 \
  -d terminal-bench/terminal-bench-2 \
  -l 10 --timeout-multiplier 2.0
```

## 对比基线

| 指标 | 本跑 | 目标 |
|------|------|------|
| Score | 0.200 | 0.400+ |
| Passed | 2 | 4+ |
| TLS errors | 2 | 0 |
| Timeout errors | 3 | 0 |
