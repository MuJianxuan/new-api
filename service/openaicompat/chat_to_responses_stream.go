package openaicompat

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	responsesEventCreated                 = "response.created"
	responsesEventOutputItemAdded         = "response.output_item.added"
	responsesEventOutputItemDone          = "response.output_item.done"
	responsesEventOutputTextDelta         = "response.output_text.delta"
	responsesEventOutputTextDone          = "response.output_text.done"
	responsesEventFunctionArgsDelta       = "response.function_call_arguments.delta"
	responsesEventFunctionArgsDone        = "response.function_call_arguments.done"
	responsesEventCompleted               = "response.completed"
)

type ChatToResponsesStreamBuilder struct {
	responseID       string
	model            string
	createdAt        int
	createdSent      bool
	textStarted      bool
	textCompleted    bool
	messageItemID    string
	textBuffer       string
	toolStates       map[int]*responsesToolStreamState
	finalUsage       *dto.Usage
	completedSent    bool
}

type responsesToolStreamState struct {
	itemID      string
	callID      string
	name        string
	arguments   string
	itemOpened  bool
	itemDone    bool
}

func NewChatToResponsesStreamBuilder() *ChatToResponsesStreamBuilder {
	return &ChatToResponsesStreamBuilder{
		toolStates: make(map[int]*responsesToolStreamState),
	}
}

func (b *ChatToResponsesStreamBuilder) ConsumeChunk(chunk dto.ChatCompletionsStreamResponse) ([]dto.ResponsesStreamResponse, error) {
	events := make([]dto.ResponsesStreamResponse, 0)
	b.captureChunkMeta(chunk)
	if !b.createdSent {
		response, err := b.snapshotResponse(nil)
		if err != nil {
			return nil, err
		}
		events = append(events, dto.ResponsesStreamResponse{Type: responsesEventCreated, Response: response})
		b.createdSent = true
	}

	for _, choice := range chunk.Choices {
		if textDelta := choice.Delta.GetContentString(); textDelta != "" {
			if !b.textStarted {
				b.textStarted = true
				if b.messageItemID == "" {
					b.messageItemID = b.responseID + "_msg_0"
				}
				events = append(events, dto.ResponsesStreamResponse{
					Type: responsesEventOutputItemAdded,
					Item: &dto.ResponsesOutput{
						ID:     b.messageItemID,
						Type:   responsesOutputMessage,
						Role:   responsesAssistantRole,
						Status: "in_progress",
					},
				})
			}
			b.textBuffer += textDelta
			outputIndex := 0
			contentIndex := 0
			events = append(events, dto.ResponsesStreamResponse{
				Type:         responsesEventOutputTextDelta,
				Delta:        textDelta,
				ItemID:       b.messageItemID,
				OutputIndex:  &outputIndex,
				ContentIndex: &contentIndex,
			})
		}

		for _, toolCall := range choice.Delta.ToolCalls {
			state := b.getOrCreateToolState(toolCall)
			if !state.itemOpened {
				state.itemOpened = true
				outputIndex := b.getToolOutputIndex(toolCall)
				events = append(events, dto.ResponsesStreamResponse{
					Type: responsesEventOutputItemAdded,
					Item: &dto.ResponsesOutput{
						ID:        state.itemID,
						Type:      responsesFunctionCall,
						CallId:    state.callID,
						Name:      state.name,
						Arguments: "",
						Status:    "in_progress",
					},
					OutputIndex: &outputIndex,
				})
			}
			if toolCall.Function.Name != "" {
				state.name = toolCall.Function.Name
			}
			if toolCall.Function.Arguments != "" {
				state.arguments += toolCall.Function.Arguments
				outputIndex := b.getToolOutputIndex(toolCall)
				events = append(events, dto.ResponsesStreamResponse{
					Type:        responsesEventFunctionArgsDelta,
					Delta:       toolCall.Function.Arguments,
					ItemID:      state.itemID,
					OutputIndex: &outputIndex,
				})
			}
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishEvents, err := b.buildFinishEvents(*choice.FinishReason)
			if err != nil {
				return nil, err
			}
			events = append(events, finishEvents...)
		}
	}
	return events, nil
}

func (b *ChatToResponsesStreamBuilder) captureChunkMeta(chunk dto.ChatCompletionsStreamResponse) {
	if b.responseID == "" {
		b.responseID = chunk.Id
	}
	if b.model == "" {
		b.model = chunk.Model
	}
	if b.createdAt == 0 {
		b.createdAt = int(chunk.Created)
	}
	if chunk.Usage != nil {
		b.finalUsage = &dto.Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			InputTokens:      chunk.Usage.PromptTokens,
			OutputTokens:     chunk.Usage.CompletionTokens,
		}
		b.finalUsage.PromptTokensDetails = chunk.Usage.PromptTokensDetails
		b.finalUsage.CompletionTokenDetails = chunk.Usage.CompletionTokenDetails
		b.finalUsage.InputTokensDetails = chunk.Usage.InputTokensDetails
	}
}


func (b *ChatToResponsesStreamBuilder) getOrCreateToolState(toolCall dto.ToolCallResponse) *responsesToolStreamState {
	idx := b.getToolIndex(toolCall)
	state, ok := b.toolStates[idx]
	if ok {
		if state.callID == "" && toolCall.ID != "" {
			state.callID = toolCall.ID
		}
		if state.name == "" && toolCall.Function.Name != "" {
			state.name = toolCall.Function.Name
		}
		return state
	}
	callID := toolCall.ID
	if callID == "" {
		callID = fmt.Sprintf("%s_call_%d", b.responseID, idx)
	}
	state = &responsesToolStreamState{
		itemID: fmt.Sprintf("%s_tool_%d", b.responseID, idx),
		callID: callID,
		name:   toolCall.Function.Name,
	}
	b.toolStates[idx] = state
	return state
}

func (b *ChatToResponsesStreamBuilder) getToolIndex(toolCall dto.ToolCallResponse) int {
	if toolCall.Index != nil {
		return *toolCall.Index
	}
	return 0
}

func (b *ChatToResponsesStreamBuilder) getToolOutputIndex(toolCall dto.ToolCallResponse) int {
	return b.getToolIndex(toolCall)
}

func (b *ChatToResponsesStreamBuilder) buildFinishEvents(finishReason string) ([]dto.ResponsesStreamResponse, error) {
	events := make([]dto.ResponsesStreamResponse, 0)
	if b.textStarted && !b.textCompleted {
		outputIndex := 0
		contentIndex := 0
		events = append(events,
			dto.ResponsesStreamResponse{Type: responsesEventOutputTextDone, Delta: b.textBuffer, ItemID: b.messageItemID, OutputIndex: &outputIndex, ContentIndex: &contentIndex},
			dto.ResponsesStreamResponse{Type: responsesEventOutputItemDone, Item: &dto.ResponsesOutput{ID: b.messageItemID, Type: responsesOutputMessage, Role: responsesAssistantRole, Status: responsesStatusCompleted, Content: []dto.ResponsesOutputContent{{Type: responsesOutputText, Text: b.textBuffer}}}, OutputIndex: &outputIndex},
		)
		b.textCompleted = true
	}
	for idx, state := range b.toolStates {
		if state.itemDone {
			continue
		}
		outputIndex := idx
		events = append(events,
			dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDone, Delta: state.arguments, ItemID: state.itemID, OutputIndex: &outputIndex},
			dto.ResponsesStreamResponse{Type: responsesEventOutputItemDone, Item: &dto.ResponsesOutput{ID: state.itemID, Type: responsesFunctionCall, CallId: state.callID, Name: state.name, Arguments: state.arguments, Status: responsesStatusCompleted}, OutputIndex: &outputIndex},
		)
		state.itemDone = true
	}
	if !b.completedSent {
		response, err := b.snapshotResponse(&finishReason)
		if err != nil {
			return nil, err
		}
		events = append(events, dto.ResponsesStreamResponse{Type: responsesEventCompleted, Response: response})
		b.completedSent = true
	}
	return events, nil
}

func (b *ChatToResponsesStreamBuilder) snapshotResponse(finishReason *string) (*dto.OpenAIResponsesResponse, error) {
	usage := b.finalUsage
	if usage == nil {
		usage = &dto.Usage{}
	}
	statusValue := "in_progress"
	if finishReason != nil {
		statusValue = responsesStatusCompleted
	}
	statusRaw, err := common.Marshal(statusValue)
	if err != nil {
		return nil, err
	}
	output := make([]dto.ResponsesOutput, 0)
	if b.textStarted {
		output = append(output, dto.ResponsesOutput{
			ID:     b.messageItemID,
			Type:   responsesOutputMessage,
			Role:   responsesAssistantRole,
			Status: statusValue,
			Content: []dto.ResponsesOutputContent{{
				Type: responsesOutputText,
				Text: b.textBuffer,
			}},
		})
	}
	for idx, state := range b.toolStates {
		_ = idx
		output = append(output, dto.ResponsesOutput{
			ID:        state.itemID,
			Type:      responsesFunctionCall,
			CallId:    state.callID,
			Name:      state.name,
			Arguments: state.arguments,
			Status:    statusValue,
		})
	}
	return &dto.OpenAIResponsesResponse{
		ID:        b.responseID,
		Object:    responsesObjectType,
		CreatedAt: b.createdAt,
		Model:     b.model,
		Status:    statusRaw,
		Output:    output,
		Usage:     usage,
	}, nil
}
