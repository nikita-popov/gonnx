// Package scheduler decides when to load and unload workers.
// v0 policy: lazy load on first request, idle unload after configured timeout.
package scheduler
