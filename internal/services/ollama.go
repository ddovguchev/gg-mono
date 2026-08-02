package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaClient — клиент для Ollama LLM.
// Используется для понимания контекста и исправления ошибок в распознанном тексте.
type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
}

type ollamaGenerateReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	// KeepAlive — держать модель в VRAM, чтобы не было холодного старта
	// в середине разговора (Ollama по умолчанию выгружает через 5 мин).
	KeepAlive int `json:"keep_alive,omitempty"` // -1 = держать загруженной
}

type ollamaGenerateResp struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

type ollamaTagsResp struct {
	Models []ollamaModel `json:"models"`
}

type ollamaModel struct {
	Name string `json:"name"`
}

func NewOllamaClient(baseURL, model string) *OllamaClient {
	if model == "" {
		model = "llama3"
	}
	return &OllamaClient{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Understand отправляет текст LLM для перевода на целевой язык.
func (o *OllamaClient) Understand(text, targetLang string) (string, error) {
	return o.UnderstandFrom(text, "", targetLang)
}

// UnderstandFrom переводит с исходного языка на целевой.
func (o *OllamaClient) UnderstandFrom(text, sourceLang, targetLang string) (string, error) {
	if sourceLang == "" {
		sourceLang = "Russian"
	}
	if targetLang == "" {
		targetLang = "English"
	}
	prompt := fmt.Sprintf(`You are a real-time speech translator.
Translate FROM %s TO %s.

STRICT RULES:
- The input is spoken %s. Keep the meaning, fix ASR typos.
- Output ONLY the %s translation. Nothing else.
- NO labels, NO "Translation:", NO quotes, NO explanations.
- Do NOT output %s. Do NOT repeat the original.

Input (%s):
%s`, sourceLang, targetLang, sourceLang, targetLang, sourceLang, sourceLang, text)

	payload := ollamaGenerateReq{
		Model:     o.model,
		Prompt:    prompt,
		Stream:    false,
		KeepAlive: -1,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ollama: marshal: %w", err)
	}

	req, err := http.NewRequest("POST", o.baseURL+"/api/generate", bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama: status %d: %s", resp.StatusCode, string(b))
	}

	var result ollamaGenerateResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ollama: decode: %w", err)
	}

	return strings.TrimSpace(result.Response), nil
}

// HealthCheck проверяет доступность Ollama и наличие загруженных моделей.
func (o *OllamaClient) HealthCheck() bool {
	resp, err := o.client.Get(o.baseURL + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var tags ollamaTagsResp
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false
	}

	return len(tags.Models) > 0
}
