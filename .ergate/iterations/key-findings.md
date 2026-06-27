# 关键发现

> 8 轮迭代的核心洞察

---

## 1. thinking_budget 是最有效的单变量

```
budget=0:    text=0   Write=0   Score=0.100
budget=4000: text=↑   Write=42  Score=0.200  ← sweet spot
budget=8000: text=↓   Write=↓   Score=0.000  ← 过度思考
```

**机制**：thinking_budget 是零和博弈。4000 刚好触发"先想再写"，8000 挤压行动 token 预算。

---

## 2. text 输出量是唯一预测器

| text 行数 | 任务数 | 通过率 |
|-----------|--------|--------|
| ≥30 | 2 | 100% |
| 6-10 | 3 | 0% |
| ≤5 | 5 | 0% |

**通过的任务不是"写了更多代码"，而是"输出了更多推理文字"。** Write 是结果，text 是原因。

---

## 3. 模型有一个错误的"任务分类器"

```
数学分析题（KL 散度）→ thinking 触发 → text=39 → ✅
文本处理题（同义词替换）→ thinking 触发 → text=30 → ✅
系统管理题（编译编译器）→ Bash 模式 → text=1 → ❌
编程题（写 C 程序）→ Bash 模式 → text=1 → ❌
```

模型把"写程序"归类为"跑命令"。两阶段协议用 prompt 强制纠正这个分类偏差。

---

## 4. WebSearch/WebFetch：工具描述约束 > 禁用工具

| 策略 | Web 使用率 | 效果 |
|------|-----------|------|
| 无约束 | 73% (32/44) | 浪费 turns |
| 禁用 | 0% | 可能削弱需要文档的任务 |
| 工具描述约束 | 7.5% | 模型自觉不用，仍可查文档 |

**关键**：工具描述里写"SECONDARY — exhaust local tools first"，比 prompt 段落更有效。

---

## 5. 基础设施问题非瓶颈

- TLS 错误：3轮修复才根除（ca-certificates + update-ca-certificates）
- apt 缓存：预热后 verifier 不再卡死
- 代理路由：LLM API 不走代理，仅 verifier 用
- uv 预装：避免 verifier 从 GitHub 下载

**结论**：基础设施打磨到 0 errors 后，瓶颈完全在模型能力。

---

## 6. deepseek-v4-pro 上限 ~0.200

8 轮迭代，所有优化方向（prompt、工具、thinking、两阶段协议）都无法突破 0.200。

| 优化手段 | 效果 |
|----------|------|
| thinking_budget=4000 | +0.100 |
| 工具描述约束 | 降 Web 使用 |
| 两阶段协议 | 测试中 |
| 所有其他优化 | ≤0.050 |

**结论**：突破 0.200 需要更强模型或架构级改变（sub-agent 分解、多轮验证）。
