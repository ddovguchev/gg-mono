package main

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var (
	bg0   = color.NRGBA{R: 15, G: 20, B: 18, A: 255}
	bg1   = color.NRGBA{R: 28, G: 36, B: 32, A: 255}
	bg2   = color.NRGBA{R: 40, G: 52, B: 46, A: 255}
	ink   = color.NRGBA{R: 232, G: 240, B: 234, A: 255}
	muted = color.NRGBA{R: 157, G: 176, B: 164, A: 255}
	// speechWhite — ярко-белый: речь пользователя.
	speechWhite = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	// transGray — серый перевод над фразой.
	transGray = color.NRGBA{R: 140, G: 150, B: 145, A: 255}
	accent    = color.NRGBA{R: 62, G: 207, B: 142, A: 255}
	errC      = color.NRGBA{R: 220, G: 80, B: 80, A: 255}
	border    = color.NRGBA{R: 70, G: 90, B: 80, A: 255}
	selBg     = color.NRGBA{R: 48, G: 72, B: 58, A: 255}
)

func (a *App) layout(gtx layout.Context) layout.Dimensions {
	paint.FillShape(gtx.Ops, bg0, clip.Rect{Max: gtx.Constraints.Max}.Op())
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.renderHeader),
		layout.Flexed(1, a.renderBody),
	)
}

func (a *App) renderHeader(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(24), Left: unit.Dp(32), Right: unit.Dp(32)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			rigidPad(unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
				title := material.H3(a.th, "mono-go")
				title.Color = ink
				return title.Layout(gtx)
			}),
			rigidPad(unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
				a.state.mu.Lock()
				n := 0
				for _, ln := range a.state.transcript {
					if ln.Source != "" || ln.Target != "" {
						n++
					}
				}
				a.state.mu.Unlock()
				meta := material.Caption(a.th, "transcript lines: "+strconv.Itoa(n))
				meta.Color = accent
				return meta.Layout(gtx)
			}),
			rigidPad(unit.Dp(16), func(gtx layout.Context) layout.Dimensions {
				sub := material.Body1(a.th, "Real-time Voice Translator")
				sub.Color = muted
				return sub.Layout(gtx)
			}),
		)
	})
}

func (a *App) renderBody(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(32), Right: unit.Dp(32), Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(a.renderControls),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(a.renderStatus),
		}
		if a.state.showConfig {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(a.renderConfigPanel),
			)
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Flexed(1, a.renderTranscript),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func (a *App) renderControls(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		rigidPad(unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return a.renderDropdown(gtx, a.micDrop)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return a.renderDropdown(gtx, a.langDrop)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := "▶ Start"
						if a.state.recording {
							lbl = "■ Stop"
						}
						btn := material.Button(a.th, a.startBtn, lbl)
						btn.CornerRadius = unit.Dp(8)
						if a.state.recording {
							btn.Background = errC
						}
						return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, btn.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						btn := material.Button(a.th, a.configBtn, "⚙ Settings")
						btn.CornerRadius = unit.Dp(8)
						if a.state.showConfig {
							btn.Background = bg2
							btn.Color = ink
						}
						return btn.Layout(gtx)
					}),
				)
			})
		}),
	)
}

func (a *App) renderStatus(gtx layout.Context) layout.Dimensions {
	return card(gtx, bg1, unit.Dp(10), layout.Inset{
		Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(14), Right: unit.Dp(14),
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			statusRow(a.th, "Status", a.state.status, nil),
			statusRow(a.th, "Server", a.cfg.ServerHost, a.configBtn),
			statusRow(a.th, "Whisper", a.cfg.WhisperURL(), nil),
			statusRow(a.th, "Ollama", a.cfg.OllamaModel, nil),
			statusRow(a.th, "TTS", a.cfg.TTSEndpoint(), nil),
		)
	})
}

func (a *App) renderTranscript(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		rigidPad(unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(a.th, "Transcript")
			lbl.Color = muted
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return card(gtx, bg1, unit.Dp(10), layout.Inset{
				Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(14), Right: unit.Dp(14),
			}, a.layoutTranscriptBody)
		}),
	)
}

func (a *App) layoutTranscriptBody(gtx layout.Context) layout.Dimensions {
	a.state.mu.Lock()
	status := a.state.status
	subtitle := a.state.subtitle
	lines := make([]TranscriptLine, 0, len(a.state.transcript))
	for _, ln := range a.state.transcript {
		if ln.Source != "" || ln.Target != "" {
			lines = append(lines, ln)
		}
	}
	a.state.mu.Unlock()

	statusLine := status
	if subtitle != "" {
		statusLine += " · " + subtitle
	}

	header := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				txt := material.Body2(a.th, statusLine)
				txt.Color = muted
				return txt.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: border, Width: unit.Dp(0.5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: 1}}
				})
			})
		}),
	}

	if len(lines) == 0 {
		header = append(header, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			hint := material.Body1(a.th, "Speak, then pause — text appears here")
			hint.Color = muted
			return hint.Layout(gtx)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, header...)
	}

	// Простой список без List: надёжнее для Gio, текст всегда виден.
	children := make([]layout.FlexChild, 0, len(header)+len(lines))
	children = append(children, header...)
	for i := range lines {
		line := lines[i]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				parts := []layout.FlexChild{}
				// Перевод — серым над фразой.
				if line.Target != "" {
					tgt := line.Target
					parts = append(parts, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							txt := material.Body2(a.th, tgt)
							txt.Color = transGray
							return txt.Layout(gtx)
						})
					}))
				}
				// Речь — ярко-белым.
				if line.Source != "" {
					src := line.Source
					parts = append(parts, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						txt := material.H6(a.th, src)
						txt.Color = speechWhite
						return txt.Layout(gtx)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, parts...)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func rigidPad(dp unit.Dp, w layout.Widget) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: dp}.Layout(gtx, w)
	})
}
