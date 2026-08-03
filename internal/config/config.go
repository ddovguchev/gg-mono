package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type Settings struct {
	ServerHost  string `json:"server_host"`
	WhisperPort string `json:"whisper_port"`
	OllamaPort  string `json:"ollama_port"`
	TTSPort     string `json:"tts_port"`
	OllamaModel string `json:"ollama_model"`
	TTSModel    string `json:"tts_model"`
	MicDevice   string `json:"mic_device"`
	VirtualMic  string `json:"virtual_mic"`
	SourceLang  string `json:"source_lang"`
	TargetLang  string `json:"target_lang"`

	// VoiceEnabled — проигрывать ли перевод голосом (TTS).
	// Отключено — только субтитры: распознавание + перевод.
	VoiceEnabled bool `json:"voice_enabled"`

	// Локальный Fish Speech на AI-хосте (не облако).
	FishBaseURL    string `json:"fish_base_url"`    // обычно туннель http://127.0.0.1:18080
	FishAPIKey     string `json:"fish_api_key"`     // опционально, локально не нужен
	FishVoiceID    string `json:"fish_voice_id"`
	FishRefsRemote string `json:"fish_refs_remote"` // /ai-volume/fish-speech/references
	FishRemotePort string `json:"fish_remote_port"` // 8080 на AI-хосте
	FishLocalPort  string `json:"fish_local_port"`  // локальный порт туннеля

	// SSH-туннель к серверу. Если SSHUser пустой — прямое подключение.
	SSHUser    string `json:"ssh_user"`
	SSHPort    int    `json:"ssh_port"`
	SSHKeyPath string `json:"ssh_key_path"`
}

var defaults = Settings{
	ServerHost:     "10.0.0.26",
	WhisperPort:    "8000",
	OllamaPort:     "11434",
	TTSPort:        "5002",
	OllamaModel:    "llama3",
	TTSModel:       "f5-tts",
	SourceLang:     "Russian",
	TargetLang:     "English",
	MicDevice:      "default",
	VirtualMic:     "default",
	VoiceEnabled:   false, // озвучка выключена по умолчанию — только субтитры
	FishBaseURL:    "http://127.0.0.1:18080",
	FishRefsRemote: "/ai-volume/fish-speech/references",
	FishRemotePort: "8080",
	FishLocalPort:  "18080",
	SSHUser:        "dd",
	SSHPort:        22,
}

func (s *Settings) WhisperURL() string  { return "http://" + s.ServerHost + ":" + s.WhisperPort }
func (s *Settings) OllamaURL() string   { return "http://" + s.ServerHost + ":" + s.OllamaPort }
func (s *Settings) TTSEndpoint() string { return "http://" + s.ServerHost + ":" + s.TTSPort }

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "mono-go")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func Load() (*Settings, error) {
	s := defaults
	dir, err := configDir()
	if err != nil {
		return applyEnv(&s), nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return applyEnv(&s), nil
		}
		return applyEnv(&s), err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("[config] ignoring invalid config.json: %v (using defaults)", err)
		s = defaults
	}
	return applyEnv(&s), nil
}

func applyEnv(s *Settings) *Settings {
	if s.FishBaseURL == "" || s.FishBaseURL == "https://api.fish.audio" {
		s.FishBaseURL = defaults.FishBaseURL
	}
	if s.FishRefsRemote == "" {
		s.FishRefsRemote = defaults.FishRefsRemote
	}
	if s.FishRemotePort == "" {
		s.FishRemotePort = defaults.FishRemotePort
	}
	if s.FishLocalPort == "" {
		s.FishLocalPort = defaults.FishLocalPort
	}
	if v := os.Getenv("FISH_API_KEY"); v != "" {
		s.FishAPIKey = v
	}
	if v := os.Getenv("FISH_BASE_URL"); v != "" {
		s.FishBaseURL = v
	}
	if v := os.Getenv("FISH_REFS_REMOTE"); v != "" {
		s.FishRefsRemote = v
	}
	return s
}

func Save(s *Settings) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}
