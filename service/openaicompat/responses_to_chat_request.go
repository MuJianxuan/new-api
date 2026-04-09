package openaicompat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
)

const responsesToChatUnsupportedToolFormat = "tool type %q is not supported in responses-to-chat compatibility mode"

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("model is required")
	}

	messages := make([]dto.Message, 0)
	instructions := parseResponsesInstructions(req.Instructions)
	if instructions != "" {
		messages = append(messages, dto.Message{Role: "system", Content: instructions})
	}

	convertedMessages, err := convertResponsesInputToChatMessages(req.Input)
	if err != nil {
		return nil, err
	}
	messages = append(messages, convertedMessages...)

	tools, err := convertResponsesToolsToChatTools(req.GetToolsMap())
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:         req.Model,
		Messages:      messages,
		Stream:        req.Stream,
		StreamOptions: req.StreamOptions,
		MaxTokens:     req.MaxOutputTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		Tools:         tools,
		ToolChoice:    parseResponsesToolChoice(req.ToolChoice),
		User:          req.User,
		ServiceTier:   jsonRawIfNotEmpty(req.ServiceTier),
		Store:         jsonRawIfNotEmpty(req.Store),
		Metadata:      jsonRawIfNotEmpty(req.Metadata),
		TopLogProbs:   req.TopLogProbs,
	}

	if req.Reasoning != nil {
		out.ReasoningEffort = req.Reasoning.Effort
	}
	if len(req.Text) > 0 {
		out.ResponseFormat = convertResponsesTextToChatResponseFormat(req.Text)
	}
	if raw := jsonRawIfNotEmpty(req.ParallelToolCalls); len(raw) > 0 {
		var v bool
		if err := common.Unmarshal(raw, &v); err == nil {
			out.ParallelTooCalls = lo.ToPtr(v)
		}
	}

	return out, nil
}

func parseResponsesInstructions(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if common.GetJsonType(raw) == "string" {
		var out string
		_ = common.Unmarshal(raw, &out)
		return strings.TrimSpace(out)
	}
	return strings.TrimSpace(string(raw))
}

func convertResponsesInputToChatMessages(raw []byte) ([]dto.Message, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	jsonType := common.GetJsonType(raw)
	if jsonType == "string" {
		var input string
		if err := common.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []dto.Message{{Role: "user", Content: input}}, nil
	}
	if jsonType != "array" {
		return nil, fmt.Errorf("input type %q is not supported in responses-to-chat compatibility mode", jsonType)
	}

	var items []map[string]any
	if err := common.Unmarshal(raw, &items); err != nil {
		return nil, err
	}

	messages := make([]dto.Message, 0, len(items))
	for _, item := range items {
		converted, err := convertResponsesInputItemToChatMessages(item)
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted...)
	}
	return messages, nil
}

func convertResponsesInputItemToChatMessages(item map[string]any) ([]dto.Message, error) {
	itemType := strings.TrimSpace(common.Interface2String(item["type"]))
	if itemType == "function_call_output" {
		return []dto.Message{{
			Role:       "tool",
			ToolCallId: common.Interface2String(item["call_id"]),
			Content:    common.Interface2String(item["output"]),
		}}, nil
	}
	if itemType == "function_call" {
		toolCall := dto.ToolCallRequest{
			ID:   common.Interface2String(item["call_id"]),
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      common.Interface2String(item["name"]),
				Arguments: common.Interface2String(item["arguments"]),
			},
		}
		msg := dto.Message{Role: "assistant", Content: ""}
		msg.SetToolCalls([]dto.ToolCallRequest{toolCall})
		return []dto.Message{msg}, nil
	}

	role := strings.TrimSpace(common.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content := item["content"]
	if content == nil {
		return []dto.Message{{Role: role, Content: ""}}, nil
	}

	contentRaw, err := common.Marshal(content)
	if err != nil {
		return nil, err
	}
	contents, err := convertResponsesContentToMediaContents(contentRaw)
	if err != nil {
		return nil, err
	}
	if len(contents) == 1 && contents[0].Type == dto.ContentTypeText {
		return []dto.Message{{Role: role, Content: contents[0].Text}}, nil
	}
	msg := dto.Message{Role: role}
	msg.SetMediaContent(contents)
	return []dto.Message{msg}, nil
}

func convertResponsesContentToMediaContents(raw []byte) ([]dto.MediaContent, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if common.GetJsonType(raw) == "string" {
		var text string
		if err := common.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return []dto.MediaContent{{Type: dto.ContentTypeText, Text: text}}, nil
	}

	var items []map[string]any
	if err := common.Unmarshal(raw, &items); err != nil {
		return nil, err
	}

	contents := make([]dto.MediaContent, 0, len(items))
	for _, item := range items {
		itemType := strings.TrimSpace(common.Interface2String(item["type"]))
		switch itemType {
		case "input_text", "output_text", dto.ContentTypeText:
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeText, Text: common.Interface2String(item["text"])})
		case "input_image":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: normalizeResponsesImageURL(item["image_url"], item["detail"])})
		case "input_audio":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeInputAudio, InputAudio: normalizeResponsesAudio(item)})
		case "input_file":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeFile, File: normalizeResponsesFile(item)})
		case "input_video":
			contents = append(contents, dto.MediaContent{Type: dto.ContentTypeVideoUrl, VideoUrl: normalizeResponsesVideo(item)})
		default:
			return nil, fmt.Errorf("input item type %q is not supported in responses-to-chat compatibility mode", itemType)
		}
	}
	return contents, nil
}

func convertResponsesToolsToChatTools(toolsMap []map[string]any) ([]dto.ToolCallRequest, error) {
	if len(toolsMap) == 0 {
		return nil, nil
	}
	tools := make([]dto.ToolCallRequest, 0, len(toolsMap))
	for _, toolMap := range toolsMap {
		toolType := strings.TrimSpace(common.Interface2String(toolMap["type"]))
		if toolType != "function" {
			return nil, fmt.Errorf(responsesToChatUnsupportedToolFormat, toolType)
		}
		functionMap, _ := toolMap["function"].(map[string]any)
		tools = append(tools, dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        common.Interface2String(functionMap["name"]),
				Description: common.Interface2String(functionMap["description"]),
				Parameters:  functionMap["parameters"],
			},
		})
	}
	return tools, nil
}

func parseResponsesToolChoice(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	jsonType := common.GetJsonType(raw)
	if jsonType == "string" {
		var out string
		if err := common.Unmarshal(raw, &out); err == nil {
			return out
		}
	}
	var out any
	if err := common.Unmarshal(raw, &out); err == nil {
		return out
	}
	return nil
}

func convertResponsesTextToChatResponseFormat(raw []byte) *dto.ResponseFormat {
	if len(raw) == 0 || common.GetJsonType(raw) != "object" {
		return nil
	}
	var textMap map[string]any
	if err := common.Unmarshal(raw, &textMap); err != nil {
		return nil
	}
	formatMap, _ := textMap["format"].(map[string]any)
	formatType := common.Interface2String(formatMap["type"])
	if formatType == "" {
		return nil
	}
	respFormat := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		jsonSchemaRaw, _ := common.Marshal(formatMap)
		respFormat.JsonSchema = jsonSchemaRaw
	}
	return respFormat
}

func normalizeResponsesImageURL(imageURL any, detail any) any {
	url := common.Interface2String(imageURL)
	if imageURLMap, ok := imageURL.(map[string]any); ok {
		url = common.Interface2String(imageURLMap["url"])
		if detail == nil {
			detail = imageURLMap["detail"]
		}
	}
	if url == "" {
		return nil
	}
	return &dto.MessageImageUrl{Url: url, Detail: common.Interface2String(detail)}
}

func normalizeResponsesAudio(item map[string]any) any {
	if inputAudio, ok := item["input_audio"]; ok && inputAudio != nil {
		return inputAudio
	}
	return map[string]any{
		"data":   common.Interface2String(item["data"]),
		"format": common.Interface2String(item["format"]),
	}
}

func normalizeResponsesFile(item map[string]any) any {
	if fileAny, ok := item["file"]; ok && fileAny != nil {
		return fileAny
	}
	if fileURL := common.Interface2String(item["file_url"]); fileURL != "" {
		return map[string]any{"file_data": fileURL}
	}
	return map[string]any{
		"file_id":   common.Interface2String(item["file_id"]),
		"filename":  common.Interface2String(item["filename"]),
		"file_data": common.Interface2String(item["file_data"]),
	}
}

func normalizeResponsesVideo(item map[string]any) any {
	if videoURL, ok := item["video_url"]; ok && videoURL != nil {
		if videoMap, ok := videoURL.(map[string]any); ok {
			return &dto.MessageVideoUrl{Url: common.Interface2String(videoMap["url"])}
		}
		return &dto.MessageVideoUrl{Url: common.Interface2String(videoURL)}
	}
	return &dto.MessageVideoUrl{Url: common.Interface2String(item["url"])}
}

func jsonRawIfNotEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
