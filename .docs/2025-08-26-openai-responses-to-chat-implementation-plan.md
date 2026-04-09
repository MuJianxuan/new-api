# OpenAI Responses 到 OpenAI Chat Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `APITypeOpenAI` 增加 `POST /v1/responses` 到上游 `POST /v1/chat/completions` 的高兼容 fallback，覆盖非流式与流式 SSE，并保持现有 relay、quota、error pipeline 不受影响。

**Architecture:** 在 `service/openaicompat/` 中新增对称 compat 模块：请求转换器、非流式响应转换器、流式事件状态机、策略判定器；在 `relay/` 中新增 `responsesViaChatCompletions(...)` 作为接入点，由 `ResponsesHelper` 基于全局策略触发。实现顺序遵循 TDD：先 policy，再请求转换，再非流式，再流式，最后做 handler 集成与回归验证。

**Tech Stack:** Go 1.22+, Gin, existing relay/adaptor architecture, testify/require, httptest, project JSON wrappers in `common/json.go`.

---

## Scope Check

本 spec 聚焦在一个单一子系统：**OpenAI Responses 到 OpenAI Chat 的 fallback 兼容层**。虽然包含 policy、compat、relay、tests 四类工作，但它们围绕同一条请求链路，适合写成一个实施计划，不需要再拆成多个独立 plan。

---

## File Structure

### Existing files to modify

- `setting/model_setting/global.go`
  - 新增 `ResponsesToChatCompletionsPolicy` 配置结构与默认值。
- `service/openaicompat/policy.go`
  - 新增 `ShouldResponsesUseChatCompletionsPolicy(...)` 与 `ShouldResponsesUseChatCompletionsGlobal(...)`。
- `service/openai_chat_responses_compat.go`
  - 导出 `ResponsesRequestToChatCompletionsRequest(...)`、`ChatCompletionsResponseToResponsesResponse(...)` 等 wrapper。
- `relay/responses_handler.go`
  - 在原生 `/v1/responses` 处理前插入 fallback 判定与 `responsesViaChatCompletions(...)` 调度。

### New files to create

- `service/openaicompat/responses_to_chat_request.go`
  - `OpenAIResponsesRequest -> GeneralOpenAIRequest` 请求转换。
- `service/openaicompat/responses_to_chat_request_test.go`
  - 请求转换单测。
- `service/openaicompat/chat_to_responses_response.go`
  - 非流式 `OpenAITextResponse -> OpenAIResponsesResponse` 转换。
- `service/openaicompat/chat_to_responses_response_test.go`
  - 非流式响应转换单测。
- `service/openaicompat/chat_to_responses_stream.go`
  - Chat chunk -> Responses SSE 事件状态机与 builder。
- `service/openaicompat/chat_to_responses_stream_test.go`
  - 流式状态机单测。
- `service/openaicompat/policy_test.go`
  - policy 判定单测。
- `relay/responses_via_chat_completions.go`
  - 复用现有 `chat_completions_via_responses.go` 样式，实现反向桥接。
- `relay/responses_via_chat_completions_test.go`
  - handler/integration 测试，mock 上游 `/v1/chat/completions`。

### Optional follow-up doc file

- `docs/openapi/relay.json`
  - 本计划不修改；若实现完成后需要对外公开说明 fallback 行为，再单独评估是否更新文档。

---

### Task 1: 新增全局策略与 policy 判定

**Files:**
- Modify: `setting/model_setting/global.go`
- Modify: `service/openaicompat/policy.go`
- Modify: `service/openai_chat_responses_compat.go`
- Test: `service/openaicompat/policy_test.go`

- [ ] **Step 1: 写失败测试，先固定 policy 行为**

创建 `service/openaicompat/policy_test.go`：

```go
package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/require"
)

func TestShouldResponsesUseChatCompletionsPolicy(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		policy      model_setting.ResponsesToChatCompletionsPolicy
		channelID   int
		channelType int
		model       string
		expected    bool
	}{
		{
			name: "disabled policy",
			policy: model_setting.ResponsesToChatCompletionsPolicy{
				Enabled:     false,
				AllChannels: true,
			},
			channelID:   1,
			channelType: 1,
			model:       "gpt-4.1",
			expected:    false,
		},
		{
			name: "enabled all channels with model match",
			policy: model_setting.ResponsesToChatCompletionsPolicy{
				Enabled:       true,
				AllChannels:   true,
				ModelPatterns: []string{"^gpt-4\\.1$"},
			},
			channelID:   1,
			channelType: 1,
			model:       "gpt-4.1",
			expected:    true,
		},
		{
			name: "enabled but model mismatch",
			policy: model_setting.ResponsesToChatCompletionsPolicy{
				Enabled:       true,
				AllChannels:   true,
				ModelPatterns: []string{"^gpt-4o$"},
			},
			channelID:   1,
			channelType: 1,
			model:       "gpt-4.1",
			expected:    false,
		},
		{
			name: "channel id allowed",
			policy: model_setting.ResponsesToChatCompletionsPolicy{
				Enabled:       true,
				ChannelIDs:    []int{7},
				ModelPatterns: []string{"^gpt-4\\.1$"},
			},
			channelID:   7,
			channelType: 2,
			model:       "gpt-4.1",
			expected:    true,
		},
		{
			name: "channel type allowed",
			policy: model_setting.ResponsesToChatCompletionsPolicy{
				Enabled:       true,
				ChannelTypes:  []int{9},
				ModelPatterns: []string{"^gpt-4\\.1$"},
			},
			channelID:   7,
			channelType: 9,
			model:       "gpt-4.1",
			expected:    true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := ShouldResponsesUseChatCompletionsPolicy(tc.policy, tc.channelID, tc.channelType, tc.model)
			require.Equal(t, tc.expected, actual)
		})
	}
}
```

- [ ] **Step 2: 运行测试，确认当前失败**

Run:

```bash
go test ./service/openaicompat -run TestShouldResponsesUseChatCompletionsPolicy -count=1
```

Expected:

```text
FAIL ... undefined: model_setting.ResponsesToChatCompletionsPolicy
FAIL ... undefined: ShouldResponsesUseChatCompletionsPolicy
```

- [ ] **Step 3: 在全局配置中增加对称 policy 结构**

修改 `setting/model_setting/global.go`，新增结构、字段和默认值。核心代码应变成：

```go
type ResponsesToChatCompletionsPolicy struct {
	Enabled       bool     `json:"enabled"`
	AllChannels   bool     `json:"all_channels"`
	ChannelIDs    []int    `json:"channel_ids,omitempty"`
	ChannelTypes  []int    `json:"channel_types,omitempty"`
	ModelPatterns []string `json:"model_patterns,omitempty"`
}

func (p ResponsesToChatCompletionsPolicy) IsChannelEnabled(channelID int, channelType int) bool {
	if !p.Enabled {
		return false
	}
	if p.AllChannels {
		return true
	}
	if channelID > 0 && len(p.ChannelIDs) > 0 && slices.Contains(p.ChannelIDs, channelID) {
		return true
	}
	if channelType > 0 && len(p.ChannelTypes) > 0 && slices.Contains(p.ChannelTypes, channelType) {
		return true
	}
	return false
}

type GlobalSettings struct {
	PassThroughRequestEnabled         bool                                 `json:"pass_through_request_enabled"`
	ThinkingModelBlacklist            []string                             `json:"thinking_model_blacklist"`
	ChatCompletionsToResponsesPolicy  ChatCompletionsToResponsesPolicy     `json:"chat_completions_to_responses_policy"`
	ResponsesToChatCompletionsPolicy  ResponsesToChatCompletionsPolicy     `json:"responses_to_chat_completions_policy"`
}

var defaultOpenaiSettings = GlobalSettings{
	PassThroughRequestEnabled: false,
	ThinkingModelBlacklist: []string{
		"moonshotai/kimi-k2-thinking",
		"kimi-k2-thinking",
	},
	ChatCompletionsToResponsesPolicy: ChatCompletionsToResponsesPolicy{
		Enabled:     false,
		AllChannels: true,
	},
	ResponsesToChatCompletionsPolicy: ResponsesToChatCompletionsPolicy{
		Enabled:     false,
		AllChannels: true,
	},
}
```

- [ ] **Step 4: 在 compat policy 层增加反向判定函数**

修改 `service/openaicompat/policy.go`，保留现有函数不动，新增：

```go
func ShouldResponsesUseChatCompletionsPolicy(policy model_setting.ResponsesToChatCompletionsPolicy, channelID int, channelType int, model string) bool {
	if !policy.IsChannelEnabled(channelID, channelType) {
		return false
	}
	return matchAnyRegex(policy.ModelPatterns, model)
}

func ShouldResponsesUseChatCompletionsGlobal(channelID int, channelType int, model string) bool {
	return ShouldResponsesUseChatCompletionsPolicy(
		model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy,
		channelID,
		channelType,
		model,
	)
}
```

同时修改 `service/openai_chat_responses_compat.go`，增加 wrapper：

```go
func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return openaicompat.ResponsesRequestToChatCompletionsRequest(req)
}

func ChatCompletionsResponseToResponsesResponse(resp *dto.OpenAITextResponse, id string) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	return openaicompat.ChatCompletionsResponseToResponsesResponse(resp, id)
}
```

- [ ] **Step 5: 运行测试，确认 policy 行为通过**

Run:

```bash
go test ./service/openaicompat -run TestShouldResponsesUseChatCompletionsPolicy -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
```

- [ ] **Step 6: Commit**

```bash
git add setting/model_setting/global.go service/openaicompat/policy.go service/openaicompat/policy_test.go service/openai_chat_responses_compat.go
git commit -m "feat: add responses to chat fallback policy"
```

---

### Task 2: 实现 Responses -> Chat 请求转换器

**Files:**
- Create: `service/openaicompat/responses_to_chat_request.go`
- Test: `service/openaicompat/responses_to_chat_request_test.go`
- Modify: `service/openai_chat_responses_compat.go`

- [ ] **Step 1: 写失败测试，先锁定最小语义映射**

创建 `service/openaicompat/responses_to_chat_request_test.go`：

```go
package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesRequestToChatCompletionsRequest_TextAndInstructions(t *testing.T) {
	t.Parallel()

	req := &dto.OpenAIResponsesRequest{
		Model:        "gpt-4.1",
		Instructions: "You are helpful.",
		Input: []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "hello"},
				},
			},
		},
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Equal(t, "gpt-4.1", chatReq.Model)
	require.Len(t, chatReq.Messages, 2)
	require.Equal(t, "system", chatReq.Messages[0].Role)
	require.Equal(t, "You are helpful.", chatReq.Messages[0].StringContent())
	require.Equal(t, "user", chatReq.Messages[1].Role)
	require.Equal(t, "hello", chatReq.Messages[1].ParseContent()[0].Text)
}

func TestResponsesRequestToChatCompletionsRequest_FunctionToolOutput(t *testing.T) {
	t.Parallel()

	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Input: []any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  "{\"city\":\"Hangzhou\"}",
			},
		},
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chatReq.Messages, 1)
	require.Equal(t, "tool", chatReq.Messages[0].Role)
	require.Equal(t, "call_1", chatReq.Messages[0].ToolCallId)
	require.Equal(t, "{\"city\":\"Hangzhou\"}", chatReq.Messages[0].StringContent())
}

func TestResponsesRequestToChatCompletionsRequest_BuiltInToolRejected(t *testing.T) {
	t.Parallel()

	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Tools: []map[string]any{
			{"type": "web_search_preview"},
		},
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.Nil(t, chatReq)
	require.ErrorContains(t, err, "tool type \"web_search_preview\" is not supported")
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./service/openaicompat -run TestResponsesRequestToChatCompletionsRequest -count=1
```

Expected:

```text
FAIL ... undefined: ResponsesRequestToChatCompletionsRequest
```

- [ ] **Step 3: 写最小实现，先支持 instructions/text/tool output/function tools**

创建 `service/openaicompat/responses_to_chat_request.go`，先写清晰、最小、可测的实现；不要一次塞满全部边界。先包含以下核心结构：

```go
package openaicompat

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
)

const (
	responsesToChatUnsupportedToolFormat = "tool type %q is not supported in responses-to-chat compatibility mode"
)

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}

	messages := make([]dto.Message, 0)
	if s := strings.TrimSpace(req.Instructions); s != "" {
		messages = append(messages, dto.Message{Role: "system", Content: s})
	}

	converted, err := convertResponsesInputItemsToChatMessages(req.Input)
	if err != nil {
		return nil, err
	}
	messages = append(messages, converted...)

	tools, err := convertResponsesToolsToChatTools(req.Tools)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   lo.ToPtr(req.Stream),
		User:     req.User,
	}
	return out, nil
}
```

在同文件中继续补：
- `convertResponsesInputItemsToChatMessages(...)`
- `convertResponsesToolsToChatTools(...)`
- `convertResponsesContentItemsToMediaContents(...)`

要求：
- 文本 item -> `dto.MediaContent{Type: dto.ContentTypeText}`
- `function_call_output` -> `role=tool`
- built-in tools -> 直接报错
- 不要直接使用 `encoding/json` marshal/unmarshal，遵守项目 `common/json.go` 规则

- [ ] **Step 4: 扩充测试，补多模态与 response_format/limits**

在 `service/openaicompat/responses_to_chat_request_test.go` 追加：

```go
func TestResponsesRequestToChatCompletionsRequest_MultimodalAndLimits(t *testing.T) {
	t.Parallel()

	req := &dto.OpenAIResponsesRequest{
		Model:           "gpt-4.1",
		MaxOutputTokens: lo.ToPtr(uint(128)),
		Text:            []byte(`{"format":{"type":"json_object"}}`),
		Input: []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "describe image"},
					map[string]any{"type": "input_image", "image_url": "https://example.com/a.png"},
				},
			},
		},
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.NotNil(t, chatReq.MaxTokens)
	require.Equal(t, uint(128), *chatReq.MaxTokens)
	require.NotNil(t, chatReq.ResponseFormat)
	parts := chatReq.Messages[0].ParseContent()
	require.Len(t, parts, 2)
	require.Equal(t, dto.ContentTypeText, parts[0].Type)
	require.Equal(t, dto.ContentTypeImageURL, parts[1].Type)
}
```

同时补 import：

```go
import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 5: 实现剩余最小字段映射，使测试通过**

在 `service/openaicompat/responses_to_chat_request.go` 中补齐：

```go
func convertResponsesContentItemsToMediaContents(items []map[string]any, role string) ([]dto.MediaContent, error) {
	contents := make([]dto.MediaContent, 0, len(items))
	for _, item := range items {
		switch strings.TrimSpace(common.Interface2String(item["type"])) {
		case "input_text", "output_text":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeText, Text: common.Interface2String(item["text"])})
		case "input_image":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: common.Interface2String(item["image_url"])})
		case "input_audio":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeInputAudio, InputAudio: item["input_audio"]})
		case "input_file":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeFile, File: item["file"]})
		default:
			return nil, fmt.Errorf("input item type %q is not supported in responses-to-chat compatibility mode", common.Interface2String(item["type"]))
		}
	}
	return contents, nil
}
```

并补：
- `req.MaxOutputTokens -> chatReq.MaxTokens`
- `req.Text -> chatReq.ResponseFormat`
- `req.ToolChoice -> chatReq.ToolChoice`

- [ ] **Step 6: 运行包级测试**

Run:

```bash
go test ./service/openaicompat -run 'TestResponsesRequestToChatCompletionsRequest|TestShouldResponsesUseChatCompletionsPolicy' -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
```

- [ ] **Step 7: Commit**

```bash
git add service/openaicompat/responses_to_chat_request.go service/openaicompat/responses_to_chat_request_test.go service/openai_chat_responses_compat.go
git commit -m "feat: add responses to chat request conversion"
```

---

### Task 3: 实现非流式 Chat -> Responses 响应包装

**Files:**
- Create: `service/openaicompat/chat_to_responses_response.go`
- Test: `service/openaicompat/chat_to_responses_response_test.go`
- Modify: `service/openai_chat_responses_compat.go`

- [ ] **Step 1: 写失败测试，先固定文本与 tool_calls 包装格式**

创建 `service/openaicompat/chat_to_responses_response_test.go`：

```go
package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsResponseToResponsesResponse_Text(t *testing.T) {
	t.Parallel()

	resp := &dto.OpenAITextResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion",
		Created: 1720000000,
		Model:   "gpt-4.1",
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: "hello from chat",
			},
			FinishReason: "stop",
		}},
		Usage: dto.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
	}

	out, usage, err := ChatCompletionsResponseToResponsesResponse(resp, "resp_1")
	require.NoError(t, err)
	require.Equal(t, "response", out.Object)
	require.Equal(t, "resp_1", out.ID)
	require.Equal(t, "gpt-4.1", out.Model)
	require.Len(t, out.Output, 1)
	require.Equal(t, "message", out.Output[0].Type)
	require.Equal(t, "assistant", out.Output[0].Role)
	require.Equal(t, "output_text", out.Output[0].Content[0].Type)
	require.Equal(t, "hello from chat", out.Output[0].Content[0].Text)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 18, usage.TotalTokens)
}

func TestChatCompletionsResponseToResponsesResponse_ToolCalls(t *testing.T) {
	t.Parallel()

	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls([]dto.ToolCallResponse{{
		ID:   "call_1",
		Type: "function",
		Function: dto.FunctionResponse{
			Name:      "get_weather",
			Arguments: `{"city":"Hangzhou"}`,
		},
	}})

	resp := &dto.OpenAITextResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion",
		Created: 1720000000,
		Model:   "gpt-4.1",
		Choices: []dto.OpenAITextResponseChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: "tool_calls",
		}},
		Usage: dto.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
	}

	out, _, err := ChatCompletionsResponseToResponsesResponse(resp, "resp_1")
	require.NoError(t, err)
	require.Len(t, out.Output, 1)
	require.Equal(t, "function_call", out.Output[0].Type)
	require.Equal(t, "call_1", out.Output[0].CallId)
	require.Equal(t, "get_weather", out.Output[0].Name)
	require.Equal(t, `{"city":"Hangzhou"}`, out.Output[0].Arguments)
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./service/openaicompat -run TestChatCompletionsResponseToResponsesResponse -count=1
```

Expected:

```text
FAIL ... undefined: ChatCompletionsResponseToResponsesResponse
```

- [ ] **Step 3: 写最小实现**

创建 `service/openaicompat/chat_to_responses_response.go`：

```go
package openaicompat

import (
	"errors"

	"github.com/QuantumNous/new-api/dto"
)

func ChatCompletionsResponseToResponsesResponse(resp *dto.OpenAITextResponse, id string) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	if resp == nil {
		return nil, nil, errors.New("response is nil")
	}
	if len(resp.Choices) == 0 {
		return nil, nil, errors.New("response choices are empty")
	}

	choice := resp.Choices[0]
	usage := &dto.Usage{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  resp.Usage.TotalTokens,
	}

	out := &dto.OpenAIResponsesResponse{
		ID:        id,
		Object:    "response",
		CreatedAt: resp.Created,
		Model:     resp.Model,
		Usage: &dto.ResponsesUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			TotalTokens:  usage.TotalTokens,
		},
	}

	responseItems := buildResponsesOutputItemsFromChatMessage(choice.Message)
	out.Output = responseItems
	return out, usage, nil
}
```

同文件补一个 focused helper：

```go
func buildResponsesOutputItemsFromChatMessage(msg dto.Message) []dto.OpenAIResponsesOutputItem {
	toolCalls := msg.ParseToolCalls()
	if len(toolCalls) > 0 {
		items := make([]dto.OpenAIResponsesOutputItem, 0, len(toolCalls))
		for _, tc := range toolCalls {
			items = append(items, dto.OpenAIResponsesOutputItem{
				Type:      "function_call",
				CallId:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		return items
	}
	return []dto.OpenAIResponsesOutputItem{{
		Type: "message",
		Role: "assistant",
		Content: []dto.OpenAIResponsesOutputContent{{
			Type: "output_text",
			Text: msg.StringContent(),
		}},
	}}
}
```

- [ ] **Step 4: 导出 wrapper 并跑测试**

修改 `service/openai_chat_responses_compat.go`，增加：

```go
func ChatCompletionsResponseToResponsesResponse(resp *dto.OpenAITextResponse, id string) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	return openaicompat.ChatCompletionsResponseToResponsesResponse(resp, id)
}
```

Run:

```bash
go test ./service/openaicompat -run TestChatCompletionsResponseToResponsesResponse -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
```

- [ ] **Step 5: Commit**

```bash
git add service/openaicompat/chat_to_responses_response.go service/openaicompat/chat_to_responses_response_test.go service/openai_chat_responses_compat.go
git commit -m "feat: add chat to responses non-stream conversion"
```

---

### Task 4: 实现 Chat SSE -> Responses SSE 状态机

**Files:**
- Create: `service/openaicompat/chat_to_responses_stream.go`
- Test: `service/openaicompat/chat_to_responses_stream_test.go`

- [ ] **Step 1: 写失败测试，锁定文本事件序列**

创建 `service/openaicompat/chat_to_responses_stream_test.go`：

```go
package openaicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesStreamBuilder_TextLifecycle(t *testing.T) {
	t.Parallel()

	builder := NewResponsesStreamBuilder("resp_1", "gpt-4.1", 1720000000)

	events := builder.OnTextDelta("Hel")
	require.Len(t, events, 3)
	require.Equal(t, "response.created", events[0].Event)
	require.Equal(t, "response.output_item.added", events[1].Event)
	require.Equal(t, "response.output_text.delta", events[2].Event)

	events = builder.OnTextDelta("lo")
	require.Len(t, events, 1)
	require.Equal(t, "response.output_text.delta", events[0].Event)

	events = builder.OnMessageDone("stop", 11, 7, 18)
	require.Len(t, events, 3)
	require.Equal(t, "response.output_text.done", events[0].Event)
	require.Equal(t, "response.output_item.done", events[1].Event)
	require.Equal(t, "response.completed", events[2].Event)
}
```

- [ ] **Step 2: 写第二个失败测试，锁定 tool arguments 增量**

在同文件追加：

```go
func TestResponsesStreamBuilder_FunctionCallLifecycle(t *testing.T) {
	t.Parallel()

	builder := NewResponsesStreamBuilder("resp_1", "gpt-4.1", 1720000000)

	events := builder.OnFunctionCallDelta(0, "call_1", "get_weather", `{"city":`)
	require.Len(t, events, 3)
	require.Equal(t, "response.created", events[0].Event)
	require.Equal(t, "response.output_item.added", events[1].Event)
	require.Equal(t, "response.function_call_arguments.delta", events[2].Event)

	events = builder.OnFunctionCallDelta(0, "call_1", "get_weather", `"Hangzhou"}`)
	require.Len(t, events, 1)
	require.Equal(t, "response.function_call_arguments.delta", events[0].Event)

	events = builder.OnFunctionCallDone(0, "tool_calls", 20, 10, 30)
	require.Len(t, events, 3)
	require.Equal(t, "response.function_call_arguments.done", events[0].Event)
	require.Equal(t, "response.output_item.done", events[1].Event)
	require.Equal(t, "response.completed", events[2].Event)
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run:

```bash
go test ./service/openaicompat -run TestResponsesStreamBuilder -count=1
```

Expected:

```text
FAIL ... undefined: NewResponsesStreamBuilder
```

- [ ] **Step 4: 写最小状态机实现**

创建 `service/openaicompat/chat_to_responses_stream.go`：

```go
package openaicompat

import "github.com/QuantumNous/new-api/common"

type ResponsesSSEEvent struct {
	Event string
	Data  []byte
}

type ResponsesStreamBuilder struct {
	responseID      string
	model           string
	createdAt       int64
	createdSent     bool
	textItemOpen    bool
	completedSent   bool
	textBuffer      string
	functionBuffers map[int]string
}

func NewResponsesStreamBuilder(responseID string, model string, createdAt int64) *ResponsesStreamBuilder {
	return &ResponsesStreamBuilder{
		responseID:      responseID,
		model:           model,
		createdAt:       createdAt,
		functionBuffers: map[int]string{},
	}
}
```

继续补 helper：
- `emit(event string, payload any) ResponsesSSEEvent`
- `ensureCreated() []ResponsesSSEEvent`
- `OnTextDelta(delta string) []ResponsesSSEEvent`
- `OnMessageDone(finishReason string, inputTokens int, outputTokens int, totalTokens int) []ResponsesSSEEvent`
- `OnFunctionCallDelta(index int, callID string, name string, delta string) []ResponsesSSEEvent`
- `OnFunctionCallDone(index int, finishReason string, inputTokens int, outputTokens int, totalTokens int) []ResponsesSSEEvent`

关键要求：
- 所有 JSON 编码使用 `common.Marshal`
- `response.created` 仅发一次
- 文本 item 生命周期必须完整闭合
- function call arguments 增量按 `index` 聚合
- `response.completed` 仅发一次

- [ ] **Step 5: 跑状态机测试**

Run:

```bash
go test ./service/openaicompat -run TestResponsesStreamBuilder -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
```

- [ ] **Step 6: Commit**

```bash
git add service/openaicompat/chat_to_responses_stream.go service/openaicompat/chat_to_responses_stream_test.go
git commit -m "feat: add chat to responses stream builder"
```

---

### Task 5: 在 relay 中接入 responsesViaChatCompletions 反向桥接

**Files:**
- Create: `relay/responses_via_chat_completions.go`
- Modify: `relay/responses_handler.go`
- Test: `relay/responses_via_chat_completions_test.go`

- [ ] **Step 1: 写失败测试，先证明命中 policy 后会走 fallback**

创建 `relay/responses_via_chat_completions_test.go`，先做 focused 单测，不急着走全套真实 adaptor：

```go
package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldUseResponsesViaChatCompletions(t *testing.T) {
	t.Parallel()

	old := model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy
	defer func() {
		model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy = old
	}()
	model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy = model_setting.ResponsesToChatCompletionsPolicy{
		Enabled:       true,
		AllChannels:   true,
		ModelPatterns: []string{"^gpt-4\\.1$"},
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	info := &relaycommon.RelayInfo{
		ApiType:        1,
		RelayMode:      relayconstant.RelayModeResponses,
		ChannelId:      11,
		ChannelType:    22,
		OriginModelName:"gpt-4.1",
		Request: &dto.OpenAIResponsesRequest{Model: "gpt-4.1"},
	}

	require.True(t, shouldUseResponsesViaChatCompletions(ctx, info))
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./relay -run TestShouldUseResponsesViaChatCompletions -count=1
```

Expected:

```text
FAIL ... undefined: shouldUseResponsesViaChatCompletions
```

- [ ] **Step 3: 实现判定函数与桥接骨架**

创建 `relay/responses_via_chat_completions.go`：

```go
package relay

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func shouldUseResponsesViaChatCompletions(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if c == nil || info == nil {
		return false
	}
	if info.ApiType != appconstant.APITypeOpenAI {
		return false
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return false
	}
	return service.ShouldResponsesUseChatCompletionsGlobal(info.ChannelId, info.ChannelType, info.OriginModelName)
}
```

在同文件中继续写 `responsesViaChatCompletions(...)`，实现方式严格参考 `relay/chat_completions_via_responses.go`：
- DeepCopy request
- `service.ResponsesRequestToChatCompletionsRequest(...)`
- `info.AppendRequestConversion(types.RelayFormatOpenAI)`
- 暂存并切换 `info.RelayMode = relayconstant.RelayModeChatCompletions`
- `info.RequestURLPath = "/v1/chat/completions"`
- `adaptor.ConvertOpenAIRequest(...)`
- `adaptor.DoRequest(...)`
- 非流式：读取 chat 响应 -> `service.ChatCompletionsResponseToResponsesResponse(...)`
- 流式：走新的 stream builder 输出 Responses SSE

- [ ] **Step 4: 在 ResponsesHelper 中接线**

修改 `relay/responses_handler.go`，在拿到 `adaptor` 并 `adaptor.Init(info)` 之后、原生 `ConvertOpenAIResponsesRequest(...)` 之前插入：

```go
	if info.RelayMode == relayconstant.RelayModeResponses && shouldUseResponsesViaChatCompletions(c, info) {
		usage, newAPIError := responsesViaChatCompletions(c, info, adaptor, request)
		if newAPIError != nil {
			service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
			return newAPIError
		}
		usageDto := usage
		if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
			service.PostAudioConsumeQuota(c, info, usageDto, "")
		} else {
			service.PostTextConsumeQuota(c, info, usageDto, nil)
		}
		return nil
	}
```

注意：
- 这里 `usage` 直接定义为 `*dto.Usage`
- 不要破坏 `responses/compact` 原有分支
- 不要影响 pass-through 模式

- [ ] **Step 5: 跑 relay 单测**

Run:

```bash
go test ./relay -run TestShouldUseResponsesViaChatCompletions -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/relay
```

- [ ] **Step 6: Commit**

```bash
git add relay/responses_via_chat_completions.go relay/responses_via_chat_completions_test.go relay/responses_handler.go
git commit -m "feat: add responses via chat relay fallback"
```

---

### Task 6: 完成流式与非流式 integration tests，并做回归验证

**Files:**
- Modify: `relay/responses_via_chat_completions_test.go`
- Test: `service/openaicompat/responses_to_chat_request_test.go`
- Test: `service/openaicompat/chat_to_responses_response_test.go`
- Test: `service/openaicompat/chat_to_responses_stream_test.go`

- [ ] **Step 1: 在 relay 集成测试中加入非流式 mock 上游**

在 `relay/responses_via_chat_completions_test.go` 追加一个 `httptest.NewServer` 用例；测试结构类似：

```go
func TestResponsesViaChatCompletions_NonStream(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"created":1720000000,
			"model":"gpt-4.1",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
		}`))
	}))
	defer upstream.Close()

	// 这里构造 gin ctx、RelayInfo、mock adaptor info.ChannelBaseUrl = upstream.URL
	// 直接调用 responsesViaChatCompletions(...)，断言 recorder.Body 中出现：
	// "object":"response"
	// "type":"message"
	// "output_text"
}
```

要求：
- 测试断言转换后的输出是 Responses 风格，不只是 200
- 断言 usage 被映射

- [ ] **Step 2: 在 relay 集成测试中加入流式 mock 上游**

在同文件追加流式用例：

```go
func TestResponsesViaChatCompletions_Stream(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1720000000,\"model\":\"gpt-4.1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1720000000,\"model\":\"gpt-4.1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1720000000,\"model\":\"gpt-4.1\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	// 调用 responsesViaChatCompletions(...) 后断言输出包含：
	// event: response.created
	// event: response.output_text.delta
	// event: response.output_text.done
	// event: response.completed
}
```

- [ ] **Step 3: 运行 compat 与 relay 目标测试**

Run:

```bash
go test ./service/openaicompat ./relay -run 'TestResponses|TestChatCompletionsResponseToResponsesResponse|TestResponsesStreamBuilder|TestShouldUseResponsesViaChatCompletions' -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
ok  github.com/QuantumNous/new-api/relay
```

- [ ] **Step 4: 跑回归测试，确保现有链路不受影响**

Run:

```bash
go test ./service/openaicompat ./relay/... -count=1
```

Expected:

```text
ok  github.com/QuantumNous/new-api/service/openaicompat
ok  github.com/QuantumNous/new-api/relay/...
```

如果这里出现编译或行为回归，优先检查：
- `relay/responses_handler.go` 是否改坏了原生 `/v1/responses`
- `service/openai_chat_responses_compat.go` wrapper 是否命名冲突
- 新增 stream builder 是否误用了 `encoding/json`

- [ ] **Step 5: Commit**

```bash
git add relay/responses_via_chat_completions_test.go service/openaicompat/responses_to_chat_request_test.go service/openaicompat/chat_to_responses_response_test.go service/openaicompat/chat_to_responses_stream_test.go
git commit -m "test: cover responses to chat fallback integration"
```

---

## Self-Review

### 1. Spec coverage

- **新增策略配置** → Task 1
- **Responses -> Chat 请求转换** → Task 2
- **Chat -> Responses 非流式包装** → Task 3
- **Chat SSE -> Responses SSE 状态机** → Task 4
- **relay 接入与 policy 触发** → Task 5
- **集成测试与回归测试** → Task 6

未发现漏掉的主需求。

### 2. Placeholder scan

已检查并移除以下风险模式：
- 没有使用 `TODO` / `TBD`
- 没有写“自行补错误处理”这类空话
- 每个任务都给了具体文件路径、命令、预期输出
- 代码步骤都给了可落地的函数名和结构名

### 3. Type consistency

本计划内使用的关键函数名保持一致：
- `ShouldResponsesUseChatCompletionsPolicy`
- `ShouldResponsesUseChatCompletionsGlobal`
- `ResponsesRequestToChatCompletionsRequest`
- `ChatCompletionsResponseToResponsesResponse`
- `NewResponsesStreamBuilder`
- `responsesViaChatCompletions`
- `shouldUseResponsesViaChatCompletions`

后续实现若发现 DTO 字段名与计划中的局部示例不一致，应以仓库实际 DTO 为准，但函数名不要再改，避免任务间断裂。

---

Plan complete and saved to `.docs/2025-08-26-openai-responses-to-chat-implementation-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
