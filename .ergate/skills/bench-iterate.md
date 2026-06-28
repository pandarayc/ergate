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
- 每个 trial 的 `trial.log` — agent 输出（工具调用序列）

### Step 2: 逐任务工具调用分析

**这是最重要的步骤。在归因之前，必须先统计每个任务的工具使用模式：**

```bash
for d in jobs/<LATEST>/*/; do
  task=$(basename "$d")
  n_bash=$(grep -c '\[Tool: Bash\]' "$d/trial.log")
  n_read=$(grep -c '\[Tool: Read\]' "$d/trial.log")
  n_write=$(grep -c '\[Tool: Write\]' "$d/trial.log")
  n_edit=$(grep -c '\[Tool: Edit\]' "$d/trial.log")
  n_web=$(grep -c '\[Tool: WebSearch\]\|\[Tool: WebFetch\]' "$d/trial.log")
  n_agent=$(grep -c '\[Tool: Agent\]' "$d/trial.log")
  n_task=$(grep -c '\[Tool: TaskCreate\]\|\[Tool: TaskOutput\]\|\[Tool: TaskList\]' "$d/trial.log")
  reward=$(cat "$d/verifier/reward.txt" 2>/dev/null || echo "?")
  echo "$task | reward=$reward | Bash=$n_bash Read=$n_read Write=$n_write Edit=$n_edit Web=$n_web Agent=$n_agent Task=$n_task"
done
```

### Step 3: 归因分析

#### 3A. 归因的两条轴

每次分析先区分问题属于哪一类：

| 类别 | 定义 | 处理方式 |
|------|------|----------|
| **Ergate 支持欠缺** | 模型做出了合理决策，但 Ergate 没有提供足够的反馈/机制让它成功 | **直接修。** 这是 bench 测试的核心目标——发现 Ergate 缺什么 |
| **模型判断力问题** | 模型做出了不合理的工具选择或策略，导致失败 | **观察频率。** 同模式出现 ≥3 次才加补丁（模型能力在持续提升，今天的补丁明天可能是多余的限制） |

判断标准：
- 模型选了 Agent/TaskCreate → **不是问题本身。** 这些是正规工具，用它们可能是合理的。
- 模型用了 Agent 但子代理返回值丢失 → **Ergate 支持欠缺。** 子代理机制有 bug。
- 模型用了 Agent 但 3 个任务都卡在同样模式 → **模型判断力问题，考虑补丁。**
- 模型连续 Write 11 次 → **Ergate 支持欠缺。** 没有 loop detection。
- 模型在 3 个不同任务中都用 Bash 替代 Write → **模型判断力问题 ≥3 次，加工具描述约束。**

#### 3B. 当前阶段目标

bench 测试处于**探索阶段**，核心目标是：

1. **发现 Ergate 对 agent 支持的欠缺**：反馈通道是否完整？工具链是否闭环？错误恢复机制是否有效？
2. **观察模型行为模式**：模型在各种任务类型下的自然策略是什么？哪些策略有效、哪些无效？
3. **区分"欠缺"和"判断力"**：不要把 Ergate 的问题归给模型，也不要把模型的合理选择误判为错误。

Score 优化是副产品，不是首要目标。

#### 3C. 归因检查清单

对每个失败任务，按顺序排查：

**L0 — 基础设施**
- [ ] 任务是否超时在 setup 阶段（agent 根本没跑）？
- [ ] TLS 证书、apt、代理等基础设施是否正常？

**L1 — Ergate 反馈通道（这是 bench 最关注的一层）**
- [ ] Evaluate 子代理返回值是否正确传递？
- [ ] 模型写了代码后是否得到了可用的编译/测试反馈？
- [ ] Agent/TaskCreate 子任务的输出是否正确回流到主 agent？
- [ ] WebSearch/WebFetch 的返回内容是否被正确格式化？

**L2 — Ergate 保护机制**
- [ ] 是否有连续 >3 次相同工具调用？（需要 anti-loop hook）
- [ ] 是否有 Write 后无验证 → 再 Write 的循环？
- [ ] PhaseEnforcer 是否生效？被拦截后模型是否改变了策略？
- [ ] Turn 预算是否被合理分配（没有单个工具类型消耗 >60% turns）？

**L3 — 模型判断力（观察频率，≥3 次才补丁）**
- [ ] 模型选择了正确的工具组合吗？如果不正确，是第一次出现还是重复模式？
- [ ] 模型是否在同类任务中反复犯同样的策略错误？
- [ ] 工具描述是否需要加"何时不该用"的反模式指引？（仅在同类问题 ≥3 次后）
- [ ] Prompt 是否需要调整工具选择策略？（仅在同类问题 ≥3 次后）

**L4 — 模型能力（排除 L0-L3 后才考虑）**
- [ ] 排除了 Ergate 支持欠缺，同一模式在不同任务中多次出现？
- [ ] 同一任务在不同模型下表现如何？

#### 3D. 常见归因错误

| ❌ 快速归因 | 问题 | ✅ 正确做法 |
|------------|------|------------|
| "模型用了 Agent，在浪费时间" | Agent 是正规工具。用子代理探索复杂代码库可能是**正确策略**。 | 检查 Agent 子代理的返回值是否正确传回、子代理产出是否被主 agent 利用。如果子代理结果正确但主 agent 没用 → Ergate 反馈通道问题。 |
| "模型把编程当跑命令" | 可能是模型判断力问题，也可能 Bash 描述暗示了"这能做一切"。 | 先查 Bash 描述是否有反模式指引。如果是同模式首次出现 → 记录观察。≥3 次 → 加描述约束。 |
| "模型写代码能力差" | 无 Evaluate 反馈的情况下，"写得不对就重写"是唯一策略。 | 先确认 Evaluate 是否可用、编译错误是否反馈。修复反馈通道后再观察。 |
| "模型容易放弃" | 2 次 Read 就退出 — 可能是指令未完整传达或上下文被截断。 | 检查 trial.log 中 instruction 的完整性。 |
| "模型能力上限 X" | 在所有 Ergate 支持欠缺修复前，"上限"没有意义。 | 修完 L0-L2 再测，且需要跨不同模型对比。 |

### Step 4: 写迭代文档

输出到 `.ergate/iterations/NNN-description.md`：

```markdown
# Iteration NNN — <描述>

## 结果
Score: X.XXX (passed/total)

## 逐任务工具使用
| 任务 | reward | Bash | Read | Write | 特殊工具 | 
|------|--------|------|------|-------|----------|

## 失败根因分析

按归因检查清单逐任务分析：

| 任务 | 基础设施 | 工具引导 | 反馈通道 | 循环检测 | 模型能力 | 主因 |
|------|---------|---------|---------|---------|---------|------|

## 改进项
按 P0/P1/P2 优先级列出。每个改进项标注：
- 修复的是 Ergate 哪一层的问题
- 预期影响的失败模式
- 验证方法

## 对比 (如有上轮)
| 指标 | 上轮 | 本轮 | delta |
```

### Step 5: 应用改进

按优先级执行改进。关键原则：**先修 Ergate 欠缺，模型判断力问题观察频率后再决定。**

1. **P0: Ergate 反馈通道修复**（直接修，不等）
   - Evaluate/Agent 子代理返回值正确传递
   - 编译错误结构化解析 hook
   - Anti-loop hook（连续 >3 次相同工具 → 阻断+诊断）

2. **P1: Ergate 保护机制**（直接修，不等）
   - PhaseEnforcer 引导改进（拦截时注入更强提示）
   - Turn 预算警告（单工具类型消耗 >60% turns 时提醒）

3. **P2: 模型判断力补丁**（同模式 ≥3 次才加）
   - 工具描述加反模式指引
   - Prompt 工具选择策略调整
   - 工具使用的优先级/时机约束

4. **P3: 模型能力**（最后手段）
   - 换更强模型
   - 架构级改变（sub-agent 分解、多轮验证）

### Step 6: 验证

```bash
# 重新构建并跑分
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tasks/ergate-static ./cmd/ergate/
harbor run ... -d terminal-bench/terminal-bench-2 -l 10
```

### Step 7: 对比

对比新旧 RunLog，确认改进效果。记录：
- **regressions**：以前通过但新版本失败的（说明改进引入问题）
- **improvements**：以前失败但新版本通过的
- **tool_usage_shift**：工具使用模式是否向正确方向变化

## 迭代日志目录

```
.ergate/iterations/
├── 001-terminal-bench-first-run.md
├── 002-fix-tls.md
├── 003-increase-max-turns.md
└── ...
```

## 归因层级参考

| 层级 | 类别 | 问题类型 | 触发规则 | 修复方向 |
|------|------|---------|---------|---------|
| L0 | 基础设施 | TLS 证书、apt 超时、代理路由 | 直接修 | 环境/配置 |
| L1 | **Ergate 反馈通道** | Evaluate 返回空串、子代理结果丢失、编译错误未解析 | **直接修** | 工程修复 |
| L2 | **Ergate 保护机制** | 无 loop detection、PhaseEnforcer 不引导、turn 预算无警告 | **直接修** | Hook 实现 |
| L3 | 模型判断力 | 工具选择策略错误、不该用 Bash 替代 Write | **≥3 次同模式才补丁** | 工具描述/prompt |
| L4 | 模型能力 | 复杂推理、代码生成质量不足 | **L0-L2 全修后** | 换模型/架构 |

**每次归因分析必须从 L0 开始逐层排查。**

L0-L2 是 Ergate 自身问题，修了就是永久收益。L3 是补丁，只在模型反复犯同类错误时才加——模型能力在提升，过早补丁是技术债。
