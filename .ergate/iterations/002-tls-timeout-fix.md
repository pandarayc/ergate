# Iteration 002 — TLS + timeout 修复验证

**日期**: 2026-06-25
**上轮**: [001](./001-terminal-bench-first-run.md)
**改动**: TLS ca-certificates + timeout 1200s

---

## Phase 1: 结果

```
Score: 0.300 (3/10)  ← +0.100 vs 001
├─ ✅  3 passed  (+1)
├─ ❌  3 failed  (-2)
└─ 💥 4 errors  (+1)
```

### 通过/失败明细

| Task | 001 | 002 | 变化 | 分析 |
|------|-----|-----|------|------|
| break-filter-js-from-html | 1 | 1 | — | 稳定通过 |
| build-pov-ray | 1 | 1 | — | 稳定通过 |
| distribution-search | ERR | **1** | ✅ 改善 | timeout→通过，TLS修复有效 |
| overfull-hbox | 0 | 0 | — | TLS修好但任务复杂 |
| compile-compcert | 0 | ERR | ⬇ 退化 | TLS修好→能调API→超时 |
| make-mips-interpreter | 0 | ERR | ⬇ 退化 | 同上 |
| circuit-fibsqrt | 0 | 0 | — | 策略问题 |
| protein-assembly | 0 | 0 | — | 策略问题 |
| path-tracing | ERR | ERR | — | 持续超时 |
| video-processing | ERR | ERR | — | 持续超时 |

---

## Phase 2: 归因

### TLS 修复：有效 ✅

`overfull-hbox` 不再报 `x509: certificate signed by unknown authority`，正常调用 Read/Bash。证明 ca-certificates 安装生效。

### 超时：部分改善

- `distribution-search`: 600s→1200s 足够完成 → ERR→1 ✅
- `compile-compcert`, `make-mips-interpreter`: TLS 修好后能调 API 了，但任务本身太重 → 0→ERR
- `path-tracing`, `video-processing`: 持续超时，需更大优化

### max_turns 未生效

Agent 日志仍显示 `max_turns=25`。`--ae ERGATE_MAX_TURNS=50` 未传递到 config YAML。需修 agent 的 `extra_env` 读取逻辑。

---

## Phase 3: 改动

### 本轮已应用

| 优先级 | 改动 | 效果 |
|--------|------|------|
| P0 | ca-certificates | ✅ TLS 错误消失 |
| P1 | timeout_sec=1200 | ✅ 1 个任务从 ERR→1 |

### 下轮待做

| 优先级 | 改动 | 预期 |
|--------|------|------|
| P0 | 修 max_turns 传递 | +1-2 pass |
| P1 | max_turns 真正调到 50 | +1-2 pass |
| P2 | 关掉 WebSearch/WebFetch 工具（慢） | 减少超时 |

---

## Phase 4: 对比

| 指标 | 001 | 002 | Delta |
|------|-----|-----|-------|
| Score | 0.200 | **0.300** | +0.100 |
| Passed | 2 | 3 | +1 |
| TLS errors | 2 | **0** | -2 ✅ |
| Timeout errors | 3 | 4 | +1 |

### Regressions
- `compile-compcert`: 0→ERR（能调API了但超时）
- `make-mips-interpreter`: 0→ERR（同上）

### Improvements
- `distribution-search`: ERR→1 ✅

---

## 经验

1. **TLS 修复是正确的** — 2 个 TLS 错误全部消失
2. **但修复暴露了下层问题** — TLS 修好后任务开始真正跑，更重 → 超时更频繁
3. **timeout 1200s 还不够** — 部分任务需要更长时间或更多 turn
4. **max_turns 传递有 bug** — `--ae ERGATE_MAX_TURNS=50` 未生效，需排查 extra_env 读取
