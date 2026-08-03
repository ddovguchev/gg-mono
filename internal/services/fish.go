package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// FishClient — клиент локального Fish Speech (AI-хост), не облако fish.audio.
// Голоса: папки references/<id>/{sample.wav,sample.lab}.
// Ключ API не обязателен.
type FishClient struct {
	baseURL   string
	apiKey    string // опционально
	voiceID   string
	refsRemote string // e.g. /ai-volume/fish-speech/references
	sshUser   string
	sshHost   string
	voicesDir string // локальный кэш голосов
	client    *http.Client
}

type fishTTSReq struct {
	Text              string  `json:"text"`
	ReferenceID       string  `json:"reference_id,omitempty"`
	Format            string  `json:"format,omitempty"`
	Normalize         bool    `json:"normalize"`
	Streaming         bool    `json:"streaming"`
	Latency           string  `json:"latency,omitempty"`
	ChunkLength       int     `json:"chunk_length,omitempty"`
	MaxNewTokens      int     `json:"max_new_tokens,omitempty"`
	TopP              float64 `json:"top_p,omitempty"`
	Temperature       float64 `json:"temperature,omitempty"`
	RepetitionPenalty float64 `json:"repetition_penalty,omitempty"`
}

// FishModel — голосовой профиль (локальный reference).
type FishModel struct {
	ID          string `json:"_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	State       string `json:"state,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Type        string `json:"type,omitempty"`
	Transcript  string `json:"transcript,omitempty"`
}

type FishClientConfig struct {
	BaseURL    string
	APIKey     string // optional
	VoiceID    string
	RefsRemote string
	SSHUser    string
	SSHHost    string
	VoicesDir  string
}

var voiceIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

func NewFishClient(baseURL, apiKey, voiceID string) *FishClient {
	return NewFishClientCfg(FishClientConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		VoiceID: voiceID,
	})
}

func NewFishClientCfg(cfg FishClientConfig) *FishClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18080"
	}
	refs := strings.TrimSpace(cfg.RefsRemote)
	if refs == "" {
		refs = "/ai-volume/fish-speech/references"
	}
	voicesDir := strings.TrimSpace(cfg.VoicesDir)
	if voicesDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			voicesDir = filepath.Join(home, ".config", "mono-go", "voices")
		} else {
			voicesDir = "voices"
		}
	}
	_ = os.MkdirAll(voicesDir, 0755)

	return &FishClient{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		voiceID:    strings.TrimSpace(cfg.VoiceID),
		refsRemote: refs,
		sshUser:    strings.TrimSpace(cfg.SSHUser),
		sshHost:    strings.TrimSpace(cfg.SSHHost),
		voicesDir:  voicesDir,
		client:     &http.Client{Timeout: 180 * time.Second},
	}
}

func (f *FishClient) auth(req *http.Request) {
	if f.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+f.apiKey)
	}
}

// Synthesize — TTS голосом из конфига.
func (f *FishClient) Synthesize(text string) ([]byte, string, error) {
	return f.SynthesizeWithVoice(text, f.voiceID)
}

// SynthesizeWithVoice — локальный Fish Speech POST /v1/tts (JSON + reference_id).
func (f *FishClient) SynthesizeWithVoice(text, voiceID string) ([]byte, string, error) {
	text = prepareTTSText(text)
	if text == "" {
		return nil, "", fmt.Errorf("fish: text is required")
	}
	voiceID = strings.TrimSpace(voiceID)
	if voiceID == "" {
		voiceID = strings.TrimSpace(f.voiceID)
	}
	if voiceID == "" {
		return nil, "", fmt.Errorf("fish: voice id is required")
	}

	// Более стабильная просодия/ударения: ниже temperature, нормальный latency, chunk≥200.
	payload := fishTTSReq{
		Text:              text,
		ReferenceID:       voiceID,
		Format:            "wav",
		Normalize:         true,
		Streaming:         false,
		Latency:           "normal",
		ChunkLength:       300,
		MaxNewTokens:      1024,
		TopP:              0.7,
		Temperature:       0.5,
		RepetitionPenalty: 1.15,
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
	f.auth(req)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fish: request failed: %w (проверь что Fish Speech запущен и туннель жив)", err)
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
		contentType = "audio/wav"
	}
	// Чуть ускорить речь (без сильного искажения тона).
	if sped, err := speedUpAudio(audioData, 1.12); err == nil && len(sped) > 1000 {
		audioData = sped
		contentType = "audio/wav"
	}
	return audioData, contentType, nil
}

// speedUpAudio — ffmpeg atempo (1.0–2.0). pitch почти не меняется.
func speedUpAudio(wav []byte, tempo float64) ([]byte, error) {
	if tempo < 0.5 {
		tempo = 0.5
	}
	if tempo > 2.0 {
		tempo = 2.0
	}
	if math.Abs(tempo-1.0) < 0.01 {
		return wav, nil
	}
	tmpDir, err := os.MkdirTemp("", "fish-tempo-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	inPath := filepath.Join(tmpDir, "in.wav")
	outPath := filepath.Join(tmpDir, "out.wav")
	if err := os.WriteFile(inPath, wav, 0600); err != nil {
		return nil, err
	}
	cmd := exec.Command(
		"ffmpeg", "-y", "-i", inPath,
		"-filter:a", fmt.Sprintf("atempo=%.3f", tempo),
		"-acodec", "pcm_s16le",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("atempo: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(outPath)
}

// prepareTTSText — подчищает текст и усиливает паузы (Fish почти не слышит обычные запятые).
func prepareTTSText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\t", " ")

	// Типографские/китайские запятые → обычная
	text = strings.ReplaceAll(text, "，", ",")
	text = strings.ReplaceAll(text, "､", ",")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	var kept []string
	for _, line := range lines {
		if line != "" {
			kept = append(kept, line)
		}
	}
	text = strings.Join(kept, ". ")

	// Fish слабо реагирует на ",", зато хорошо на "..." — делаем паузу явной.
	text = regexp.MustCompile(`,\s*`).ReplaceAllString(text, "... ")
	text = regexp.MustCompile(`;\s*`).ReplaceAllString(text, "... ")
	text = regexp.MustCompile(`:\s*`).ReplaceAllString(text, "... ")
	// не раздувать уже стоящие многоточия
	text = regexp.MustCompile(`\.{4,}`).ReplaceAllString(text, "...")
	text = strings.ReplaceAll(text, ".. .", "...")
	text = strings.ReplaceAll(text, ". ...", ".")
	text = strings.ReplaceAll(text, "!...", "!")
	text = strings.ReplaceAll(text, "?...", "?")
	text = strings.ReplaceAll(text, "....", "...")

	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, "... ...", "...")

	if r, _ := utf8.DecodeLastRuneInString(text); r != '.' && r != '!' && r != '?' && r != '…' {
		text += "."
	}
	return text
}

// CreateModel сохраняет голос локально и синкает на AI-хост в references/<id>/.
func (f *FishClient) CreateModel(title, description, transcript string, audio []byte, filename string) (*FishModel, error) {
	title = strings.TrimSpace(title)
	transcript = strings.TrimSpace(transcript)
	if title == "" {
		return nil, fmt.Errorf("fish: title is required")
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("fish: audio is required")
	}
	if transcript == "" {
		return nil, fmt.Errorf("fish: transcript is required")
	}

	id := slugVoiceID(title)
	dir := filepath.Join(f.voicesDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	wavName := "sample.wav"
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".opus" || ext == ".flac" {
		wavName = "sample" + ext
	}
	wavPath := filepath.Join(dir, wavName)
	if err := os.WriteFile(wavPath, audio, 0644); err != nil {
		return nil, err
	}
	labPath := filepath.Join(dir, "sample.lab")
	if err := os.WriteFile(labPath, []byte(transcript), 0644); err != nil {
		return nil, err
	}

	meta := FishModel{
		ID:          id,
		Title:       title,
		Description: strings.TrimSpace(description),
		State:       "ready",
		Visibility:  "private",
		Type:        "tts",
		Transcript:  transcript,
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0644)

	if err := f.syncVoiceRemote(id, wavPath, transcript); err != nil {
		meta.State = "local-only"
		warn := "sync на AI-хост не удался: " + err.Error()
		if meta.Description != "" {
			meta.Description = meta.Description + " · " + warn
		} else {
			meta.Description = warn
		}
		raw, _ = json.MarshalIndent(meta, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0644)
		// Голос лежит локально; без sync reference_id на Fish не заработает, пока не починишь SSH.
		return &meta, nil
	}
	return &meta, nil
}

// DeleteModel удаляет голос локально и на AI-хосте (references/<id>).
func (f *FishClient) DeleteModel(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return fmt.Errorf("fish: invalid voice id")
	}
	localDir := filepath.Join(f.voicesDir, id)
	_ = os.RemoveAll(localDir)

	target := f.sshTarget()
	if target == "" {
		return nil
	}
	remoteDir := f.refsRemote + "/" + id
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", target,
		fmt.Sprintf("rm -rf %s", shellQuote(remoteDir)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remote delete: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListModels — локальные голоса + remote references (если SSH доступен).
func (f *FishClient) ListModels(pageSize int) ([]FishModel, error) {
	_ = pageSize
	byID := map[string]FishModel{}

	entries, _ := os.ReadDir(f.voicesDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		m := FishModel{ID: id, Title: id, State: "ready", Type: "tts", Visibility: "private"}
		if raw, err := os.ReadFile(filepath.Join(f.voicesDir, id, "meta.json")); err == nil {
			_ = json.Unmarshal(raw, &m)
		}
		if t, err := os.ReadFile(filepath.Join(f.voicesDir, id, "sample.lab")); err == nil {
			m.Transcript = strings.TrimSpace(string(t))
		}
		byID[id] = m
	}

	if remote, err := f.listRemoteRefs(); err == nil {
		for _, id := range remote {
			if _, ok := byID[id]; ok {
				continue
			}
			byID[id] = FishModel{
				ID:         id,
				Title:      id,
				State:      "ready",
				Type:       "tts",
				Visibility: "private",
			}
		}
	}

	out := make([]FishModel, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Title < out[j].Title || (out[i].Title == out[j].Title && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (f *FishClient) HealthCheck() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(f.baseURL + "/v1/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (f *FishClient) sshTarget() string {
	if f.sshUser == "" || f.sshHost == "" {
		return ""
	}
	return f.sshUser + "@" + f.sshHost
}

func (f *FishClient) listRemoteRefs() ([]string, error) {
	target := f.sshTarget()
	if target == "" {
		return nil, fmt.Errorf("ssh not configured")
	}
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target,
		fmt.Sprintf("ls -1 %s 2>/dev/null", shellQuote(f.refsRemote)))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(line)
		if id == "" || strings.HasPrefix(id, ".") {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *FishClient) syncVoiceRemote(id, wavPath, transcript string) error {
	target := f.sshTarget()
	if target == "" {
		return fmt.Errorf("ssh не настроен (ssh_user / server_host)")
	}
	remoteDir := f.refsRemote + "/" + id
	mkdir := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", target,
		fmt.Sprintf("mkdir -p %s", shellQuote(remoteDir)))
	if out, err := mkdir.CombinedOutput(); err != nil {
		return fmt.Errorf("mkdir: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	remoteWav := remoteDir + "/sample.wav"
	// Если файл не wav — всё равно кладём как sample.wav имя; Fish читает по расширению.
	// При mp3/m4a сохраняем с исходным именем sample.<ext>, плюс копируем lab.
	ext := strings.ToLower(filepath.Ext(wavPath))
	if ext == "" {
		ext = ".wav"
	}
	remoteWav = remoteDir + "/sample" + ext
	if ext != ".wav" && ext != ".mp3" && ext != ".m4a" && ext != ".opus" && ext != ".flac" {
		remoteWav = remoteDir + "/sample.wav"
	}

	scp := exec.Command("scp", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		wavPath, target+":"+remoteWav)
	if out, err := scp.CombinedOutput(); err != nil {
		return fmt.Errorf("scp: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// .lab рядом с sample.* — Fish ищет sample.lab
	labCmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", target,
		fmt.Sprintf("cat > %s", shellQuote(remoteDir+"/sample.lab")))
	labCmd.Stdin = strings.NewReader(transcript)
	if out, err := labCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lab: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func slugVoiceID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) < 2 {
		id = fmt.Sprintf("voice-%d", time.Now().Unix()%100000)
	}
	if len(id) > 24 {
		id = id[:24]
	}
	id = fmt.Sprintf("%s-%d", id, time.Now().Unix()%1000000)
	if !voiceIDRe.MatchString(id) {
		id = fmt.Sprintf("voice-%d", time.Now().Unix())
	}
	return id
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
