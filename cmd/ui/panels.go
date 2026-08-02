package main

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func (a *App) renderConfigPanel(gtx layout.Context) layout.Dimensions {
	return card(gtx, bg1, unit.Dp(10), layout.Inset{
		Top: unit.Dp(12), Bottom: unit.Dp(12), Left: unit.Dp(14), Right: unit.Dp(14),
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					h := material.Body1(a.th, "Settings")
					h.Color = ink
					return h.Layout(gtx)
				})
			}),
			editorField(a, "Server IP", &a.serverEdit),
			editorField(a, "Ollama Model", &a.ollamaEdit),
			editorField(a, "TTS Model", &a.ttsModelEdit),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					btn := material.Button(a.th, a.saveBtn, "Save")
					btn.CornerRadius = unit.Dp(8)
					return btn.Layout(gtx)
				})
			}),
		)
	})
}

func editorField(a *App, label string, ed *widget.Editor) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return a.renderEditor(gtx, label, ed)
		})
	})
}
