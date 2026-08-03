package main

import (
	"log"
	"os"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/ddouhushau/go-transcoder/internal/audio"
	"github.com/ddouhushau/go-transcoder/internal/config"
	"github.com/ddouhushau/go-transcoder/internal/pipeline"
)

type App struct {
	th *material.Theme

	state *AppState
	cfg   *config.Settings
	pipe  *pipeline.Pipeline

	startBtn  *widget.Clickable
	configBtn *widget.Clickable
	saveBtn   *widget.Clickable
	micDrop   *dropdown
	langDrop  *dropdown
	txList    widget.List
	voiceBtn  widget.Bool // 🔊 Voice — проигрывать перевод голосом
	localWhisper widget.Bool // 🐍 Local Whisper — whisper.cpp на Mac

	serverEdit   widget.Editor
	ollamaEdit   widget.Editor
	ttsModelEdit widget.Editor
}

// TranscriptLine — одна фраза: что сказал пользователь + перевод.
type TranscriptLine struct {
	Source string // распознанная речь (что сказал пользователь)
	Target string // перевод
}

type AppState struct {
	micDevices   []string
	selectedMic  int
	langDevices  []string
	selectedLang int

	recording  bool
	showConfig bool
	status     string
	subtitle   string
	transcript []TranscriptLine
	mu         sync.Mutex
}

func newApp(th *material.Theme) *App {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[ui] config: %v (using defaults)", err)
	}

	micDevices := enumerateInputDevices()
	selectedMic := findDeviceIndex(micDevices, cfg.MicDevice)

	langDevices := []string{"English", "Deutsch", "Français", "Español", "Polski", "Русский"}
	selectedLang := findLangIndex(langDevices, cfg.TargetLang)

	state := &AppState{
		micDevices:   micDevices,
		selectedMic:  selectedMic,
		langDevices:  langDevices,
		selectedLang: selectedLang,
		status:       "Idle",
		subtitle:     "Press Start",
	}

	a := &App{
		th:        th,
		state:     state,
		cfg:       cfg,
		startBtn:  new(widget.Clickable),
		configBtn: new(widget.Clickable),
		saveBtn:   new(widget.Clickable),
		micDrop:   newDropdown("Microphone", micDevices, &state.selectedMic),
		langDrop:  newDropdown("Translate to", langDevices, &state.selectedLang),
	}
	a.txList.Axis = layout.Vertical

	setSingleLine(&a.serverEdit, cfg.ServerHost)
	setSingleLine(&a.ollamaEdit, cfg.OllamaModel)
	setSingleLine(&a.ttsModelEdit, cfg.TTSModel)
	a.voiceBtn.Value = cfg.VoiceEnabled
	// Локальный Whisper включён по умолчанию: whisper.cpp на 127.0.0.1:8080.
	a.localWhisper.Value = true

	return a
}

// whisperDisplayURL — актуальный адрес Whisper с учётом локального режима.
func (a *App) whisperDisplayURL() string {
	if a.localWhisper.Value {
		return "127.0.0.1:8080 (local)"
	}
	return a.cfg.WhisperURL()
}

func setSingleLine(ed *widget.Editor, text string) {
	ed.SingleLine = true
	ed.Submit = true
	ed.SetText(text)
}

func findDeviceIndex(devices []string, name string) int {
	for i, d := range devices {
		if d == name {
			return i
		}
	}
	return 0
}

func findLangIndex(langs []string, name string) int {
	for i, l := range langs {
		if l == name {
			return i
		}
	}
	return 0
}

func enumerateInputDevices() []string {
	devices, err := audio.ListDevices()
	if err != nil {
		log.Printf("[ui] list devices: %v", err)
		return []string{"default"}
	}
	var mics []string
	for _, d := range devices {
		if d.MaxInputs > 0 {
			mics = append(mics, d.Name)
		}
	}
	if len(mics) == 0 {
		return []string{"default"}
	}
	return mics
}

// ─── Event Loop ────────────────────────────────────────────────────

func (a *App) run(w *app.Window) {
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			if a.pipe != nil {
				a.pipe.Stop()
			}
			os.Exit(0)
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.handleClicks(gtx)
			a.pollStatus()
			a.layout(gtx)
			// Пока идёт запись — перерисовываем непрерывно, чтобы живые
			// обновления статуса и транскрипта появлялись без движения мыши.
			if a.pipe != nil && a.pipe.IsRunning() {
				w.Invalidate()
			}
			e.Frame(&ops)
		}
	}
}

// ─── Clicks ────────────────────────────────────────────────────────

func (a *App) handleClicks(gtx layout.Context) {
	if a.startBtn.Clicked(gtx) {
		a.toggleRecording()
		a.micDrop.open = false
		a.langDrop.open = false
	}

	if a.configBtn.Clicked(gtx) {
		a.micDrop.open = false
		a.langDrop.open = false
		a.state.mu.Lock()
		a.state.showConfig = !a.state.showConfig
		if a.state.showConfig {
			a.serverEdit.SetText(a.cfg.ServerHost)
			a.ollamaEdit.SetText(a.cfg.OllamaModel)
			a.ttsModelEdit.SetText(a.cfg.TTSModel)
		}
		a.state.mu.Unlock()
	}

	a.micDrop.update(gtx)
	a.langDrop.update(gtx)

	if a.saveBtn.Clicked(gtx) {
		a.applyConfig()
	}
}

// ─── Pipeline ──────────────────────────────────────────────────────

func (a *App) toggleRecording() {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()

	if a.state.recording {
		if a.pipe != nil {
			a.pipe.Stop()
			a.pipe = nil
		}
		a.state.recording = false
		a.state.status = "Stopped"
		a.state.subtitle = "Press Start"
		return
	}

	mic := a.cfg.MicDevice
	if a.state.selectedMic >= 0 && a.state.selectedMic < len(a.state.micDevices) {
		mic = a.state.micDevices[a.state.selectedMic]
	}
	lang := a.cfg.TargetLang
	if a.state.selectedLang >= 0 && a.state.selectedLang < len(a.state.langDevices) {
		lang = a.state.langDevices[a.state.selectedLang]
	}

	// Локальный whisper.cpp (whisper-server на 127.0.0.1:8080) — туннель
	// его не трогает (isLocalURL). Остальные сервисы — через туннель.
	whisperURL := a.cfg.WhisperURL()
	if a.localWhisper.Value {
		whisperURL = "http://127.0.0.1:8080"
	}

	cfg := pipeline.Config{
		WhisperURL:  whisperURL,
		OllamaURL:   a.cfg.OllamaURL(),
		OllamaModel: a.cfg.OllamaModel,
		TTSEndpoint: a.cfg.TTSEndpoint(),
		TTSModel:    a.cfg.TTSModel,
		MicDevice:   mic,
		VirtualMic:  a.cfg.VirtualMic,
		TargetLang:  lang,
		SourceLang:  a.cfg.SourceLang,
		VoiceEnabled: a.voiceBtn.Value,
		LocalWhisper: a.localWhisper.Value,

		// Сервисы на сервере закрыты файрволом → доступ только через SSH-туннель.
		SSHHost:    a.cfg.ServerHost,
		SSHUser:    a.cfg.SSHUser,
		SSHPort:    a.cfg.SSHPort,
		SSHKeyPath: a.cfg.SSHKeyPath,
	}

	p := pipeline.New(cfg)
	if err := p.Start(); err != nil {
		log.Printf("[ui] start error: %v", err)
		a.state.status = "Error"
		a.state.subtitle = err.Error()
		return
	}

	a.pipe = p
	a.state.recording = true
	a.state.status = "Starting..."
	a.state.subtitle = "Connecting to " + a.cfg.ServerHost + "..."
}

// ─── Status ────────────────────────────────────────────────────────

func (a *App) pollStatus() {
	if a.pipe == nil {
		return
	}
	for {
		select {
		case update := <-a.pipe.StatusChan():
			a.applyStatus(update)
		default:
			return
		}
	}
}

// applyStatus применяет одно событие пайплайна к состоянию UI.
func (a *App) applyStatus(update pipeline.StatusUpdate) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()

	switch update.Stage {
	case "listening":
		a.state.status = "Listening"
		a.state.subtitle = update.Message
	case "transcribing":
		a.state.status = "Transcribing"
		a.state.subtitle = update.Message
	case "transcribed":
		src := update.Text
		if src == "" {
			src = update.Message
		}
		if src == "" {
			break
		}
		a.state.transcript = append(a.state.transcript, TranscriptLine{Source: src})
		a.state.status = "Transcribed"
		a.state.subtitle = src
		log.Printf("[ui] transcript %d: +source %q", len(a.state.transcript), src)
		if len(a.state.transcript) > 40 {
			a.state.transcript = a.state.transcript[len(a.state.transcript)-40:]
		}
	case "translating":
		a.state.status = "Translating"
		a.state.subtitle = update.Message
	case "translated":
		tgt := update.Text
		if tgt == "" {
			tgt = update.Message
		}
		if tgt == "" {
			break
		}
		// Перевод привязываем к последней распознанной фразе.
		if n := len(a.state.transcript); n > 0 {
			a.state.transcript[n-1].Target = tgt
		} else {
			a.state.transcript = append(a.state.transcript, TranscriptLine{Target: tgt})
		}
		a.state.status = "Translated"
		a.state.subtitle = tgt
		log.Printf("[ui] transcript %d: +target %q", len(a.state.transcript), tgt)
		if len(a.state.transcript) > 40 {
			a.state.transcript = a.state.transcript[len(a.state.transcript)-40:]
		}
	case "synthesizing":
		a.state.status = "Synthesizing"
		a.state.subtitle = update.Message
	case "playing":
		a.state.status = "Playing"
		a.state.subtitle = update.Message
	case "error":
		a.state.status = "Error"
		a.state.subtitle = update.Message
	case "idle":
		a.state.status = "Idle"
		a.state.subtitle = "Press Start"
	default:
		a.state.subtitle = update.Message
	}
}

// ─── Config ────────────────────────────────────────────────────────

func (a *App) applyConfig() {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()

	a.cfg.ServerHost = trimOr(a.serverEdit.Text(), "10.0.0.26")
	a.cfg.OllamaModel = trimOr(a.ollamaEdit.Text(), "llama3")
	a.cfg.TTSModel = trimOr(a.ttsModelEdit.Text(), "f5-tts")
	a.cfg.TargetLang = a.state.langDevices[a.state.selectedLang]

	_ = config.Save(a.cfg)

	a.state.showConfig = false
	a.state.status = "Config saved"
	a.state.subtitle = a.cfg.ServerHost
}

func trimOr(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}
