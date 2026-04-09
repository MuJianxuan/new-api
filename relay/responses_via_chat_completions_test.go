package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleChatCompletionsAsResponses(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	chatResp := dto.OpenAITextResponse{
		Id:      "chatcmpl-1",
		Object:  "chat.completion",
		Created: 1720000000,
		Model:   "gpt-4.1",
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: "hello",
			},
			FinishReason: "stop",
		}},
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}
	body, err := common.Marshal(chatResp)
	require.NoError(t, err)

	httpResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	usage, newAPIError := handleChatCompletionsAsResponses(ctx, &relaycommon.RelayInfo{}, httpResp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 2, usage.OutputTokens)
	require.Equal(t, http.StatusOK, recorder.Code)

	var out dto.OpenAIResponsesResponse
	err = common.Unmarshal(recorder.Body.Bytes(), &out)
	require.NoError(t, err)
	require.Equal(t, "response", out.Object)
	require.Len(t, out.Output, 1)
	require.Equal(t, "message", out.Output[0].Type)
	require.Equal(t, "hello", out.Output[0].Content[0].Text)
}
