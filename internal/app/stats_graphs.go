// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/ui"
)

// histBounds are the upper bounds of the buckets of the histograms of
// times, on a log scale; the last bucket has none.
var histBounds = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute}

var histLabels = []string{"<10s", "10–30s", "30s–1m", "1–2m", "2–5m", "5–15m", "15m+"}

// histogram counts times in the buckets of histBounds.
func histogram(ds []time.Duration) []int {
	out := make([]int, len(histBounds)+1)
	for _, d := range ds {
		k, _ := slices.BinarySearch(histBounds, d+1)
		out[k]++
	}
	return out
}

// atOnce returns, for each of cols slices of the time from from to to, the
// most spans that overlapped in it.
func atOnce(spans [][2]time.Time, from, to time.Time, cols int) []int {
	out := make([]int, max(cols, 0))
	total := to.Sub(from)
	if total <= 0 {
		return out
	}
	for c := range out {
		a := from.Add(total * time.Duration(c) / time.Duration(cols))
		b := from.Add(total * time.Duration(c+1) / time.Duration(cols))
		var in [][2]time.Time
		for _, sp := range spans {
			if sp[0].Before(b) && sp[1].After(a) {
				in = append(in, [2]time.Time{maxTime(sp[0], a), minTime(sp[1], b)})
			}
		}
		// The most at once is reached at the start of one of them.
		for _, x := range in {
			n := 0
			for _, y := range in {
				if !y[0].After(x[0]) && y[1].After(x[0]) {
					n++
				}
			}
			out[c] = max(out[c], n)
		}
	}
	return out
}

// hourAxis is the line of hours over a grid of 24 hours, each wide cells,
// after indent cells of labels.
func hourAxis(indent, wide int) string {
	axis := []rune(strings.Repeat(" ", indent+24*wide))
	every := 3
	if wide == 1 {
		every = 6
	}
	for h := 0; h < 24; h += every {
		copy(axis[indent+h*wide:], []rune(fmt.Sprintf("%02d", h)))
	}
	return ui.Dim.Render(strings.TrimRight(string(axis), " "))
}

// hourWidth is how wide an hour is drawn in a grid w cells wide after
// indent cells of labels.
func hourWidth(indent, w int) int {
	if w >= indent+24*2 {
		return 2
	}
	return 1
}

// graphWhen draws the turns by day of the week and hour as a punchcard.
func graphWhen(sum summary, w int) []string {
	out := []string{section("when", w)}
	wide := hourWidth(6, w)
	most := 0
	for _, day := range sum.grid {
		most = max(most, slices.Max(day[:]))
	}
	out = append(out, hourAxis(6, wide))
	for d, name := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		var b strings.Builder
		b.WriteString("  " + name + " ")
		for _, n := range sum.grid[d] {
			mark := ui.Heat(n, most)
			if n == 0 {
				mark = ui.Dim.Render(mark)
			} else {
				mark = ui.Accent.Render(mark)
			}
			b.WriteString(mark + strings.Repeat(" ", wide-1))
		}
		out = append(out, b.String())
	}
	return out
}

// turnRows is how high the columns of the turns are.
const turnRows = 5

// dayColumns draws a column for each cell of the time shown, standing on its
// label, rows high: values are their heights, most the height of a full
// column, label the label left of a row, and style the style of the cell of
// column k whose bottom is below eighths up the column.
func dayColumns(sum summary, values []int, most, rows, w int, label func(row int) string, style func(k, below int) lipgloss.Style) []string {
	const eighths = " ▁▂▃▄▅▆▇█"
	parts := []rune(eighths)
	labelW := 0
	for row := range rows {
		labelW = max(labelW, ansi.StringWidth(label(row)))
	}
	n := len(values)
	// Each column is a cell narrower than its slot, for a gap.
	slot := min(max((w-labelW-4)/max(n, 1), 1), 6)
	colW := max(slot-1, 1)
	var out []string
	for row := range rows {
		var b strings.Builder
		b.WriteString(ui.Dim.Render(fmt.Sprintf(" %*s ┤ ", labelW, label(row))))
		below := (rows - 1 - row) * 8
		for k, v := range values {
			h := 0
			if v > 0 && most > 0 {
				h = max(min(v, most)*rows*8/most, 1)
			}
			fill := min(max(h-below, 0), 8)
			cell := strings.Repeat(string(parts[fill]), colW)
			if fill > 0 {
				cell = style(k, below).Render(cell)
			}
			b.WriteString(cell + strings.Repeat(" ", slot-colW))
		}
		out = append(out, b.String())
	}
	return append(out, columnLabels(sum, labelW+4, slot)...)
}

// graphTurns draws the turns of each day, or of each hour today, as a
// column: typed below, sent by cicoria above.
func graphTurns(sum summary, w int) []string {
	title := "turns per day"
	if sum.buckets == 24 {
		title = "turns per hour"
	}
	totals := make([]int, sum.buckets)
	for k := range totals {
		totals[k] = sum.typedBy[k] + sum.sentBy[k]
	}
	most := max(slices.Max(totals), 1)
	label := func(row int) string {
		switch row {
		case 0:
			return fmt.Sprint(most)
		case turnRows - 1:
			return "0"
		}
		return ""
	}
	style := func(k, below int) lipgloss.Style {
		// Eighths of the column that are typed; a cell across the line
		// between the two is drawn typed.
		typed := sum.typedBy[k] * turnRows * 8 / most
		if typed > below {
			return ui.Accent
		}
		return ui.Info
	}
	out := append([]string{section(title, w)}, dayColumns(sum, totals, most, turnRows, w, label, style)...)
	return append(out, "", "  "+ui.Accent.Render("██")+" typed by you  "+ui.Info.Render("██")+" sent by cicoria")
}

// columnLabels are the labels under the columns of graphTurns: the hours
// today; the weekday and the day of each day, or of each Monday when the
// columns are too narrow for every one.
func columnLabels(sum summary, indent, slot int) []string {
	top := []rune(strings.Repeat(" ", indent+sum.buckets*slot+6))
	bottom := slices.Clone(top)
	put := func(line []rune, k int, text string) {
		copy(line[indent+k*slot:], []rune(text))
	}
	for k := range sum.buckets {
		if sum.buckets == 24 {
			if k%max(6/slot, 1) == 0 {
				put(top, k, fmt.Sprintf("%02d", k))
			}
			continue
		}
		day := sum.from.AddDate(0, 0, k)
		if slot >= 4 || day.Weekday() == time.Monday || k == 0 {
			put(top, k, day.Format("Mon"))
			put(bottom, k, day.Format("2"))
		}
	}
	out := []string{ui.Dim.Render(strings.TrimRight(string(top), " "))}
	if sum.buckets != 24 {
		out = append(out, ui.Dim.Render(strings.TrimRight(string(bottom), " ")))
	}
	return out
}

// share is n out of all, in percent.
func share(n, all int) int {
	if all <= 0 {
		return 0
	}
	return (n*100 + all/2) / all
}

// graphStarts draws how the turns started, and how many were cut short, as
// bars of the whole.
func graphStarts(sum summary, w int) []string {
	out := []string{section("how turns start", w)}
	bw := max(w-4, 1)
	part := func(n int) int {
		if sum.turns == 0 || n == 0 {
			return 0
		}
		return max(n*bw/sum.turns, 1)
	}
	typed := part(sum.typed)
	sent := min(part(sum.sent), bw-typed)
	out = append(out,
		"  "+ui.Accent.Render(strings.Repeat("█", typed))+ui.Info.Render(strings.Repeat("█", sent)),
		"",
		fmt.Sprintf("  %s typed by you %d%%  %s sent by cicoria %d%%", ui.Accent.Render("█"), share(sum.typed, sum.turns), ui.Info.Render("█"), share(sum.sent, sum.turns)),
		"",
	)
	cut, failed := part(sum.interrupted), part(sum.failed)
	failed = min(failed, bw-cut)
	rest := max(bw-cut-failed, 0)
	return append(out,
		"  "+ui.Warn.Render(strings.Repeat("█", cut))+ui.Bad.Render(strings.Repeat("█", failed))+ui.Dim.Render(strings.Repeat("░", rest)),
		"",
		fmt.Sprintf("  %s interrupted %d%%  %s failed %d%%  %s done", ui.Warn.Render("█"), share(sum.interrupted, sum.turns), ui.Bad.Render("█"), share(sum.failed, sum.turns), ui.Dim.Render("░")),
	)
}

// graphLengths draws how long the turns took as a histogram.
func graphLengths(sum summary, w int) []string {
	title := "turn length"
	if len(sum.lengths) > 0 {
		title += " · median " + span(quantile(sum.lengths, 0.5))
	}
	out := []string{section(title, w)}
	counts := histogram(sum.lengths)
	most := slices.Max(counts)
	for k, n := range counts {
		out = append(out, fmt.Sprintf("  %-7s %s %s", histLabels[k], ui.Accent.Render(bar(n, most, w-16)), ui.Dim.Render(fmt.Sprint(n))))
	}
	return out
}

// graphWaits draws how long agents waited for you, on the left, and for
// their next message after a turn, on the right, as histograms back to back.
func graphWaits(sum summary, w int) []string {
	out := []string{section("waited on you ◀  ▶ idle after done", w)}
	waits, idle := histogram(sum.waits), histogram(sum.idle)
	most := max(slices.Max(waits), slices.Max(idle))
	half := max((w-12)/2, 1)
	for k := range waits {
		l, r := bar(waits[k], most, half), bar(idle[k], most, half)
		pad := strings.Repeat(" ", max(half-ansi.StringWidth(l), 0)+1)
		out = append(out, pad+ui.Warn.Render(l)+" "+ui.Dim.Render(fmt.Sprintf("%-6s", histLabels[k]))+" "+ui.Accent.Render(r))
	}
	var total time.Duration
	for _, d := range sum.waits {
		total += d
	}
	return append(out, ui.Dim.Render(fmt.Sprintf("  %d wait(s), %s in all · %d idle, median %s", len(sum.waits), span(total), len(sum.idle), span(quantile(sum.idle, 0.5)))))
}

// togetherValues returns the most agents that worked at once in each day,
// or in each hour today, up to togetherRows.
func togetherValues(sum summary) []int {
	y, mo, d := sum.from.Date()
	// Cell k starts at midnight plus k days, or at hour k of the clock today:
	// a day can have 23 or 25 hours, and an hour that does not exist is empty.
	at := func(k int) time.Time { return time.Date(y, mo, d+k, 0, 0, 0, 0, sum.from.Location()) }
	if sum.buckets == 24 {
		at = func(k int) time.Time { return time.Date(y, mo, d, k, 0, 0, 0, sum.from.Location()) }
	}
	values := make([]int, sum.buckets)
	for k := range values {
		values[k] = min(atOnce(sum.work, at(k), minTime(at(k+1), sum.now), 1)[0], togetherRows)
	}
	return values
}

// togetherRows is the most agents at once its columns tell apart.
const togetherRows = 4

// graphTogether draws the most agents that worked at once in each day, or
// in each hour today, as a column a row high for each agent.
func graphTogether(sum summary, w int) []string {
	title := "agents at once, the most in a day"
	if sum.buckets == 24 {
		title = "agents at once, the most in an hour"
	}
	values := togetherValues(sum)
	label := func(row int) string {
		if row == 0 {
			return fmt.Sprintf("%d+", togetherRows)
		}
		return fmt.Sprint(togetherRows - row)
	}
	style := func(_, below int) lipgloss.Style {
		// Each agent more is a row up, in a colour of its own.
		return []lipgloss.Style{ui.Accent, ui.Good, ui.Warn, ui.Bad}[min(below/8, togetherRows-1)]
	}
	return append([]string{section(title, w)}, dayColumns(sum, values, togetherRows, togetherRows, w, label, style)...)
}

// graphAgents compares the agents with a bar for each measure, scaled to
// the highest of any agent.
func graphAgents(sum summary, w int) []string {
	nameW := 5
	for _, a := range sum.agents {
		nameW = max(nameW, min(ansi.StringWidth(a.name), 16))
	}
	bw := min(max((w-nameW-4*10)/4, 1), 14)
	var mostTurns, mostAsks, mostCut int
	var mostLen time.Duration
	for _, a := range sum.agents {
		mostTurns = max(mostTurns, a.turns)
		mostLen = max(mostLen, quantile(a.lengths, 0.5))
		mostAsks = max(mostAsks, perTurn(a.asks, a.turns))
		mostCut = max(mostCut, share(a.interrupted, a.turns))
	}
	cell := func(b, v string) string { return ui.Fit(b+" "+ui.Dim.Render(v), bw+8) + "  " }
	head := "  " + ui.Fit("", nameW) + " " + cell("turns", "") + cell("median turn", "") + cell("asked you", "") + cell("interrupted", "")
	out := []string{ui.Dim.Render(ansi.Strip(head))}
	for _, a := range sum.agents {
		med := quantile(a.lengths, 0.5)
		row := "  " + ui.Fit(a.name, nameW) + " " +
			cell(ui.Accent.Render(bar(a.turns, mostTurns, bw)), fmt.Sprint(a.turns)) +
			cell(ui.Accent.Render(bar(int(med), int(mostLen), bw)), orDash(med)) +
			cell(ui.Warn.Render(bar(perTurn(a.asks, a.turns), mostAsks, bw)), fmt.Sprintf("%d", a.asks)) +
			cell(ui.Warn.Render(bar(share(a.interrupted, a.turns), mostCut, bw)), fmt.Sprintf("%d%%", share(a.interrupted, a.turns)))
		if a.name == notRecorded {
			row = ui.Dim.Render(ansi.Strip(row))
		}
		out = append(out, ui.Fit(row, w))
	}
	return out
}

// perTurn is n for each turn, in hundredths.
func perTurn(n, turns int) int {
	if turns <= 0 {
		return 0
	}
	return n * 100 / turns
}

// graphFiles draws the files changed most as bars.
func graphFiles(sum summary, w int) []string {
	most := 0
	pathW := 0
	for _, f := range sum.files {
		most = max(most, f.n)
		pathW = max(pathW, ansi.StringWidth(f.path))
	}
	pathW = min(pathW, max(w/2, 10))
	var out []string
	for _, f := range sum.files {
		out = append(out, ui.Fit("  "+ui.Fit(f.path, pathW)+" "+ui.Accent.Render(bar(f.n, most, w-pathW-10))+" "+ui.Dim.Render(fmt.Sprint(f.n)), w))
	}
	return out
}
