package audio

import (
	"math"
	"testing"
)

// TestEchoCanceller проверяет, что компенсатор убирает эхо и сохраняет голос.
//
// Реальный тайминг: refPos (позиция записи в плеере) синхронизирован с
// wall-time; микрофон слышит то, что играло physDelay назад. Значит
// задержка канселера = physDelay.
func TestEchoCanceller(t *testing.T) {
	const (
		sr       = 16000
		physDelay = 800 // 50 мс задержка эхо-пути
		echoGain = 0.6
	)

	// Референс — белый шум (непериодический, корреляция не подведёт).
	rngState := uint32(12345)
	nextRand := func() float32 {
		rngState = rngState*1664525 + 1013904223
		return (float32(int32(rngState)) / 2147483647)
	}

	const total = sr * 4
	ref := make([]float32, total)
	for i := range ref {
		ref[i] = nextRand()
	}

	aec := NewEchoCanceller()

	// Микрофон = эхо (задержанный референс) + голос (220 Гц) с 1.5с по 2.5с.
	// Первая секунда — чистый эхо (фаза сходимости фильтра).
	mic := make([]float32, total)
	for i := range mic {
		echo := float32(0)
		if i >= physDelay {
			echo = echoGain * ref[i-physDelay]
		}
		voice := float32(0)
		if i > 3*sr/2 && i < 5*sr/2 {
			voice = float32(0.4 * math.Sin(2*math.Pi*220*float64(i)/sr))
		}
		mic[i] = echo + voice
	}

	// Поток: референс записывается с опережением на physDelay (как реальный
	// плеер — он играет, а микрофон слышит с задержкой). Микрофонный чанк
	// [W:W+1024] слышит аудио, сыгранное physDelay назад.
	chunkSize := 1024
	out := make([]float32, 0, total)
	refPos := 0
	for wall := 0; wall < total; wall += chunkSize {
		// опережаем референс на physDelay от wall
		target := wall + physDelay
		for refPos < target && refPos < total {
			end := refPos + chunkSize
			if end > target {
				end = target
			}
			if end > total {
				end = total
			}
			aec.AddReference(ref[refPos:end])
			refPos = end
		}
		// компенсируем эхо на микрофонном чанке
		mEnd := wall + chunkSize
		if mEnd > total {
			mEnd = total
		}
		out = append(out, aec.Cancel(mic[wall:mEnd])...)
	}

	// Замеряем остаток эха в зоне без голоса после повторной сходимости (2.5-3.5с).
	echoRMS := 0.0
	for i := 5 * sr / 2; i < 7*sr/2; i++ {
		echoRMS += float64(out[i]) * float64(out[i])
	}
	echoRMS = math.Sqrt(echoRMS / float64(sr))

	// Справочно: RMS эха без подавления в той же зоне.
	rawEcho := 0.0
	for i := 5 * sr / 2; i < 7*sr/2; i++ {
		rawEcho += float64(mic[i]) * float64(mic[i])
	}
	rawEcho = math.Sqrt(rawEcho / float64(sr))

	// Голос в зоне с голосом (вторая половина голосового окна, 2-2.5с).
	voiceRMS := 0.0
	for i := 2 * sr; i < 5*sr/2; i++ {
		voiceRMS += float64(out[i]) * float64(out[i])
	}
	voiceRMS = math.Sqrt(voiceRMS / float64(sr/2))

	t.Logf("эхо: raw=%.4f → residual=%.4f (подавление %.1fx), voice RMS=%.4f",
		rawEcho, echoRMS, rawEcho/(echoRMS+1e-9), voiceRMS)

	// Эхо должно быть подавлено минимум в 3 раза.
	if echoRMS > rawEcho/3 {
		t.Errorf("эхо подавлено недостаточно: residual=%.4f raw=%.4f", echoRMS, rawEcho)
	}
	// Голос должен остаться заметным (синус 0.4 → RMS ~0.28).
	if voiceRMS < 0.1 {
		t.Errorf("голос потерян: RMS=%.4f", voiceRMS)
	}
}
