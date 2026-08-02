package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ddouhushau/go-transcoder/internal/config"
	"github.com/ddouhushau/go-transcoder/internal/pipeline"
)

func main() {
	fmt.Println("=== Pipeline Test (via SSH tunnel) ===")
	fmt.Println()

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("⚠  config: %v\n", err)
	}
	fmt.Printf("Server: %s (tunnel %s@%s)\n", cfg.ServerHost, cfg.SSHUser, cfg.ServerHost)

	pc := pipeline.Config{
		WhisperURL:  cfg.WhisperURL(),
		OllamaURL:   cfg.OllamaURL(),
		OllamaModel: cfg.OllamaModel,
		TTSEndpoint: cfg.TTSEndpoint(),
		TTSModel:    cfg.TTSModel,
		MicDevice:   cfg.MicDevice,
		VirtualMic:  cfg.VirtualMic,
		TargetLang:  cfg.TargetLang,
		SourceLang:  cfg.SourceLang,

		SSHHost:    cfg.ServerHost,
		SSHUser:    cfg.SSHUser,
		SSHPort:    cfg.SSHPort,
		SSHKeyPath: cfg.SSHKeyPath,
	}

	p := pipeline.New(pc)

	go func() {
		for update := range p.StatusChan() {
			switch update.Stage {
			case "connecting":
				fmt.Printf("  🔗  %s\n", update.Message)
			case "listening":
				fmt.Printf("  🎙  %s\n", update.Message)
			case "transcribing":
				fmt.Printf("  📝 %s\n", update.Message)
			case "transcribed":
				fmt.Printf("  🎤 → %s\n", update.Text)
			case "translating":
				fmt.Printf("  🌐 %s\n", update.Message)
			case "translated":
				fmt.Printf("  🌍 → %s\n", update.Text)
			case "synthesizing":
				fmt.Printf("  🔊 %s\n", update.Message)
			case "playing":
				fmt.Printf("  ▶  %s\n", update.Message)
			case "error":
				fmt.Printf("  ❌ %s\n", update.Message)
			}
		}
	}()

	fmt.Println("Starting pipeline...")
	if err := p.Start(); err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Running! Speak into mic. Audio → speakers.")
	fmt.Println("   Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("Stopping...")
	p.Stop()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("Done.")
}
