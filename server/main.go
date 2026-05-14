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
	geminiKey     string
	geminiModel   string
}

type sensorRequest struct {
	DeviceID    string   `json:"deviceId"`
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

func main() {
	cfg := config{
		addr:          env("ADDR", ":8080"),
		threshold:     envFloat("VOLUME_THRESHOLD", 65),
		loudThreshold: envFloat("LOUD_VOLUME_THRESHOLD", 1000),
		geminiKey:     os.Getenv("GEMINI_API_KEY"),
		geminiModel:   env("GEMINI_MODEL", "gemini-3-flash-preview"),
	}

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

	if strings.TrimSpace(req.AudioBase64) != "" {
		if cfg.geminiKey != "" {
			geminiResult, err := analyzeWithGemini(ctx, cfg, req)
			if err == nil {
				result = geminiResult
				source = "gemini"
			} else {
				result = localVerdict(cfg, req)
				result.Message = "Gemini nicht erreichbar, lokal bewertet"
				source = "local-fallback"
			}
		} else {
			result = localVerdict(cfg, req)
			result.Message = "Kein Gemini-Key, lokal bewertet"
			source = "local"
		}
	} else if req.Crying != nil {
		result.Crying = *req.Crying
		result.Confidence = confidence(req.Confidence, result.Crying, req.Volume, cfg.threshold)
		result.Message = strings.TrimSpace(req.Message)
		source = "sensor"
	} else {
		result = localVerdict(cfg, req)
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

	alertType := "quiet"
	if result.Crying {
		alertType = "cry"
	}

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
	if req.Crying != nil {
		crying = *req.Crying
	}

	return verdict{
		Crying:     crying,
		Confidence: confidence(req.Confidence, crying, req.Volume, cfg.threshold),
		Message:    strings.TrimSpace(req.Message),
	}
}

func analyzeWithGemini(ctx context.Context, cfg config, req sensorRequest) (verdict, error) {
	audio, mimeType, err := normalizeAudio(req.AudioBase64, req.MimeType)
	if err != nil {
		return verdict{}, err
	}

	payload := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"text": "You are a baby-cry classifier. Decide if this audio contains a crying baby. Return only JSON with keys crying (boolean), confidence (0.0 to 1.0), and message (short German text).",
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

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", cfg.geminiModel)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return verdict{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", cfg.geminiKey)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return verdict{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return verdict{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return verdict{}, fmt.Errorf("gemini returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
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
		return verdict{}, err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return verdict{}, errors.New("gemini returned no text")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text
	var result verdict
	if err := json.Unmarshal([]byte(extractJSON(text)), &result); err != nil {
		return verdict{}, err
	}
	return result, nil
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
		return *raw
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
