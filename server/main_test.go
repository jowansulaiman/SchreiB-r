package main

import (
	"context"
	"testing"
)

func TestLocalVerdictTreatsHighVolumeAsCryEvenWhenSensorFlagFalse(t *testing.T) {
	falseFlag := false
	lowConfidence := 0.35

	got := localVerdict(config{threshold: 65}, sensorRequest{
		Volume:     82,
		Crying:     &falseFlag,
		Confidence: &lowConfidence,
	})

	if !got.Crying {
		t.Fatalf("localVerdict() crying = false, want true for volume above threshold")
	}
	if got.Confidence < 0.55 {
		t.Fatalf("localVerdict() confidence = %.2f, want at least 0.55 for crying", got.Confidence)
	}
}

func TestBuildEventKeepsConnectedAlertType(t *testing.T) {
	event, err := buildEvent(context.Background(), config{threshold: 65}, sensorRequest{
		DeviceID:  "esp32",
		AlertType: "connected",
		Volume:    0,
		Message:   "Board verbunden",
	})
	if err != nil {
		t.Fatalf("buildEvent() error = %v", err)
	}
	if event.AlertType != "connected" {
		t.Fatalf("buildEvent() alertType = %q, want connected", event.AlertType)
	}
	if event.Crying {
		t.Fatalf("buildEvent() crying = true, want false")
	}
}

func TestMergeAIVerdictDoesNotSuppressLocalCry(t *testing.T) {
	localResult := verdict{
		Crying:     true,
		Confidence: 0.82,
		Message:    "Sensor: Weinen moeglich",
	}
	aiResult := verdict{
		Crying:     false,
		Confidence: 0.99,
		Message:    "Kein Babyweinen im Audio erkannt",
	}

	got := mergeAIVerdict(localResult, aiResult)

	if !got.Crying {
		t.Fatalf("mergeAIVerdict() crying = false, want true when local sensor reports crying")
	}
	if got.Message == aiResult.Message {
		t.Fatalf("mergeAIVerdict() kept quiet AI message %q", got.Message)
	}
	if source := mergedSource("openai", localResult, aiResult); source != "sensor" {
		t.Fatalf("mergedSource() = %q, want sensor", source)
	}
}

func TestMergeAIVerdictKeepsAIWhenLocalQuiet(t *testing.T) {
	localResult := verdict{Crying: false, Confidence: 0.4}
	aiResult := verdict{
		Crying:     false,
		Confidence: 0.94,
		Message:    "Kein Babyweinen im Audio erkannt",
	}

	got := mergeAIVerdict(localResult, aiResult)

	if got != aiResult {
		t.Fatalf("mergeAIVerdict() = %#v, want AI result %#v", got, aiResult)
	}
	if source := mergedSource("openai", localResult, aiResult); source != "openai" {
		t.Fatalf("mergedSource() = %q, want openai", source)
	}
}

func TestMergeAIVerdictCombinesConfirmedCry(t *testing.T) {
	localResult := verdict{Crying: true, Confidence: 0.82}
	aiResult := verdict{
		Crying:     true,
		Confidence: 0.7,
		Message:    "Babyweinen erkannt",
	}

	got := mergeAIVerdict(localResult, aiResult)

	if !got.Crying {
		t.Fatalf("mergeAIVerdict() crying = false, want true")
	}
	if got.Confidence != localResult.Confidence {
		t.Fatalf("mergeAIVerdict() confidence = %.2f, want %.2f", got.Confidence, localResult.Confidence)
	}
	if source := mergedSource("openai", localResult, aiResult); source != "sensor+openai" {
		t.Fatalf("mergedSource() = %q, want sensor+openai", source)
	}
}

func TestAIProvidersAutoPrefersOpenAIThenGemini(t *testing.T) {
	got := aiProviders(config{
		aiProvider: "auto",
		openAIKey:  "openai-key",
		geminiKey:  "gemini-key",
	})

	if len(got) != 2 || got[0] != "openai" || got[1] != "gemini" {
		t.Fatalf("aiProviders() = %#v, want [openai gemini]", got)
	}
}

func TestLoadConfigAcceptsChatGPTKeyAlias(t *testing.T) {
	t.Setenv("AI_PROVIDER", "chatgbt")
	t.Setenv("CHATGPT_API_KEY", "chatgpt-key")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	got := loadConfig()

	if got.aiProvider != "openai" {
		t.Fatalf("loadConfig() aiProvider = %q, want openai", got.aiProvider)
	}
	if got.openAIKey != "chatgpt-key" {
		t.Fatalf("loadConfig() openAIKey = %q, want chatgpt-key", got.openAIKey)
	}
}

func TestLoadConfigUsesGenericAIKeyForExplicitOpenAI(t *testing.T) {
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("AI_API_KEY", "generic-key")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CHATGPT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	got := loadConfig()

	if got.openAIKey != "generic-key" {
		t.Fatalf("loadConfig() openAIKey = %q, want generic-key", got.openAIKey)
	}
}

func TestOpenAIAudioFormat(t *testing.T) {
	tests := map[string]string{
		"audio/wav":         "wav",
		"audio/x-wav":       "wav",
		"audio/mpeg":        "mp3",
		"audio/mp3;codec=x": "mp3",
	}

	for mimeType, want := range tests {
		got, err := openAIAudioFormat(mimeType)
		if err != nil {
			t.Fatalf("openAIAudioFormat(%q) error = %v", mimeType, err)
		}
		if got != want {
			t.Fatalf("openAIAudioFormat(%q) = %q, want %q", mimeType, got, want)
		}
	}
}
