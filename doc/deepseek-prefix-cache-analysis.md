# DeepSeek Prefix Cache 优化分析

> 基于 DeepSeek 官方文档、Reasonix 开源项目分析、及社区实践的梳理
>
> 日期：2026-06-12

## 1. DeepSeek Prefix Cache 机制

### 1.1 核心原理

DeepSeek 的上下文硬盘缓存（Context Caching on Disk）基于 **字节级前缀匹配**：

- 后续请求的**前缀从第 0 个 token 开始**与之前缓存的请求完全一致，才算命中
- **中间部分的局部匹配不会触发缓存命中**
- 即使 JSON key 顺序变化、多一个空格或换行，都会导致缓存失效
- 以 64 tokens 为一个存储单元，不足 64 tokens 不会被缓存
- 缓存不使用后自动清空（数小时到数天），尽最大努力（best-effort）

> 官方文档：https://api-docs.deepseek.com/guides/kv_cache
>
> "only requests with identical prefixes (starting from the 0th token) will be considered duplicates. Partial matches in the middle of the input will not trigger a cache hit."

### 1.2 公共前缀检测

系统会检测多次请求之间的公共前缀，并自动将其作为独立缓存单元落盘。这意味着：

- System prompt 在所有请求中相同 → 自动被缓存
- 对话历史的前半段在多轮中出现 → 可能被检测并缓存

但这一机制不保证 100% 命中，不适合作为设计依据。

### 1.3 监控指标

API 返回的 `usage` 字段包含：

```json
{
  "prompt_cache_hit_tokens": 2048,    // 缓存命中 token 数（$0.014/1M）
  "prompt_cache_miss_tokens": 55      // 未命中 token 数（$0.14/1M）
}
```

## 2. Reasonix 缓存架构（参考模型）

### 2.1 三区上下文模型

| 区域 | 职责 | 行为 |
|------|------|------|
| **ImmutablePrefix** | system prompt + tool specs + few-shot | SHA-256 锁定，只在启动时构建 |
| **AppendOnlyLog** | 对话历史 `[user][assistant][tool]...` | 只追加，禁止任何 mutate |
| **VolatileScratch** | R1 推理链、临时计划状态 | 每轮 reset()，永不上传 |

### 2.2 Compaction 策略

- 尾部保留：fold 时保留最尾部 20% 消息原文不变
- 只有前缀段被替换为摘要
- 后续轮次中，尾部原文的字节不变 → 缓存延续

### 2.3 实测效果

| 场景 | 缓存命中率 | 成本节省 |
|------|-----------|---------|
| 5 轮多轮对话 | 85.2% | vs Claude 93.9% |
| 2 轮 tool use | 94.9% | 95.8% |

## 3. Ergate 现状

### 3.1 已实现的优化

| 特性 | 实现位置 | 状态 |
|------|---------|------|
| BuildStable / BuildDynamicContext 分离 | `internal/prompt/prompt.go` | ✅ |
| OpenAI 兼容 provider 的 system message 不变 | `internal/engine/engine.go:440-452` | ✅ |
| 动态上下文注入为首条 user message | `internal/engine/engine.go:445-451` | ✅ |
| cachestability SHA-256 指纹监控 | `internal/cachestability/` | ✅ |
| DeepSeek cache_hit/miss token 提取 | `internal/llm/provider/deepseek.go` | ✅ |
| SnipCompact（thinking 剪枝） | `internal/compact/compact.go` | ✅ |
| MicroCompact（旧 tool result 清除） | `internal/compact/compact.go` | ✅ |
| TUI 状态栏命中率显示 | `internal/tui/statusbar.go` | ✅ |

### 3.2 待改进

| 维度 | 现状 | 影响 |
|------|------|------|
| **Compaction 尾部保留** | AutoCompact 整体替换 `e.messages` | 🔴 极高 |
| **Append-Only 约束** | 无类型约束，任意代码可 mutate messages | 🔴 高 |
| **Thinking 入消息历史** | thinking 作为 content block 写入 assistant message | 🟡 中 |
| **工具 Schema 确定性序列化** | 未保证 JSON 输出的 map key 顺序 | 🟡 中 |

## 4. 关键发现

### 4.1 DeepSeek 缓存是严格前缀匹配，非语义匹配

通过三组对比实验验证（详见 §5），核心结论：

- **字节级严格前缀匹配**：缓存只从 byte 0 开始比对。中间插入摘要 → 后续所有字节位置变化 → 缓存全丢。即使尾部消息语义相同、字节相同，因为它们在请求中的**位置变了**（在 summary 之后而非原位置），也无法命中缓存。
- **最小缓存单元 64 tokens**：system prompt 不足 64 tokens 时，跨轮都不会命中。engine 的 BuildStable 有 ~500 tokens，所以能正常命中。
- **Compaction 当轮缓存必丢**：无论 AutoCompact 还是 Fold。Fold 的尾部消息虽然字节和上一轮相同，但它们的前面被插入了 summary → DeepSeek 看到的字节前缀变了 → miss。

### 4.2 Fold 的真正价值

经过实验验证，Fold 的价值**不在 compaction 当轮的缓存命中**，而在：

1. **上下文质量**：保留尾部原文 → LLM 看到更完整的最近对话细节（而非压缩后的摘要）
2. **下一轮缓存 baseline**：compaction 之后，Fold 的 baseline 包含 summary + tail（~数千 token 的预热缓存），AutoCompact 只有 summary（~数百 token）。后续轮次 Fold 的缓存基线更丰富
3. **延迟下次 compaction**：Fold 保留了原文详情，LLM 不容易因为丢失上下文而追加重复的问题，减少了不必要的 token 消耗

### 4.3 Append-Only 是 Fold 的前提

只有 Append-Only 保证每条消息"一旦写入就永远是那个字节"，Fold 保留的尾部才可信（确实和上轮字节一致）。如果允许随意 mutate，尾部可能已经被改过，Fold 保留也没用。

### 4.4 Thinking 不进入消息历史的双重收益

- 减少 API 请求的 token 量（reasoning 内容变化频繁，对缓存无益）
- 保持消息历史更小，延迟 compaction 触发时机

## 5. 实验验证

### 5.1 测试环境

- 模型：`deepseek-v4-flash`
- 测试代码：`internal/engine/cache_benchmark_test.go`
- 运行方式：`DEEPSEEK_API_KEY=sk-xxx go test -tags=integration -run <TestName> -v ./internal/engine/`

### 5.2 实验 A：10 轮 Engine 对话（AutoCompact）

模拟真实编程场景，10 轮递增的代码分析对话，engine 的 AutoCompact 自然触发。

```
#  | In       | Out    | Hit     | Miss    | Hit%
 1 | 9999     | 428    | 512     | 9487    |  5.1%   ← 首轮，构建缓存
 2 | 3485     | 70     | 2304    | 1181    | 66.1%   ← 前缀命中
 3 | 16764    | 1100   | 12928   | 3836    | 77.1%   ← 持续命中
 4 | 6606     | 279    | 256     | 6350    |  3.9%   ← 🔴 compaction 触发，暴跌
 5 | 14249    | 592    | 6784    | 7465    | 47.6%   ← 缓存重建中
 6 | 7914     | 568    | 6528    | 1386    | 82.5%
 7 | 18668    | 917    | 8064    | 10604   | 43.2%
 8 | 11049    | 710    | 7808    | 3241    | 70.7%
 9 | 25731    | 407    | 17920   | 7811    | 69.6%
10 | 14342    | 197    | 14208   | 134     | 99.1%
────────────────────────────────────────────────
Tot | In: 128807 | Out: 5268 | Hit: 77312 | Miss: 51495
    | Overall: 60.0% | Cost: $0.0218
```

**关键观察：**
- Turn 1 system prompt 命中 512 tokens（证明 BuildStable 缓存正常）
- Turn 4 命中率从 77% 暴跌到 3.9%（AutoCompact 全替换的证据）
- Compaction 后需要 4+ 轮才恢复到 99%（缓存重建缓慢）
- 整体命中率 60%，compaction 是最大的破坏源

### 5.3 实验 B：手动 Fold vs AutoCompact 对比

绕过 engine，用原始 LLM client 精确控制消息结构。3 轮对话建立前缀，然后分别用两种策略发送 turn 4。

**结果：**

| 实验 | System Prompt | Turn 间隔 | Turn 1-3 缓存 | AutoCompact | Fold |
|------|-------------|-----------|---------------|-------------|------|
| B1 | 短（~10tokens） | 0.3s | 全 0 命中 | hit=0 miss=214 | hit=128 miss=327 (28.1%) |
| B2 | 短（~10tokens） | 0.3s | 全 0 命中 | hit=0 miss=228 | hit=0 miss=375 |
| B3 | 短（~10tokens） | 2s | 全 0 命中 | hit=0 miss=208 | hit=0 miss=345 |
| B4 | 长（~100tokens） | 2s | Turn3: hit=128 | hit=0 miss=459 | hit=0 miss=2192 |

**B1 的 128 hit 来源分析：** B1 是先跑 AutoCompact 再跑 Fold。AutoCompact 的 [system][summary3msgs] 被 DeepSeek 缓存后，Fold 复用了这个前缀。当调整为先跑 Fold（B2），这个优势就消失了。

**B4 的 Turn3 缓存命中：** 说明更长的 system prompt（~100 tokens）跨越了 64 token 最小缓存单元，被 DeepSeek 成功缓存。

### 5.4 结论

1. **DeepSeek 严格前缀匹配**：Fold 的尾部消息虽然在上一轮位置有缓存，但在 compaction 后的新位置没有命中——"中间部分的局部匹配不会触发缓存命中"得到直接验证
2. **最小缓存单元 64 tokens**：短 system prompt 无法建立缓存，长的可以
3. **Compaction 当轮缓存必丢**：无论哪种策略，都逃不掉。Fold 的收益在 compaction **之后**的轮次
4. **Fold 的 cache 收益是间接的**：通过建立更丰富的 baseline（summary + tail），为后续轮次提供更大的缓存命中基础
5. **Append-Only 是前提**：Fold 保留了字节级不变的尾部消息。如果消息历史曾被 mutate，保留的尾部就不可信

### 5.5 测试代码位置

- `internal/engine/cache_benchmark_test.go` — `TestDeepSeekCache10Turns`（实验 A）和 `TestFoldVsAutoCompact`（实验 B）
- 运行需要 `-tags=integration` 和 `DEEPSEEK_API_KEY` 环境变量

## 6. ReCAP 交叉分析

> 论文：ReCAP: Recursive Context-Aware Reasoning and Planning for Large Language Model Agents
>
> Zhang et al., NeurIPS 2025 | [arXiv:2510.23822](https://arxiv.org/abs/2510.23822) | [HTML](https://ar5iv.labs.arxiv.org/html/2510.23822)

ReCAP 的核心洞察——**"不是给 Agent 更多上下文，而是更好地组织上下文"**——与 Ergate Fold 优化的底层逻辑完全一致。

### 6.1 机制对应

| ReCAP 概念 | Ergate 对应 | 共通原理 |
|-----------|------------|---------|
| **固定前缀** — "always start eviction from second element to preserve initial prompt" | **BuildStable()** — system message 永不改变 | 锚点前缀 = 缓存基础 |
| **结构化回注** — 子任务完成后注入 ⟨T, S[1:]⟩（任务描述 + 剩余计划） | **Fold 尾部保留** — 旧消息折叠为摘要，尾部原文保留 | 不堆更多上下文，而是**重新组织**已有上下文 |
| **滑动窗口 K=64** — 活动上下文限制，超出的移除 | **Compaction 80% 阈值** — 折叠旧消息为摘要 | 边界控制，避免上下文膨胀 |
| **Few-shot 只放一次** — 递归调用不重复注入 | **BuildStable 只构建一次** — 不每轮重建 system prompt | 减少冗余前缀 |

### 6.2 消融验证：结构 > 推理细节

| 配置 | 成功率 | 说明 |
|------|--------|------|
| 完整 ReCAP | 80% | 包含计划 + 推理痕迹 |
| No Think（去掉推理） | **60%** | 结构贡献了 60% 能力 |
| Name Only（仅计划名） | 55% | 缺推理痕迹再降 5% |
| Think Many（全部历史） | 70% | 推理过量影响不大 |

**→ 推理内容（Thinking）贡献有限，结构是核心。** 这直接验证了把 thinking 移到 VolatileScratch（不上传 API）的安全性——预期对 LLM 理解质量影响 < 20%。

### 6.3 成本权衡：3x 代价，84% 提升

| | ReAct | ReCAP | 比例 |
|---|---|---|---|
| ALFWorld 成本 | $37.89 | $118.40 | 3x |
| Robotouille 成功率 | 38% | **70%** | +84% |
| SWE-bench 成功率 | 39.6% | **44.8%** | +13% |

**→ Fold 也面临同样权衡。** 保留尾部消息 → 每轮多消耗 token → 更贵，但 LLM 获得更完整的上下文。ReCAP 证明了这种 trade-off 在收益足够大时是值得的。

### 6.4 可借鉴模式

| # | ReCAP 模式 | Ergate 应用 |
|---|-----------|------------|
| 1 | 固定前缀 + 滑动窗口 | BuildStable + AppendOnlyLog（已做） |
| 2 | **结构化回注** ⟨T, S[1:]⟩ | Fold 摘要改为结构化模板（待做） |
| 3 | **每 10 轮规则重注入** | 长会话中周期性追加系统约束，防止"规则遗忘" |
| 4 | **Think 可牺牲** (60% vs 80%) | thinking → VolatileScratch 安全（待做） |
| 5 | Few-shot 只放一次 | BuildStable 只构建一次（已做） |
| 6 | **驱逐从第二个元素开始** | 压缩/删除只动旧消息，不动前缀（待做） |

### 6.5 核心启示

ReCAP 用三种 prompt 模板（root / recursive / backtracking）在不同粒度注入不同结构的信息，设计原则是 **"只注入必要信息，保持结构清晰"**。Ergate Fold 的摘要也应该遵循这个原则——不是"越详细越好"，而是"结构越清晰越好"。

## 7. Reasonix Go v1.0 源码分析

> 源码：https://github.com/esengine/DeepSeek-Reasonix/tree/main-v2
>
> Reasonix 从 TypeScript v0.x 重写为 Go v1.0。Go 版作为迭代版本，简化了部分设计。

### 7.1 Go 版实际裁剪策略

| 文件 | 裁剪对象 | 机制 |
|------|---------|------|
| `agent/prune.go` | ≥1024 字节的工具结果 | 替换为 `"[elided tool result — X bytes]"`，原文归档磁盘 |
| `history/strip.go` | compose XML 块 | 正则移除 `<memory-update>`、`<background-jobs>`、`<active-goal>` |
| `agent/compact.go` | 旧对话历史 | Fold 为摘要，保留 pinned prefix + 16k token 尾部 |

### 7.2 Thinking 显式保留

```go
// provider.go
type Message struct {
    ReasoningContent   string  // ← 显式保留，不裁剪
    ReasoningSignature string  // ← Anthropic 签名，必须回放
}
```

注释："Anthropic requires the signed thinking block be replayed on the next turn when a tool call followed thinking"

### 7.3 TypeScript → Go 的迭代决策

Go v1.0 从 TypeScript 版的 DeepSeek-only 演进为多 provider（Anthropic + OpenAI + DeepSeek）。Anthropic 的 extended thinking 签名机制要求 thinking block 必须在下一轮原样回放——这是 API 协议要求，不是可选的优化。因此 Go 版**放弃了 VolatileScratch**，改为保留 thinking。

推测其判断：**fold + prune 已经管住了上下文膨胀，thinking 的额外空间开销不值得引入 VolatileScratch 的复杂度。**

### 7.4 Fold 具体实现

```
[pinned prefix] [folded summaries...] [recent tail ≤ 16384 tokens]
     ↑                    ↑                    ↑
  system prompt    累积的多个摘要       最近几轮原文
  第一个 user turn  (不合并为一个)       最少保留 2 条消息
  所有之前的摘要                        在 tool result 边界对齐
```

额外机制：
- **连续 compact 检测**：两次连续 compact → `compactStuck = true`，暂停自动 compact 并警告
- **强制比例**：`compactForceRatio = 0.9`，即使 prune 能释放空间也强制 compact
- **摘要累积**：每次 fold 新增一个摘要消息，不覆盖之前的摘要——防止渐进式事实丢失

## 8. 实现记录

### 8.1 已完成的优化

| 优先级 | 改动 | Commit | 实现位置 |
|--------|------|--------|---------|
| P0 | **FoldCompact** — compaction 尾部保留 | `282b805` | `internal/compact/compact.go` |
| P0 | **PruneCompact** — 大工具结果归档 | `49d8b5c` | `internal/compact/compact.go` |
| P1 | **工具 Schema 确定性序列化** — 排序 + 缓存 | `54f65cc` + `4e662fe` | `internal/tool/registry.go` |
| P2 | **Append-Only Log 抽象** — 类型系统强制 | `2a8a3e7` | `internal/log/log.go` |
| P3 | **约束重注入** — 每 N 轮提醒 | `6833388` | `internal/engine/engine.go` |

### 8.2 新增 Config 字段

```yaml
# compaction configuration
compact_threshold: 0.8            # fraction of context window, default 0.8
compact_keep_tail: 3              # messages to preserve at end, default 3
compact_prune_bytes: 4096         # tool result > this will be archived, default 4096
compact_constraint_interval: 0    # turns between constraint reminders, 0=disabled
```

### 8.3 Compaction 三层级联

```
maybeCompact 触发时:

  Layer 1: SnipCompact     — 剪掉旧 thinking 块（免费，正则替换）
  Layer 2: PruneCompact    — 大工具结果归档为文件指针（免费，I/O 开销小）
  Layer 3: FoldCompact     — LLM 摘要 prefix + 保留尾部（付费，API 调用）
           ↑                                       ↑
     成本递增                                上下文质量递增
```

### 8.4 FoldCompact 安全分割

FoldCompact 在切割头/尾时，会向后检查确保不会拆散 tool_call→tool_result 对。如果尾部包含 orphan 工具结果（其 tool_call 在头部），分割点会自动前移以保持配对完整。

### 8.5 最终效果

```
10 轮 conversation benchmark (deepseek-v4-flash):

优化前:  缓存命中率 60.0%，compaction 当轮命中率 3.9%（悬崖）
优化后:  缓存命中率 89.1%，compaction 悬崖消失

Turn 分布（优化后）:
  Turn 1:  67%  → Turn 2: 98% → Turn 3: 76% → Turn 4: 98%
  Turn 5:  95%  → Turn 6: 99% → Turn 7: 80% → Turn 8: 99%
  Turn 9:  89%  → Turn 10: 99%
```

### 8.6 降级/待定项

| 改动 | 状态 | 原因 |
|------|------|------|
| Thinking 裁剪 (VolatileScratch) | **降级** | Reasonix Go 显式保留 thinking（Anthropic 签名硬约束）；fold+prune 足够管住上下文；ReCAP 消融数据保留作后续参考 |
| 结构化摘要 | **待定** | 概念性回忆测试中 LLM 摘要质量已足够好，边际收益不明确 |

## 9. 参考

- [DeepSeek Context Caching 文档](https://api-docs.deepseek.com/guides/kv_cache)
- [ReCAP: Recursive Context-Aware Reasoning and Planning (NeurIPS 2025)](https://arxiv.org/abs/2510.23822)
- [The boring secret to a cheap AI coding agent — a byte-stable prompt prefix](https://dev.to/esengine/the-boring-secret-to-a-cheap-ai-coding-agent-a-byte-stable-prompt-prefix-5f7k)
- [How a DeepSeek-only agent framework hit 85% prefix cache rate](https://dev.to/esengine/how-a-deepseek-only-agent-framework-hit-85-prefix-cache-rate-and-saved-93-vs-claude-5c9g)
- [Reasonix 设计哲学：不是在 Agent 上加缓存，而是把 Agent Loop 改造成可缓存的形状](https://segmentfault.com/a/1190000047798403)
- [Permafrost: CC 插件，byte-stable prefix proxy](https://github.com/jianzhichun/permafrost)
