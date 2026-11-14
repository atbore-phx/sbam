//go:build race

package optimize

// raceEnabled reports whether the race detector is on; timing assertions are
// meaningless under its 5-20x slowdown.
const raceEnabled = true
