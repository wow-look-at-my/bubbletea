package tea

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/assert"
	"os"
	"sync/atomic"
	"testing"
)

func TestOptions(t *testing.T) {
	t.Run("output", func(t *testing.T) {
		t.Parallel()
		var b bytes.Buffer
		p := NewProgram(nil, WithOutput(&b))
		_, ok := p.output.(*os.File)
		assert.False(t, ok)

	})

	t.Run("renderer", func(t *testing.T) {
		t.Parallel()
		p := NewProgram(nil, WithoutRenderer())
		assert.True(t, p.disableRenderer)

	})

	t.Run("without signals", func(t *testing.T) {
		t.Parallel()
		p := NewProgram(nil, WithoutSignals())
		assert.NotEqual(t, uint32(0), atomic.LoadUint32(&p.ignoreSignals))

	})

	t.Run("filter", func(t *testing.T) {
		t.Parallel()
		p := NewProgram(nil, WithFilter(func(_ Model, msg Msg) Msg { return msg }))
		assert.NotNil(t, p.filter)

	})

	t.Run("external context", func(t *testing.T) {
		t.Parallel()
		extCtx, extCancel := context.WithCancel(context.Background())
		defer extCancel()

		p := NewProgram(nil, WithContext(extCtx))
		assert.False(t, p.externalCtx != extCtx || p.externalCtx == context.Background())

	})

	t.Run("input options", func(t *testing.T) {
		exercise := func(t *testing.T, opt ProgramOption, fn func(*Program)) {
			p := NewProgram(nil, opt)
			fn(p)
		}

		t.Run("nil input", func(t *testing.T) {
			t.Parallel()
			exercise(t, WithInput(nil), func(p *Program) {
				assert.False(t, !p.disableInput || p.input != nil)

			})
		})

		t.Run("custom input", func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			exercise(t, WithInput(&b), func(p *Program) {
				assert.Equal(t, &b, p.input)

			})
		})
	})

	t.Run("startup options", func(t *testing.T) {
		exercise := func(t *testing.T, opt ProgramOption, fn func(*Program)) {
			p := NewProgram(nil, opt)
			fn(p)
		}

		t.Run("without catch panics", func(t *testing.T) {
			t.Parallel()
			exercise(t, WithoutCatchPanics(), func(p *Program) {
				assert.True(t, p.disableCatchPanics)

			})
		})

		t.Run("without signal handler", func(t *testing.T) {
			t.Parallel()
			exercise(t, WithoutSignalHandler(), func(p *Program) {
				assert.True(t, p.disableSignalHandler)

			})
		})
	})
}
