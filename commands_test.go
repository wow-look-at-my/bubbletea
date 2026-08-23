package tea

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestEvery(t *testing.T) {
	t.Parallel()
	expected := "every ms"
	msg := Every(time.Millisecond, func(t time.Time) Msg {
		return expected
	})()
	require.Equal(t, msg, expected)

}

func TestTick(t *testing.T) {
	t.Parallel()
	expected := "tick"
	msg := Tick(time.Millisecond, func(t time.Time) Msg {
		return expected
	})()
	require.Equal(t, msg, expected)

}

func TestBatch(t *testing.T) {
	t.Parallel()
	testMultipleCommands[BatchMsg](t, Batch)
}

func TestSequence(t *testing.T) {
	t.Parallel()
	testMultipleCommands[sequenceMsg](t, Sequence)
}

func testMultipleCommands[T ~[]Cmd](t *testing.T, createFn func(cmd ...Cmd) Cmd) {
	t.Run("nil cmd", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, createFn(nil))

	})
	t.Run("empty cmd", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, createFn())

	})
	t.Run("single cmd", func(t *testing.T) {
		t.Parallel()
		b := createFn(Quit)()
		_, ok := b.(QuitMsg)
		require.True(t, ok)

	})
	t.Run("mixed nil cmds", func(t *testing.T) {
		t.Parallel()
		b := createFn(nil, Quit, nil, Quit, nil, nil)()
		l := len(b.(T))
		require.Equal(t, 2, l)

	})
}
