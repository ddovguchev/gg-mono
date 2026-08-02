package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ddouhushau/go-transcoder/internal/config"
	"github.com/ddouhushau/go-transcoder/internal/services"
	"github.com/ddouhushau/go-transcoder/internal/tunnel"
)

// healthcheck — диагностика доступа к сервисам на сервере.
// Поднимает SSH-туннель (как в pipeline) и проверяет Whisper / Ollama / TTS.
func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("⚠  config: %v\n", err)
	}
	fmt.Printf("Server: %s  (tunnel %s@%s)\n\n", cfg.ServerHost, cfg.SSHUser, cfg.ServerHost)

	whisperURL, ollamaURL, ttsURL := cfg.WhisperURL(), cfg.OllamaURL(), cfg.TTSEndpoint()

	// 1. SSH-туннель
	t, err := openTunnel(cfg)
	if err != nil {
		fmt.Printf("❌ SSH tunnel: %v\n", err)
		os.Exit(1)
	}
	defer t.Close()
	fmt.Println("✅ SSH tunnel up")

	// Переписываем URL на localhost
	whisperURL = localize(whisperURL)
	ollamaURL = localize(ollamaURL)
	ttsURL = localize(ttsURL)
	fmt.Printf("   whisper → %s\n   ollama  → %s\n   tts     → %s\n\n", whisperURL, ollamaURL, ttsURL)

	// 2. Health-проверки
	check("Whisper (STT)", services.NewWhisperClient(whisperURL).HealthCheck, whisperURL)
	check("Ollama (LLM)", services.NewOllamaClient(ollamaURL, cfg.OllamaModel).HealthCheck, cfg.OllamaModel)
	check("TTS (F5)", services.NewTTSClient(ttsURL, cfg.TTSModel).HealthCheck, cfg.TTSModel)
}

func check(name string, fn func() bool, detail string) {
	done := make(chan bool, 1)
	go func() { done <- fn() }()
	select {
	case ok := <-done:
		if ok {
			fmt.Printf("✅ %-14s %s\n", name, detail)
		} else {
			fmt.Printf("❌ %-14s %s\n", name, detail)
		}
	case <-time.After(15 * time.Second):
		fmt.Printf("❌ %-14s %s (timeout)\n", name, detail)
	}
}

func openTunnel(cfg *config.Settings) (*tunnel.SSHTunnel, error) {
	keyPath := cfg.SSHKeyPath
	if keyPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			keyPath = filepath.Join(home, ".ssh", "id_ed25519")
		}
	}
	port := cfg.SSHPort
	if port == 0 {
		port = 22
	}

	t, err := tunnel.NewSSHTunnel(tunnel.SSHConfig{
		Host:    cfg.ServerHost,
		User:    cfg.SSHUser,
		Port:    port,
		KeyPath: keyPath,
	})
	if err != nil {
		return nil, err
	}

	for _, p := range []string{cfg.WhisperPort, cfg.OllamaPort, cfg.TTSPort} {
		local := "127.0.0.1:" + p
		if err := t.Forward(local, local); err != nil {
			t.Close()
			return nil, err
		}
	}
	return t, nil
}

func localize(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return raw
	}
	return "http://127.0.0.1:" + strconv.Itoa(p)
}
