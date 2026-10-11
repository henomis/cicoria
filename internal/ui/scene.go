package ui

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Scene is a short piece of ASCII art in motion. It draws the frame at the
// part p of its length, from 0 to 1, as height rows of width cells.
type Scene func(p float64, width, height int) []string

// canvas is a frame being drawn: a grid of characters, each with a style.
type canvas struct {
	w, h   int
	cells  []rune
	styles []*lipgloss.Style
}

func newCanvas(w, h int) *canvas {
	w, h = max(w, 0), max(h, 0)
	c := &canvas{w: w, h: h, cells: make([]rune, w*h), styles: make([]*lipgloss.Style, w*h)}
	for i := range c.cells {
		c.cells[i] = ' '
	}
	return c
}

// put draws one character; what falls outside the canvas is dropped.
func (c *canvas) put(x, y int, r rune, st *lipgloss.Style) {
	if x < 0 || x >= c.w || y < 0 || y >= c.h {
		return
	}
	c.cells[y*c.w+x], c.styles[y*c.w+x] = r, st
}

// draw puts a drawing with its upper left corner at x, y. Its spaces let
// show what is underneath.
func (c *canvas) draw(x, y int, art []string, st *lipgloss.Style) {
	for dy, line := range art {
		for dx, r := range []rune(line) {
			if r != ' ' {
				c.put(x+dx, y+dy, r, st)
			}
		}
	}
}

func (c *canvas) rows() []string {
	out := make([]string, c.h)
	for y := range out {
		var b strings.Builder
		for x := 0; x < c.w; {
			st := c.styles[y*c.w+x]
			end := x
			for end < c.w && c.styles[y*c.w+end] == st {
				end++
			}
			run := string(c.cells[y*c.w+x : y*c.w+end])
			if st != nil {
				run = st.Render(run)
			}
			b.WriteString(run)
			x = end
		}
		out[y] = b.String()
	}
	return out
}

// scatter gives a number that looks random but is always the same for the
// same place.
func scatter(x, y int) int {
	n := uint32(x)*73856093 ^ uint32(y)*19349663
	n ^= n >> 13
	n *= 0x5bd1e995
	return int((n ^ n>>15) % 1000)
}

var (
	rocket = []string{
		`   /\   `,
		`  /  \  `,
		`  |[]|  `,
		`  |  |  `,
		` /|  |\ `,
		`/_|__|_\`,
	}
	flames = [][]string{
		{`  )||(  `, `   \/   `},
		{`  (||)  `, `   ''   `},
	}
)

// Liftoff is a rocket leaving for the sky.
func Liftoff(p float64, width, height int) []string {
	c := newCanvas(width, height)

	// The rocket gathers speed, and the stars fall behind as fast.
	climb := int(math.Round(p * p * float64(height+2)))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			switch scatter(x, (y-climb/2)%height+height) {
			case 0, 1, 2, 3, 4, 5, 6:
				c.put(x, y, '.', &Dim)
			case 7, 8:
				c.put(x, y, '*', &Dim)
			}
		}
	}

	x := (width - len(rocket[0])) / 2
	pad := height - 1
	top := pad - len(rocket) - len(flames[0]) - climb
	c.draw(x-2, pad, []string{"============"}, &Dim)
	// The smoke stays where the rocket has been.
	for y := top + len(rocket) + len(flames[0]); y < pad; y++ {
		c.put(x+3+scatter(1, y)%2, y, []rune(".:'")[scatter(2, y)%3], &Dim)
	}
	c.draw(x, top, rocket, &Bold)
	c.draw(x, top+len(rocket), flames[int(p*16)%2], &Warn)
	return c.rows()
}

// Burst is something going up in a puff of sparks.
func Burst(p float64, width, height int) []string {
	c := newCanvas(width, height)
	cx, cy := width/2, height/2

	spark, st := '#', &Warn
	switch {
	case p > 0.75:
		spark, st = '.', &Dim
	case p > 0.5:
		spark, st = '+', &Bad
	case p > 0.25:
		spark, st = '*', &Bad
	}
	// Settle slowly, in rings that travel at different speeds.
	reach := (1 - (1-p)*(1-p)) * float64(height/2+1)
	for ring, sparks := range []int{16, 11, 7} {
		r := reach * (1 - 0.3*float64(ring))
		for i := 0; i < sparks; i++ {
			a := 2*math.Pi*float64(i)/float64(sparks) + float64(ring)*0.4
			// A cell is about twice as tall as it is wide.
			x := cx + int(math.Round(math.Cos(a)*r*2.1))
			y := cy + int(math.Round(math.Sin(a)*r))
			c.put(x, y, spark, st)
		}
	}
	if p < 0.5 {
		c.draw(cx-3, cy, []string{"*poof*"}, &Bold)
	}
	return c.rows()
}

// plant is a chicory growing, one stage after the other: its flower, and
// the stem under it.
var plant = []struct{ flower, stem []string }{
	{nil, []string{
		`      .      `,
	}},
	{nil, []string{
		`      ,      `,
		`      |      `,
	}},
	{nil, []string{
		`      |/     `,
		`     \|      `,
		`      |      `,
	}},
	{[]string{
		`      o      `,
	}, []string{
		`      |/     `,
		`     \|      `,
		`      |      `,
	}},
	{[]string{
		`     \|/     `,
		`    -(@)-    `,
		`     /|\     `,
	}, []string{
		`      |/     `,
		`     \|      `,
		`      |      `,
	}},
	{[]string{
		`    \ | /    `,
		`  '. \|/ .'  `,
		` ---( @ )--- `,
		`  .' /|\ '.  `,
		`    / | \    `,
	}, []string{
		`      |/     `,
		`     \|      `,
		`      |      `,
	}},
}

// grown draws the plant at a stage of its growth, with words under it.
func grown(stage int, words string, width, height int) []string {
	c := newCanvas(width, height)
	const artW, tallest = 13, 8
	x := (width - artW) / 2
	// The ground stays where the full plant, with the words, is centred.
	ground := (height-tallest-3)/2 + tallest

	st := plant[max(min(stage, len(plant)-1), 0)]
	c.draw(x, ground-len(st.stem), st.stem, &Good)
	c.draw(x, ground-len(st.stem)-len(st.flower), st.flower, &Info)
	c.draw(x, ground, []string{strings.Repeat("~", artW)}, &Dim)
	c.draw((width-len(words))/2, ground+2, []string{words}, &Bold)
	return c.rows()
}

// Bloom is a chicory growing into flower, over the name of the program.
func Bloom(p float64, width, height int) []string {
	const name = "c i c o r i a"
	// The flower is out with a fifth of the time to go; the name is spelled
	// while it grows.
	stage := int(p * 1.25 * float64(len(plant)))
	letters := min(int(p*1.4*float64(len(name))), len(name))
	return grown(stage, name[:letters], width, height)
}

// Wilt is the chicory going back into the ground.
func Wilt(p float64, width, height int) []string {
	return grown(len(plant)-1-int(p*float64(len(plant))), "c i a o !", width, height)
}
