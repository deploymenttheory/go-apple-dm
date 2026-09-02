//go:build race

package postgres_test

// raceEnabled relaxes timing assertions: the race detector slows every
// statement several-fold, so wall-clock gates only hold without it.
const raceEnabled = true
