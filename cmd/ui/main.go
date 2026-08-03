package main

import (
	"log"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/ddouhushau/go-transcoder/internal/fonts"
)

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("mono-go"))
		w.Option(app.Size(unit.Dp(760), unit.Dp(720)))

		theme := newTheme()
		app := newApp(theme)

		log.Printf("[ui] starting mono-go")
		app.run(w)
	}()
	app.Main()
}

func newTheme() *material.Theme {
	collection := gofont.Collection()

	// Noto Sans — кириллица для русских субтитров (gofont её не имеет).
	if data, err := fonts.FS.ReadFile(fonts.NotoFileName); err == nil {
		if faces, err := opentype.ParseCollection(data); err == nil {
			collection = append(collection, faces...)
			log.Printf("[ui] loaded %d face(s) from %s", len(faces), fonts.NotoFileName)
		} else {
			log.Printf("[ui] font parse error: %v", err)
		}
	} else {
		log.Printf("[ui] font read error: %v", err)
	}

	shaper := text.NewShaper(text.WithCollection(collection))
	th := material.NewTheme()
	th.Shaper = shaper
	// Noto Sans — основной шрифт темы, чтобы кириллица рендерилась гарантированно.
	th.Face = "Noto Sans"
	return th
}
