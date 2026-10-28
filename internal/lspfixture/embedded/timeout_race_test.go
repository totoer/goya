//go:build race

package embedded_test

import "time"

// Race instruments the native checker and replays both endpoints in one
// transcript budget. Keep functional assertions exact without a speed gate.
const recordedFixtureTimeout = 90 * time.Second
