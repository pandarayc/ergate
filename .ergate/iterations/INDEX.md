# Terminal-Bench 迭代记录

> 每次跑分、改动的完整追踪

## 迭代列表

| # | 日期 | 描述 | Score | Delta | 关键发现 |
|---|------|------|-------|-------|---------|
| 001 | 2026-06-25 | 首跑 + TLS/timeout 修复 | 0.200 | baseline | 基础设施问题（TLS、超时） |
| 002 | 2026-06-25 | TLS 修复验证 | 0.300 | +0.100 | TLS 修好，但 timeout 暴露 |
| 003 | 2026-06-25 | max_turns=50 + provider 修正 | 0.200 | -0.100 | turns 不是瓶颈 |
| 004 | 2026-06-26 | WebFetch/WebSearch 网络错误分类 + 退避信号 | — | — | 代理路由问题导致全 RuntimeError |
| 005 | 2026-06-26 | API 不走代理 + 容器代理注入 | 0.000 | — | 首次 0 RuntimeError！但 verifier apt 卡死 |
| 006 | 2026-06-26 | ca-certificates 检测修复 + uv 预装 | 0.100 | +0.100 | build-pov-ray 通过。TLS 回归修复 |
| 007 | 2026-06-26 | 关闭 WebSearch/WebFetch → 恢复 + 工具描述约束 | 0.100 | 0 | Web 使用从 73%→7.5%。约束生效 |
| **008** | **2026-06-27** | **thinking_budget=4000 + 两阶段协议** | **0.200** | **+0.100** | **Write 从 0→42, 发现 text 输出量是唯一预测器** |

## 核心发现

详见 [[key-findings]]。
