# Iteration 001 — Terminal-Bench 首跑 + 修复

**日期**: 2026-06-25
**Agent**: ergate v0.1.0
**Model**: deepseek-v4-pro (Anthropic protocol → api.deepseek.com/anthropic)
**Benchmark**: terminal-bench/terminal-bench-2 (10 trials)

---

## Phase 1: 首跑结果

```
Score: 0.200 (2/10)
├─ ✅  2 passed
├─ ❌  5 failed
└─ 💥 3 errors (timeout)
```

### 通过

| Task | 分析 |
|------|------|
| break-filter-js-from-html | 安全审计类，Read→分析→Edit，短工具链 |
| build-pov-ray | 构建类，./configure && make 标准流程 |

### 失败归因

| Task | 根因 | 类别 |
|------|------|------|
| compile-compcert | `tls: failed to verify certificate: x509` | **TLS证书** |
| overfull-hbox | `tls: failed to verify certificate: x509` | **TLS证书** |
| distribution-search | `Command timed out after 600 seconds` | **超时** |
| path-tracing | `Command timed out after 600 seconds` | **超时** |
| video-processing | `Command timed out after 600 seconds` | **超时** |
| circuit-fibsqrt | Read→Bash 后策略卡住 | **能力** |
| make-mips-interpreter | 13次 Bash 后无法突破 | **能力** |
| protein-assembly | Read→Glob→数据检索后卡住 | **能力** |

---

## Phase 2: 修复应用

### 改动 1: TLS 证书 (P0)

**文件**: `tasks/ergate_agent.py:98-101`

```python
# 在 setup() 中安装 ca-certificates
await environment.exec(
    "apt-get update -qq && apt-get install -y -qq ca-certificates 2>/dev/null || true",
    timeout_sec=60,
)
```

**预期**: compile-compcert, overfull-hbox 的 TLS 错误消失 → +2 pass

### 改动 2: Agent timeout 600→1200s (P1)

**文件**: `tasks/ergate_agent.py:167`

```python
timeout_sec=1200,  # was 600
```

**预期**: 3 个 timeout 任务有足够时间运行

### 改动 3: max_turns 默认 25→50（推荐配置）

**CLI 参数**: `--ae ERGATE_MAX_TURNS=50`

**预期**: 复杂任务有更多 turn 尝试

### 新增基础设施

| 文件 | 用途 |
|------|------|
| `internal/iteration/runlog.go` | RunLog 结构体（分数/分布/对比） |
| `.ergate/skills/bench-iterate.md` | 迭代分析 skill 定义 |
| `.ergate/iterations/` | 迭代记录目录 |

---

## Phase 3: 验证

> 详见 [迭代 002](./002-tls-timeout-fix.md)

```
Score: 0.300 (+0.100)
├─ TLS errors: 2→0 ✅
├─ distribution-search: ERR→1 ✅
├─ compile-compcert: 0→ERR (TLS修好→超时)
└─ overfull-hbox: 0→0 (TLS修好但任务复杂)
```

---

## 对比

| 指标 | 首跑 | 目标 | 002实际 |
|------|------|------|---------|
| Score | 0.200 | 0.400+ | **0.300** |
| Passed | 2 | 4+ | 3 |
| TLS errors | 2 | 0 | **0** ✅ |
| Timeout | 3 | 0 | 4 |
| 配置 | max_turns=25, timeout=600s | max_turns=50, timeout=1200s | timeout=1200s, max_turns=25(未生效) |

---

## 经验

1. **TLS 是容器环境的常见问题** — benchmark 容器通常是 minimal image，缺少 CA 证书
2. **25 turns 对 Terminal-Bench 不够** — 构建类任务(make, configure)本身就需要多轮
3. **600s 超时太紧** — ergate 每轮 LLM 调用 2-4s，加上工具执行时间，复杂任务很容易超
4. **Tool chain 通路正常** — Read→Bash→Edit→Bash verify 链路已有，问题在 turn 数和环境
