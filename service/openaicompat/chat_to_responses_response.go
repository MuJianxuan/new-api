package openaicompat

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	responsesObjectType      = "response"
	responsesStatusCompleted = "completed"
	responsesOutputMessage   = "message"
	responsesOutputText      = "output_text"
	responsesFunctionCall    = "function_call"
	responsesAssistantRole   = "assistant"
)

func ChatCompletionsResponseToResponsesResponse(resp *dto.OpenAITextResponse, id string) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	if resp == nil {
		return nil, nil, errors.New("response is nil")
	}
	if len(resp.Choices) == 0 {
		return nil, nil, errors.New("response choices are empty")
	}

	createdAt, err := convertCreatedAtToInt(resp.Created)
	if err != nil {
		return nil, nil, err
	}

	choice := resp.Choices[0]
	usage := &dto.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		InputTokens:      resp.Usage.PromptTokens,
		OutputTokens:     resp.Usage.CompletionTokens,
	}
	usage.PromptTokensDetails = resp.Usage.PromptTokensDetails
	usage.CompletionTokenDetails = resp.Usage.CompletionTokenDetails
	usage.InputTokensDetails = resp.Usage.InputTokensDetails

	status := responsesStatusCompleted
	statusRaw, _ := common.Marshal(status)
	out := &dto.OpenAIResponsesResponse{
		ID:        id,
		Object:    responsesObjectType,
		CreatedAt: createdAt,
		Status:    statusRaw,
		Model:     resp.Model,
		Output:    buildResponsesOutputItemsFromChatMessage(choice.Message),
		Usage:     usage,
	}
	return out, usage, nil
}

func buildResponsesOutputItemsFromChatMessage(msg dto.Message) []dto.ResponsesOutput {
	toolCalls := msg.ParseToolCalls()
	if len(toolCalls) > 0 {
		items := make([]dto.ResponsesOutput, 0, len(toolCalls))
		for _, tc := range toolCalls {
			items = append(items, dto.ResponsesOutput{
				Type:      responsesFunctionCall,
				CallId:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				Status:    responsesStatusCompleted,
			})
		}
		return items
	}
	return []dto.ResponsesOutput{{
		Type:   responsesOutputMessage,
		Role:   responsesAssistantRole,
		Status: responsesStatusCompleted,
		Content: []dto.ResponsesOutputContent{{
			Type: responsesOutputText,
			Text: msg.StringContent(),
		}},
	}}
}

func convertCreatedAtToInt(created any) (int, error) {
	switch v := created.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unsupported created type %T", created)
	}
}
