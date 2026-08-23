package tea

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
	"runtime"
	"testing"
)

type execFinishedMsg struct{ err error }

type testExecModel struct {
	cmd string
	err error
}

type testExecNoInputModel struct{ testExecModel }

func (m *testExecModel) Init() Cmd {
	c := exec.Command(m.cmd) //nolint:gosec
	return ExecProcess(c, func(err error) Msg {
		return execFinishedMsg{err}
	})
}

func (m *testExecNoInputModel) Init() Cmd {
	return ExecProcess(successExecCommand(), func(err error) Msg {
		return execFinishedMsg{err}
	})
}

func (m *testExecModel) Update(msg Msg) (Model, Cmd) {
	switch msg := msg.(type) {
	case execFinishedMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		return m, Quit
	}

	return m, nil
}

func (m *testExecModel) View() View {
	return NewView("\n")
}

type spyRenderer struct {
	renderer
	calledReset bool
}

func successExecCommand() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "exit 0")
	}
	return exec.Command("true")
}

func TestTeaExec(t *testing.T) {
	type test struct {
		name      string
		cmd       string
		expectErr bool
	}

	// TODO: add more tests for windows
	tests := []test{
		{
			name:      "invalid command",
			cmd:       "invalid",
			expectErr: true,
		},
	}

	if runtime.GOOS != "windows" {
		tests = append(tests, []test{
			{
				name:      "true",
				cmd:       "true",
				expectErr: false,
			},
			{
				name:      "false",
				cmd:       "false",
				expectErr: true,
			},
		}...)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			var in bytes.Buffer

			m := &testExecModel{cmd: test.cmd}
			p := NewProgram(m,
				WithInput(&in),
				WithOutput(&buf),
			)
			_, err := p.Run()
			assert.Nil(t, err)

			p.renderer = &spyRenderer{renderer: p.renderer}

			assert.False(t, m.err != nil && !test.expectErr)
			if m.err != nil && !test.expectErr {
				assert.True(t, p.renderer.(*spyRenderer).calledReset, "expected renderer to be reset")
			}

			assert.False(t, m.err == nil && test.expectErr)

		})
	}
}

func TestTeaExecWithNilInput(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	m := &testExecNoInputModel{}
	p := NewProgram(m,
		WithInput(nil),
		WithOutput(&buf),
	)

	_, err := p.Run()
	require.Nil(t, err)

	require.Nil(t, m.err)

}
