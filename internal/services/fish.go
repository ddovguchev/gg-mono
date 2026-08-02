package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FishClient — клиент для Fish Audio TTS (text-to-speech).
// Поддерживает клонирование голоса и мультиязычный синтез.
type FishClient struct {
	baseURL   string
	apiKey    string
	voiceID   string
	client    *http.Client
}

type fishTTSReq struct {
	Model      string  `json:"model"`
	Text       string  `json:"text"`
	Format     string  `json:"format,omitempty"`
	Normalize  bool    `json:"normalize,omitempty"`
	Balance    float64 `json:"balance,omitempty"`
}

func NewFishClient(baseURL, apiKey, voiceID string) *FishClient {
	if baseURL == "" {
		baseURL = "https://api.fish.audio"
	}
	return &FishClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		voiceID: voiceID,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Synthesize отправляет текст и возвращает аудиоданные (MP3/WAV).
func (f *FishClient) Synthesize(text string) ([]byte, string, error) {
	payload := fishTTSReq{
		Model:      f.voiceID,
		Text:       text,
		Format:     "wav",
		Normalize:  true,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("fish: marshal: %w", err)
	}

	req, err := http.NewRequest("POST", f.baseURL+"/v1/tts", bytes.NewReader(jsonData))
	if err != nil {
		return nil, "", fmt.Errorf("fish: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if f.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+f.apiKey)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fish: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("fish: status %d: %s", resp.StatusCode, string(b))
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("fish: read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}

	return audioData, contentType, nil
}

// HealthCheck проверяет доступность Fish Audio API.
func (f *FishClient) HealthCheck() bool {
	resp, err := f.client.Get(f.baseURL + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
