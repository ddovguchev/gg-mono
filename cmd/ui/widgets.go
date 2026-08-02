package main

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ─── Dropdown ──────────────────────────────────────────────────────

type dropdown struct {
	label    string
	options  []string
	selected *int
	open     bool
	header   widget.Clickable
	items    []widget.Clickable
}

func newDropdown(label string, options []string, selected *int) *dropdown {
	return &dropdown{
		label:    label,
		options:  options,
		selected: selected,
		items:    make([]widget.Clickable, len(options)),
	}
}

func (d *dropdown) value() string {
	if d.selected == nil || len(d.options) == 0 {
		return ""
	}
	i := *d.selected
	if i < 0 || i >= len(d.options) {
		return d.options[0]
	}
	return d.options[i]
}

func (d *dropdown) update(gtx layout.Context) bool {
	if len(d.items) != len(d.options) {
		d.items = make([]widget.Clickable, len(d.options))
	}
	if d.header.Clicked(gtx) {
		d.open = !d.open
	}
	if !d.open {
		return false
	}
	for i := range d.items {
		if d.items[i].Clicked(gtx) {
			*d.selected = i
			d.open = false
			return true
		}
	}
	return false
}

func (a *App) renderDropdown(gtx layout.Context, d *dropdown) layout.Dimensions {
	chevron := "▾"
	if d.open {
		chevron = "▴"
	}

	children := []layout.FlexChild{
		// Label
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(a.th, d.label)
				lbl.Color = muted
				return lbl.Layout(gtx)
			})
		}),
		// Trigger
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, &d.header, func(gtx layout.Context) layout.Dimensions {
				bg := bg1
				if d.open {
					bg = bg2
				}
				return card(gtx, bg, unit.Dp(8), layout.Inset{
					Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(12), Right: unit.Dp(12),
				}, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							txt := material.Body1(a.th, d.value())
							txt.Color = ink
							txt.MaxLines = 1
							return txt.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							hint := material.Body2(a.th, chevron)
							hint.Color = accent
							return hint.Layout(gtx)
						}),
					)
				})
			})
		}),
	}

	// Menu
	if d.open {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.renderDropdownMenu(gtx, d)
			})
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (a *App) renderDropdownMenu(gtx layout.Context, d *dropdown) layout.Dimensions {
	return card(gtx, bg2, unit.Dp(8), layout.Inset{
		Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(4), Right: unit.Dp(4),
	}, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(d.options))
		for i, opt := range d.options {
			i, opt := i, opt
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, &d.items[i], func(gtx layout.Context) layout.Dimensions {
					bg := color.NRGBA{}
					if i == *d.selected {
						bg = selBg
					} else if d.items[i].Hovered() {
						bg = bg1
					}
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							rr := gtx.Dp(unit.Dp(6))
							defer clip.RRect{
								Rect: image.Rectangle{Max: gtx.Constraints.Min},
								NE:   rr, NW: rr, SE: rr, SW: rr,
							}.Push(gtx.Ops).Pop()
							if bg.A != 0 {
								paint.Fill(gtx.Ops, bg)
							}
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{
								Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10),
							}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										txt := material.Body2(a.th, opt)
										txt.Color = ink
										txt.MaxLines = 1
										return txt.Layout(gtx)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if i != *d.selected {
											return layout.Dimensions{}
										}
										mark := material.Body2(a.th, "✓")
										mark.Color = accent
										return mark.Layout(gtx)
									}),
								)
							})
						},
					)
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// ─── Editor ────────────────────────────────────────────────────────

func (a *App) renderEditor(gtx layout.Context, label string, ed *widget.Editor) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(a.th, label)
				lbl.Color = muted
				return lbl.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return card(gtx, bg2, unit.Dp(6), layout.Inset{
				Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10),
			}, func(gtx layout.Context) layout.Dimensions {
				for {
					ev, ok := ed.Update(gtx)
					if !ok {
						break
					}
					if _, ok := ev.(widget.SubmitEvent); ok {
						a.applyConfig()
					}
				}
				edStyle := material.Editor(a.th, ed, label)
				edStyle.Color = ink
				edStyle.HintColor = muted
				return edStyle.Layout(gtx)
			})
		}),
	)
}

// ─── Card ──────────────────────────────────────────────────────────

func card(gtx layout.Context, bg color.NRGBA, radius unit.Dp, inset layout.Inset, w layout.Widget) layout.Dimensions {
	return widget.Border{
		Color:        border,
		CornerRadius: radius,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				rr := gtx.Dp(radius)
				defer clip.RRect{
					Rect: image.Rectangle{Max: gtx.Constraints.Min},
					NE:   rr, NW: rr, SE: rr, SW: rr,
				}.Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, bg)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				dims := inset.Layout(gtx, w)
				if dims.Size.X < gtx.Constraints.Min.X {
					dims.Size.X = gtx.Constraints.Min.X
				}
				if dims.Size.Y < gtx.Constraints.Min.Y {
					dims.Size.Y = gtx.Constraints.Min.Y
				}
				return dims
			},
		)
	})
}

// ─── Status Row ────────────────────────────────────────────────────

func statusRow(th *material.Theme, label, value string, btn *widget.Clickable) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			row := func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, label)
						lbl.Color = muted
						return lbl.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						val := material.Body2(th, value)
						if btn != nil {
							val.Color = accent
						} else {
							val.Color = ink
						}
						val.Alignment = text.End
						val.MaxLines = 1
						return val.Layout(gtx)
					}),
				)
			}
			if btn == nil {
				return row(gtx)
			}
			return material.Clickable(gtx, btn, row)
		})
	})
}
