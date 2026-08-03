package audio

import "sync"

// EchoCanceller — компенсация акустического эха.
//
// Микрофон слышит перевод, который играет плеер через колонки (эхо).
// Мы знаем, какие сэмплы уходят на выход, поэтому вычитаем из микрофонного
// сигнала оценку эха ДО VAD — это позволяет распознавать речь пользователя
// даже во время озвучки перевода.
//
// Референс хранится на 16 кГц (сэмплы плеера ресемплируются перед
// AddReference). Задержка эхо-пути оценивается кросс-корреляцией;
// доминирующий прямой путь вычитается через блочный least-squares gain
// (сходится за один чанк), остаточная реверберация — коротким NLMS.
type EchoCanceller struct {
	mu     sync.Mutex
	ref    []float32 // кольцевой буфер сыгранных сэмплов (16 кГц)
	refPos int
	delay  int       // текущая оценка задержки эхо-пути, сэмплов
	taps   []float32 // NLMS-фильтр (реверберация)
	procCnt int
	learn  int       // фаза сходимости (адаптация всегда)
}

func NewEchoCanceller() *EchoCanceller {
	return &EchoCanceller{
		ref:    make([]float32, 3*16000), // 3 с истории
		delay:  1600,                     // ~100 мс (refPos опережает микрофон)
		taps:   make([]float32, 256),     // 16 мс реверберации
		learn:  16000,                    // 1 с без double-talk
	}
}

// AddReference — плеер сообщает, какие сэмплы ушли на устройство вывода.
func (e *EchoCanceller) AddReference(samples []float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range samples {
		e.ref[e.refPos] = s
		e.refPos = (e.refPos + 1) % len(e.ref)
	}
}

// Reset сбрасывает фильтр и оценку задержки (при старте записи).
func (e *EchoCanceller) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.taps {
		e.taps[i] = 0
	}
	e.delay = 1600
	e.learn = 16000
}

// estimateDelay ищет задержку, при которой референс коррелирует с микрофоном.
func (e *EchoCanceller) estimateDelay(mic []float32) {
	n := len(e.ref)
	win := len(mic)
	if win > 512 {
		win = 512
	}
	best := e.delay
	bestCorr := -1.0
	// Диапазон поиска: 25–250 мс (400–4000 сэмплов @16кГц).
	for d := 400; d < 4000; d += 8 {
		var corr, refE, micE float64
		for i := 0; i < win; i++ {
			ri := (e.refPos - d + i + n) % n
			m := float64(mic[i])
			r := float64(e.ref[ri])
			corr += m * r
			refE += r * r
			micE += m * m
		}
		if refE < 1e-9 || micE < 1e-9 {
			continue
		}
		c := corr / (refE + 1e-9)
		if c > bestCorr {
			bestCorr = c
			best = d
		}
	}
	e.delay = best
}

// Cancel вычитает оценку эха из микрофонного чанка. Возвращает новую копию.
func (e *EchoCanceller) Cancel(mic []float32) []float32 {
	e.mu.Lock()
	defer e.mu.Unlock()

	n := len(e.ref)
	tn := len(e.taps)

	// Переоценка задержки раз в ~1с.
	e.procCnt += len(mic)
	if e.procCnt > 16000 {
		e.procCnt = 0
		e.estimateDelay(mic)
	}

	// Индекс референса для mic[0] чанка.
	idx := (e.refPos - e.delay + n) % n

	// Блочная оценка усиления прямого пути эха (least squares) по чанку.
	var num, den float32
	{
		ri := idx
		for i := 0; i < len(mic); i++ {
			num += mic[i] * e.ref[ri]
			den += e.ref[ri] * e.ref[ri]
			ri++
			if ri >= n {
				ri = 0
			}
		}
	}
	var gain float32
	if den > 1e-6 {
		// Квадратный корень не нужен: это уже нормализованная оценка.
		gain = num / den
	}

	out := make([]float32, len(mic))
	ref := e.ref
	taps := e.taps

	// Энергия для NLMS (по окну реверберации).
	var energy float32
	{
		ri := idx
		for j := 0; j < tn; j++ {
			energy += ref[ri] * ref[ri]
			ri--
			if ri < 0 {
				ri += n
			}
		}
	}

	ri := idx
	for i := range mic {
		// Оценка эха: прямой путь (gain) + реверберация (NLMS).
		var rev float32
		rr := ri
		for j := 0; j < tn; j++ {
			rev += taps[j] * ref[rr]
			rr--
			if rr < 0 {
				rr += n
			}
		}
		est := gain*ref[ri] + rev
		err := mic[i] - est
		out[i] = err

		// Адаптация NLMS с double-talk. В фазе learn — всегда.
		micAbs := mic[i]
		if micAbs < 0 {
			micAbs = -micAbs
		}
		estAbs := est
		if estAbs < 0 {
			estAbs = -estAbs
		}
		if e.learn > 0 {
			e.learn--
		}
		if energy > 1e-4 && (e.learn > 0 || micAbs < 4*estAbs+0.05) {
			step := float32(0.1)
			if e.learn <= 0 {
				step = 0.02
			}
			mu := step / (energy + 1e-6)
			rr := ri
			for j := 0; j < tn; j++ {
				taps[j] += mu * err * ref[rr]
				rr--
				if rr < 0 {
					rr += n
				}
			}
		}

		ri++
		if ri >= n {
			ri = 0
		}
	}
	return out
}
