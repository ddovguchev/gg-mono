package pipeline

import "testing"

func TestIsWhisperHallucination(t *testing.T) {
	halluc := []string{
		"Субтитры сделал DimaTorzok",
		"Подписку оформил канал",
		"Thanks for watching",
		"here now here now",
		"да", // обрывок
		"гм",
	}
	for _, h := range halluc {
		if !isWhisperHallucination(h) {
			t.Errorf("должно быть hallucination: %q", h)
		}
	}
	ok := []string{
		"Привет, меня зовут Дима, я DevOps инженер",
		"Мне двадцать три года",
		"Я работаю в IT",
	}
	for _, s := range ok {
		if isWhisperHallucination(s) {
			t.Errorf("не должно быть hallucination: %q", s)
		}
	}
}
