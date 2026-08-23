package tea

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// render implements renderer.
func (s *cursedRenderer) render(v View) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.view = v
}

// reset implements renderer.
func (s *cursedRenderer) reset() {
	s.mu.Lock()
	reset(s)
	s.mu.Unlock()
}

func reset(s *cursedRenderer) {
	s.buf.Reset()
	scr := uv.NewTerminalRenderer(&s.buf, s.env)
	scr.SetColorProfile(s.profile)
	scr.SetRelativeCursor(true) // Always start in inline mode
	scr.SetFullscreen(false)    // Always start in inline mode
	if s.hardTabs {
		scr.SetTabStops(s.width)
	} else {
		scr.SetTabStops(-1)
	}
	scr.SetBackspace(s.backspace)
	scr.SetMapNewline(s.mapnl)
	scr.SetScrollOptim(runtime.GOOS != "windows") // disable scroll optimization on Windows due to bugs in some terminals
	s.scr = scr
}

// setColorProfile implements renderer.
func (s *cursedRenderer) setColorProfile(p colorprofile.Profile) {
	s.mu.Lock()
	s.profile = p
	s.scr.SetColorProfile(p)
	s.mu.Unlock()
}

// resize implements renderer.
func (s *cursedRenderer) resize(w, h int) {
	s.mu.Lock()
	// We need to mark the screen for clear to force a redraw. However, we
	// only do so if we're using alt screen or the width has changed.
	// That's because redrawing is expensive and we can avoid it if the
	// width hasn't changed in inline mode. On the other hand, when using
	// alt screen mode, we always want to redraw because some terminals
	// would scroll the screen and our content would be lost.
	s.scr.Erase()
	s.width, s.height = w, h
	s.scr.Resize(s.width, s.height)
	s.mu.Unlock()
}

// clearScreen implements renderer.
func (s *cursedRenderer) clearScreen() {
	s.mu.Lock()
	// Move the cursor to the top left corner of the screen and trigger a full
	// screen redraw.
	s.scr.MoveTo(0, 0)
	s.scr.Erase()
	s.mu.Unlock()
}

// enableAltScreen sets the alt screen mode.
// Note that this writes to the buffer directly if write is true.
func enableAltScreen(s *cursedRenderer, enable bool, write bool) {
	if enable {
		enterAltScreen(s, write)
	} else {
		exitAltScreen(s, write)
	}
}

func enterAltScreen(s *cursedRenderer, write bool) {
	s.scr.SaveCursor()
	if write {
		s.buf.WriteString(ansi.SetModeAltScreenSaveCursor)
	}
	s.scr.SetFullscreen(true)
	s.scr.SetRelativeCursor(false)
	s.scr.Erase()
}

func exitAltScreen(s *cursedRenderer, write bool) {
	s.scr.Erase()
	s.scr.SetRelativeCursor(true)
	s.scr.SetFullscreen(false)
	if write {
		s.buf.WriteString(ansi.ResetModeAltScreenSaveCursor)
	}
	s.scr.RestoreCursor()
}

// enableTextCursor sets the text cursor mode.
func enableTextCursor(s *cursedRenderer, enable bool) {
	if enable {
		_, _ = s.scr.WriteString(ansi.SetModeTextCursorEnable)
	} else {
		_, _ = s.scr.WriteString(ansi.ResetModeTextCursorEnable)
	}
}

// setSyncdUpdates implements renderer.
func (s *cursedRenderer) setSyncdUpdates(syncd bool) {
	s.mu.Lock()
	s.syncdUpdates = syncd
	s.mu.Unlock()
}

// setWidthMethod implements renderer.
func (s *cursedRenderer) setWidthMethod(method ansi.Method) {
	s.mu.Lock()
	if method == ansi.GraphemeWidth {
		// Turn on Unicode mode (2027) for accurate grapheme width calculation.
		// This is needed for proper rendering of wide characters and emojis.
		_, _ = s.scr.WriteString(ansi.SetModeUnicodeCore)
	} else if s.cellbuf.Method == ansi.GraphemeWidth {
		// Turn off Unicode mode if we're switching away from grapheme width
		// calculation to avoid issues with some terminals that might still be
		// in Unicode mode and render characters incorrectly.
		_, _ = s.scr.WriteString(ansi.ResetModeUnicodeCore)
	}
	s.cellbuf.Method = method
	s.mu.Unlock()
}

// insertAbove implements renderer.
func (s *cursedRenderer) insertAbove(str string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(str) == 0 {
		return nil
	}

	var sb strings.Builder
	w, h := s.cellbuf.Width(), s.cellbuf.Height()
	_, y := s.scr.Position()

	// We need to scroll the screen up by the number of lines in the queue.
	sb.WriteByte('\r')
	down := h - y - 1
	if down > 0 {
		sb.WriteString(ansi.CursorDown(down))
	}

	lines := strings.Split(str, "\n")
	offset := len(lines)
	for _, line := range lines {
		lineWidth := ansi.StringWidth(line)
		if w > 0 && lineWidth > w {
			offset += (lineWidth / w)
		}
	}

	// Scroll the screen up by the offset to make room for the new lines.
	sb.WriteString(strings.Repeat("\n", offset))

	// XXX: Now go to the top of the screen, insert new lines, and write
	// the queued strings. It is important to use [Screen.moveCursor]
	// instead of [Screen.move] because we don't want to perform any checks
	// on the cursor position.
	up := offset + h - 1
	sb.WriteString(ansi.CursorUp(up))
	sb.WriteString(ansi.InsertLine(offset))
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString(ansi.EraseLineRight)
		sb.WriteString("\r\n")
	}

	s.scr.SetPosition(0, 0)

	if s.logger != nil {
		s.logger.Printf("insert above: %q", sb.String())
	}

	_, err := io.WriteString(s.w, sb.String())
	if err != nil {
		return fmt.Errorf("bubbletea: error writing insert above to the writer: %w", err)
	}

	return nil
}

// onMouse implements renderer.
func (s *cursedRenderer) onMouse(m MouseMsg) Cmd {
	var onMouse func(MouseMsg) Cmd
	s.mu.Lock()
	if s.lastView != nil {
		onMouse = s.lastView.OnMouse
	}
	s.mu.Unlock()
	if onMouse != nil {
		return onMouse(m)
	}
	return nil
}

func setProgressBar(s *cursedRenderer, pb *ProgressBar) {
	if pb == nil {
		_, _ = s.scr.WriteString(ansi.ResetProgressBar)
		return
	}

	var seq string
	switch pb.State {
	case ProgressBarNone:
		seq = ansi.ResetProgressBar
	case ProgressBarDefault:
		seq = ansi.SetProgressBar(pb.Value)
	case ProgressBarError:
		seq = ansi.SetErrorProgressBar(pb.Value)
	case ProgressBarIndeterminate:
		seq = ansi.SetIndeterminateProgressBar
	case ProgressBarWarning:
		seq = ansi.SetWarningProgressBar(pb.Value)
	}
	if seq != "" {
		_, _ = s.scr.WriteString(seq)
	}
}

func viewEquals(a, b *View) bool {
	if a == nil || b == nil {
		return false
	}

	if a.Content != b.Content ||
		a.AltScreen != b.AltScreen ||
		a.DisableBracketedPasteMode != b.DisableBracketedPasteMode ||
		a.ReportFocus != b.ReportFocus ||
		a.MouseMode != b.MouseMode ||
		a.WindowTitle != b.WindowTitle ||
		a.ForegroundColor != b.ForegroundColor ||
		a.BackgroundColor != b.BackgroundColor ||
		a.KeyboardEnhancements != b.KeyboardEnhancements {
		return false
	}

	if (a.Cursor == nil) != (b.Cursor == nil) {
		return false
	}
	if a.Cursor != nil && b.Cursor != nil {
		if a.Cursor.X != b.Cursor.X ||
			a.Cursor.Y != b.Cursor.Y ||
			a.Cursor.Shape != b.Cursor.Shape ||
			a.Cursor.Blink != b.Cursor.Blink ||
			a.Cursor.Color != b.Cursor.Color {
			return false
		}
	}

	if (a.ProgressBar == nil) != (b.ProgressBar == nil) {
		return false
	}
	if a.ProgressBar != nil && b.ProgressBar != nil {
		if *a.ProgressBar != *b.ProgressBar {
			return false
		}
	}

	return true
}

func keyboardEnhancementsFlags(ke KeyboardEnhancements) int {
	flags := 1 // always enable basic key disambiguation
	if ke.ReportEventTypes {
		flags |= ansi.KittyReportEventTypes
	}
	if ke.ReportAlternateKeys {
		flags |= ansi.KittyReportAlternateKeys
	}
	if ke.ReportAllKeysAsEscapeCodes {
		flags |= ansi.KittyReportAllKeysAsEscapeCodes
	}
	if ke.ReportAssociatedText {
		flags |= ansi.KittyReportAssociatedKeys
	}
	return flags
}
