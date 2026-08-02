package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WhisperClient — клиент для Whisper ASR (speech-to-text).
// Ожидает HTTP-сервер (faster-whisper-server / whisper.cpp + wrapper)
// с endpoint POST /v1/audio/transcriptions (OpenAI-compatible).
type WhisperClient struct {
	baseURL string
	client  *http.Client
}

type whisperReq struct {
	File []byte `json:"-"`
	// OpenAI-compatible fields
	Language string `json:"language,omitempty"`
}

// whisperResp — ответ Whisper-compatible API.
type whisperResp struct {
	Text string `json:"text"`
}

func NewWhisperClient(baseURL string) *WhisperClient {
	return &WhisperClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Transcribe отправляет WAV-аудио и возвращает текст.
// audioData — полный WAV-файл (16kHz, mono, 16-bit PCM).
func (w *WhisperClient) Transcribe(audioData []byte, language string) (string, error) {
	// Формируем multipart/form-data запрос (стандарт OpenAI-compatible API)
	body := &bytes.Buffer{}
	boundary := "----GoTranscoderBoundary"

	// PCM-данные как файл
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString(`Content-Disposition: form-data; name="file"; filename="audio.wav"` + "\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\n")
	body.Write(audioData)
	body.WriteString("\r\n")

	if language != "" {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString(`Content-Disposition: form-data; name="language"` + "\r\n\r\n")
		body.WriteString(language + "\r\n")
	}

	body.WriteString("--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", w.baseURL+"/v1/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper: status %d: %s", resp.StatusCode, string(b))
	}

	var result whisperResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("whisper: decode: %w", err)
	}

	return result.Text, nil
}

// HealthCheck проверяет доступность Whisper-сервера.
func (w *WhisperClient) HealthCheck() bool {
	resp, err := w.client.Get(w.baseURL + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
