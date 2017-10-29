/* color.go
 *
 * This file is part of cfilter
 * Copyright (C) 2017  Alexey Gladkov <gladkov.alexey@gmail.com>
 *
 * This file is covered by the GNU General Public License,
 * which should be included with cfilter as the file COPYING.
 */
package main

import (
	"regexp"
	"strings"
)

const (
	AnsiStart       = "\033["
	AnsiReset       = "\033[0m"
	bold            = 1
	blink           = 5
	underline       = 4
	inverse         = 7
	backgroundColor = 10
	revertProp      = 20
	brightColor     = 60

	ResetForeground = 39
	ResetBackground = 49

	ForegroundColor   = "foreground"
	BackgroundColor   = "background"
	BoldProperty      = "bold"
	BlinkProperty     = "blink"
	UnderlineProperty = "underline"
	InverseProperty   = "inverse"
)

const (
	Black int = (30 + iota)
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
)

var AnsiColors = map[string]int{
	"black":   Black,
	"red":     Red,
	"green":   Green,
	"yellow":  Yellow,
	"blue":    Blue,
	"magenta": Magenta,
	"cyan":    Cyan,
	"white":   White,
}

var AnsiProperties = map[string]int{
	BoldProperty:      bold,
	BlinkProperty:     blink,
	UnderlineProperty: underline,
	InverseProperty:   inverse,
}

type Colorize map[string]int

func ParseColorize(spec string) Colorize {
	c := make(Colorize)
	colorAddon := 0
	colorType := "foreground"

	for _, word := range regexp.MustCompile("[ \t]+").Split(spec, -1) {
		word = strings.ToLower(word)
		switch word {
		case "bg", "background":
			colorType = BackgroundColor
			c[colorType] = 0
			colorAddon = backgroundColor
		case "fg", "foreground":
			colorType = ForegroundColor
			c[colorType] = 0
			colorAddon = 0
		case "bright":
			colorAddon += brightColor
		default:
			if v, ok := AnsiColors[word]; ok {
				c[colorType] = colorAddon + v
				continue
			}
			if v, ok := AnsiProperties[word]; ok {
				c[word] = v
				continue
			}
			if len(word) > 0 {
				panic("unknown keyword: " + word)
			}
		}
	}
	return c
}

func Property(name string, isset bool) int {
	v, ok := AnsiProperties[name]
	if !ok {
		return 0
	}
	if !isset {
		if name == "bold" {
			v = 2
		}
		v += revertProp
	}
	return v
}
