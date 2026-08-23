package tea

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func (p *Program) Send(msg Msg) {
	select {
	case <-p.ctx.Done():
	case p.msgs <- msg:
	}
}

// Quit is a convenience function for quitting Bubble Tea programs. Use it
// when you need to shut down a Bubble Tea program from the outside.
//
// If you wish to quit from within a Bubble Tea program use the Quit command.
//
// If the program is not running this will be a no-op, so it's safe to call
// if the program is unstarted or has already exited.
func (p *Program) Quit() {
	p.Send(Quit())
}

// Kill stops the program immediately and restores the former terminal state.
// The final render that you would normally see when quitting will be skipped.
// [program.Run] returns a [ErrProgramKilled] error.
func (p *Program) Kill() {
	p.shutdown(true)
}

// Wait waits/blocks until the underlying Program finished shutting down.
func (p *Program) Wait() {
	<-p.finished
}

// execute writes the given sequence to the program output.
func (p *Program) execute(seq string) {
	p.mu.Lock()
	_, _ = p.outputBuf.WriteString(seq)
	p.mu.Unlock()
}

// flush flushes the output buffer to the program output.
func (p *Program) flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.outputBuf.Len() == 0 {
		return nil
	}
	if p.logger != nil {
		p.logger.Printf("output: %q", p.outputBuf.String())
	}
	_, err := p.output.Write(p.outputBuf.Bytes())
	p.outputBuf.Reset()
	if err != nil {
		return fmt.Errorf("error writing to output: %w", err)
	}
	return nil
}

// shutdown performs operations to free up resources and restore the terminal
// to its original state.
func (p *Program) shutdown(kill bool) {
	p.shutdownOnce.Do(func() {
		p.cancel()

		// Wait for all handlers to finish.
		p.handlers.shutdown()

		// Check if the cancel reader has been setup before waiting and closing.
		if p.cancelReader != nil {
			// Wait for input loop to finish.
			if p.cancelReader.Cancel() {
				if !kill {
					p.waitForReadLoop()
				}
			}
			_ = p.cancelReader.Close()
		}

		if p.renderer != nil {
			p.stopRenderer(kill)
		}

		_ = p.restoreTerminalState()
	})
}

// recoverFromPanic recovers from a panic, prints the stack trace, and restores
// the terminal to a usable state.
func (p *Program) recoverFromPanic(r interface{}) {
	select {
	case p.errs <- ErrProgramPanic:
	default:
	}
	p.shutdown(true) // Ok to call here, p.Run() cannot do it anymore.
	// We use "\r\n" to ensure the output is formatted even when restoring the
	// terminal does not work or when raw mode is still active.
	rec := strings.ReplaceAll(fmt.Sprintf("%s", r), "\n", "\r\n")
	fmt.Fprintf(os.Stderr, "Caught panic:\r\n\r\n%s\r\n\r\nRestoring terminal...\r\n\r\n", rec)
	stack := strings.ReplaceAll(fmt.Sprintf("%s\n", debug.Stack()), "\n", "\r\n")
	fmt.Fprint(os.Stderr, stack)
	if v, err := strconv.ParseBool(os.Getenv("TEA_DEBUG")); err == nil && v {
		f, err := os.Create(fmt.Sprintf("bubbletea-panic-%d.log", time.Now().Unix()))
		if err == nil {
			defer f.Close()        //nolint:errcheck
			fmt.Fprintln(f, rec)   //nolint:errcheck
			fmt.Fprintln(f)        //nolint:errcheck
			fmt.Fprintln(f, stack) //nolint:errcheck
		}
	}
}

// recoverFromGoPanic recovers from a goroutine panic, prints a stack trace and
// signals for the program to be killed and terminal restored to a usable state.
func (p *Program) recoverFromGoPanic(r interface{}) {
	select {
	case p.errs <- ErrProgramPanic:
	default:
	}
	p.cancel()
	// We use "\r\n" to ensure the output is formatted even when restoring the
	// terminal does not work or when raw mode is still active.
	rec := strings.ReplaceAll(fmt.Sprintf("%s", r), "\n", "\r\n")
	fmt.Fprintf(os.Stderr, "Caught panic:\r\n\r\n%s\r\n\r\nRestoring terminal...\r\n\r\n", rec)
	stack := strings.ReplaceAll(fmt.Sprintf("%s\n", debug.Stack()), "\n", "\r\n")
	fmt.Fprint(os.Stderr, stack)
	if v, err := strconv.ParseBool(os.Getenv("TEA_DEBUG")); err == nil && v {
		f, err := os.Create(fmt.Sprintf("bubbletea-panic-%d.log", time.Now().Unix()))
		if err == nil {
			defer f.Close()        //nolint:errcheck
			fmt.Fprintln(f, rec)   //nolint:errcheck
			fmt.Fprintln(f)        //nolint:errcheck
			fmt.Fprintln(f, stack) //nolint:errcheck
		}
	}
}

// ReleaseTerminal restores the original terminal state and cancels the input
// reader. You can return control to the Program with RestoreTerminal.
func (p *Program) ReleaseTerminal() error {
	return p.releaseTerminal(false)
}

func (p *Program) releaseTerminal(reset bool) error {
	atomic.StoreUint32(&p.ignoreSignals, 1)
	if p.cancelReader != nil {
		p.cancelReader.Cancel()
	}

	p.waitForReadLoop()

	if p.renderer != nil {
		p.stopRenderer(false)
		if reset {
			p.renderer.reset()
		}
	}

	return p.restoreTerminalState()
}

// RestoreTerminal reinitializes the Program's input reader, restores the
// terminal to the former state when the program was running, and repaints.
// Use it to reinitialize a Program after running ReleaseTerminal.
func (p *Program) RestoreTerminal() error {
	atomic.StoreUint32(&p.ignoreSignals, 0)

	if err := p.initTerminal(); err != nil {
		return err
	}
	if p.input != nil {
		if err := p.initInputReader(false); err != nil {
			return err
		}
	}

	p.startRenderer()

	// If the output is a terminal, it may have been resized while another
	// process was at the foreground, in which case we may not have received
	// SIGWINCH. Detect any size change now and propagate the new size as
	// needed.
	go p.checkResize()

	// Flush queued commands.
	return p.flush()
}

// Println prints above the Program. This output is unmanaged by the program
// and will persist across renders by the Program.
//
// If the altscreen is active no output will be printed.
func (p *Program) Println(args ...any) {
	p.msgs <- printLineMessage{
		messageBody: fmt.Sprint(args...),
	}
}

// Printf prints above the Program. It takes a format template followed by
// values similar to fmt.Printf. This output is unmanaged by the program and
// will persist across renders by the Program.
//
// Unlike fmt.Printf (but similar to log.Printf) the message will be print on
// its own line.
//
// If the altscreen is active no output will be printed.
func (p *Program) Printf(template string, args ...any) {
	p.msgs <- printLineMessage{
		messageBody: fmt.Sprintf(template, args...),
	}
}

// startRenderer starts the renderer.
func (p *Program) startRenderer() {
	framerate := time.Second / time.Duration(p.fps)
	if p.ticker == nil {
		p.ticker = time.NewTicker(framerate)
	} else {
		// If the ticker already exists, it has been stopped and we need to
		// reset it.
		p.ticker.Reset(framerate)
	}

	// Since the renderer can be restarted after a stop, we need to reset
	// the done channel and its corresponding sync.Once.
	p.once = sync.Once{}

	// Start the renderer.
	p.renderer.start()
	go func() {
		for {
			select {
			case <-p.rendererDone:
				p.ticker.Stop()
				return

			case <-p.ticker.C:
				_ = p.flush()
				_ = p.renderer.flush(false)
			}
		}
	}()
}

// stopRenderer stops the renderer.
// If kill is true, the renderer will be stopped immediately without flushing
// the last frame.
func (p *Program) stopRenderer(kill bool) {
	// Stop the renderer before acquiring the mutex to avoid a deadlock.
	p.once.Do(func() {
		p.rendererDone <- struct{}{}
	})

	if !kill {
		// flush locks the mutex
		_ = p.renderer.flush(true)
	}

	_ = p.renderer.close()
}
