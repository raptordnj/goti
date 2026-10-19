//go:build !android

package platform

// Mobile is false off Android (it is true on Android, where the process
// outlives its activity).
const Mobile = false
