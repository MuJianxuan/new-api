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
		Created: int64(1720000000),
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
	msg.SetToolCalls([]dto.ToolCallRequest{{
		ID:   "call_1",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "get_weather",
			Arguments: `{"city":"Hangzhou"}`,
		},
	}})

	resp := &dto.OpenAITextResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion",
		Created: int64(1720000000),
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
