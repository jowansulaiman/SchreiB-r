package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	addr          string
	threshold     float64
	loudThreshold float64
	aiProvider    string
	openAIKey     string
	openAIModel   string
	geminiKey     string
	geminiModels  []string
}

type sensorRequest struct {
	DeviceID    string   `json:"deviceId"`
	AlertType   string   `json:"alertType,omitempty"`
	AudioBase64 string   `json:"audioBase64,omitempty"`
	MimeType    string   `json:"mimeType,omitempty"`
	Volume      float64  `json:"volume,omitempty"`
	Crying      *bool    `json:"crying,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
	Message     string   `json:"message,omitempty"`
}

type cryEvent struct {
	ID         int64     `json:"id"`
	DeviceID   string    `json:"deviceId"`
	AlertType  string    `json:"alertType"`
	Crying     bool      `json:"crying"`
	Confidence float64   `json:"confidence"`
	Volume     float64   `json:"volume"`
	Source     string    `json:"source"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}

type volumeRequest struct {
	DeviceID string  `json:"deviceId"`
	Volume   float64 `json:"volume"`
	Message  string  `json:"message,omitempty"`
}

type volumeReading struct {
	ID        int64     `json:"id"`
	DeviceID  string    `json:"deviceId"`
	Volume    float64   `json:"volume"`
	Loud      bool      `json:"loud"`
	Threshold float64   `json:"threshold"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type volumeResponse struct {
	Reading volumeReading `json:"reading"`
	Alert   *cryEvent     `json:"alert,omitempty"`
}

type state struct {
	mu           sync.Mutex
	nextID       int64
	nextVolumeID int64
	events       []cryEvent
	volumes      []volumeReading
	subscribers  map[chan cryEvent]struct{}
}

type verdict struct {
	Crying     bool    `json:"crying"`
	Confidence float64 `json:"confidence"`
	Message    string  `json:"message"`
	Reason     string  `json:"reason"`
}

type geminiHTTPError struct {
	statusCode int
	status     string
	body       string
}

func (e geminiHTTPError) Error() string {
	return fmt.Sprintf("gemini returned %s: %s", e.status, strings.TrimSpace(e.body))
}

type openAIHTTPError struct {
	statusCode int
	status     string
	body       string
}

func (e openAIHTTPError) Error() string {
	return fmt.Sprintf("openai returned %s: %s", e.status, strings.TrimSpace(e.body))
}

var errNoAIProvider = errors.New("no ai provider configured")

func main() {
	cfg := loadConfig()

	appState := &state{subscribers: make(map[chan cryEvent]struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/events", appState.handleEvents(cfg))
	mux.HandleFunc("/api/events/stream", appState.handleStream)
	mux.HandleFunc("/api/volume", appState.handleVolume(cfg))

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           cors(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("meidan server listening on %s", cfg.addr)
	log.Fatal(server.ListenAndServe())
}

func loadConfig() config {
	aiProvider := normalizeAIProvider(env("AI_PROVIDER", "auto"))
	genericAIKey := strings.TrimSpace(os.Getenv("AI_API_KEY"))
	openAIKey := firstEnv("OPENAI_API_KEY", "CHATGPT_API_KEY")
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))

	if genericAIKey != "" {
		switch aiProvider {
		case "openai":
			if openAIKey == "" {
				openAIKey = genericAIKey
			}
		case "gemini":
			if geminiKey == "" {
				geminiKey = genericAIKey
			}
		}
	}

	return config{
		addr:          env("ADDR", ":8080"),
		threshold:     envFloat("VOLUME_THRESHOLD", 65),
		loudThreshold: envFloat("LOUD_VOLUME_THRESHOLD", 1000),
		aiProvider:    aiProvider,
		openAIKey:     openAIKey,
		openAIModel:   env("OPENAI_MODEL", "gpt-audio-mini"),
		geminiKey:     geminiKey,
		geminiModels:  geminiModelList(env("GEMINI_MODEL", "gemini-2.5-flash"), env("GEMINI_FALLBACK_MODELS", "gemini-2.0-flash,gemini-2.5-flash-lite")),
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":      "Meidan server",
		"health":    "/health",
		"events":    "/api/events",
		"volume":    "/api/volume",
		"sseStream": "/api/events/stream",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *state) handleEvents(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.latest(30))
		case http.MethodPost:
			s.handleCreateEvent(w, r, cfg)
		default:
			w.Header().Set("Allow", "GET, POST, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (s *state) handleCreateEvent(w http.ResponseWriter, r *http.Request, cfg config) {
	var req sensorRequest
	body := http.MaxBytesReader(w, r.Body, 18<<20)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	event, err := buildEvent(r.Context(), cfg, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusCreated, s.add(event))
}

func (s *state) handleVolume(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.latestVolumes(30))
		case http.MethodPost:
			s.handleCreateVolume(w, r, cfg)
		default:
			w.Header().Set("Allow", "GET, POST, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (s *state) handleCreateVolume(w http.ResponseWriter, r *http.Request, cfg config) {
	var req volumeRequest
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Volume < 0 {
		http.Error(w, "volume must be positive", http.StatusBadRequest)
		return
	}

	reading := buildVolumeReading(cfg, req)
	reading = s.addVolume(reading)

	var alert *cryEvent
	if reading.Loud {
		event := s.add(buildLoudEvent(reading))
		alert = &event
	}

	writeJSON(w, http.StatusCreated, volumeResponse{
		Reading: reading,
		Alert:   alert,
	})
}

func (s *state) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan cryEvent, 8)
	s.subscribe(ch)
	defer s.unsubscribe(ch)

	for _, event := range reverse(s.latest(5)) {
		writeSSE(w, event)
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event := <-ch:
			writeSSE(w, event)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func buildEvent(ctx context.Context, cfg config, req sensorRequest) (cryEvent, error) {
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		deviceID = "unknown-device"
	}

	result := verdict{}
	source := "local"
	localResult := localVerdict(cfg, req)

	if strings.TrimSpace(req.AudioBase64) != "" {
		if cfg.aiProvider == "local" {
			result = localResult
			source = "local"
		} else {
			aiResult, aiSource, err := analyzeWithConfiguredAI(ctx, cfg, req, localResult)
			if err == nil {
				result = aiResult
				source = aiSource
			} else {
				if !errors.Is(err, errNoAIProvider) {
					log.Printf("ai analysis failed: %v", err)
				}
				result = localResult
				result.Message = aiFallbackMessage(err)
				source = "local-fallback"
			}
		}
	} else if req.Crying != nil {
		result = localResult
		source = "sensor"
	} else {
		result = localResult
	}

	if result.Message == "" {
		if result.Reason != "" {
			result.Message = result.Reason
		} else if result.Crying {
			result.Message = "Weinen erkannt"
		} else {
			result.Message = "Kein Weinen erkannt"
		}
	}

	alertType := eventAlertType(req.AlertType, result.Crying)

	return cryEvent{
		DeviceID:   deviceID,
		AlertType:  alertType,
		Crying:     result.Crying,
		Confidence: clamp(result.Confidence, 0, 1),
		Volume:     req.Volume,
		Source:     source,
		Message:    result.Message,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

func eventAlertType(raw string, crying bool) string {
	if crying {
		return "cry"
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "connected", "startup", "online":
		return "connected"
	default:
		return "quiet"
	}
}

func buildVolumeReading(cfg config, req volumeRequest) volumeReading {
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		deviceID = "unknown-device"
	}
	threshold := cfg.loudThreshold
	if threshold <= 0 {
		threshold = 1000
	}
	message := strings.TrimSpace(req.Message)
	loud := req.Volume >= threshold
	if message == "" {
		if loud {
			message = "Lautstaerke-Warnung"
		} else {
			message = "Lautstaerke normal"
		}
	}

	return volumeReading{
		DeviceID:  deviceID,
		Volume:    req.Volume,
		Loud:      loud,
		Threshold: threshold,
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
}

func buildLoudEvent(reading volumeReading) cryEvent {
	confidence := 0.75
	if reading.Threshold > 0 {
		confidence = clamp(reading.Volume/reading.Threshold, 0.75, 1)
	}

	return cryEvent{
		DeviceID:   reading.DeviceID,
		AlertType:  "loud",
		Crying:     false,
		Confidence: confidence,
		Volume:     reading.Volume,
		Source:     "volume",
		Message:    "Sehr laut: " + reading.Message,
		CreatedAt:  reading.CreatedAt,
	}
}

func localVerdict(cfg config, req sensorRequest) verdict {
	crying := req.Volume >= cfg.threshold
	if req.Crying != nil && *req.Crying {
		crying = true
	}

	return verdict{
		Crying:     crying,
		Confidence: confidence(req.Confidence, crying, req.Volume, cfg.threshold),
		Message:    strings.TrimSpace(req.Message),
	}
}

func analyzeWithConfiguredAI(ctx context.Context, cfg config, req sensorRequest, localResult verdict) (verdict, string, error) {
	providers := aiProviders(cfg)
	if len(providers) == 0 {
		return verdict{}, "", errNoAIProvider
	}

	var lastErr error
	for _, provider := range providers {
		var aiResult verdict
		var err error

		switch provider {
		case "openai":
			aiResult, err = analyzeWithOpenAI(ctx, cfg, req, localResult)
		case "gemini":
			aiResult, err = analyzeWithGemini(ctx, cfg, req, localResult)
		default:
			err = fmt.Errorf("unsupported ai provider %q", provider)
		}

		if err == nil {
			return mergeAIVerdict(localResult, aiResult), mergedSource(provider, localResult, aiResult), nil
		}
		lastErr = err
		log.Printf("%s analysis failed: %v", provider, err)
	}

	if lastErr == nil {
		lastErr = errNoAIProvider
	}
	return verdict{}, "", lastErr
}

func aiProviders(cfg config) []string {
	switch cfg.aiProvider {
	case "openai":
		if cfg.openAIKey == "" {
			return nil
		}
		return []string{"openai"}
	case "gemini":
		if cfg.geminiKey == "" {
			return nil
		}
		return []string{"gemini"}
	case "local":
		return nil
	default:
		providers := make([]string, 0, 2)
		if cfg.openAIKey != "" {
			providers = append(providers, "openai")
		}
		if cfg.geminiKey != "" {
			providers = append(providers, "gemini")
		}
		return providers
	}
}

func mergeAIVerdict(localResult verdict, aiResult verdict) verdict {
	if !localResult.Crying {
		return aiResult
	}
	if aiResult.Crying {
		aiResult.Confidence = maxFloat(aiResult.Confidence, localResult.Confidence)
		if strings.TrimSpace(aiResult.Message) == "" {
			aiResult.Message = localResult.Message
		}
		return aiResult
	}

	message := "Sensor meldet moegliches Weinen; KI-Audio nicht eindeutig"
	if localMessage := strings.TrimSpace(localResult.Message); localMessage != "" && looksCryMessage(localMessage) && !looksQuietMessage(localMessage) {
		message = localMessage
	}
	return verdict{
		Crying:     true,
		Confidence: clamp(localResult.Confidence, 0.6, 0.88),
		Message:    message,
		Reason:     "local sensor reported crying while AI did not",
	}
}

func mergedSource(provider string, localResult verdict, aiResult verdict) string {
	if localResult.Crying {
		if aiResult.Crying {
			return "sensor+" + provider
		}
		return "sensor"
	}
	return provider
}

func looksQuietMessage(message string) bool {
	normalized := strings.ToLower(message)
	return strings.Contains(normalized, "ruhig") ||
		strings.Contains(normalized, "kein weinen") ||
		strings.Contains(normalized, "kein baby")
}

func looksCryMessage(message string) bool {
	normalized := strings.ToLower(message)
	return strings.Contains(normalized, "weinen") ||
		strings.Contains(normalized, "schrei") ||
		strings.Contains(normalized, "cry")
}

func aiFallbackMessage(err error) string {
	if errors.Is(err, errNoAIProvider) {
		return "Kein KI-Key, lokal bewertet"
	}

	var openAIError openAIHTTPError
	if errors.As(err, &openAIError) {
		return openAIFallbackMessage(openAIError)
	}

	var geminiError geminiHTTPError
	if errors.As(err, &geminiError) {
		return geminiFallbackMessage(geminiError)
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "openai"):
		return "OpenAI nicht erreichbar, lokal bewertet"
	case strings.Contains(message, "gemini"):
		return "Gemini nicht erreichbar, lokal bewertet"
	case strings.Contains(message, "deadline") || strings.Contains(message, "timeout"):
		return "KI Timeout, lokal bewertet"
	default:
		return "KI nicht erreichbar, lokal bewertet"
	}
}

func openAIFallbackMessage(err openAIHTTPError) string {
	switch {
	case err.statusCode == http.StatusUnauthorized || err.statusCode == http.StatusForbidden:
		return "OpenAI API-Key ungueltig oder Zugriff nicht erlaubt, lokal bewertet"
	case err.statusCode == http.StatusTooManyRequests:
		return "OpenAI Limit erreicht, lokal bewertet"
	case err.statusCode == http.StatusNotFound:
		return "OpenAI-Modell nicht verfuegbar, lokal bewertet"
	case err.statusCode >= 500:
		return "OpenAI nicht erreichbar, lokal bewertet"
	default:
		return "OpenAI Anfrage abgelehnt, lokal bewertet"
	}
}

func geminiFallbackMessage(err error) string {
	if err == nil {
		return "Gemini nicht erreichbar, lokal bewertet"
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "api key not valid") || strings.Contains(message, "api_key_invalid"):
		return "Gemini API-Key ungueltig, lokal bewertet"
	case strings.Contains(message, "permission_denied") || strings.Contains(message, "api has not been used"):
		return "Gemini API-Zugriff nicht erlaubt, lokal bewertet"
	case strings.Contains(message, "resource_exhausted") || strings.Contains(message, "quota"):
		return "Gemini Limit erreicht, lokal bewertet"
	case strings.Contains(message, "service unavailable") || strings.Contains(message, "unavailable") || strings.Contains(message, "high demand"):
		return "Gemini ueberlastet, lokal bewertet"
	case strings.Contains(message, "not found") || strings.Contains(message, "not supported for generatecontent"):
		return "Gemini-Modell nicht verfuegbar, lokal bewertet"
	case strings.Contains(message, "deadline") || strings.Contains(message, "timeout"):
		return "Gemini Timeout, lokal bewertet"
	default:
		return "Gemini nicht erreichbar, lokal bewertet"
	}
}

func geminiModelList(primary string, fallbacks string) []string {
	models := make([]string, 0, 3)
	seen := make(map[string]struct{})

	add := func(value string) {
		model := strings.TrimSpace(value)
		if model == "" {
			return
		}
		if _, ok := seen[model]; ok {
			return
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}

	add(primary)
	for _, fallback := range strings.Split(fallbacks, ",") {
		add(fallback)
	}
	return models
}

func babyCryPrompt(cfg config, req sensorRequest, localResult verdict) string {
	return fmt.Sprintf(
		"You are a baby-cry classifier. Decide if this audio contains a crying baby. "+
			"Return only JSON with keys crying (boolean), confidence (0.0 to 1.0), and message (short German text). "+
			"Sensor context: volume=%.0f, localCrying=%t, threshold=%.0f. "+
			"The recording comes from a small ESP32 analog microphone and can be noisy or quiet. "+
			"If localCrying is true and the audio is ambiguous, return crying true with moderate confidence and mention uncertainty in German. "+
			"Only return crying false when baby crying is not plausible.",
		req.Volume,
		localResult.Crying,
		cfg.threshold,
	)
}

func analyzeWithOpenAI(ctx context.Context, cfg config, req sensorRequest, localResult verdict) (verdict, error) {
	audio, mimeType, err := normalizeAudio(req.AudioBase64, req.MimeType)
	if err != nil {
		return verdict{}, err
	}
	format, err := openAIAudioFormat(mimeType)
	if err != nil {
		return verdict{}, err
	}

	payload := map[string]any{
		"model": cfg.openAIModel,
		"messages": []any{
			map[string]any{
				"role":    "system",
				"content": babyCryPrompt(cfg, req, localResult),
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Analysiere das Audio. Antworte ausschliesslich mit dem JSON-Objekt.",
					},
					map[string]any{
						"type": "input_audio",
						"input_audio": map[string]any{
							"data":   audio,
							"format": format,
						},
					},
				},
			},
		},
		"max_completion_tokens": 160,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return verdict{}, err
	}

	text, err := callOpenAIChat(ctx, cfg, data)
	if err != nil {
		return verdict{}, err
	}

	var result verdict
	if err := json.Unmarshal([]byte(extractJSON(text)), &result); err != nil {
		return verdict{}, err
	}
	return result, nil
}

func openAIAudioFormat(mimeType string) (string, error) {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch mimeType {
	case "audio/wav", "audio/wave", "audio/x-wav":
		return "wav", nil
	case "audio/mpeg", "audio/mp3", "audio/mpga":
		return "mp3", nil
	default:
		if strings.Contains(mimeType, "wav") {
			return "wav", nil
		}
		if strings.Contains(mimeType, "mp3") || strings.Contains(mimeType, "mpeg") {
			return "mp3", nil
		}
		return "", fmt.Errorf("openai audio format not supported for MIME type %q", mimeType)
	}
}

func callOpenAIChat(ctx context.Context, cfg config, data []byte) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.openAIKey)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", openAIHTTPError{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       string(respBody),
		}
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return "", err
	}
	if len(openAIResp.Choices) == 0 {
		return "", errors.New("openai returned no choices")
	}

	text := openAIMessageText(openAIResp.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("openai returned no text")
	}
	return text, nil
}

func openAIMessageText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return builder.String()
	}
	return ""
}

func analyzeWithGemini(ctx context.Context, cfg config, req sensorRequest, localResult verdict) (verdict, error) {
	audio, mimeType, err := normalizeAudio(req.AudioBase64, req.MimeType)
	if err != nil {
		return verdict{}, err
	}

	payload := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"text": babyCryPrompt(cfg, req, localResult),
					},
					map[string]any{
						"inline_data": map[string]any{
							"mime_type": mimeType,
							"data":      audio,
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return verdict{}, err
	}

	text, err := generateGeminiText(ctx, cfg, data)
	if err != nil {
		return verdict{}, err
	}

	var result verdict
	if err := json.Unmarshal([]byte(extractJSON(text)), &result); err != nil {
		return verdict{}, err
	}
	return result, nil
}

func generateGeminiText(ctx context.Context, cfg config, data []byte) (string, error) {
	var lastErr error
	for modelIndex, model := range cfg.geminiModels {
		for attempt := 1; attempt <= 2; attempt++ {
			text, err := callGeminiModel(ctx, cfg, model, data)
			if err == nil {
				if modelIndex > 0 || attempt > 1 {
					log.Printf("gemini analysis succeeded with model %s after fallback/retry", model)
				}
				return text, nil
			}

			lastErr = err
			if !isTemporaryGeminiError(err) {
				return "", err
			}
			if attempt < 2 {
				log.Printf("gemini temporary failure on model %s, retrying: %v", model, err)
				if err := waitForRetry(ctx, 750*time.Millisecond); err != nil {
					return "", err
				}
			}
		}

		if modelIndex+1 < len(cfg.geminiModels) {
			log.Printf("gemini model %s unavailable, trying fallback model %s", model, cfg.geminiModels[modelIndex+1])
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("no gemini models configured")
}

func callGeminiModel(ctx context.Context, cfg config, model string, data []byte) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", cfg.geminiKey)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", geminiHTTPError{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       string(respBody),
		}
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return "", err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini returned no text")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

func isTemporaryGeminiError(err error) bool {
	var httpErr geminiHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.statusCode == http.StatusTooManyRequests || httpErr.statusCode == http.StatusServiceUnavailable || httpErr.statusCode >= 500
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "deadline") || strings.Contains(message, "temporary")
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeAudio(raw string, mimeType string) (string, string, error) {
	audio := strings.TrimSpace(raw)
	if strings.HasPrefix(audio, "data:") {
		comma := strings.Index(audio, ",")
		if comma < 0 {
			return "", "", errors.New("invalid data url")
		}
		meta := strings.TrimPrefix(audio[:comma], "data:")
		if semicolon := strings.Index(meta, ";"); semicolon >= 0 {
			meta = meta[:semicolon]
		}
		if meta != "" {
			mimeType = meta
		}
		audio = audio[comma+1:]
	}

	audio = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(audio)
	if _, err := base64.StdEncoding.DecodeString(audio); err != nil {
		return "", "", fmt.Errorf("audioBase64 is not valid base64: %w", err)
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "audio/wav"
	}
	return audio, mimeType, nil
}

func extractJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return trimmed[start : end+1]
	}
	return trimmed
}

func (s *state) add(event cryEvent) cryEvent {
	s.mu.Lock()
	s.nextID++
	event.ID = s.nextID
	s.events = append([]cryEvent{event}, s.events...)
	if len(s.events) > 50 {
		s.events = s.events[:50]
	}

	subscribers := make([]chan cryEvent, 0, len(s.subscribers))
	for ch := range s.subscribers {
		subscribers = append(subscribers, ch)
	}
	s.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}

	return event
}

func (s *state) addVolume(reading volumeReading) volumeReading {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextVolumeID++
	reading.ID = s.nextVolumeID
	s.volumes = append([]volumeReading{reading}, s.volumes...)
	if len(s.volumes) > 50 {
		s.volumes = s.volumes[:50]
	}
	return reading
}

func (s *state) latest(limit int) []cryEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}

	events := make([]cryEvent, limit)
	copy(events, s.events[:limit])
	return events
}

func (s *state) latestVolumes(limit int) []volumeReading {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 || limit > len(s.volumes) {
		limit = len(s.volumes)
	}

	readings := make([]volumeReading, limit)
	copy(readings, s.volumes[:limit])
	return readings
}

func (s *state) subscribe(ch chan cryEvent) {
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
}

func (s *state) unsubscribe(ch chan cryEvent) {
	s.mu.Lock()
	delete(s.subscribers, ch)
	close(ch)
	s.mu.Unlock()
}

func reverse(events []cryEvent) []cryEvent {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

func writeSSE(w io.Writer, event cryEvent) {
	data, _ := json.Marshal(event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func confidence(raw *float64, crying bool, volume float64, threshold float64) float64 {
	if raw != nil {
		if crying {
			return clamp(*raw, 0.55, 1)
		}
		return clamp(*raw, 0, 0.8)
	}
	if threshold <= 0 {
		threshold = 1
	}
	if crying {
		return clamp(0.55+(volume-threshold)/(threshold*1.5), 0.55, 0.95)
	}
	return clamp(0.8-(volume/threshold)*0.4, 0.25, 0.8)
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func normalizeAIProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "openai", "chatgpt", "chatgbt", "gpt":
		return "openai"
	case "gemini", "google":
		return "gemini"
	case "local", "none", "off":
		return "local"
	default:
		return "auto"
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
