# OpenAI Responses 调 OpenAI Chat 转换设计

## 1. 背景与目标

当前项目已经支持以下兼容链路：

- OpenAI Chat -> OpenAI Responses
- Claude -> OpenAI Chat
- Gemini -> OpenAI Chat
- 多协议统一经由 relay / adaptor / compat 层进行转换

现阶段需要新增一条反向兼容能力：

> 当客户端调用 `POST /v1/responses`，而当前上游通道更适合或仅支持 `POST /v1/chat/completions` 时，网关自动将 OpenAI Responses 请求转换为 OpenAI Chat 请求，并将上游 Chat 响应重新包装为 OpenAI Responses 响应。

### 目标

首版目标如下：

- 仅支持 `APITypeOpenAI`
- 客户端兼容优先
- 首版必须支持流式 SSE
- 文本、tools、多模态都纳入首版核心范围
- 关键语义不兼容时报错，非关键字段允许降级

### 非目标

首版暂不覆盖以下内容：

- 非 `APITypeOpenAI` 通道（如 Claude 原生、Cohere 原生等）的通用 fallback
- `/v1/responses/compact` 反向转 `/v1/chat/completions`
- Built-in tools 的完整原生语义模拟
- 所有 OpenAI Responses 专属 item/event 的 100% 拟真复刻

本设计优先保证主流客户端和 SDK 的兼容性，而不是追求协议边缘细节的绝对一致。

---

## 2. 可行性分析

### 2.1 可行性结论

该能力可行，且具备较好的现有代码基础，不属于高架构风险改造。

### 2.2 现有基础

项目中已经存在以下直接可复用能力：

1. **Chat -> Responses 请求转换**
   - 已有 `ChatCompletionsRequestToResponsesRequest(...)`
2. **Responses -> Chat 响应转换基础**
   - 已有 `ResponsesResponseToChatCompletionsResponse(...)`
3. **内部桥接能力**
   - 已有 `chatCompletionsViaResponses(...)` 作为正向桥接样例
4. **统一 relay / adaptor / quota / error pipeline**
   - 可以承接新的反向 compat 实现

这些基础说明：

- 项目已经具备两种协议之间的语义模型
- relay 层已经支持“入口协议 != 上游协议”
- 新需求主要是补足对称路径，而不是重建整个协议框架

### 2.3 技术难点

本功能真正的复杂点集中在响应侧，尤其是流式：

1. `Responses -> Chat` 请求映射需要处理文本、tool、多模态混合输入
2. `Chat -> Responses` 非流式包装需要构造 `response` / `output[]` 结构
3. `Chat SSE -> Responses SSE` 需要重建事件序列，而不是简单透传 chunk
4. 某些 Responses 特有语义无法被 Chat 上游表达，需要制定明确降级与报错规则

因此本需求适合实现为：

- 中等复杂度
- 强测试约束
- 结构化 compat 子模块

---

## 3. 总体架构设计

### 3.1 设计原则

本功能采用以下原则：

1. **客户端兼容优先**
   - 优先保证主流 SDK / 客户端可直接消费
2. **结构对称**
   - 与现有 `chat -> responses` 方向形成对称设计
3. **边界清晰**
   - 请求转换、响应转换、流式状态机、能力判定分离
4. **最小入侵**
   - 尽量复用现有 relay / adaptor / quota / error 流程
5. **显式降级**
   - 关键不兼容直接失败，非关键字段允许降级但必须可观测

### 3.2 推荐架构

推荐采用：

> compat 子模块 + relay 对称接入

#### relay 层

在 `ResponsesHelper` 中新增一条反向桥接路径，例如：

- `responsesViaChatCompletions(...)`

该函数负责：

1. 拷贝并清洗请求
2. 判断是否触发 fallback
3. 调用 compat 请求转换器
4. 调用上游 chat adaptor
5. 将响应转回 Responses
6. 走现有 usage / quota / error 处理链路

#### compat 层

建议新增一组职责清晰的模块：

1. **请求转换器**
   - `OpenAIResponsesRequest -> GeneralOpenAIRequest`
2. **非流式响应转换器**
   - `Chat response -> OpenAI Responses response`
3. **流式事件转换器**
   - `Chat SSE -> Responses SSE`
4. **能力判定器**
   - 判断 fallback 条件、硬错误和软降级

### 3.3 推荐文件组织

建议放在 `service/openaicompat/` 及 `relay/` 下，示例：

- `service/openaicompat/responses_to_chat_request.go`
- `service/openaicompat/chat_to_responses_response.go`
- `service/openaicompat/chat_to_responses_stream.go`
- `service/openaicompat/responses_to_chat_policy.go`
- `relay/responses_via_chat_completions.go`

说明：
- 具体文件名可微调
- 但必须保持“请求转换 / 响应转换 / 流式状态机 / 策略判定”分层

---

## 4. 触发策略与能力判定

### 4.1 新增全局策略

建议新增全局配置项：

- `global.responses_to_chat_completions_policy`

结构与现有 `chat_completions_to_responses_policy` 保持对称：

```json
{
  "enabled": false,
  "all_channels": true,
  "channel_ids": [],
  "channel_types": [],
  "model_patterns": []
}
```

### 4.2 触发条件

仅当以下条件同时满足时，`/v1/responses` 请求允许 fallback 到 `/v1/chat/completions`：

1. 当前入口为 `/v1/responses`
2. 当前 `ApiType == APITypeOpenAI`
3. 未开启全局或通道级 pass-through
4. 命中 `responses_to_chat_completions_policy`
5. 当前模型/通道适合走 chat fallback

### 4.3 优先级

建议优先级如下：

1. 管理员显式配置优先
2. 原生 `/v1/responses` 能力优先
3. 无原生能力时才触发 fallback

即：

- 能原生 responses 就优先原生
- policy 命中且能力需要时，才转 chat fallback

### 4.4 能力判定器

建议引入统一判定器，输出：

- 是否支持 `Responses -> Chat`
- 是否存在关键不兼容字段
- 是否存在允许降级的字段
- 是否建议原生优先

建议输出状态：

- `HardReject`
- `SoftDowngrade`
- `NativePreferred`
- `ChatFallbackAllowed`

这样 relay 层只做调度，不做复杂协议判断。

---

## 5. 请求转换设计（Responses -> Chat）

### 5.1 基本目标

将 `OpenAIResponsesRequest` 转换为 `GeneralOpenAIRequest`，保持如下语义：

- 文本输入可转
- 历史消息可转
- tools / tool outputs 可转
- 多模态可转
- 关键执行语义尽量保真

### 5.2 字段分级策略

#### A. 必须保真，否则报错

以下字段或结构如果无法可靠映射，直接返回 `400`：

1. 无法用 chat messages 表达的 item 类型
2. 依赖严格 item 顺序且 chat 无法保真的复杂结构
3. 关键 built-in tools 语义，若 chat 上游无对应执行能力
4. 会影响最终模型行为的关键控制字段且无安全降级路径

#### B. 允许降级的字段

以下字段允许按规则降级：

- `instructions`
- `metadata`
- `user`
- `store`
- `parallel_tool_calls`
- 某些非关键增强字段

#### C. 需要单独策略判断的字段

- `previous_response_id`
- built-in tools
- 某些多模态对象中的扩展属性

默认建议：

- 如果字段会影响模型执行主语义，则报错
- 如果仅影响增强能力或上下文标记，则可降级

### 5.3 核心映射规则

#### `instructions`
映射为 chat 的 `system` 或 `developer` 消息。

建议策略：
- 如果已有系统消息，则按统一覆盖/前置规则处理
- 与现有 `system prompt` 注入机制兼容

#### `input`
按顺序转换为 chat `messages`。

支持的首版核心类型包括：

- 文本
- assistant 历史
- function call
- function call output
- image
- audio
- file
- video（在现有 DTO 能稳定承载时）

#### function call / tool output
映射规则：

- `function_call` -> assistant `tool_calls`
- `function_call_output` -> tool role message 或现有工具回传消息

这部分需要保持与后续 `Chat -> Responses` 响应包装的语义一致。

#### `tools`
映射为 chat 的 function tools。

对于 Responses 原生 built-in tools：
- 若无可靠 chat 表达方式，默认判定为不兼容
- 首版不承诺完整原生 built-in tools 模拟

#### `tool_choice`
映射为 chat 的 tool choice。

#### `text.format`
映射为 chat `response_format`。

#### `max_output_tokens`
映射为：
- `max_completion_tokens` 或
- `max_tokens`

具体优先使用项目中现有 OpenAI Chat 字段习惯。

#### `reasoning`
尽量映射到 chat 可承载字段，至少保留 effort 等主语义。

---

## 6. 非流式响应转换设计（Chat -> Responses）

### 6.1 基本目标

将上游 `/v1/chat/completions` 非流式响应包装为：

- `object: "response"`
- `output[]`
- `usage.input_tokens / output_tokens / total_tokens`

### 6.2 核心包装规则

#### 普通文本响应
如果 chat response 中存在 assistant content：
- 包装为一个 `message` output item
- `content[]` 中写入文本项

#### tool calls
如果 chat response 中包含 `tool_calls`：
- 生成 `function_call` output items
- 保留 call id、function name、arguments

#### usage 映射
映射规则：

- `prompt_tokens -> input_tokens`
- `completion_tokens -> output_tokens`
- `total_tokens -> total_tokens`

如果 chat response usage 缺失：
- 允许走现有 fallback 估算逻辑

### 6.3 完成态

非流式最终响应应尽量呈现为一个完整的 OpenAI Responses 风格对象，而不是简单塞一个兼容字段集合。

目标不是完全复刻 OpenAI 内部实现细节，而是保证客户端拿到的结构与预期一致。

---

## 7. 流式响应转换设计（Chat SSE -> Responses SSE）

### 7.1 设计目标

首版必须支持流式，并优先保证客户端兼容性。

因此推荐实现：

> Responses 事件状态机

即：
- 上游继续消费 chat stream
- 网关内部聚合 chat chunks
- 对外发出 Responses 风格 SSE 事件

### 7.2 首版建议稳定支持的事件集

文本主路径：

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.completed`

tool call 主路径：

- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`

### 7.3 状态机结构

建议维护一个 stream state，至少包含：

- `response_id`
- `model`
- `created_at`
- `current_output_index`
- `current_content_index`
- `text_started`
- `aggregated_text`
- `tool_calls_in_progress`
- `aggregated_tool_arguments`
- `usage`
- `completed_sent`

### 7.4 事件生成流程

#### 阶段 A：首个 chunk
收到第一个有效 chat chunk 后：

1. 发送 `response.created`
2. 如为 assistant 文本输出，发送 `response.output_item.added`

#### 阶段 B：文本增量
每当 chat chunk 中出现文本 delta：

- 发送 `response.output_text.delta`
- 累积文本缓冲

#### 阶段 C：文本结束
在以下时机判断文本完成：

- finish_reason 到达
- 切换到 tool call
- 流结束

然后补发：
- `response.output_text.done`
- `response.output_item.done`

#### 阶段 D：tool call 增量
如果 chat chunk 中出现 `tool_calls`：

- 第一次出现某个 tool call 时，发送 `response.output_item.added`
- 参数增量到达时，发送 `response.function_call_arguments.delta`
- 参数完成时，发送 `response.function_call_arguments.done`
- 最终发送 `response.output_item.done`

#### 阶段 E：完成态
在拿到 usage 或上游结束时：

- 发送 `response.completed`

### 7.5 usage 处理

优先级如下：

1. 使用 chat stream 最终 chunk 自带 usage
2. 若缺失，则复用现有 token fallback 逻辑估算

最终映射为：

- `input_tokens`
- `output_tokens`
- `total_tokens`

并在 `response.completed` 中返回。

### 7.6 中断和异常处理

#### 上游中断
如果已经发送过 `response.created`：
- 不伪造 `response.completed`
- 记录日志并终止流

#### 事件组装失败
- 优先记录错误日志
- 中止流，避免向客户端发送结构损坏的事件

#### tool arguments 不完整
- delta 阶段允许非完整 JSON
- done 阶段尽量输出累计原始字符串
- 记录兼容降级日志

---

## 8. 错误处理设计

### 8.1 转换阶段错误

如果请求包含关键不兼容字段，应直接返回 `400`。

错误信息要求：

- 明确字段名
- 明确原因
- 明确当前处于 responses-to-chat compatibility mode

示例风格：

- `field "previous_response_id" is not supported in responses-to-chat compatibility mode`

### 8.2 上游错误

沿用现有 relay 错误体系：

- `RelayErrorHandler(...)`
- `ResetStatusCode(...)`

compat 层不新建独立错误体系，仅负责转换前校验错误。

### 8.3 降级日志

对允许降级的字段：

- 不要求对普通客户端暴露复杂 warning 字段
- 必须记录 debug / warning 日志
- 建议追加 request conversion trace

---

## 9. 配额、日志与可观测性

### 9.1 配额

不建议新建独立计费链路。

建议复用现有 usage / quota 流程，只是在 compat 层完成 usage 映射。

### 9.2 conversion chain

建议为本次 fallback 增加 conversion trace，至少标记：

- 入口协议：OpenAI Responses
- 最终上游协议：OpenAI Chat Completions

这样便于日志分析、问题定位、费用核对和后续运维。

### 9.3 日志建议

建议增加如下日志点：

1. 命中 fallback policy
2. 触发 native -> chat fallback
3. 关键字段报错
4. 非关键字段降级
5. 流式状态机异常

---

## 10. 测试方案

### 10.1 请求转换单测

覆盖以下场景：

- `instructions`
- 文本 input
- assistant 历史
- function call
- function call output
- image / audio / file / video
- tools / tool_choice
- incompatible 字段报错
- 可降级字段处理

### 10.2 非流式响应转换单测

覆盖以下场景：

- 普通文本 chat response -> responses
- tool_calls chat response -> responses function_call item
- usage 映射

### 10.3 流式状态机单测

给定一组 chat chunks，断言输出事件序列正确，包括：

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.completed`

### 10.4 handler 集成测试

使用 `httptest`：

- 入口请求 `/v1/responses`
- mock 上游 `/v1/chat/completions`
- 验证最终 response body / SSE event stream

### 10.5 回归测试

必须验证以下路径不受影响：

- 原生 `/v1/responses`
- 现有 `chat -> responses`
- pass-through 模式
- 非 OpenAI ApiType 的既有行为

---

## 11. 实施顺序建议

建议按以下顺序推进，降低风险：

1. 新增 policy 与能力判定器
2. 实现 `Responses -> Chat` 请求转换
3. 实现非流式 `Chat -> Responses` 响应包装
4. 实现流式 SSE 状态机
5. 补全集成测试与回归测试
6. 灰度启用 policy 验证线上行为

---

## 12. 最终建议

建议将本需求定义为：

> OpenAI Responses 到 OpenAI Chat 的高兼容 fallback 能力

该能力应作为项目协议兼容层的重要组成部分实现，并遵循以下原则：

- 以客户端兼容为首要目标
- 以策略控制保证上线安全
- 以结构化 compat 模块保证维护性
- 以强测试覆盖保证协议行为稳定

本设计已明确：

- 范围
- 架构
- 请求/响应映射
- 流式状态机
- 错误处理
- 测试方案
- 实施顺序

可以进入实现计划阶段。
