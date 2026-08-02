package main

import (
	"testing"

	"github.com/ddouhushau/go-transcoder/internal/pipeline"
)

// Проверяет, что applyStatus наполняет транскрипт из событий пайплайна.
func TestApplyStatusPopulatesTranscript(t *testing.T) {
	a := &App{state: &AppState{}}

	a.applyStatus(pipeline.StatusUpdate{Stage: "transcribed", Text: "Привет, это тест"})
	a.applyStatus(pipeline.StatusUpdate{Stage: "translated", Text: "Hello, this is a test"})

	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if len(a.state.transcript) != 1 {
		t.Fatalf("ожидал 1 строку, получил %d", len(a.state.transcript))
	}
	line := a.state.transcript[0]
	if line.Source != "Привет, это тест" {
		t.Errorf("Source = %q", line.Source)
	}
	if line.Target != "Hello, this is a test" {
		t.Errorf("Target = %q", line.Target)
	}
}

// Проверяет, что перевод, пришедший без распознавания, создаёт строку.
func TestApplyStatusTranslationOnly(t *testing.T) {
	a := &App{state: &AppState{}}
	a.applyStatus(pipeline.StatusUpdate{Stage: "translated", Text: "Hello only"})
	if n := len(a.state.transcript); n != 1 {
		t.Fatalf("ожидал 1 строку, получил %d", n)
	}
	if a.state.transcript[0].Target != "Hello only" {
		t.Errorf("Target = %q", a.state.transcript[0].Target)
	}
}

// Раньше пайплайн клал текст только в Message — UI должен это тоже понимать.
func TestApplyStatusFallsBackToMessage(t *testing.T) {
	a := &App{state: &AppState{}}
	a.applyStatus(pipeline.StatusUpdate{Stage: "transcribed", Message: "Речь из Message"})
	a.applyStatus(pipeline.StatusUpdate{Stage: "translated", Message: "Speech from Message"})
	if n := len(a.state.transcript); n != 1 {
		t.Fatalf("ожидал 1 строку, получил %d", n)
	}
	if a.state.transcript[0].Source != "Речь из Message" {
		t.Errorf("Source = %q", a.state.transcript[0].Source)
	}
	if a.state.transcript[0].Target != "Speech from Message" {
		t.Errorf("Target = %q", a.state.transcript[0].Target)
	}
}
