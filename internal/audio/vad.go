package audio

import "math"

const (
	// Energy threshold для определения речи.
	// Тишина: ~0.001-0.01, речь: ~0.01-0.1+
	silenceThreshold = 0.015

	// Сколько тишины подряд (в чанках) считать концом фразы.
	// При 16kHz и 1024 samples/chunk, один чанк ≈ 64ms.
	// 8 чанков ≈ 0.5с — пауза, после которой фраза уходит в Whisper.
	silenceChunksForEnd = 6

	// Минимальная длина фразы в чанках (~0.5с).
	minPhraseChunks = 8

	// Максимальная длина фразы (~4с). Если говорят без паузы —
	// принудительно режем, иначе транскрипт никогда не появится.
	maxPhraseChunks = 64
)

// VAD — Voice Activity Detection на основе энергии сигнала.
type VAD struct {
	silenceCount int
	inSpeech     bool
	phraseBuffer [][]float32
}

func NewVAD() *VAD {
	return &VAD{}
}

// Process обрабатывает аудиочанк и возвращает:
// - phrase: готовая фраза (конец речи / принудительный срез), иначе nil
// - speaking: сейчас идёт речь
func (v *VAD) Process(chunk []float32) (phrase []float32, speaking bool) {
	energy := rmsEnergy(chunk)

	if energy > silenceThreshold {
		v.silenceCount = 0
		v.inSpeech = true
		v.phraseBuffer = append(v.phraseBuffer, chunk)

		// Без паузы фраза всё равно должна уходить на распознавание.
		if len(v.phraseBuffer) >= maxPhraseChunks {
			return v.collectPhrase(), true
		}
		return nil, true
	}

	if v.inSpeech {
		v.silenceCount++
		v.phraseBuffer = append(v.phraseBuffer, chunk)

		if v.silenceCount >= silenceChunksForEnd {
			return v.collectPhrase(), false
		}
		return nil, true
	}

	return nil, false
}

// Flush принудительно завершает текущую фразу (при остановке записи).
func (v *VAD) Flush() []float32 {
	if len(v.phraseBuffer) == 0 {
		return nil
	}
	return v.collectPhrase()
}

// Reset сбрасывает состояние VAD, отбрасывая накопленные чанки.
// Вызывается в момент старта/конца озвучки, чтобы эхо перевода
// не попало в следующую фразу.
func (v *VAD) Reset() {
	v.reset()
}

func (v *VAD) collectPhrase() []float32 {
	if len(v.phraseBuffer) < minPhraseChunks {
		v.reset()
		return nil
	}

	totalLen := 0
	for _, chunk := range v.phraseBuffer {
		totalLen += len(chunk)
	}

	result := make([]float32, 0, totalLen)
	for _, chunk := range v.phraseBuffer {
		result = append(result, chunk...)
	}

	v.reset()
	return result
}

func (v *VAD) reset() {
	v.silenceCount = 0
	v.inSpeech = false
	v.phraseBuffer = v.phraseBuffer[:0]
}

func rmsEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
