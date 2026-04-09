package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestResponsesRequestToChatCompletionsRequest_TextAndInstructions(t *testing.T) {
	t.Parallel()

	inputRaw, err := common.Marshal([]map[string]any{{
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": "hello",
		}},
	}})
	require.NoError(t, err)

	instructionsRaw, err := common.Marshal("You are helpful.")
	require.NoError(t, err)

	req := &dto.OpenAIResponsesRequest{
		Model:        "gpt-4.1",
		Instructions: instructionsRaw,
		Input:        inputRaw,
	}

	chatReq, convErr := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, convErr)
	require.Equal(t, "gpt-4.1", chatReq.Model)
	require.Len(t, chatReq.Messages, 2)
	require.Equal(t, "system", chatReq.Messages[0].Role)
	require.Equal(t, "You are helpful.", chatReq.Messages[0].StringContent())
	require.Equal(t, "user", chatReq.Messages[1].Role)
	parts := chatReq.Messages[1].ParseContent()
	require.Len(t, parts, 1)
	require.Equal(t, "hello", parts[0].Text)
}

func TestResponsesRequestToChatCompletionsRequest_FunctionToolOutput(t *testing.T) {
	t.Parallel()

	inputRaw, err := common.Marshal([]map[string]any{{
		"type":    "function_call_output",
		"call_id": "call_1",
		"output":  "{\"city\":\"Hangzhou\"}",
	}})
	require.NoError(t, err)

	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Input: inputRaw,
	}

	chatReq, convErr := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, convErr)
	require.Len(t, chatReq.Messages, 1)
	require.Equal(t, "tool", chatReq.Messages[0].Role)
	require.Equal(t, "call_1", chatReq.Messages[0].ToolCallId)
	require.Equal(t, "{\"city\":\"Hangzhou\"}", chatReq.Messages[0].StringContent())
}

func TestResponsesRequestToChatCompletionsRequest_BuiltInToolRejected(t *testing.T) {
	t.Parallel()

	toolsRaw, err := common.Marshal([]map[string]any{{
		"type": "web_search_preview",
	}})
	require.NoError(t, err)

	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Tools: toolsRaw,
	}

	chatReq, convErr := ResponsesRequestToChatCompletionsRequest(req)
	require.Nil(t, chatReq)
	require.ErrorContains(t, convErr, "tool type \"web_search_preview\" is not supported")
}

func TestResponsesRequestToChatCompletionsRequest_MultimodalAndLimits(t *testing.T) {
	t.Parallel()

	textRaw, err := common.Marshal(map[string]any{
		"format": map[string]any{
			"type": "json_object",
		},
	})
	require.NoError(t, err)

	inputRaw, err := common.Marshal([]map[string]any{{
		"role": "user",
		"content": []map[string]any{
			{
				"type": "input_text",
				"text": "describe image",
			},
			{
				"type": "input_image",
				"image_url": "https://example.com/a.png",
			},
		},
	}})
	require.NoError(t, err)

	req := &dto.OpenAIResponsesRequest{
		Model:           "gpt-4.1",
		MaxOutputTokens: lo.ToPtr(uint(128)),
		Text:            textRaw,
		Input:           inputRaw,
	}

	chatReq, convErr := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, convErr)
	require.NotNil(t, chatReq.MaxTokens)
	require.Equal(t, uint(128), *chatReq.MaxTokens)
	require.NotNil(t, chatReq.ResponseFormat)
	parts := chatReq.Messages[0].ParseContent()
	require.Len(t, parts, 2)
	require.Equal(t, dto.ContentTypeText, parts[0].Type)
	require.Equal(t, dto.ContentTypeImageURL, parts[1].Type)
}
