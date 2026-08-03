package pipeline

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ddouhushau/go-transcoder/internal/audio"
	"github.com/ddouhushau/go-transcoder/internal/services"
	"github.com/ddouhushau/go-transcoder/internal/tunnel"
)

// Config — URLs к сервисам и опции подключения.
type Config struct {
	WhisperURL  string // http://10.0.0.26:8000
	OllamaURL   string // http://10.0.0.26:11434
	OllamaModel string
	TTSEndpoint string // http://10.0.0.26:5002
	TTSModel    string
	MicDevice   string
	VirtualMic  string
	TargetLang  string
	SourceLang  string
	VoiceEnabled bool // false — только субтитры, без озвучки
	LocalWhisper bool // true — whisper.cpp на 127.0.0.1 (endpoint /inference)

	// Fish Speech (клон голоса). Если FishVoiceID задан — TTS идёт через Fish,
	// а не через F5. FishBaseURL обычно локальный туннель 127.0.0.1:18080.
	FishBaseURL string
	FishAPIKey  string
	FishVoiceID string

	// SSH-туннель. Если SSHHost непустой — сервисы доступны только
	// через туннель (порты на сервере закрыты файрволом), поэтому
	// URL переписываются на 127.0.0.1:порт.
	SSHHost    string
	SSHUser    string
	SSHPort    int
	SSHKeyPath string
}

type StatusUpdate struct {
	Stage   string
	Message string
	Text    string
}

type Pipeline struct {
	cfg      Config
	capturer *audio.Capturer
	player   *audio.Player
	vad      *audio.VAD
	whisper  *services.WhisperClient
	ollama   *services.OllamaClient
	tts      *services.TTSClient
	fish     *services.FishClient
	tunnel   *tunnel.SSHTunnel

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	statusCh chan StatusUpdate
	phraseCh chan []float32 // фразы на обработку (последовательно)

	// Контекст для перевода: последние распознанные фразы пользователя.
	// Передаются в Ollama, чтобы перевод учитывал предыдущие высказывания
	// (местоимения, тему) — иначе каждая фраза переводится изолированно.
	ctxMu        sync.Mutex
	recentSource []string
}

func New(cfg Config) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		vad:      audio.NewVAD(),
		statusCh: make(chan StatusUpdate, 100),
	}
}

func (p *Pipeline) StatusChan() <-chan StatusUpdate {
	return p.statusCh
}

func (p *Pipeline) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.stopCh = make(chan struct{})

	// SSH-туннель: пробрасываем локальные порты на сервисы сервера
	if err := p.openTunnel(); err != nil {
		return err
	}

	// Клиенты — подключение к сервисам
	// Локальный whisper.cpp использует /inference; удалённый (faster-whisper)
	// — OpenAI-compatible /v1/audio/transcriptions.
	endpoint := "/v1/audio/transcriptions"
	if p.cfg.LocalWhisper {
		endpoint = "/inference"
	}
	p.whisper = services.NewWhisperClientEndpoint(p.cfg.WhisperURL, endpoint)
	p.ollama = services.NewOllamaClient(p.cfg.OllamaURL, p.cfg.OllamaModel)
	if p.cfg.VoiceEnabled {
		if strings.TrimSpace(p.cfg.FishVoiceID) != "" {
			p.fish = services.NewFishClientCfg(services.FishClientConfig{
				BaseURL: p.cfg.FishBaseURL,
				APIKey:  p.cfg.FishAPIKey,
				VoiceID: p.cfg.FishVoiceID,
				SSHUser: p.cfg.SSHUser,
				SSHHost: p.cfg.SSHHost,
			})
			p.tts = nil
		} else {
			p.tts = services.NewTTSClient(p.cfg.TTSEndpoint, p.cfg.TTSModel)
			p.fish = nil
		}
	}

	if !p.whisper.HealthCheck() {
		msg := fmt.Sprintf("Whisper недоступен на %s (проверь SSH-туннель / сервис)", p.cfg.WhisperURL)
		log.Printf("[pipeline] %s", msg)
		p.sendStatus("error", msg)
		p.closeTunnel()
		return fmt.Errorf("%s", msg)
	}
	log.Printf("[pipeline] whisper OK at %s", p.cfg.WhisperURL)

	// Микрофон
	p.sendStatus("init", "Opening microphone...")
	capturer, err := audio.NewCapturer(p.cfg.MicDevice)
	if err != nil {
		log.Printf("[pipeline] capture error: %v", err)
		p.sendStatus("error", "Mic: "+err.Error())
		p.closeTunnel()
		return err
	}
	p.capturer = capturer

	// Плеер (колонки / виртуальный микр)
	player, err := audio.NewPlayer(p.cfg.VirtualMic)
	if err != nil {
		log.Printf("[pipeline] player error: %v", err)
		p.sendStatus("error", "Player: "+err.Error())
		p.capturer.Close()
		p.closeTunnel()
		return err
	}
	p.player = player

	if err := p.capturer.Start(); err != nil {
		log.Printf("[pipeline] capture start error: %v", err)
		p.sendStatus("error", "Capture: "+err.Error())
		p.closeTunnel()
		return err
	}
	if err := p.player.Start(); err != nil {
		log.Printf("[pipeline] player start error: %v", err)
		p.sendStatus("error", "Player: "+err.Error())
		p.capturer.Close()
		p.closeTunnel()
		return err
	}

	p.running = true
	p.phraseCh = make(chan []float32, 16)
	go p.phraseWorker()
	go p.processLoop()
	ttsInfo := p.cfg.TTSEndpoint
	if p.fish != nil {
		ttsInfo = fmt.Sprintf("fish:%s (%s)", p.cfg.FishVoiceID, p.cfg.FishBaseURL)
	}
	log.Printf("[pipeline] started — whisper=%s ollama=%s tts=%s", p.cfg.WhisperURL, p.cfg.OllamaURL, ttsInfo)
	return nil
}

// phraseWorker обрабатывает фразы строго по очереди — иначе параллельные
// goroutine переставляли переводы местами («перевод начинается с конца»).
func (p *Pipeline) phraseWorker() {
	for phrase := range p.phraseCh {
		p.processPhrase(phrase)
	}
}

func (p *Pipeline) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	close(p.stopCh)
	p.running = false

	if phrase := p.vad.Flush(); len(phrase) > 0 {
		select {
		case p.phraseCh <- phrase:
		default:
		}
	}
	if p.phraseCh != nil {
		close(p.phraseCh)
		p.phraseCh = nil
	}

	if p.capturer != nil {
		p.capturer.Stop()
		p.capturer.Close()
	}
	if p.player != nil {
		p.player.Stop()
		p.player.Close()
	}

	p.closeTunnel()

	p.sendStatus("idle", "Stopped")
	log.Printf("[pipeline] stopped")
}

func (p *Pipeline) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Pipeline) closeTunnel() {
	if p.tunnel != nil {
		p.tunnel.Close()
		p.tunnel = nil
	}
}

// ─── SSH Tunnel ────────────────────────────────────────────────────

// openTunnel поднимает SSH-туннель до сервера, если задан SSHHost.
// Порты сервисов на сервере закрыты файрволом, поэтому каждый сервис
// пробрасывается на 127.0.0.1:порт, а URL переписываются на localhost.
func (p *Pipeline) openTunnel() error {
	if p.cfg.SSHHost == "" {
		return nil
	}

	p.sendStatus("connecting", "Opening SSH tunnel to "+p.cfg.SSHHost+"...")
	log.Printf("[pipeline] opening SSH tunnel to %s ...", p.cfg.SSHHost)

	user := p.cfg.SSHUser
	if user == "" {
		user = "dd"
	}
	port := p.cfg.SSHPort
	if port == 0 {
		port = 22
	}
	keyPath := p.cfg.SSHKeyPath
	if keyPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"id_ed25519", "id_rsa"} {
				candidate := filepath.Join(home, ".ssh", name)
				if _, err := os.Stat(candidate); err == nil {
					keyPath = candidate
					break
				}
			}
		}
	}

	t, err := tunnel.NewSSHTunnel(tunnel.SSHConfig{
		Host:    p.cfg.SSHHost,
		User:    user,
		Port:    port,
		KeyPath: keyPath,
	})
	if err != nil {
		p.sendStatus("error", "SSH: "+err.Error())
		return err
	}
	p.tunnel = t

	// Пробрасываем каждый сервис: локальный порт → 127.0.0.1:порт на сервере.
	for _, u := range []*string{&p.cfg.WhisperURL, &p.cfg.OllamaURL, &p.cfg.TTSEndpoint} {
		if isLocalURL(*u) {
			continue // сервис уже на localhost — туннель не нужен
		}
		svcPort, err := urlPort(*u)
		if err != nil {
			p.tunnel.Close()
			p.tunnel = nil
			return fmt.Errorf("tunnel: %w", err)
		}
		local := fmt.Sprintf("127.0.0.1:%d", svcPort)
		if err := t.Forward(local, local); err != nil {
			p.tunnel.Close()
			p.tunnel = nil
			return err
		}
		*u = "http://" + local
	}

	log.Printf("[pipeline] tunnel ready — whisper=%s ollama=%s tts=%s",
		p.cfg.WhisperURL, p.cfg.OllamaURL, p.cfg.TTSEndpoint)
	return nil
}

// isLocalURL — true, если URL уже указывает на локальную машину.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == ""
}

// urlPort возвращает TCP-порт из URL.
func urlPort(raw string) (int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("bad url %q: %w", raw, err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0, fmt.Errorf("no port in url %q", raw)
	}
	return p, nil
}

// ─── Main Loop ─────────────────────────────────────────────────────

func (p *Pipeline) processLoop() {
	var lastListen time.Time
	for {
		select {
		case <-p.stopCh:
			return
		case chunk := <-p.capturer.Channel():
			// Капча всегда активна (асинхронно): пользователь может говорить,
			// пока играет перевод. Вычитаем эхо перевода из микрофонного сигнала
			// (AEC) — иначе Whisper будет слышать свой же перевод вместо речи.
			chunk = p.player.AEC().Cancel(chunk)
			phrase, speaking := p.vad.Process(chunk)
			// Не спамим Listening на каждый чанк — иначе канал забивается
			// и события transcribed/translated дропаются.
			if speaking && time.Since(lastListen) > 400*time.Millisecond {
				p.sendStatus("listening", "Listening...")
				lastListen = time.Now()
			}
			if phrase != nil {
				log.Printf("[pipeline] phrase ready: %d samples (%.1fs)", len(phrase), float64(len(phrase))/16000)
				// Неблокирующая отправка в worker; при переполнении очередь
				// уже занята — фразу пропускаем (лучше, чем блокировать капчу).
				select {
				case p.phraseCh <- phrase:
				default:
					log.Printf("[pipeline] phrase queue full, dropping %d samples", len(phrase))
				}
			}
		}
	}
}

func (p *Pipeline) processPhrase(phrase []float32) {
	start := time.Now()

	// 1. Whisper: аудио → текст (source language, e.g. Russian → ru)
	srcCode := langCode(p.cfg.SourceLang)
	if srcCode == "" {
		srcCode = "ru" // голос пользователя — русский
	}
	p.sendStatus("transcribing", "Transcribing ("+srcCode+")...")
	// Нормализуем амплитуду — тихий микрофонный сигнал (RMS ~0.015) заставляет
	// Whisper галлюцинировать. После нормализации peak ≈ 0.9.
	audio.NormalizeFloat32(phrase)

	wavData := audio.Float32ToWAV(phrase, 16000)
	// Диагностика: RMS и длительность фразы (без записи на диск).
	{
		var sum, peak float64
		for _, s := range phrase {
			v := float64(s)
			sum += v * v
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		rms := math.Sqrt(sum / float64(len(phrase)))
		log.Printf("[pipeline] phrase: %.1fs, RMS=%.4f, peak=%.4f, %d bytes → Whisper lang=%s",
			float64(len(phrase))/16000, rms, peak, len(wavData), srcCode)
	}

	text, err := p.whisper.Transcribe(wavData, srcCode)
	if err != nil {
		log.Printf("[pipeline] whisper error: %v", err)
		p.sendStatus("error", "Whisper: "+err.Error())
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		log.Printf("[pipeline] whisper: empty")
		return
	}
	// Фильтр галлюцинаций Whisper. На тихих/коротких отрезках модель
	// выдаёт заученные фразы («Субтитры сделал DimaTorzok», «Thanks for
	// watching» и т.п.). Такие куски — не речь пользователя, отбрасываем.
	if isWhisperHallucination(text) {
		log.Printf("[pipeline] whisper hallucination skipped: %q", text)
		return
	}
	log.Printf("[pipeline] whisper [%s]: %q", srcCode, text)
	p.sendText("transcribed", text)

	// 2. Ollama: перевод source → target (русский → English) с контекстом.
	// Последние фразы пользователя дают модели память о теме и местоимениях.
	p.sendStatus("translating", "Translating...")
	context := p.buildTranslationContext()
	translated, err := p.ollama.UnderstandFromContext(text, context, p.cfg.SourceLang, p.cfg.TargetLang)
	if err != nil {
		log.Printf("[pipeline] ollama error: %v", err)
		p.sendStatus("error", "Ollama: "+err.Error())
		translated = text
	}
	log.Printf("[pipeline] translated: %q", translated)
	p.sendText("translated", translated)

	// Добавляем фразу в контекст для следующих переводов.
	p.pushContext(text)

	// Озвучка опциональна: пока она выключена, работаем только с субтитрами
	// (распознавание + перевод), чтобы не бороться с эхом из колонок.
	if p.cfg.VoiceEnabled {
		p.sendStatus("synthesizing", "Synthesizing voice...")
		var audioData []byte
		if p.fish != nil {
			audioData, _, err = p.fish.SynthesizeWithVoice(translated, p.cfg.FishVoiceID)
		} else if p.tts != nil {
			audioData, err = p.tts.Synthesize(translated, langCode(p.cfg.TargetLang))
		} else {
			err = fmt.Errorf("нет TTS-клиента (выбери голос или F5)")
		}
		if err != nil {
			log.Printf("[pipeline] tts error: %v", err)
			p.sendStatus("error", "TTS: "+err.Error())
			return
		}
		log.Printf("[pipeline] tts: %d bytes audio", len(audioData))

		p.sendStatus("playing", "Playing...")
		if err := p.playAudio(audioData); err != nil {
			log.Printf("[pipeline] playback error: %v", err)
			p.sendStatus("error", "Playback: "+err.Error())
			return
		}
	}

	elapsed := time.Since(start)
	log.Printf("[pipeline] done in %v", elapsed.Round(time.Millisecond))
}

func (p *Pipeline) playAudio(data []byte) error {
	pcmData, sampleRate, err := audio.ParseWAV(data)
	if err != nil {
		return fmt.Errorf("parse wav: %w", err)
	}

	samples := make([]float32, len(pcmData)/2)
	for i := 0; i < len(pcmData)-1; i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcmData[i : i+2]))
		samples[i/2] = float32(sample) / 32768.0
	}

	if sampleRate != 24000 {
		samples = audio.ResampleFloat32(samples, sampleRate, 24000)
	}

	return p.player.PlayChunk(samples)
}

func (p *Pipeline) sendStatus(stage, message string) {
	p.emit(StatusUpdate{Stage: stage, Message: message}, false)
}

// sendText отправляет распознанный/переведённый текст в поле Text.
// Доставка обязательна — иначе в UI пустой Transcript.
func (p *Pipeline) sendText(stage, text string) {
	p.emit(StatusUpdate{Stage: stage, Message: text, Text: text}, true)
}

func (p *Pipeline) emit(u StatusUpdate, important bool) {
	select {
	case p.statusCh <- u:
		return
	default:
	}
	if !important {
		return
	}
	// Канал забит статусами — выкидываем старое и вставляем текст.
	select {
	case <-p.statusCh:
	default:
	}
	select {
	case p.statusCh <- u:
	default:
		log.Printf("[pipeline] status DROPPED: stage=%s text=%q", u.Stage, u.Text)
	}
}

// isWhisperHallucination отсекает известные фразы-призраки, которые Whisper
// выдаёт на тихих/коротких отрезках аудио (документированные галлюцинации).
func isWhisperHallucination(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if len([]rune(t)) < 3 {
		return true // «да», «ну», «гм» и прочие обрывки — не фразы
	}
	known := []string{
		// классика Whisper-галлюцинаций на тишине
		"субтитры сделал",
		"подписку оформил",
		"подписчик",
		"подписчики",
		"спасибо за просмотр",
		"продолжение следует",
		"thanks for watching",
		"please subscribe",
		"subscribe to",
		"here now",
		"the weather is nice",
		"your captions",
		"субтитры",
	}
	for _, k := range known {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// buildTranslationContext собирает последние распознанные фразы в строку
// контекста для промпта перевода.
func (p *Pipeline) buildTranslationContext() string {
	p.ctxMu.Lock()
	defer p.ctxMu.Unlock()
	if len(p.recentSource) == 0 {
		return ""
	}
	// Показываем все, кроме самой свежей (она и переводится сейчас).
	lines := p.recentSource[:len(p.recentSource)-1]
	out := make([]string, 0, len(lines))
	for _, s := range lines {
		out = append(out, "- "+s)
	}
	return strings.Join(out, "\n")
}

// pushContext запоминает распознанную фразу для следующих переводов.
// Окно ограничено (5 фраз), чтобы промпт не разрастался.
func (p *Pipeline) pushContext(text string) {
	p.ctxMu.Lock()
	defer p.ctxMu.Unlock()
	p.recentSource = append(p.recentSource, text)
	if len(p.recentSource) > 5 {
		p.recentSource = p.recentSource[len(p.recentSource)-5:]
	}
}

func langCode(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en", "english":
		return "en"
	case "de", "deutsch", "german":
		return "de"
	case "fr", "français", "francais", "french":
		return "fr"
	case "es", "español", "espanol", "spanish":
		return "es"
	case "pl", "polski", "polish":
		return "pl"
	case "ru", "russian", "русский", "рус":
		return "ru"
	default:
		// Не угадываем английский по умолчанию — пусть Whisper определит сам.
		return ""
	}
}
