package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// The comet: a round, bright nucleus inside its coma on the right, and a tail
// that streams out to the left, fading from # to = ; : and scattered dust.
var cometArt = []string{
	`  *        .               .              *            .        .`,
	`         .  .  . . .....::::::::::-----.....________.--""--.`,
	`    .  .  . . ....:::::::::;;;;;;;;;======-----.._.'  .@@.  '.`,
	` .  .  . . ....::::::::;;;;;;;;;;===========#####(  @@@@@@@@  )`,
	`    .  .  . . ....:::::::::;;;;;;;;;======-----''"'.  '@@'  .'`,
	`         .  .  . . .....::::::::::-----'''''""""""""'--..--'`,
	`      .          *              .              .         *`,
}

// cometHead is the column where the coma starts on each line (-1: no coma).
var cometHead = []int{-1, 52, 50, 49, 50, 52, -1}

const (
	ansiReset      = "\033[0m"
	ansiBold       = "\033[1m"
	ansiDim        = "\033[2m"
	ansiNucleus    = "\033[1;93m" // bright yellow
	ansiComa       = "\033[1;97m" // bright white
	ansiTailHot    = "\033[33m"   // yellow
	ansiTailWarm   = "\033[96m"   // bright cyan
	ansiTailCool   = "\033[36m"   // cyan
	ansiTailFar    = "\033[34m"   // blue
	ansiTailStream = "\033[2;36m" // dim cyan
	ansiStar       = "\033[93m"   // yellow
)

func tailColor(c rune) string {
	switch c {
	case '#':
		return ansiTailHot
	case '=':
		return ansiTailWarm
	case ';':
		return ansiTailCool
	case ':':
		return ansiTailFar
	case '-', '_', '\'', '"':
		return ansiTailStream
	case '*':
		return ansiStar
	case '.':
		return ansiDim
	}
	return ""
}

func colorLine(line string, head int) string {
	var b strings.Builder
	for i, c := range line {
		var color string
		switch {
		case head >= 0 && i >= head && c == '@':
			color = ansiNucleus
		case head >= 0 && i >= head:
			color = ansiComa
		default:
			color = tailColor(c)
		}
		if color == "" || c == ' ' {
			b.WriteRune(c)
			continue
		}
		b.WriteString(color)
		b.WriteRune(c)
		b.WriteString(ansiReset)
	}
	return b.String()
}

func printBanner(w io.Writer, color bool) {
	fmt.Fprintln(w)
	for i, line := range cometArt {
		if color {
			line = colorLine(line, cometHead[i])
		}
		fmt.Fprintln(w, line)
	}
	title := fmt.Sprintf("            cometseed %s  -  stateless seed node for CometBFT networks", buildVersion)
	if color {
		title = ansiBold + title + ansiReset
	}
	fmt.Fprintln(w, title)
	fmt.Fprintln(w)
}

// isTerminal reports whether f is a character device (an interactive
// terminal), so colors are not written into journald or files.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
