// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muncus/qmcp/queue"
	"github.com/muncus/qmcp/server"
)

func main() {
	defaultStoreFile := os.Getenv("QMCP_STORE_FILE")
	if defaultStoreFile == "" {
		defaultStoreFile = "qmcp.jsonl"
	}
	defaultHTTPAddr := os.Getenv("QMCP_HTTP_ADDR")
	defaultFirestoreProject := os.Getenv("QMCP_FIRESTORE_PROJECT")
	defaultFirestoreCollection := os.Getenv("QMCP_FIRESTORE_COLLECTION")
	if defaultFirestoreCollection == "" {
		defaultFirestoreCollection = queue.DefaultFirestoreCollection
	}

	storeFile := flag.String("store-file", defaultStoreFile, "Path to the JSONL persistence file (or set QMCP_STORE_FILE)")
	firestoreProject := flag.String("firestore-project", defaultFirestoreProject, "Google Cloud project ID for Firestore store (or set QMCP_FIRESTORE_PROJECT)")
	firestoreCollection := flag.String("firestore-collection", defaultFirestoreCollection, "Firestore collection name (or set QMCP_FIRESTORE_COLLECTION)")
	httpAddr := flag.String("http", defaultHTTPAddr, "Optional HTTP listen address (e.g. :8080) to serve the browser UI at / and MCP over HTTP at /mcp")
	alsoStdio := flag.Bool("stdio", false, "Also run MCP over stdio when -http is enabled")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store queue.Store
	if *firestoreProject != "" {
		fsStore, err := queue.NewFirestoreStore(ctx, *firestoreProject, *firestoreCollection)
		if err != nil {
			log.Fatalf("Failed to initialize Firestore store: %v", err)
		}
		defer fsStore.Close()
		store = fsStore
	} else {
		jsonlStore, err := queue.NewJSONLStore(*storeFile)
		if err != nil {
			log.Fatalf("Failed to initialize JSONL store: %v", err)
		}
		store = jsonlStore
	}

	srv := server.New(store)

	if *httpAddr != "" {
		httpServer := &http.Server{
			Addr:              *httpAddr,
			Handler:           server.NewHTTPHandler(srv, store),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		}()

		if *alsoStdio {
			go func() {
				log.Printf("qmcp browser UI listening on http://localhost%s (MCP at /mcp, store: %s)", *httpAddr, *storeFile)
				if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatalf("HTTP server failed: %v", err)
				}
			}()
			if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
				log.Fatalf("Stdio server failed: %v", err)
			}
			return
		}

		log.Printf("qmcp browser UI listening on http://localhost%s (MCP at /mcp, store: %s)", *httpAddr, *storeFile)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v", err)
		}
		return
	}

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("Stdio server failed: %v", err)
	}
}
