// Worker represents a running handler subprocess for one bundle revision.
// It communicates with the core daemon via HTTP over a Unix domain socket
// at run/<worker-id>.sock.
// Required worker endpoints:
//
//	GET  /health
//	GET  /describe
//	POST /predict
//	POST /shutdown
package runtime
