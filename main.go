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

	storeFile := flag.String("store-file", defaultStoreFile, "Path to the JSONL persistence file (or set QMCP_STORE_FILE)")
	httpAddr := flag.String("http", defaultHTTPAddr, "Optional HTTP listen address (e.g. :8080) to serve the browser UI at / and MCP over HTTP at /mcp")
	alsoStdio := flag.Bool("stdio", false, "Also run MCP over stdio when -http is enabled")
	flag.Parse()

	store, err := queue.NewJSONLStore(*storeFile)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	srv := server.New(store)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
