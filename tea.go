// Package tea provides a framework for building rich terminal user interfaces
// based on the paradigms of The Elm Architecture. It's well-suited for simple
// and complex terminal applications, either inline, full-window, or a mix of
// both. It's been battle-tested in several large projects and is
// production-ready.
//
// A tutorial is available at https://github.com/charmbracelet/bubbletea/tree/main/tutorials
//
// Example programs can be found at https://github.com/charmbracelet/bubbletea/tree/main/examples
package tea

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

// ErrProgramPanic is returned by [Program.Run] when the program recovers from a panic.
var ErrProgramPanic = errors.New("program experienced a panic")

// ErrProgramKilled is returned by [Program.Run] when the program gets killed.
var ErrProgramKilled = errors.New("program was killed")

// ErrInterrupted is returned by [Program.Run] when the program get a SIGINT
// signal, or when it receives a [InterruptMsg].
var ErrInterrupted = errors.New("program was interrupted")

// Msg contain data from the result of a IO operation. Msgs trigger the update
// function and, henceforth, the UI.
type Msg = uv.Event

// Model contains the program's state as well as its core functions.
type Model interface {
	// Init is the first function that will be called. It returns an optional
	// initial command. To not perform an initial command return nil.
	Init() Cmd

	// Update is called when a message is received. Use it to inspect messages
	// and, in response, update the model and/or send a command.
	Update(Msg) (Model, Cmd)

	// View renders the program's UI, which can be a string or a [Layer]. The
	// view is rendered after every Update.
	View() View
}

// NewView is a helper function to create a new [View] with the given styled
// string. A styled string represents text with styles and hyperlinks encoded
// as ANSI escape codes.
//
// Example:
//
//	```go
//	v := tea.NewView("Hello, World!")
//	```
func NewView(s string) View {
	var view View
	view.SetContent(s)
	return view
}

// View represents a terminal view that can be composed of multiple layers.
// It can also contain a cursor that will be rendered on top of the layers.
type View struct {
	// Content is the screen content of the view. It holds styled strings that
	// will be rendered to the terminal when the view is rendered.
	//
	// A styled string represents text with styles and hyperlinks encoded as
	// ANSI escape codes.
	//
	// Example:
	//
	//  ```go
	//  v := tea.NewView("Hello, World!")
	//  ```
	Content string

	// OnMouse is an optional mouse message handler that can be used to
	// intercept mouse messages that depends on view content from last render.
	// It can be useful for implementing view-specific behavior without
	// breaking the unidirectional data flow of Bubble Tea.
	//
	// Example:
	//
	//  ```go
	//  content := "Hello, World!"
	//  v := tea.NewView(content)
	//  v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
	//      return func() tea.Msg {
	//        m := msg.Mouse()
	//        // Check if the mouse is within the bounds of "World!"
	//        start := strings.Index(content, "World!")
	//        end := start + len("World!")
	//        if m.Y == 0 && m.X >= start && m.X < end {
	//          // Mouse is over "World!"
	//          return MyCustomMsg{
	//            MouseMsg: msg,
	//          }
	//		  }
	//      }
	//    }
	//    return nil
	//  }
	//  return v
	//  ```
	OnMouse func(msg MouseMsg) Cmd

	// Cursor represents the cursor position, style, and visibility on the
	// screen. When not nil, the cursor will be shown at the specified
	// position.
	Cursor *Cursor

	// BackgroundColor when not nil, sets the terminal background color. Use
	// nil to reset to the terminal's default background color.
	BackgroundColor color.Color

	// ForegroundColor when not nil, sets the terminal foreground color. Use
	// nil to reset to the terminal's default foreground color.
	ForegroundColor color.Color

	// WindowTitle sets the terminal window title. Support depends on the
	// terminal.
	WindowTitle string

	// ProgressBar when not nil, shows a progress bar in the terminal's
	// progress bar section. Support depends on the terminal.
	ProgressBar *ProgressBar

	// AltScreen puts the program in the alternate screen buffer
	// (i.e. the program goes into full window mode). Note that the altscreen will
	// be automatically exited when the program quits.
	//
	// Example:
	//
	//	func (m model) View() tea.View {
	//	    v := tea.NewView("Hello, World!")
	//	    v.AltScreen = true
	//	    return v
	//	}
	//
	AltScreen bool

	// ReportFocus enables reporting when the terminal gains and loses focus.
	// When this is enabled [FocusMsg] and [BlurMsg] messages will be sent to
	// your Update method.
	//
	// Note that while most terminals and multiplexers support focus reporting,
	// some do not. Also note that tmux needs to be configured to report focus
	// events.
	ReportFocus bool

	// DisableBracketedPasteMode disables bracketed paste mode for this view.
	DisableBracketedPasteMode bool

	// MouseMode sets the mouse mode for this view. It can be one of
	// [MouseModeNone], [MouseModeCellMotion], or [MouseModeAllMotion].
	MouseMode MouseMode

	// KeyboardEnhancements describes what keyboard enhancement features Bubble
	// Tea should request from the terminal.
	//
	// Bubble Tea supports requesting the following keyboard enhancement features:
	//   - ReportEventTypes: requests the terminal to report key repeat and
	//     release events.
	//
	// If the terminal supports any of these features, your program will
	// receive  a [KeyboardEnhancementsMsg] that indicates which features are
	// available.
	KeyboardEnhancements KeyboardEnhancements
}

// KeyboardEnhancements describes the requested keyboard enhancement features.
// If the terminal supports any of them, it will respond with a
// [KeyboardEnhancementsMsg] that indicates which features are supported.

// KeyboardEnhancements defines different keyboard enhancement features that
// can be requested from the terminal.

// KeyboardEnhancements defines different keyboard enhancement features that
// can be requested from the terminal.
//
// By default, Bubble Tea requests basic key disambiguation features from the
// terminal. If the terminal supports keyboard enhancements, or any of its
// additional features, it will respond with a [KeyboardEnhancementsMsg] that
// indicates which features are supported.
//
// Example:
//
//	```go
//	func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
//	  switch msg := msg.(type) {
//	  case tea.KeyboardEnhancementsMsg:
//	    // We have basic key disambiguation support.
//	    // We can handle "shift+enter", "ctrl+i", etc.
//		m.keyboardEnhancements = msg
//		if msg.ReportEventTypes {
//		  // Even better! We can now handle key repeat and release events.
//		}
//	  case tea.KeyPressMsg:
//	    switch msg.String() {
//	    case "shift+enter":
//	      // Handle shift+enter
//	      // This would not be possible without keyboard enhancements.
//	    case "ctrl+j":
//	      // Handle ctrl+j
//	    }
//	  case tea.KeyReleaseMsg:
//	    // Whoa! A key was released!
//	  }
//
//	  return m, nil
//	}
//
//	func (m model) View() tea.View {
//	  v := tea.NewView("Press some keys!")
//	  // Request reporting key repeat and release events.
//	  v.KeyboardEnhancements.ReportEventTypes = true
//	  return v
//	}
//	```
type KeyboardEnhancements struct {
	// ReportEventTypes requests the terminal to report key repeat and release
	// events.
	// If supported, your program will receive [KeyReleaseMsg]s and
	// [KeyPressMsg] with the [Key.IsRepeat] field set indicating that this is
	// a it's part of a key repeat sequence.
	ReportEventTypes bool

	// ReportAlternateKeys requests the terminal to report alternate key values
	// in addition to the main ones.
	// Note that only key events represented as escape codes will affected by
	// this enhancement.
	ReportAlternateKeys bool

	// ReportAllKeysAsEscapeCodes requests the terminal to report all key
	// events, including plain text keys, as escape codes.
	// When this is enabled, text won't be sent as plain text but instead as
	// escape codes that encode the key value and modifiers.
	ReportAllKeysAsEscapeCodes bool

	// ReportAssociatedText requests the terminal to report the text associated
	// with key events.
	// Note that this is an enhancement to
	// [KeyboardEnhancements.ReportAllKeysAsEscapeCodes] and only has an effect
	// if that is enabled.
	ReportAssociatedText bool
}

// SetContent is a helper method to set the content of a [View] with a styled
// string. A styled string represents text with styles and hyperlinks encoded
// as ANSI escape codes.
//
// Example:
//
//	```go
//	var v tea.View
//	v.SetContent("Hello, World!")
//	```
func (v *View) SetContent(s string) {
	v.Content = s
}

// MouseMode represents the mouse mode of a view.
type MouseMode int

const (
	// MouseModeNone disables mouse events.
	MouseModeNone MouseMode = iota

	// MouseModeCellMotion enables mouse click, release, and wheel events.
	// Mouse movement events are also captured if a mouse button is pressed
	// (i.e., drag events). Cell motion mode is better supported than all
	// motion mode.
	//
	// This will try to enable the mouse in extended mode (SGR), if that is not
	// supported by the terminal it will fall back to normal mode (X10).
	MouseModeCellMotion

	// MouseModeAllMotion enables all mouse events, including click, release,
	// wheel, and movement events. You will receive mouse movement events even
	// when no buttons are pressed.
	//
	// This will try to enable the mouse in extended mode (SGR), if that is not
	// supported by the terminal it will fall back to normal mode (X10).
	MouseModeAllMotion
)

// ProgressBarState represents the state of the progress bar.
type ProgressBarState int

// Progress bar states.
const (
	ProgressBarNone ProgressBarState = iota
	ProgressBarDefault
	ProgressBarError
	ProgressBarIndeterminate
	ProgressBarWarning
)

// String return a human-readable value for the given [ProgressBarState].
func (s ProgressBarState) String() string {
	return [...]string{
		"None",
		"Default",
		"Error",
		"Indeterminate",
		"Warning",
	}[s]
}

// ProgressBar represents the terminal progress bar.
//
// Support depends on the terminal.
//
// See https://learn.microsoft.com/en-us/windows/terminal/tutorials/progress-bar-sequences
type ProgressBar struct {
	// State is the current state of the progress bar. It can be one of
	// [ProgressBarNone], [ProgressBarDefault], [ProgressBarError],
	// [ProgressBarIndeterminate], and [ProgressBarWarning].
	State ProgressBarState
	// Value is the current value of the progress bar. It should be between
	// 0 and 100.
	Value int
}

// NewProgressBar returns a new progress bar with the given state and value.
// The value is ignored if the state is [ProgressBarNone] or
// [ProgressBarIndeterminate].
func NewProgressBar(state ProgressBarState, value int) *ProgressBar {
	return &ProgressBar{
		State: state,
		Value: min(max(value, 0), 100),
	}
}

// Cursor represents a cursor on the terminal screen.
type Cursor struct {
	// Position is a [Position] that determines the cursor's position on the
	// screen relative to the top left corner of the frame.
	Position

	// Color is a [color.Color] that determines the cursor's color.
	Color color.Color

	// Shape is a [CursorShape] that determines the cursor's shape.
	Shape CursorShape

	// Blink is a boolean that determines whether the cursor should blink.
	Blink bool
}

// NewCursor returns a new cursor with the default settings and the given
// position.
func NewCursor(x, y int) *Cursor {
	return &Cursor{
		Position: Position{X: x, Y: y},
		Color:    nil,
		Shape:    CursorBlock,
		Blink:    true,
	}
}

// Cmd is an IO operation that returns a message when it's complete. If it's
// nil it's considered a no-op. Use it for things like HTTP requests, timers,
// saving and loading from disk, and so on.
//
// Note that there's almost never a reason to use a command to send a message
// to another part of your program. That can almost always be done in the
// update function.
type Cmd func() Msg

// channelHandlers manages the series of channels returned by various processes.
// It allows us to wait for those processes to terminate before exiting the
// program.
type channelHandlers struct {
	handlers []chan struct{}
	mu       sync.RWMutex
}

// Adds a channel to the list of handlers. We wait for all handlers to terminate
// gracefully on shutdown.
func (h *channelHandlers) add(ch chan struct{}) {
	h.mu.Lock()
	h.handlers = append(h.handlers, ch)
	h.mu.Unlock()
}

// shutdown waits for all handlers to terminate.
func (h *channelHandlers) shutdown() {
	var wg sync.WaitGroup

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.handlers {
		wg.Add(1)
		go func(ch chan struct{}) {
			<-ch
			wg.Done()
		}(ch)
	}
	wg.Wait()
}

// Program is a terminal user interface.
type Program struct {
	// disableInput disables all input. This is useful for programs that
	// don't need input, like a progress bar or a spinner.
	disableInput bool

	// disableSignalHandler disables the signal handler that Bubble Tea sets up
	// for Programs. This is useful if you want to handle signals yourself.
	disableSignalHandler bool

	// disableCatchPanics disables the panic catching that Bubble Tea does by
	// default. If panic catching is disabled the terminal will be in a fairly
	// unusable state after a panic because Bubble Tea will not perform its usual
	// cleanup on exit.
	disableCatchPanics bool

	// filter supplies an event filter that will be invoked before Bubble Tea
	// processes a tea.Msg. The event filter can return any tea.Msg which will
	// then get handled by Bubble Tea instead of the original event. If the
	// event filter returns nil, the event will be ignored and Bubble Tea will
	// not process it.
	//
	// As an example, this could be used to prevent a program from shutting
	// down if there are unsaved changes.
	//
	// Example:
	//
	//	func filter(m tea.Model, msg tea.Msg) tea.Msg {
	//		if _, ok := msg.(tea.QuitMsg); !ok {
	//			return msg
	//		}
	//
	//		model := m.(myModel)
	//		if model.hasChanges {
	//			return nil
	//		}
	//
	//		return msg
	//	}
	//
	//	p := tea.NewProgram(Model{});
	//	p.filter = filter
	//
	//	if _,err := p.Run(context.Background()); err != nil {
	//		fmt.Println("Error running program:", err)
	//		os.Exit(1)
	//	}
	filter func(Model, Msg) Msg

	// fps sets a custom maximum fps at which the renderer should run. If less
	// than 1, the default value of 60 will be used. If over 120, the fps will
	// be capped at 120.
	fps int

	// initialModel is the initial model for the program and is the only
	// required field when creating a new program.
	initialModel Model

	// disableRenderer prevents the program from rendering to the terminal.
	// This can be useful for running daemon-like programs that don't require a
	// UI but still want to take advantage of Bubble Tea's architecture.
	disableRenderer bool

	// handlers is a list of channels that need to be waited on before the
	// program can exit.
	handlers channelHandlers

	// ctx is the programs's internal context for signalling internal teardown.
	// It is built and derived from the externalCtx in NewProgram().
	ctx    context.Context
	cancel context.CancelFunc

	// externalCtx is a context that was passed in via WithContext, otherwise defaulting
	// to ctx.Background() (in case it was not), the internal context is derived from it.
	externalCtx context.Context

	msgs         chan Msg
	errs         chan error
	finished     chan struct{}
	shutdownOnce sync.Once

	profile *colorprofile.Profile // the terminal color profile

	// where to send output, this will usually be os.Stdout.
	output    io.Writer
	outputBuf bytes.Buffer // buffer used to queue commands to be sent to the output

	// ttyOutput is null if output is not a TTY.
	ttyOutput           term.File
	previousOutputState *term.State
	renderer            renderer

	// the environment variables for the program, defaults to os.Environ().
	environ uv.Environ
	// the program's logger for debugging.
	logger uv.Logger

	// where to read inputs from, this will usually be os.Stdin.
	input io.Reader
	// ttyInput is null if input is not a TTY.
	ttyInput              term.File
	previousTtyInputState *term.State
	cancelReader          cancelreader.CancelReader
	inputScanner          *uv.TerminalReader
	readLoopDone          chan struct{}

	// modes keeps track of terminal modes that have been enabled or disabled.
	ignoreSignals uint32

	// ticker is the ticker that will be used to write to the renderer.
	ticker *time.Ticker

	// once is used to stop the renderer.
	once sync.Once

	// rendererDone is used to stop the renderer.
	rendererDone chan struct{}

	// Initial window size. Mainly used for testing.
	width, height int

	// whether to use hard tabs to optimize cursor movements
	useHardTabs bool
	// whether to use backspace to optimize cursor movements
	useBackspace bool

	mu sync.Mutex
}

// Quit is a special command that tells the Bubble Tea program to exit.
func Quit() Msg {
	return QuitMsg{}
}

// QuitMsg signals that the program should quit. You can send a [QuitMsg] with
// [Quit].
type QuitMsg struct{}

// Suspend is a special command that tells the Bubble Tea program to suspend.
func Suspend() Msg {
	return SuspendMsg{}
}

// SuspendMsg signals the program should suspend.
// This usually happens when ctrl+z is pressed on common programs, but since
// bubbletea puts the terminal in raw mode, we need to handle it in a
// per-program basis.
//
// You can send this message with [Suspend()].
type SuspendMsg struct{}

// ResumeMsg can be listen to do something once a program is resumed back
// from a suspend state.
type ResumeMsg struct{}

// InterruptMsg signals the program should suspend.
// This usually happens when ctrl+c is pressed on common programs, but since
// bubbletea puts the terminal in raw mode, we need to handle it in a
// per-program basis.
//
// You can send this message with [Interrupt()].
type InterruptMsg struct{}

// Interrupt is a special command that tells the Bubble Tea program to
// interrupt.
func Interrupt() Msg {
	return InterruptMsg{}
}

// NewProgram creates a new [Program].
