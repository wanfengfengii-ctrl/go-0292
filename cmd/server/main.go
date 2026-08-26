// Command server is the runnable entry point for the UHPC wet-joint
// traffic-release HTTP service. It builds the application engine, recovers the
// durable snapshot, and serves the versioned JSON API.
package main

import (
	"log"
	"net/http"
	"os"

	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/httpapi"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dataPath := os.Getenv("DATA_PATH")
	if dataPath == "" {
		dataPath = "data/state.db"
	}

	eng := engine.New(dataPath)
	if err := eng.Recover(); err != nil {
		log.Fatalf("recovery failed: %v", err)
	}

	srv := httpapi.NewWithEngine(eng)
	log.Printf("uhpc-wet-joint-traffic-release listening on %s (data=%s)", addr, dataPath)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}
