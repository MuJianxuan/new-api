package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChatToResponsesStreamBuilder_TextFlow(t *testing.T) {
	t.Parallel()

	builder := NewChatToResponsesStreamBuilder()
	chunk1 := dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion.chunk",
		Created: 1720000000,
		Model:   "gpt-4.1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant", Content: stringPtr("hel")},
		}},
	}

	events1, err := builder.ConsumeChunk(chunk1)
	require.NoError(t, err)
	require.Len(t, events1, 3)
	require.Equal(t, responsesEventCreated, events1[0].Type)
	require.Equal(t, responsesEventOutputItemAdded, events1[1].Type)
	require.Equal(t, responsesEventOutputTextDelta, events1[2].Type)
	require.Equal(t, "hel", events1[2].Delta)

	finishReason := "stop"
	chunk2 := dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion.chunk",
		Created: 1720000000,
		Model:   "gpt-4.1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: stringPtr("lo")},
			FinishReason: &finishReason,
		}},
		Usage: &dto.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}

	events2, err := builder.ConsumeChunk(chunk2)
	require.NoError(t, err)
	require.Len(t, events2, 4)
	require.Equal(t, responsesEventOutputTextDelta, events2[0].Type)
	require.Equal(t, responsesEventOutputTextDone, events2[1].Type)
	require.Equal(t, responsesEventOutputItemDone, events2[2].Type)
	require.Equal(t, responsesEventCompleted, events2[3].Type)
	require.Equal(t, "hello", events2[1].Delta)
	require.NotNil(t, events2[3].Response)
	require.Equal(t, 5, events2[3].Response.Usage.InputTokens)
	require.Equal(t, 2, events2[3].Response.Usage.OutputTokens)
}

func TestChatToResponsesStreamBuilder_ToolCallFlow(t *testing.T) {
	t.Parallel()

	builder := NewChatToResponsesStreamBuilder()
	toolIndex := 0
	chunk := dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl-2",
		Object:  "chat.completion.chunk",
		Created: 1720000001,
		Model:   "gpt-4.1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
				Index: &toolIndex,
				ID:    "call_1",
				Type:  "function",
				Function: dto.FunctionResponse{
					Name:      "get_weather",
					Arguments: "{\"city\":",
				},
			}}},
		}},
	}

	events1, err := builder.ConsumeChunk(chunk)
	require.NoError(t, err)
	require.Len(t, events1, 3)
	require.Equal(t, responsesEventCreated, events1[0].Type)
	require.Equal(t, responsesEventOutputItemAdded, events1[1].Type)
	require.Equal(t, responsesEventFunctionArgsDelta, events1[2].Type)

	finishReason := "tool_calls"
	chunk2 := dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl-2",
		Object:  "chat.completion.chunk",
		Created: 1720000001,
		Model:   "gpt-4.1",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
				Index: &toolIndex,
				ID:    "call_1",
				Type:  "function",
				Function: dto.FunctionResponse{Arguments: "\"Hangzhou\"}"},
			}}},
			FinishReason: &finishReason,
		}},
	}

	events2, err := builder.ConsumeChunk(chunk2)
	require.NoError(t, err)
	require.Len(t, events2, 4)
	require.Equal(t, responsesEventFunctionArgsDelta, events2[0].Type)
	require.Equal(t, responsesEventFunctionArgsDone, events2[1].Type)
	require.Equal(t, responsesEventOutputItemDone, events2[2].Type)
	require.Equal(t, responsesEventCompleted, events2[3].Type)
	require.Equal(t, "{\"city\":\"Hangzhou\"}", events2[1].Delta)
}

func stringPtr(s string) *string {
	return &s
}
