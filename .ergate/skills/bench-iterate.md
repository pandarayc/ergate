# bench-iterate: Benchmark 迭代分析

分析 ergate 在 benchmark 中的表现，产出归因报告和改进项。

## 触发条件

- 用户说"分析 benchmark 结果"、"跑分分析"、"迭代"、"bench analyze"
- Harbor 任务完成后

## 工作流

### Step 1: 收集数据

```bash
# 从最新 Harbor job 收集结果
LATEST=$(ls -td jobs/*/ | head -1)
cat $LATEST/result.json
```

关键数据点：
- `stats.evals.<agent>.<benchmark>.reward_stats` — 通过/失败分布
- `stats.evals.<agent>.<benchmark>.exception_stats` — 异常类型
- 每个 trial 的 `verifier/reward.txt` — 通过(1)/失败(0)
- 每个 trial 的 `trial.log` — agent 输出

### Step 2: 归类失败

对每个失败/异常任务，从 trial.log 提取：

| 信号 | 归类 |
|------|------|
| `tls: failed to verify certificate` | **TLS证书** |
| `Command timed out` | **超时** |
| `api_key is required` / `401` / `403` | **认证** |
| `Error: API call` (非 TLS) | **API错误** |
| 工具调用后无后续输出 | **能力/策略** |
| `[Exit code: N]` (N≠0) | **工具失败** |

### Step 3: 写迭代文档

输出到 `.ergate/iterations/NNN-description.md`：

```markdown
# Iteration NNN — <描述>

## 结果
Score: X.XXX (passed/total)

## 失败分布
| 类别 | 数量 | 占比 |
|------|------|------|

## 根因分析
每个失败类别的详细分析

## 改进项
按 P0/P1/P2 优先级列出

## 对比 (如有上轮)
| 指标 | 上轮 | 本轮 | delta |
```

### Step 4: 应用改进

按优先级执行改进：
1. P0: 基础设施修复（TLS、超时、认证）
2. P1: 参数调优（max_turns、timeout、model）
3. P2: 策略优化（system prompt、工具描述、错误恢复）

### Step 5: 验证

```bash
# 重新构建并跑分
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tasks/ergate-static ./cmd/ergate/
harbor run ... -d terminal-bench/terminal-bench-2 -l 10
```

### Step 6: 对比

对比新旧 RunLog，确认改进效果。记录 regressions（以前通过但新版本失败的）和 improvements（以前失败但新版本通过的）。

## 迭代日志目录

```
.ergate/iterations/
├── 001-terminal-bench-first-run.md
├── 002-fix-tls.md
├── 003-increase-max-turns.md
└── ...
```

## 改进优先级常数

| 优先级 | 条件 | 预期增益 |
|--------|------|---------|
| P0 | 基础设施问题（TLS/认证/超时），影响所有任务 | 每修1个 +N pass |
| P1 | 参数调优，影响特定类别任务 | 每调1个 +1-2 pass |
| P2 | 策略优化，时间长但长期增益大 | 难以量化 |
