package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/ddouhushau/go-transcoder/internal/config"
	"github.com/ddouhushau/go-transcoder/internal/services"
	"github.com/ddouhushau/go-transcoder/internal/tunnel"
)

type screen int

const (
	screenTranslate screen = iota
	screenVoices
	screenVoiceAdd
)

type voiceUI struct {
	models []services.FishModel
	busy   bool
	status string
	err    string

	// train
	titleEdit widget.Editor
	subEdit   widget.Editor
	filePath  string
	fileName  string

	// voices + test (merged)
	testText   widget.Editor
	activeID   string
	voiceIDs   []string
	selectBtns []widget.Clickable
	deleteBtns []widget.Clickable

	pickBtn    widget.Clickable
	createBtn  widget.Clickable
	refreshBtn widget.Clickable
	playBtn    widget.Clickable
	pasteBtn   widget.Clickable
	editArea   widget.Clickable
	wantFocus  bool
	pendingPaste bool
	list       widget.List
	mu         sync.Mutex
}

func (a *App) initVoiceUI() {
	a.voice = &voiceUI{
		status:   "Загрузи голоса",
		activeID: strings.TrimSpace(a.cfg.FishVoiceID),
	}
	setSingleLine(&a.voice.titleEdit, "")
	a.voice.subEdit.SingleLine = false
	a.voice.testText.SingleLine = false
	a.voice.testText.SetText("Привет! Это проверка моего нового голоса.")
	a.voice.list.Axis = layout.Vertical
}

func (a *App) ensureFish() error {
	if a.fish != nil {
		return nil
	}
	a.startFishTunnel()
	a.fish = services.NewFishClientCfg(services.FishClientConfig{
		BaseURL:    a.cfg.FishBaseURL,
		APIKey:     a.cfg.FishAPIKey,
		VoiceID:    a.cfg.FishVoiceID,
		RefsRemote: a.cfg.FishRefsRemote,
		SSHUser:    a.cfg.SSHUser,
		SSHHost:    a.cfg.ServerHost,
	})
	return nil
}

func (a *App) startFishTunnel() {
	if a.fishTun != nil {
		return
	}
	base := strings.TrimSpace(a.cfg.FishBaseURL)
	localPort := strings.TrimSpace(a.cfg.FishLocalPort)
	if localPort == "" {
		localPort = "18080"
	}
	want := strings.Contains(base, "127.0.0.1:"+localPort) || strings.Contains(base, "localhost:"+localPort)
	if !want || a.cfg.SSHUser == "" || a.cfg.ServerHost == "" {
		return
	}
	keyPath := a.cfg.SSHKeyPath
	if keyPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"id_ed25519", "id_rsa"} {
				p := filepath.Join(home, ".ssh", name)
				if _, err := os.Stat(p); err == nil {
					keyPath = p
					break
				}
			}
		}
	}
	tun, err := tunnel.NewSSHTunnel(tunnel.SSHConfig{
		Host:    a.cfg.ServerHost,
		User:    a.cfg.SSHUser,
		Port:    a.cfg.SSHPort,
		KeyPath: keyPath,
	})
	if err != nil {
		log.Printf("[ui] fish tunnel: %v", err)
		return
	}
	remotePort := a.cfg.FishRemotePort
	if remotePort == "" {
		remotePort = "8080"
	}
	if err := tun.Forward("127.0.0.1:"+localPort, "127.0.0.1:"+remotePort); err != nil {
		log.Printf("[ui] fish forward: %v", err)
		tun.Close()
		return
	}
	a.fishTun = tun
	log.Printf("[ui] fish tunnel 127.0.0.1:%s → %s:%s", localPort, a.cfg.ServerHost, remotePort)
}

func (a *App) setVoiceStatus(msg string, isErr bool) {
	a.voice.mu.Lock()
	defer a.voice.mu.Unlock()
	if isErr {
		a.voice.err = msg
		a.voice.status = ""
	} else {
		a.voice.err = ""
		a.voice.status = msg
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *App) setVoiceBusy(v bool) {
	a.voice.mu.Lock()
	a.voice.busy = v
	a.voice.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *App) syncVoiceButtons(n int) {
	if len(a.voice.selectBtns) != n {
		a.voice.selectBtns = make([]widget.Clickable, n)
	}
	if len(a.voice.deleteBtns) != n {
		a.voice.deleteBtns = make([]widget.Clickable, n)
	}
}

// syncVoiceDropdownLocked — обновить dropdown на вкладке Перевод.
// Вызывать при удержанном a.voice.mu.
func (a *App) syncVoiceDropdownLocked() {
	models := a.voice.models
	ids := make([]string, len(models))
	labels := make([]string, len(models))
	sel := 0
	for i, m := range models {
		ids[i] = m.ID
		title := m.Title
		if title == "" {
			title = m.ID
		}
		labels[i] = title
		if m.ID == a.voice.activeID {
			sel = i
		}
	}
	a.voice.voiceIDs = ids
	if a.voiceDrop == nil {
		return
	}
	if len(labels) == 0 {
		a.voiceDrop.setOptions([]string{"— нет голосов —"})
		a.state.selectedVoice = 0
		return
	}
	a.voiceDrop.setOptions(labels)
	a.state.selectedVoice = sel
}

func (a *App) applyVoiceDropdownSelection() {
	if a.voice == nil || a.voiceDrop == nil {
		return
	}
	a.voice.mu.Lock()
	ids := append([]string(nil), a.voice.voiceIDs...)
	a.voice.mu.Unlock()
	idx := a.state.selectedVoice
	if idx < 0 || idx >= len(ids) {
		return
	}
	a.setActiveVoice(ids[idx])
}

func (a *App) refreshVoicesAsync() {
	go func() {
		a.setVoiceBusy(true)
		a.setVoiceStatus("Загружаю голоса…", false)
		if err := a.ensureFish(); err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		models, err := a.fish.ListModels(50)
		a.voice.mu.Lock()
		if err != nil {
			a.voice.err = err.Error()
			a.voice.status = ""
		} else {
			a.voice.models = models
			a.voice.err = ""
			a.voice.status = fmt.Sprintf("Голосов: %d", len(models))
			a.syncVoiceButtons(len(models))
			// сохранить активный, если ещё есть в списке
			found := false
			for _, m := range models {
				if m.ID == a.voice.activeID {
					found = true
					break
				}
			}
			if !found {
				if len(models) > 0 {
					a.voice.activeID = models[0].ID
				} else {
					a.voice.activeID = ""
				}
			}
			if a.voice.activeID != "" && a.cfg.FishVoiceID != a.voice.activeID {
				a.cfg.FishVoiceID = a.voice.activeID
				_ = config.Save(a.cfg)
			}
			a.syncVoiceDropdownLocked()
		}
		a.voice.busy = false
		a.voice.mu.Unlock()
		if a.win != nil {
			a.win.Invalidate()
		}
	}()
}

func (a *App) setActiveVoice(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	a.voice.mu.Lock()
	a.voice.activeID = id
	a.voice.status = "Активный: " + id
	a.voice.err = ""
	for i, vid := range a.voice.voiceIDs {
		if vid == id {
			a.state.selectedVoice = i
			break
		}
	}
	a.voice.mu.Unlock()
	a.cfg.FishVoiceID = id
	_ = config.Save(a.cfg)
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *App) deleteVoiceAsync(id string) {
	go func() {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		a.voice.mu.Lock()
		busy := a.voice.busy
		a.voice.mu.Unlock()
		if busy {
			return
		}
		a.setVoiceBusy(true)
		a.setVoiceStatus("Удаляю «"+id+"»…", false)
		if err := a.ensureFish(); err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		if err := a.fish.DeleteModel(id); err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		a.voice.mu.Lock()
		if a.voice.activeID == id {
			a.voice.activeID = ""
			a.cfg.FishVoiceID = ""
			_ = config.Save(a.cfg)
		}
		a.voice.mu.Unlock()
		a.setVoiceBusy(false)
		a.setVoiceStatus("Удалено: "+id, false)
		a.refreshVoicesAsync()
	}()
}

func (a *App) pickVoiceFileAsync() {
	go func() {
		a.setVoiceStatus("Выбери запись голоса…", false)
		out, err := exec.Command("osascript", "-e",
			`POSIX path of (choose file with prompt "Выбери mp3 / wav / mov" of type {"public.audio", "public.movie", "com.apple.quicktime-movie", "public.mpeg-4"})`,
		).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" || strings.Contains(msg, "User canceled") {
				a.setVoiceStatus("Выбор файла отменён", false)
				return
			}
			a.setVoiceStatus("Не удалось открыть диалог: "+msg, true)
			return
		}
		path := strings.TrimSpace(string(out))
		a.voice.mu.Lock()
		a.voice.filePath = path
		a.voice.fileName = filepath.Base(path)
		a.voice.status = "Запись: " + a.voice.fileName
		a.voice.err = ""
		a.voice.mu.Unlock()
		if a.win != nil {
			a.win.Invalidate()
		}
	}()
}

func (a *App) createVoiceAsync() {
	go func() {
		a.voice.mu.Lock()
		title := strings.TrimSpace(a.voice.titleEdit.Text())
		transcript := strings.TrimSpace(a.voice.subEdit.Text())
		path := a.voice.filePath
		busy := a.voice.busy
		a.voice.mu.Unlock()
		if busy {
			return
		}
		if title == "" {
			a.setVoiceStatus("Введи имя модели", true)
			return
		}
		if transcript == "" {
			a.setVoiceStatus("Введи текст записи (субтитры)", true)
			return
		}
		if path == "" {
			a.setVoiceStatus("Скинь mov / mp3 запись голоса", true)
			return
		}

		a.setVoiceBusy(true)
		a.setVoiceStatus("Готовлю запись…", false)
		raw, err := os.ReadFile(path)
		if err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus("Не прочитать файл: "+err.Error(), true)
			return
		}
		audio, outName, err := prepareVoiceSample(raw, filepath.Base(path))
		if err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		if err := a.ensureFish(); err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		a.setVoiceStatus("Сохраняю модель на AI-хост…", false)
		model, err := a.fish.CreateModel(title, "", transcript, audio, outName)
		a.setVoiceBusy(false)
		if err != nil {
			a.setVoiceStatus(err.Error(), true)
			return
		}
		msg := "Модель «" + model.Title + "» готова"
		if model.State == "local-only" {
			msg += " (только локально — sync на AI не прошёл)"
		}
		a.setActiveVoice(model.ID)
		a.setVoiceStatus(msg, false)
		a.refreshVoicesAsync()
		a.state.mu.Lock()
		a.state.screen = screenVoices
		a.state.mu.Unlock()
		if a.win != nil {
			a.win.Invalidate()
		}
	}()
}

func (a *App) pasteTestTextAsync() {
	// применяем на UI-потоке в handleEditorHotkeys
	a.voice.pendingPaste = true
	a.voice.wantFocus = true
	if a.win != nil {
		a.win.Invalidate()
	}
}

// focusedVoiceEditor — какой редактор сейчас в фокусе (или целевой для голосов).
func (a *App) focusedVoiceEditor(gtx layout.Context) *widget.Editor {
	if a.voice == nil {
		return nil
	}
	candidates := []*widget.Editor{
		&a.voice.testText,
		&a.voice.subEdit,
		&a.voice.titleEdit,
	}
	for _, ed := range candidates {
		if gtx.Focused(ed) {
			return ed
		}
	}
	switch a.state.screen {
	case screenVoices:
		return &a.voice.testText
	case screenVoiceAdd:
		// если кликнули зону / ждём фокус — текст записи, иначе имя
		if a.voice.wantFocus {
			return &a.voice.subEdit
		}
		return &a.voice.titleEdit
	}
	return nil
}

func macPBCopy(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func macPBPaste() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// handleEditorHotkeys — Cmd+A/C/V/X через pbcopy/pbpaste.
// На русской раскладке macOS отдаёт «Ф/С/М/Ч» вместо A/C/V/X — учитываем оба.
func (a *App) handleEditorHotkeys(gtx layout.Context) {
	if a.voice == nil {
		return
	}
	if a.state.screen != screenVoices && a.state.screen != screenVoiceAdd {
		return
	}

	ed := a.focusedVoiceEditor(gtx)
	if a.voice.pendingPaste {
		a.voice.pendingPaste = false
		if ed == nil {
			ed = &a.voice.testText
		}
		a.applyPaste(gtx, ed)
	}

	// Пустой Name = любой клавиши с Cmd (иначе на RU-раскладке «V» не приходит).
	for {
		ev, ok := gtx.Event(key.Filter{
			Required: key.ModShortcut,
			Optional: key.ModShift,
		})
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		if ed == nil {
			continue
		}
		cmd := shortcutCmd(string(ke.Name))
		if cmd == "" {
			continue
		}
		gtx.Execute(key.FocusCmd{Tag: ed})
		switch cmd {
		case "A":
			ed.SetCaret(0, ed.Len())
			a.setVoiceStatus("Выделено всё", false)
		case "C":
			sel := ed.SelectedText()
			if sel == "" {
				sel = ed.Text()
			}
			if err := macPBCopy(sel); err != nil {
				a.setVoiceStatus("Копирование: "+err.Error(), true)
			} else {
				a.setVoiceStatus("Скопировано", false)
			}
		case "X":
			sel := ed.SelectedText()
			if sel == "" {
				continue
			}
			if err := macPBCopy(sel); err != nil {
				a.setVoiceStatus("Вырезание: "+err.Error(), true)
				continue
			}
			ed.Insert("")
			a.setVoiceStatus("Вырезано", false)
		case "V":
			a.applyPaste(gtx, ed)
		}
	}
}

func (a *App) applyPaste(gtx layout.Context, ed *widget.Editor) {
	if ed == nil {
		return
	}
	text, err := macPBPaste()
	if err != nil {
		a.setVoiceStatus("Буфер: "+err.Error(), true)
		return
	}
	if text == "" {
		a.setVoiceStatus("Буфер обмена пуст", true)
		return
	}
	gtx.Execute(key.FocusCmd{Tag: ed})
	ed.Insert(text)
	a.setVoiceStatus("Вставлено", false)
}

// shortcutCmd — латиница или русская раскладка (Ф/С/М/Ч).
func shortcutCmd(name string) string {
	switch strings.ToUpper(name) {
	case "A", "Ф":
		return "A"
	case "C", "С":
		return "C"
	case "V", "М":
		return "V"
	case "X", "Ч":
		return "X"
	default:
		return ""
	}
}

func (a *App) playVoiceAsync() {
	go func() {
		a.voice.mu.Lock()
		busy := a.voice.busy
		text := strings.TrimSpace(a.voice.testText.Text())
		voiceID := a.voice.activeID
		a.voice.mu.Unlock()
		if busy {
			return
		}
		if text == "" {
			a.setVoiceStatus("Введи текст", true)
			return
		}
		if voiceID == "" {
			a.setVoiceStatus("Выбери голос в списке", true)
			return
		}
		a.setVoiceBusy(true)
		a.setVoiceStatus("Синтезирую…", false)
		if err := a.ensureFish(); err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		audioData, _, err := a.fish.SynthesizeWithVoice(text, voiceID)
		if err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		tmp, err := os.CreateTemp("", "fish-tts-*.wav")
		if err != nil {
			a.setVoiceBusy(false)
			a.setVoiceStatus(err.Error(), true)
			return
		}
		tmpPath := tmp.Name()
		_, _ = tmp.Write(audioData)
		_ = tmp.Close()

		a.setVoiceStatus("Играет…", false)
		cmd := exec.Command("afplay", tmpPath)
		err = cmd.Run()
		_ = os.Remove(tmpPath)
		a.setVoiceBusy(false)
		if err != nil {
			a.setVoiceStatus("Воспроизведение: "+err.Error(), true)
			return
		}
		a.setVoiceStatus("Готово", false)
	}()
}

// prepareVoiceSample — mp3/wav как есть; mov/mp4 → wav через ffmpeg.
func prepareVoiceSample(data []byte, filename string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".wav", ".mp3", ".m4a", ".opus", ".ogg", ".flac", ".webm":
		return data, filename, nil
	case ".mov", ".mp4", ".m4v", ".avi", ".mkv":
	default:
		if ext == "" {
			return data, "sample.wav", nil
		}
		return data, filename, nil
	}

	tmpDir, err := os.MkdirTemp("", "fish-voice-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)
	inPath := filepath.Join(tmpDir, "input"+ext)
	outPath := filepath.Join(tmpDir, "sample.wav")
	if err := os.WriteFile(inPath, data, 0600); err != nil {
		return nil, "", err
	}
	cmd := exec.Command("ffmpeg", "-y", "-i", inPath, "-vn", "-acodec", "pcm_s16le", "-ar", "44100", "-ac", "1", outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, "", fmt.Errorf("не удалось извлечь аудио (ffmpeg): %s", msg)
	}
	wav, err := os.ReadFile(outPath)
	if err != nil {
		return nil, "", err
	}
	if len(wav) < 1000 {
		return nil, "", fmt.Errorf("из видео не извлеклось аудио")
	}
	return wav, "sample.wav", nil
}

func (a *App) handleVoiceClicks(gtx layout.Context) {
	if a.voice == nil {
		return
	}
	a.handleEditorHotkeys(gtx)

	a.voice.mu.Lock()
	busy := a.voice.busy
	n := len(a.voice.models)
	a.syncVoiceButtons(n)
	models := append([]services.FishModel(nil), a.voice.models...)
	a.voice.mu.Unlock()

	if a.voice.refreshBtn.Clicked(gtx) && !busy {
		a.refreshVoicesAsync()
	}
	if a.voice.pickBtn.Clicked(gtx) && !busy {
		a.pickVoiceFileAsync()
	}
	if a.voice.createBtn.Clicked(gtx) && !busy {
		a.createVoiceAsync()
	}
	if a.voice.playBtn.Clicked(gtx) && !busy {
		a.playVoiceAsync()
	}
	if a.voice.pasteBtn.Clicked(gtx) && !busy {
		a.pasteTestTextAsync()
	}
	if a.voice.editArea.Clicked(gtx) {
		a.voice.wantFocus = true
	}

	for i := range models {
		if i >= len(a.voice.selectBtns) || i >= len(a.voice.deleteBtns) {
			break
		}
		if a.voice.deleteBtns[i].Clicked(gtx) && !busy {
			a.deleteVoiceAsync(models[i].ID)
			continue
		}
		if a.voice.selectBtns[i].Clicked(gtx) && !busy {
			a.setActiveVoice(models[i].ID)
		}
	}
}

func (a *App) renderNav(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		a.navBtn(gtx, "Перевод", screenTranslate, a.navTranslate),
		a.navBtn(gtx, "Голоса", screenVoices, a.navVoices),
		a.navBtn(gtx, "Тренировка", screenVoiceAdd, a.navAdd),
	)
}

func (a *App) navBtn(_ layout.Context, label string, sc screen, btn *widget.Clickable) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		active := a.state.screen == sc
		b := material.Button(a.th, btn, label)
		b.CornerRadius = unit.Dp(8)
		b.TextSize = unit.Sp(13)
		b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
		if active {
			b.Background = accent
			b.Color = bg0
		} else {
			b.Background = bg2
			b.Color = ink
		}
		return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
	})
}

func (a *App) renderVoicesScreen(gtx layout.Context) layout.Dimensions {
	a.voice.mu.Lock()
	models := append([]services.FishModel(nil), a.voice.models...)
	activeID := a.voice.activeID
	status := a.voice.status
	errMsg := a.voice.err
	busy := a.voice.busy
	a.syncVoiceButtons(len(models))
	a.voice.mu.Unlock()

	msg := status
	col := muted
	if errMsg != "" {
		msg = errMsg
		col = errC
	}
	playLbl := "Play"
	if busy {
		playLbl = "…"
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					h := material.Body1(a.th, "Голоса")
					h.Color = ink
					return h.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := "Обновить"
					if busy {
						lbl = "…"
					}
					btn := material.Button(a.th, &a.voice.refreshBtn, lbl)
					btn.CornerRadius = unit.Dp(8)
					return btn.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.Caption(a.th, msg)
			t.Color = col
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(models) == 0 {
				h := material.Body2(a.th, "Пока нет голосов — открой «Тренировка».")
				h.Color = muted
				return h.Layout(gtx)
			}
			return a.voice.list.Layout(gtx, len(models), func(gtx layout.Context, i int) layout.Dimensions {
				m := models[i]
				title := m.Title
				if title == "" {
					title = m.ID
				}
				selected := m.ID == activeID
				bg := bg1
				if selected {
					bg = selBg
				}
				return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return card(gtx, bg, unit.Dp(8), layout.Inset{
						Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(4), Right: unit.Dp(4),
					}, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								btn := material.Button(a.th, &a.voice.selectBtns[i], title)
								btn.CornerRadius = unit.Dp(6)
								btn.Background = bg
								btn.Color = ink
								btn.Inset = layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(8)}
								if selected {
									btn.Color = accent
								}
								return btn.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								del := material.Button(a.th, &a.voice.deleteBtns[i], "✕")
								del.CornerRadius = unit.Dp(6)
								del.Background = bg2
								del.Color = errC
								del.Inset = layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(12), Right: unit.Dp(12)}
								return del.Layout(gtx)
							}),
						)
					})
				})
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Caption(a.th, "Текст для озвучки")
							lbl.Color = muted
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							btn := material.Button(a.th, &a.voice.pasteBtn, "Вставить")
							btn.CornerRadius = unit.Dp(6)
							btn.TextSize = unit.Sp(12)
							btn.Inset = layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}
							btn.Background = bg2
							btn.Color = ink
							return btn.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.renderTTSEditor(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			btn := material.Button(a.th, &a.voice.playBtn, playLbl)
			btn.CornerRadius = unit.Dp(8)
			btn.Background = accent
			btn.Color = bg0
			return btn.Layout(gtx)
		}),
	)
}

func (a *App) renderTTSEditor(gtx layout.Context) layout.Dimensions {
	height := gtx.Dp(unit.Dp(100))
	if a.voice.wantFocus {
		gtx.Execute(key.FocusCmd{Tag: &a.voice.testText})
		a.voice.wantFocus = false
	}
	return card(gtx, bg1, unit.Dp(8), layout.Inset{
		Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10),
	}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Min.Y = height
		if gtx.Constraints.Max.Y < height {
			gtx.Constraints.Max.Y = height
		} else if gtx.Constraints.Max.Y > height {
			gtx.Constraints.Max.Y = height
		}
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Constraints.Max.X, height)
				return a.voice.editArea.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: size}
				})
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				ed := material.Editor(a.th, &a.voice.testText, "Текст для озвучки…")
				ed.Color = ink
				ed.HintColor = muted
				return ed.Layout(gtx)
			}),
		)
	})
}

func (a *App) renderVoiceAddScreen(gtx layout.Context) layout.Dimensions {
	a.voice.mu.Lock()
	fileName := a.voice.fileName
	status := a.voice.status
	errMsg := a.voice.err
	busy := a.voice.busy
	a.voice.mu.Unlock()
	if fileName == "" {
		fileName = "запись не выбрана"
	}
	btnLabel := "Обучить / сохранить"
	if busy {
		btnLabel = "Сохраняю…"
	}
	msg := status
	col := muted
	if errMsg != "" {
		msg = errMsg
		col = errC
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			h := material.Body1(a.th, "Тренировка модели")
			h.Color = ink
			return h.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			c := material.Caption(a.th, "Имя + mov/mp3 + текст записи → клон на Fish Speech")
			c.Color = muted
			return c.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		editorField(a, "Имя модели", &a.voice.titleEdit),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						btn := material.Button(a.th, &a.voice.pickBtn, "Запись (mov/mp3)")
						btn.CornerRadius = unit.Dp(8)
						return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, btn.Layout)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						t := material.Body2(a.th, fileName)
						t.Color = muted
						t.MaxLines = 1
						return t.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Caption(a.th, "Текст записи")
						lbl.Color = muted
						return lbl.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return card(gtx, bg1, unit.Dp(8), layout.Inset{
							Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10),
						}, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(90))
							ed := material.Editor(a.th, &a.voice.subEdit, "Точный текст того, что сказано…")
							ed.Color = ink
							ed.HintColor = muted
							return ed.Layout(gtx)
						})
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			btn := material.Button(a.th, &a.voice.createBtn, btnLabel)
			btn.CornerRadius = unit.Dp(8)
			btn.Background = accent
			btn.Color = bg0
			return btn.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.Caption(a.th, msg)
			t.Color = col
			return t.Layout(gtx)
		}),
	)
}
