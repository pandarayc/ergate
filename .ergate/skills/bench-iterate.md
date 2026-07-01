# bench-iterate: Benchmark 迭代分析

分析 ergate 在 benchmark 中的表现，产出归因报告和改进项。

## 触发条件

- 用户说"分析 benchmark 结果"、"跑分分析"、"迭代"、"bench analyze"
- Harbor 任务完成后

## 工作流

### ⚠️ FORENSICS FIRST — 强制证据规则

**在读完 trial log 之前，禁止做任何归因。** 禁止：

- ❌ "这个题是模型能力问题"（没看日志 = 没资格说）
- ❌ "这个题是环境问题"（没看日志 = 在猜）
- ❌ 扫一眼 Bash/Read 计数就下结论（计数值不是证据）
- ❌ 用上一次 run 的结论套本次（每次 run 不同）
- ❌ 归因为 `MODEL_CAPABILITY` 而不引用具体日志行

**自检**：如果归因里没有出现 `trial.log` 的具体行号或内容，说明还没到可以归因的阶段。

### Step 1: 初读 — 快速扫描锁定候选

**目标**：2 分钟内从 10 题中锁定 3-4 个需要精读的题。不做归因，只做分类。

```bash
for d in jobs/<LATEST>/*/; do
  task=$(basename "$d")
  log="$d/trial.log"
  
  n_bash=$(grep -c '\[Tool: Bash\]' "$log")
  n_write=$(grep -c '\[Tool: Write\]' "$log")
  n_think=$(grep -c '\[Thinking\]' "$log")
  n_turn=$(grep -c '\[Turn.*end\]' "$log")
  n_p3c=$(grep -c 'python3 -c' "$log")
  n_apt=$(grep -c 'apt-get install' "$log")
  n_read=$(grep -c '\[Tool: Read\]' "$log")
  n_edit=$(grep -c '\[Tool: Edit\]' "$log")
  reward=$(cat "$d/verifier/reward.txt" 2>/dev/null || echo "?")
  
  # 快速分类
  tag=""
  [ "$n_think" -gt 2000 ] && [ "$((n_bash + n_write + n_read))" -lt 5 ] && tag="ANALYSIS_PARALYSIS"
  [ "$n_p3c" -gt "$((n_turn / 2))" ] && tag="${tag:+$tag,}REPL_LOOP"
  [ "$n_apt" -gt 3 ] && tag="${tag:+$tag,}ENV"
  grep -q "Network error\|HTTP 202\|TLS\|certificate" "$log" && tag="${tag:+$tag,}NET"
  echo "$task | $reward | B=$n_bash R=$n_read W=$n_write E=$n_edit T=$n_turn Th=$n_think p3c=$n_p3c | $tag"
done | sort -t'|' -k2
```

**初读决策**：
- 通过题 → 跳过，不需要精读（除非要找"为什么这次过了"）
- 环境/网络问题 → 标记 `ENV`/`NET`，检查基础设施
- 分析瘫痪 → 必精读（最隐蔽的问题）
- REPL 循环 → 必精读（看最后一次转向 Write 的时机）
- 其他失败 → 选 think/tool 比最高的 2 个精读

### Step 2: 精读 — 对 3-4 个候选题深挖 trace

**只对初读选中的题做这一步。** 每个题产出：
- 完整 trace 摘要（thinking + tool call + tool result 时间线）
- 最后一个有意义的 turn 之前不少于 5 turns 的详细展开
- 引用具体的日志行
- 区分"我看到了什么"和"我推测是什么"
- 反证检查：如果这个归因是错的，日志里哪一行会否定它？

```bash
# 精读脚本：提取指定任务的完整 thinking + tool + result 时间线
TASK_DIR="jobs/<LATEST>/<task-name>"
# 展开最后 8 turns
grep -E "\[Turn.*end\]|\[Tool:|\] \[Thinking\]|Tool Error:|Tool Result:" "$TASK_DIR/trial.log" | tail -60
```

### Step 2: 收集数据

```bash
LATEST=$(ls -td jobs/*/ | head -1)
cat $LATEST/result.json
```

关键数据点：
- `stats.evals.<agent>.<benchmark>.reward_stats` — 通过/失败分布
- `stats.evals.<agent>.<benchmark>.exception_stats` — 异常类型
- 每个 trial 的 `verifier/reward.txt` — 通过(1)/失败(0)
- 每个 trial 的 `trial.log` — agent 输出（工具调用序列）

### Step 3: 逐任务工具调用分析

```bash
for d in jobs/<LATEST>/*/; do
  task=$(basename "$d")
  n_bash=$(grep -c '\[Tool: Bash\]' "$d/trial.log")
  n_read=$(grep -c '\[Tool: Read\]' "$d/trial.log")
  n_write=$(grep -c '\[Tool: Write\]' "$d/trial.log")
  n_edit=$(grep -c '\[Tool: Edit\]' "$d/trial.log")
  n_web=$(grep -c '\[Tool: WebSearch\]\|\[Tool: WebFetch\]' "$d/trial.log")
  n_agent=$(grep -c '\[Tool: Agent\]' "$d/trial.log")
  n_p3c=$(grep -c 'python3 -c' "$d/trial.log")
  n_think=$(grep -c '\[Thinking\]' "$d/trial.log")
  n_turn=$(grep -c '\[Turn.*end\]' "$d/trial.log")
  reward=$(cat "$d/verifier/reward.txt" 2>/dev/null || echo "?")
  echo "$task | reward=$reward | Bash=$n_bash Read=$n_read Write=$n_write Edit=$n_edit Web=$n_web p3c=$n_p3c think=$n_think turns=$n_turn"
done
```

### Step 4: 战略审查（自动化决策链）

**这是最重要的步骤。在归因之前，先完成三层战略判断。每层产出"做什么"和"停止做什么"。**

#### 5A. 参照系检查 — 结构性对齐

```bash
# 对比 phistory.cc 上 Claude Code / Codex 最新 prompt
# 检查项：
# - prompt 结构差异（是否有分阶段、是否防御性措辞）
# - 工具描述差异（是否用命令名而非目的描述）
# - Bash 约束是否精确到具体命令
```

检查清单：
- [ ] prompt 是否对标 Claude Code/Codex 的行动导向措辞？
- [ ] 工具描述是否与业界领先实现同构（不要求同文）？
- [ ] Bash 约束是否是具体命令名（`cat`/`head`）而非抽象目的（"读文件"）？

**原则**：禁止微调措辞。只允许结构性对齐。措辞优化是死路——模型差异远大于措辞差异。

#### 5B. 根因分类 — 失败模式自动归类

```bash
# 自动检测 failure mode
for d in jobs/<LATEST>/*/; do
  task=$(basename "$d")
  log="$d/trial.log"
  
  n_bash=$(grep -c '\[Tool: Bash\]' "$log")
  n_write=$(grep -c '\[Tool: Write\]' "$log")
  n_think=$(grep -c '\[Thinking\]' "$log")
  n_turn=$(grep -c '\[Turn.*end\]' "$log")
  n_p3c=$(grep -c 'python3 -c' "$log")
  n_apt=$(grep -c 'apt-get install' "$log")
  n_read=$(grep -c '\[Tool: Read\]' "$log")
  reward=$(cat "$d/verifier/reward.txt" 2>/dev/null || echo "?")
  
  # 检测模式
  mode="unknown"
  if [ "$n_think" -gt 2000 ] && [ "$((n_bash + n_write + n_read))" -lt 5 ]; then
    mode="ANALYSIS_PARALYSIS"  # 纯思考，不行动
  elif [ "$n_p3c" -gt "$((n_turn / 2))" ]; then
    mode="REPL_LOOP"  # python3 -c 循环
  elif echo "$task" | grep -qi "video\|path-tracing\|image\|pixel"; then
    mode="CV_SKIP"  # 多模态任务，纯文本模型无力
  elif [ "$n_apt" -gt 3 ]; then
    mode="ENV_DEPENDENCY"  # 大量回合消耗在安装依赖
  elif grep -q "Network error\|HTTP 202\|timeout\|TLS\|certificate" "$log"; then
    mode="NETWORK_ERROR"
  else
    mode="MODEL_CAPABILITY"  # 其他 — 归类为模型能力
  fi
  
  echo "$task: reward=$reward mode=$mode (Bash=$n_bash Write=$n_write Read=$n_read Think=$n_think Turns=$n_turn)"
done
```

分类决策表：

| 模式 | 特征 | 动作 |
|------|------|------|
| `ANALYSIS_PARALYSIS` | Think > 2000, Tools < 5 | 标记为模型能力边界，不调 prompt |
| `REPL_LOOP` | python3 -c > turns/2 | 检查 Write 工具可用性，加"write script first"引导 |
| `CV_SKIP` | 视频/图像/像素分析 | **直接跳过**。纯文本模型没有多模态能力 |
| `ENV_DEPENDENCY` | apt-get > 3 次 | 加预装依赖到容器 setup |
| `NETWORK_ERROR` | HTTP 202 / TLS / timeout | 检查代理、预装 CA 证书 |
| `MODEL_CAPABILITY` | 其他失败 | 标记换模型，不调 prompt |

#### 4C. 路线评估 — 停止信号

连续两轮 score 无改善时，自动触发终止检查：

- [ ] 过去 2 轮是否有 `ANALYSIS_PARALYSIS` 模式出现？
  → 停止调 prompt。进入"换模型"路线。
- [ ] 过去 2 轮是否有 `CV_SKIP` 任务？
  → 从后续跑分中排除这些任务。`harbor run` 加 `--include-task-name` 过滤。
- [ ] 过去 2 轮是否有 `ENV_DEPENDENCY` 任务？
  → 停止跑分。先修环境（预装依赖）。
- [ ] 当前路线（如"改 prompt"）是否已迭代 ≥3 轮无改善？
  → 停止当前路线。换方向（工具链重构/换模型/预装环境）。

**产出**：每次审查必须产出"停止做什么"清单，而不只是"做什么"。

### Step 5: 归因分析

#### 5A. 归因的两条轴

| 类别 | 定义 | 处理方式 |
|------|------|----------|
| **Ergate 支持欠缺** | 模型做出了合理决策，但 Ergate 没有提供足够的反馈/机制让它成功 | **直接修。** |
| **模型判断力问题** | 模型做出了不合理的工具选择或策略，导致失败 | **观察频率。** 同模式出现 ≥3 次才加补丁 |

#### 5B. 归因检查清单

**L0 — 基础设施**
- [ ] 任务是否超时在 setup 阶段（agent 根本没跑）？
- [ ] TLS 证书、apt、代理等基础设施是否正常？
- [ ] 是否有 `NETWORK_ERROR` 模式？

**L1 — Ergate 反馈通道**
- [ ] Evaluate 子代理返回值是否正确传递？
- [ ] 模型写了代码后是否得到了可用的编译/测试反馈？
- [ ] EventToolResult 是否正确 emit？

**L2 — Ergate 保护机制**
- [ ] 是否有连续 >3 次相同工具调用？（anti-loop hook）
- [ ] 是否有 `REPL_LOOP` 模式（超过 half turns 用 python3 -c）？
- [ ] 是否有 `ANALYSIS_PARALYSIS` 模式（>2000 thinking, <5 tools）？

**L3 — 模型判断力（观察频率，≥3 次才补丁）**
- [ ] 模型选择了正确的工具组合吗？
- [ ] 工具描述是否需要加"何时不该用"的反模式指引？
- [ ] 步骤 3B 归类为 `MODEL_CAPABILITY` 是否有 ≥3 个不同任务？

**L4 — 模型能力（排除 L0-L3 后才考虑）**
- [ ] 排除了 Ergate 支持欠缺，同一模式在不同任务中多次出现→换模型

### Step 6: 写迭代文档

输出到 `.ergate/iterations/NNN-description.md`：

```markdown
# Iteration NNN — <描述>

## 结果
Score: X.XXX (passed/total)
排除 CV 后: X.XXX (passed/total)

## 战略审查
- 参照系差异: <与 Claude Code/Codex 的结构性差异>
- 失败分类:
  | 任务 | 模式 | 动作 |
  |------|------|------|
- 停止信号: <是否有需要终止的路线>

## 逐任务工具使用
| 任务 | reward | Bash | Read | Write | Edit | Think | 模式 | 
|------|--------|------|------|-------|------|-------|------|

## 改进项
按 P0/P1/P2 优先级列出。每个改进项标注：
- 修复的是 Ergate 哪一层的问题
- 预期影响的失败模式
- 验证方法

## 停止项
- <本迭代确认不该继续做的事情>

## 对比 (如有上轮)
| 指标 | 上轮 | 本轮 | delta |
```

### Step 7: 验证

```bash
# 排除 CV 任务
harbor run \
  --agent-import-path tasks.ergate_agent:ErgateAgent \
  --ae ERGATE_API_KEY=$ANTHROPIC_AUTH_TOKEN \
  --ae ERGATE_BASE_URL=$ANTHROPIC_BASE_URL \
  --ae ERGATE_MODEL=deepseek-v4-flash \
  --include-task-name "break-filter-js-from-html" \
  --include-task-name "build-pov-ray" \
  --include-task-name "circuit-fibsqrt" \
  --include-task-name "compile-compcert" \
  --include-task-name "distribution-search" \
  --include-task-name "make-mips-interpreter" \
  --include-task-name "overfull-hbox" \
  --include-task-name "protein-assembly" \
  -d terminal-bench/terminal-bench-2 -l 8
```

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
| L0 | 基础设施 | TLS 证书、apt 超时、代理路由、网络错误 | 直接修 | 环境/配置 |
| L1 | **Ergate 反馈通道** | Evaluate 返回空串、EventToolResult 缺失 | **直接修** | 工程修复 |
| L2 | **Ergate 保护机制** | 无 loop detection、ANALYSIS_PARALYSIS | **直接修** | Hook/检测 |
| L3 | 模型判断力 | 工具选择策略错误 | **≥3 次同模式才补丁** | 工具描述/prompt |
| L4 | 模型能力 | 复杂推理、代码生成质量不足 | **L0-L2 全修后** | 换模型/架构 |
| — | **CV 任务** | 视频分析、图像像素处理 | **自动跳过** | 排除任务 |
