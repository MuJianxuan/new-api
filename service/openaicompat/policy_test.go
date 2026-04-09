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
