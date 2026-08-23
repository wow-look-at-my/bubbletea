package tea

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// NewProgram creates a new [Program].
func NewProgram(model Model, opts ...ProgramOption) *Program {
	p := &Program{
		initialModel: model,
		msgs:         make(chan Msg),
		errs:         make(chan error, 1),
		rendererDone: make(chan struct{}),
	}

	// Apply all options to the program.
	for _, opt := range opts {
		opt(p)
	}

	// A context can be provided with a ProgramOption, but if none was provided
	// we'll use the default background context.
	if p.externalCtx == nil {
		p.externalCtx = context.Background()
	}
	// Initialize context and teardown channel.
	p.ctx, p.cancel = context.WithCancel(p.externalCtx)

	// if no output was set, set it to stdout
	if p.output == nil {
		p.output = os.Stdout
	}

	// if no environment was set, set it to os.Environ()
	if p.environ == nil {
		p.environ = os.Environ()
	}

	if p.fps < 1 {
		p.fps = defaultFPS
	} else if p.fps > maxFPS {
		p.fps = maxFPS
	}

	tracePath, traceOk := os.LookupEnv("TEA_TRACE")
	if traceOk && len(tracePath) > 0 {
		// We have a trace filepath.
		if f, err := os.OpenFile(tracePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600); err == nil {
			p.logger = log.New(f, "bubbletea: ", log.LstdFlags|log.Lshortfile)
		}
	}

	return p
}

func (p *Program) handleSignals() chan struct{} {
	ch := make(chan struct{})

	// Listen for SIGINT and SIGTERM.
	//
	// In most cases ^C will not send an interrupt because the terminal will be
	// in raw mode and ^C will be captured as a keystroke and sent along to
	// Program.Update as a KeyMsg. When input is not a TTY, however, ^C will be
	// caught here.
	//
	// SIGTERM is sent by unix utilities (like kill) to terminate a process.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		defer func() {
			signal.Stop(sig)
			close(ch)
		}()

		for {
			select {
			case <-p.ctx.Done():
				return

			case s := <-sig:
				if atomic.LoadUint32(&p.ignoreSignals) == 0 {
					switch s {
					case syscall.SIGINT:
						p.msgs <- InterruptMsg{}
					default:
						p.msgs <- QuitMsg{}
					}
					return
				}
			}
		}
	}()

	return ch
}

// handleResize handles terminal resize events.
func (p *Program) handleResize() chan struct{} {
	ch := make(chan struct{})

	if p.ttyOutput != nil {
		// Listen for window resizes.
		go p.listenForResize(ch)
	} else {
		close(ch)
	}

	return ch
}

// handleCommands runs commands in a goroutine and sends the result to the
// program's message channel.
func (p *Program) handleCommands(cmds chan Cmd) chan struct{} {
	ch := make(chan struct{})

	go func() {
		defer close(ch)

		for {
			select {
			case <-p.ctx.Done():
				return

			case cmd := <-cmds:
				if cmd == nil {
					continue
				}

				// Don't wait on these goroutines, otherwise the shutdown
				// latency would get too large as a Cmd can run for some time
				// (e.g. tick commands that sleep for half a second). It's not
				// possible to cancel them so we'll have to leak the goroutine
				// until Cmd returns.
				go func() {
					// Recover from panics.
					if !p.disableCatchPanics {
						defer func() {
							if r := recover(); r != nil {
								p.recoverFromPanic(r)
							}
						}()
					}

					msg := cmd() // this can be long.
					p.Send(msg)
				}()
			}
		}
	}()

	return ch
}

// eventLoop is the central message loop. It receives and handles the default
// Bubble Tea messages, update the model and triggers redraws.
func (p *Program) eventLoop(model Model, cmds chan Cmd) (Model, error) {
	for {
		select {
		case <-p.ctx.Done():
			return model, nil

		case err := <-p.errs:
			return model, err

		case msg := <-p.msgs:
			msg = p.translateInputEvent(msg)

			// Filter messages.
			if p.filter != nil {
				msg = p.filter(model, msg)
			}
			if msg == nil {
				continue
			}

			// Handle special internal messages.
			switch msg := msg.(type) {
			case QuitMsg:
				return model, nil

			case InterruptMsg:
				return model, ErrInterrupted

			case SuspendMsg:
				if suspendSupported {
					p.suspend()
				}

			case CapabilityMsg:
				switch msg.Content {
				case "RGB", "Tc":
					if *p.profile != colorprofile.TrueColor {
						tc := colorprofile.TrueColor
						p.profile = &tc
						go p.Send(ColorProfileMsg{*p.profile})
					}
				}

			case ModeReportMsg:
				switch msg.Mode {
				case ansi.ModeSynchronizedOutput:
					if msg.Value == ansi.ModeReset {
						// The terminal supports synchronized output and it's
						// currently disabled, so we can enable it on the renderer.
						p.renderer.setSyncdUpdates(true)
					}
				case ansi.ModeUnicodeCore:
					if msg.Value == ansi.ModeReset || msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet {
						p.renderer.setWidthMethod(ansi.GraphemeWidth)
					}
				}

			case MouseMsg:
				switch msg.(type) {
				case MouseClickMsg, MouseReleaseMsg, MouseWheelMsg, MouseMotionMsg:
					// Only send mouse messages to the renderer if they are an
					// actual mouse event.
					if cmd := p.renderer.onMouse(msg); cmd != nil {
						go p.Send(cmd())
					}
				}

			case readClipboardMsg:
				p.execute(ansi.RequestSystemClipboard)

			case setClipboardMsg:
				p.execute(ansi.SetSystemClipboard(string(msg)))

			case readPrimaryClipboardMsg:
				p.execute(ansi.RequestPrimaryClipboard)

			case setPrimaryClipboardMsg:
				p.execute(ansi.SetPrimaryClipboard(string(msg)))

			case backgroundColorMsg:
				p.execute(ansi.RequestBackgroundColor)

			case foregroundColorMsg:
				p.execute(ansi.RequestForegroundColor)

			case cursorColorMsg:
				p.execute(ansi.RequestCursorColor)

			case execMsg:
				// NB: this blocks.
				p.exec(msg.cmd, msg.fn)

			case terminalVersion:
				p.execute(ansi.RequestNameVersion)

			case requestCapabilityMsg:
				p.execute(ansi.RequestTermcap(string(msg)))

			case BatchMsg:
				go p.execBatchMsg(msg)
				continue

			case sequenceMsg:
				go p.execSequenceMsg(msg)
				continue

			case WindowSizeMsg:
				p.renderer.resize(msg.Width, msg.Height)

			case windowSizeMsg:
				go p.checkResize()

			case requestCursorPosMsg:
				p.execute(ansi.RequestCursorPositionReport)

			case RawMsg:
				p.execute(fmt.Sprint(msg.Msg))

			case printLineMessage:
				p.renderer.insertAbove(msg.messageBody) //nolint:errcheck,gosec

			case clearScreenMsg:
				p.renderer.clearScreen()

			case ColorProfileMsg:
				p.renderer.setColorProfile(msg.Profile)
			}

			var cmd Cmd
			model, cmd = model.Update(msg) // run update

			select {
			case <-p.ctx.Done():
				return model, nil
			case cmds <- cmd: // process command (if any)
			}

			p.render(model) // render view
		}
	}
}

// render renders the given view to the renderer.
func (p *Program) render(model Model) {
	if p.renderer != nil {
		p.renderer.render(model.View()) // send view to renderer
	}
}

func (p *Program) execSequenceMsg(msg sequenceMsg) {
	if !p.disableCatchPanics {
		defer func() {
			if r := recover(); r != nil {
				p.recoverFromGoPanic(r)
			}
		}()
	}

	// Execute commands one at a time, in order.
	for _, cmd := range msg {
		if cmd == nil {
			continue
		}
		msg := cmd()
		switch msg := msg.(type) {
		case BatchMsg:
			p.execBatchMsg(msg)
		case sequenceMsg:
			p.execSequenceMsg(msg)
		default:
			p.Send(msg)
		}
	}
}

func (p *Program) execBatchMsg(msg BatchMsg) {
	if !p.disableCatchPanics {
		defer func() {
			if r := recover(); r != nil {
				p.recoverFromGoPanic(r)
			}
		}()
	}

	// Execute commands one at a time.
	var wg sync.WaitGroup
	for _, cmd := range msg {
		if cmd == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()

			if !p.disableCatchPanics {
				defer func() {
					if r := recover(); r != nil {
						p.recoverFromGoPanic(r)
					}
				}()
			}

			msg := cmd()
			switch msg := msg.(type) {
			case BatchMsg:
				p.execBatchMsg(msg)
			case sequenceMsg:
				p.execSequenceMsg(msg)
			default:
				p.Send(msg)
			}
		}()
	}

	wg.Wait() // wait for all commands from batch msg to finish
}

// shouldQuerySynchronizedOutput determines whether the terminal should be
// queried for various capabilities.
//
// This function checks for terminals that are known to support mode 2026,
// while excluding SSH sessions which may be unreliable, unless it's a
// known-good terminal like Windows Terminal.
//
// The function returns true for:
//   - Terminals without TERM_PROGRAM set and not in SSH sessions
//   - Windows Terminal (WT_SESSION is set)
//   - Terminals with TERM_PROGRAM set (except Apple Terminal) and not in SSH sessions
//   - Specific terminal types: ghostty, wezterm, alacritty, kitty, rio
func shouldQuerySynchronizedOutput(environ uv.Environ) bool {
	termType := environ.Getenv("TERM")
	termProg, okTermProg := environ.LookupEnv("TERM_PROGRAM")
	_, okSSHTTY := environ.LookupEnv("SSH_TTY")
	_, okWTSession := environ.LookupEnv("WT_SESSION")

	return (!okTermProg && !okSSHTTY) ||
		okWTSession ||
		(okTermProg && !strings.Contains(termProg, "Apple") && !okSSHTTY) ||
		strings.Contains(termType, "ghostty") ||
		strings.Contains(termType, "wezterm") ||
		strings.Contains(termType, "alacritty") ||
		strings.Contains(termType, "kitty") ||
		strings.Contains(termType, "rio")
}

// Run initializes the program and runs its event loops, blocking until it gets
// terminated by either [Program.Quit], [Program.Kill], or its signal handler.
// Returns the final model.
func (p *Program) Run() (returnModel Model, returnErr error) {
	if p.initialModel == nil {
		return nil, errors.New("bubbletea: InitialModel cannot be nil")
	}

	// Initialize context and teardown channel.
	p.handlers = channelHandlers{}
	cmds := make(chan Cmd)

	p.finished = make(chan struct{})
	defer func() {
		close(p.finished)
	}()

	defer p.cancel()

	if p.disableInput {
		p.input = nil
	} else if p.input == nil {
		p.input = os.Stdin
		if !term.IsTerminal(os.Stdin.Fd()) {
			ttyIn, _, err := OpenTTY()
			if err != nil {
				return p.initialModel, fmt.Errorf("bubbletea: error opening TTY: %w", err)
			}
			p.input = ttyIn
		}
	}

	// Handle signals.
	if !p.disableSignalHandler {
		p.handlers.add(p.handleSignals())
	}

	// Recover from panics.
	if !p.disableCatchPanics {
		defer func() {
			if r := recover(); r != nil {
				returnErr = fmt.Errorf("%w: %w", ErrProgramKilled, ErrProgramPanic)
				p.recoverFromPanic(r)
			}
		}()
	}

	// Check if output is a TTY before entering raw mode, hiding the cursor and
	// so on.
	if err := p.initTerminal(); err != nil {
		return p.initialModel, err
	}

	// Get the initial window size.
	width, height := p.width, p.height
	if p.ttyOutput != nil {
		// Set the initial size of the terminal.
		w, h, err := term.GetSize(p.ttyOutput.Fd())
		if err != nil {
			return p.initialModel, fmt.Errorf("bubbletea: error getting terminal size: %w", err)
		}

		width, height = w, h
	}

	p.width, p.height = width, height
	resizeMsg := WindowSizeMsg{Width: p.width, Height: p.height}

	if p.renderer == nil {
		if p.disableRenderer {
			p.renderer = &nilRenderer{}
		} else {
			// If no renderer is set use the cursed one.
			r := newCursedRenderer(
				p.output,
				p.environ,
				p.width,
				p.height,
			)
			r.setLogger(p.logger)
			// XXX: This breaks many things especially when we want the output
			// to be compatible with terminals that are not necessary a TTY.
			// This was originally done to work around a Wish emulated-pty
			// issue where when a PTY session is detected, and we don't
			// allocate a real PTY, the terminal settings (Termios and WinCon)
			// don't change and the we end up working in cooked mode instead of
			// raw mode. See issue #1572.
			mapNl := runtime.GOOS != "windows" && p.ttyInput == nil
			r.setOptimizations(p.useHardTabs, p.useBackspace, mapNl)
			p.renderer = r
		}
	}

	// Get the color profile and send it to the program.
	if p.profile == nil {
		cp := colorprofile.Detect(p.output, p.environ)
		p.profile = &cp
	}

	// Set the color profile on the renderer and send it to the program.
	p.renderer.setColorProfile(*p.profile)
	go p.Send(ColorProfileMsg{*p.profile})

	// Send the initial size to the program.
	go p.Send(resizeMsg)
	p.renderer.resize(resizeMsg.Width, resizeMsg.Height)

	// Send the environment variables used by the program.
	go p.Send(EnvMsg(p.environ))

	// Init the input reader and initial model.
	model := p.initialModel
	if p.input != nil {
		if err := p.initInputReader(false); err != nil {
			return model, err
		}
	}

	// Start the renderer.
	p.startRenderer()

	if !p.disableRenderer && shouldQuerySynchronizedOutput(p.environ) {
		// Query for synchronized updates support (mode 2026) and unicode core
		// (mode 2027). If the terminal supports it, the renderer will enable
		// it once we get the response.
		p.execute(ansi.RequestModeSynchronizedOutput +
			ansi.RequestModeUnicodeCore)
	}

	// Initialize the program.
	initCmd := model.Init()
	if initCmd != nil {
		ch := make(chan struct{})
		p.handlers.add(ch)

		go func() {
			defer close(ch)

			select {
			case cmds <- initCmd:
			case <-p.ctx.Done():
			}
		}()
	}

	// Render the initial view.
	p.render(model)

	// Handle resize events.
	p.handlers.add(p.handleResize())

	// Process commands.
	p.handlers.add(p.handleCommands(cmds))

	// Run event loop, handle updates and draw.
	var err error
	model, err = p.eventLoop(model, cmds)

	if err == nil && len(p.errs) > 0 {
		err = <-p.errs // Drain a leftover error in case eventLoop crashed.
	}

	killed := p.externalCtx.Err() != nil || p.ctx.Err() != nil || err != nil
	if killed {
		if err == nil && p.externalCtx.Err() != nil {
			// Return also as context error the cancellation of an external context.
			// This is the context the user knows about and should be able to act on.
			err = fmt.Errorf("%w: %w", ErrProgramKilled, p.externalCtx.Err())
		} else if err == nil && p.ctx.Err() != nil {
			// Return only that the program was killed (not the internal mechanism).
			// The user does not know or need to care about the internal program context.
			err = ErrProgramKilled
		} else {
			// Return that the program was killed and also the error that caused it.
			err = fmt.Errorf("%w: %w", ErrProgramKilled, err)
		}
	} else {
		// Graceful shutdown of the program (not killed):
		// Ensure we rendered the final state of the model.
		p.render(model)
	}

	// Restore terminal state.
	p.shutdown(killed)

	return model, err
}

// Send sends a message to the main update function, effectively allowing
// messages to be injected from outside the program for interoperability
// purposes.
//
// If the program hasn't started yet this will be a blocking operation.
// If the program has already been terminated this will be a no-op, so it's safe
// to send messages after the program has exited.
