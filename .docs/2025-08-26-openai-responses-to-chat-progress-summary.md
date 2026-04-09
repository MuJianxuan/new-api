# OpenAI Responses -> OpenAI Chat Fallback 阶段性总结

## 1. 本轮目标

本轮实现目标是为 `APITypeOpenAI` 增加：

- 客户端调用 `POST /v1/responses`
- 网关在命中策略时自动 fallback 到上游 `POST /v1/chat/completions`
- 再把上游 chat 响应包装回 OpenAI Responses 协议

当前实现优先覆盖：

- policy 开关与触发条件
- `Responses -> Chat` 请求转换
- `Chat -> Responses` 非流式响应转换
- `Chat -> Responses` 流式 SSE 事件构建
- relay 层 fallback 接入

---

## 2. 已修改/新增文件

### 配置与策略
- `setting/model_setting/global.go`
- `service/openaicompat/policy.go`
- `service/openaicompat/policy_test.go`
- `service/openai_chat_responses_compat.go`

### 请求转换
- `service/openaicompat/responses_to_chat_request.go`
- `service/openaicompat/responses_to_chat_request_test.go`

### 非流式响应转换
- `service/openaicompat/chat_to_responses_response.go`
- `service/openaicompat/chat_to_responses_response_test.go`

### 流式响应转换
- `service/openaicompat/chat_to_responses_stream.go`
- `service/openaicompat/chat_to_responses_stream_test.go`

### relay 接入
- `relay/responses_via_chat_completions.go`
- `relay/responses_via_chat_completions_test.go`
- `relay/responses_handler.go`

---

## 3. 当前已实现能力

## 3.1 Policy
新增了对称策略：

- `ResponsesToChatCompletionsPolicy`
- `ShouldResponsesUseChatCompletionsPolicy(...)`
- `ShouldResponsesUseChatCompletionsGlobal(...)`

当前默认值：

```json
{
  "enabled": false,
  "all_channels": true
}
```

即：默认不自动启用 fallback，需要显式打开。

---

## 3.2 Responses -> Chat 请求转换
当前已落地的映射包括：

- `instructions -> system message`
- `input(string/json array) -> chat messages`
- `function_call_output -> tool role message`
- `function_call -> assistant tool_calls`
- 文本 / 图片 / 音频 / 文件 / 视频 输入转换
- `tools(function)` -> chat tools
- built-in tools 显式拒绝
- `max_output_tokens -> max_tokens`
- `text.format -> response_format`
- `reasoning.effort -> reasoning_effort`
- `parallel_tool_calls -> ParallelTooCalls`

当前策略是：
- 关键语义不兼容时报错
- built-in tools 先拒绝，不做静默错误降级

---

## 3.3 Chat -> Responses 非流式转换
当前已实现：

- `OpenAITextResponse -> OpenAIResponsesResponse`
- 文本响应包装为：
  - `object: "response"`
  - `output[].type = "message"`
  - `content[].type = "output_text"`
- tool calls 包装为：
  - `output[].type = "function_call"`
  - `call_id / name / arguments`
- usage 映射：
  - `prompt_tokens -> input_tokens`
  - `completion_tokens -> output_tokens`
  - `total_tokens -> total_tokens`

---

## 3.4 Chat -> Responses 流式 SSE
当前已实现的首版事件包括：

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.completed`

当前 builder 已覆盖两条主路径：

1. 文本流
2. tool call 参数增量流

并且已经修过一个关键问题：

- 首个 chunk 的 `response.created` 漏发问题已修正

---

## 3.5 relay 接入
当前已新增：

- `responsesViaChatCompletions(...)`

接入链路为：

1. `ResponsesHelper` 中判断是否命中 fallback policy
2. 命中后调用 `responsesViaChatCompletions(...)`
3. `ResponsesRequestToChatCompletionsRequest(...)`
4. `adaptor.ConvertOpenAIRequest(...)`
5. 上游 `/v1/chat/completions`
6. 非流式：包装成 Responses response
7. 流式：包装成 Responses SSE

当前只针对：

- `APITypeOpenAI`
- 非 pass-through
- `RelayModeResponses`

---

## 4. 本轮主动修复的问题

在实现过程中已主动修掉这些明确问题：

1. 多模态消息写入方式错误
   - 由直接塞 `Content` 改为 `SetMediaContent(...)`

2. `response.created` 首包漏发
   - 改为独立 `createdSent` 状态控制

3. compat wrapper 缺失
   - 已补 `service.NewChatToResponsesStreamBuilder()`

4. 测试中的错误常量引用
   - `dto.BuildInToolWebSearchPreview` 不存在，已改为字符串 `"web_search_preview"`

5. relay 反向 handler 的未使用参数
   - 已清理

---

## 5. 当前已知风险

虽然主链已成形，但当前仍然**不能宣称功能已完成**，原因是：

### 5.1 尚未完成真实编译/测试验证
当前环境阻塞在 Go toolchain 下载，原始错误为：

```text
go: download go1.25.1: golang.org/toolchain@v0.0.1-go1.25.1.darwin-arm64.zip: dial tcp 142.250.73.113:443: i/o timeout
```

因此目前还没有拿到：

- `go test` 全通过
- 真实编译通过
- 集成测试通过

### 5.2 仍可能存在的剩余问题
由于缺少最终编译验证，当前仍可能残留：

- 少数 import/未使用变量问题
- 个别 DTO 字段名不一致
- 个别测试断言与真实结构不完全一致
- 流式边缘事件或 tool 并发场景处理不足

---

## 6. 待验证项

一旦环境恢复可跑测试，优先验证：

### 6.1 包级测试
```bash
go test ./service/openaicompat -count=1
```

### 6.2 relay 相关测试
```bash
go test ./relay/... -count=1
```

### 6.3 重点验证路径
1. 非流式 `/v1/responses -> /v1/chat/completions`
2. 流式 `/v1/responses -> /v1/chat/completions`
3. 文本输入
4. function tools / tool outputs
5. 多模态输入
6. fallback policy 开/关行为
7. pass-through 不受影响
8. 原生 `/v1/responses` 不受影响

---

## 7. 当前结论

当前代码状态可以定义为：

> **主功能链路已实现首版，但仍处于“待编译/测试验证”的阶段性完成状态。**

也就是说：
- 设计已落地到代码
- 主链已接通
- 关键已知 bug 已修
- 但还不能在没有测试结果的情况下宣称 fully done

---

## 8. 建议下一步

环境恢复后按以下顺序推进：

1. 跑 `service/openaicompat` 测试
2. 跑 `relay` 测试
3. 修剩余编译/测试错误
4. 做一次真实请求回归验证
5. 再决定是否进入第二轮增强（built-in tools / 更高拟真 SSE / 更细粒度错误语义）
