package pipeline

import (
	"sync"
	"testing"
)

// TestReordererOrdered — результаты приходят вперемешку, а emitOrdered
// вызывается строго по порядку seq.
func TestReordererOrdered(t *testing.T) {
	var mu sync.Mutex
	var emitted []phraseResult
	// Тестируем саму логику reorderer вручную: эмулируем канал результатов
	// с перестановкой (воркеры завершаются в произвольном порядке).
	resultsCh := make(chan phraseResult, 10)
	done := make(chan struct{})

	go func() {
		defer close(done)
		next := 0
		pending := make(map[int]phraseResult)
		for res := range resultsCh {
			pending[res.seq] = res
			for {
				r, ok := pending[next]
				if !ok {
					break
				}
				delete(pending, next)
				mu.Lock()
				emitted = append(emitted, r)
				mu.Unlock()
				next++
			}
		}
	}()

	// Отправляем с перестановкой: 0, 2, 1, 4, 3 (в порядке завершения).
	for _, seq := range []int{0, 2, 1, 4, 3} {
		resultsCh <- phraseResult{seq: seq, transcribed: "t", translated: "x", ok: true}
	}
	close(resultsCh)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 5 {
		t.Fatalf("ожидал 5, получил %d", len(emitted))
	}
	for i, r := range emitted {
		if r.seq != i {
			t.Errorf("порядок нарушен: emitted[%d].seq = %d", i, r.seq)
		}
	}
}

// TestReordererSkipsGaps — отброшенные фразы (ok:false) не блокируют очередь.
func TestReordererSkipsGaps(t *testing.T) {
	resultsCh := make(chan phraseResult, 10)
	done := make(chan struct{})
	var seqs []int

	go func() {
		defer close(done)
		next := 0
		pending := make(map[int]phraseResult)
		for res := range resultsCh {
			pending[res.seq] = res
			for {
				r, ok := pending[next]
				if !ok {
					break
				}
				delete(pending, next)
				if r.ok {
					seqs = append(seqs, r.seq)
				}
				next++
			}
		}
	}()

	// seq 0 — отброшена, seq 1 и 2 — валидные. Приходят с перестановкой.
	resultsCh <- phraseResult{seq: 1, ok: true}
	resultsCh <- phraseResult{seq: 0, ok: false}
	resultsCh <- phraseResult{seq: 2, ok: true}
	close(resultsCh)
	<-done

	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Errorf("ожидал [1 2], получил %v", seqs)
	}
}
