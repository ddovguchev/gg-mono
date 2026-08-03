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

	// SSH-туннель к серверу. Если SSHUser пустой — прямое подключение.
	SSHUser    string `json:"ssh_user"`
	SSHPort    int    `json:"ssh_port"`
	SSHKeyPath string `json:"ssh_key_path"`
}

var defaults = Settings{
	ServerHost:  "10.0.0.26",
	WhisperPort: "8000",
	OllamaPort:  "11434",
	TTSPort:     "5002",
	OllamaModel: "llama3",
	TTSModel:    "f5-tts",
	SourceLang:  "Russian",
	TargetLang:  "English",
	MicDevice:   "default",
	VirtualMic:  "default",
	VoiceEnabled: false, // озвучка выключена по умолчанию — только субтитры
	SSHUser:     "dd",
	SSHPort:     22,
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
		return &s, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return &s, nil
		}
		return &s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("[config] ignoring invalid config.json: %v (using defaults)", err)
		s = defaults
	}
	return &s, nil
}

func Save(s *Settings) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}
