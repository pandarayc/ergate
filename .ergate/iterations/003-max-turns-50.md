# Iteration 003 — max_turns=50 + provider 配置修正

**日期**: 2026-06-25
**上轮**: [002](./002-tls-timeout-fix.md)
**改动**: provider 配置结构修正 + max_turns=50 + stdout flush

---

## Phase 1: 结果

```
Score: 0.200 (2/10)  ← 无变化 vs 001
├─ ✅  2 passed
├─ ❌  5 failed
└─ 💥 3 errors
```

### 对比 001

| Task | 001 | 003 | 变化 |
|------|-----|-----|------|
| break-filter-js-from-html | 1 | 1 | — |
| build-pov-ray | 1 | 1 | — |
| distribution-search | ERR | **0** | ✅ ERR→完成 |
| video-processing | ERR | **0** | ✅ ERR→完成 |
| circuit-fibsqrt | 0 | **ERR** | ⬇ 超时 |
| compile-compcert | 0 | **ERR** | ⬇ 超时 |
| make-mips-interpreter | 0 | 0 | — |
| overfull-hbox | 0 | 0 | — |
| protein-assembly | 0 | 0 | — |
| path-tracing | ERR | ERR | — |

## Phase 2: 归因

### stdout flush 生效 ✅
所有任务现在都有输出。之前 timeout 任务日志空白的问题已修复。

### max_turns=50 不提升分数
25→50 turns 没有增加通过数。说明瓶颈不在 turn 数：
- **通过的依然通过**（break-filter-js, build-pov-ray）：这些任务在 25 turns 内就能完成
- **不通过的依然不通过**：增加 turns 只是在错误路径上走更远
- **2 个从 0→ERR**：max_turns 增加导致更多 API 调用 → 更容易超时

### 退化原因
`circuit-fibsqrt`, `compile-compcert` 0→ERR：50 turns 导致 agent 在错误策略上执行更多轮 → 超时。25 turns 时反而早停更安全。

### 改善原因
`distribution-search`, `video-processing` ERR→0：之前纯超时无输出，现在能跑完并有部分正确输出。说明这些任务在 25 turns 内就应该能跑完但之前被 timeout 机制误杀。

## Phase 3: 结论

**瓶颈不在 max_turns，在模型能力和工具策略。**

| 真实的改进方向 | 优先级 |
|---------------|--------|
| 关掉 WebSearch/WebFetch（benchmark 容器无网络，浪费时间） | P0 |
| System prompt 优化（任务拆解、错误恢复） | P1 |
| 切更强模型（Claude Opus 4.8） | P2 |
| max_turns=25 足够，不需要 50 | ✅ 已确认 |

## Phase 4: 对比

| 指标 | 001 | 003 | Delta |
|------|-----|-----|-------|
| Score | 0.200 | 0.200 | 0 |
| Passed | 2 | 2 | 0 |
| stdout 可见 | 部分 | **全部** | ✅ |
| 模型 | v4-pro | v4-pro | — |
| max_turns | 25 | 50 | — |

---

## 经验

1. **max_turns 不是瓶颈** — 通过的 25 turns 就够，不通过的 50 turns 只是在错误方向走更远
2. **stdout flush 修正确实重要** — 现在能看到 timeout 任务的部分进展
3. **WebSearch/WebFetch 在 benchmark 容器中无效** — 容器无外网，这两个工具浪费 turn
4. **Ergate 的 Read→Bash→Edit 核心链正常** — 问题在更上层（任务理解、策略选择）
