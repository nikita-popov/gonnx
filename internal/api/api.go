// Package api implements the external HTTP API of the gonnxd daemon.
// Routes:
//
//	GET  /healthz
//	GET  /v1/models
//	GET  /v1/models/{id}
//	POST /v1/models/{id}:predict
//	POST /v1/models/{id}:load
//	POST /v1/models/{id}:unload
//	POST /v1/models:install
//	POST /v1/models/{id}:update
//	GET  /v1/models/{id}/logs
//	GET  /metrics
package api
