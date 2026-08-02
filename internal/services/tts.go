package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TTSClient — клиент для локального TTS (F5-TTS).
// Запускается на сервере dd@10.0.0.26, доступ через SSH tunnel.
type TTSClient struct {
	endpoint string
	model    string
	client   *http.Client
}

type ttsRequest struct {
	Text     string  `json:"text"`
	Model    string  `json:"model,omitempty"`
	Language string  `json:"language,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
}

func NewTTSClient(endpoint, model string) *TTSClient {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:5002"
	}
	if model == "" {
		model = "f5-tts"
	}
	return &TTSClient{
		endpoint: endpoint,
		model:    model,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Synthesize отправляет текст и возвращает аудиоданные (WAV, 24kHz mono).
func (t *TTSClient) Synthesize(text, language string) ([]byte, error) {
	payload := ttsRequest{
		Text:     text,
		Model:    t.model,
		Language: language,
		Speed:    1.0,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tts: marshal: %w", err)
	}

	resp, err := t.client.Post(
		t.endpoint+"/api/tts",
		"application/json",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("tts: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tts: status %d: %s", resp.StatusCode, string(b))
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tts: read body: %w", err)
	}

	return audioData, nil
}

// HealthCheck проверяет доступность TTS-сервера.
func (t *TTSClient) HealthCheck() bool {
	resp, err := t.client.Get(t.endpoint + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
