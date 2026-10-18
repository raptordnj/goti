//go:build race

package downloader

// raceEnabled: the race detector makes sync.Pool drop items at random, so
// allocation counts are not meaningful.
const raceEnabled = true
