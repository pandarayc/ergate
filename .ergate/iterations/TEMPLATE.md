# Iteration NNN — <描述>

**日期**: YYYY-MM-DD
**Agent**: ergate vX.Y.Z
**Model**: <model>
**Benchmark**: terminal-bench/terminal-bench-X
**上轮**: [NNN-previous](./NNN-previous.md)

---

## Phase 1: 结果

```
Score: X.XXX (passed/total)
├─ ✅  N passed
├─ ❌  N failed
└─ 💥 N errors
```

### 通过/失败明细

| Task | Score | 根因 |
|------|-------|------|

### 失败分布

| 类别 | 数量 |
|------|------|

---

## Phase 2: 归因

（每个失败类别的详细根因分析）

---

## Phase 3: 改动

| 优先级 | 改动 | 文件 | 预期影响 |
|--------|------|------|---------|
| P0 | ... | ... | ... |

### 具体 diff

```diff
```

---

## Phase 4: 验证结果

（重跑后的分数和对比）

---

## 对比上轮

| 指标 | 上轮 | 本轮 | Delta |
|------|------|------|-------|
| Score | X | X | +X |
| Passed | X | X | +X |
| TLS errors | X | X | -X |
| Timeout | X | X | -X |

### Regressions

（以前通过本轮失败的）

### Improvements

（以前失败本轮通过的）

---

## 经验

1. ...
2. ...
