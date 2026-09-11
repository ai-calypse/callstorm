package worker

import "github.com/yakshgandhi/callstorm/internal/scenario"

// The Voice Agent WebSocket takes no query parameters -- all configuration
// travels in a single Settings message that must be sent after Welcome and
// before any audio.
const deepgramAgentURL = "wss://agent.deepgram.com/v1/agent/converse"

type settings struct {
	Type  string        `json:"type"`
	Audio settingsAudio `json:"audio"`
	Agent settingsAgent `json:"agent"`
}

type settingsAudio struct {
	Input  audioInput  `json:"input"`
	Output audioOutput `json:"output"`
}

type audioInput struct {
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
}

type audioOutput struct {
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
	Container  string `json:"container"`
}

type settingsAgent struct {
	Language string       `json:"language"`
	Listen   listenConfig `json:"listen"`
	Think    thinkConfig  `json:"think"`
	Speak    speakConfig  `json:"speak"`
	Greeting string       `json:"greeting,omitempty"`
}

type listenConfig struct {
	Provider listenProvider `json:"provider"`
}

type listenProvider struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

type thinkConfig struct {
	Provider thinkProvider `json:"provider"`
	Prompt   string        `json:"prompt,omitempty"`
}

type thinkProvider struct {
	Type        string  `json:"type"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}

type speakConfig struct {
	Provider speakProvider `json:"provider"`
}

type speakProvider struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

// buildSettings turns a scenario's target config into the Settings message.
// Input and output share a sample rate so neither side needs resampling --
// resampling would add latency we would then wrongly attribute to the agent.
func buildSettings(s *scenario.Scenario, sampleRate int) settings {
	return settings{
		Type: "Settings",
		Audio: settingsAudio{
			Input:  audioInput{Encoding: "linear16", SampleRate: sampleRate},
			Output: audioOutput{Encoding: "linear16", SampleRate: sampleRate, Container: "none"},
		},
		Agent: settingsAgent{
			Language: "en",
			Listen: listenConfig{
				Provider: listenProvider{Type: "deepgram", Model: s.Target.ListenModel},
			},
			Think: thinkConfig{
				Provider: thinkProvider{Type: "open_ai", Model: s.Target.ThinkModel, Temperature: 0.7},
				Prompt:   s.Target.Prompt,
			},
			Speak: speakConfig{
				Provider: speakProvider{Type: "deepgram", Model: s.Target.Voice},
			},
			Greeting: s.Target.Greeting,
		},
	}
}

// serverMessage is the union of every text frame the agent sends us. Only the
// fields Phase 1 actually reads are declared.
type serverMessage struct {
	Type        string `json:"type"`
	RequestID   string `json:"request_id"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Message     string `json:"message"`
	Code        string `json:"code"`
}
