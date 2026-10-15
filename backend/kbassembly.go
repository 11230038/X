package main

// The knowledge base as this process builds it, and the worker that keeps its
// index current.
//
// The pieces here are the ones that are about the knowledge base rather than about
// the process: the provider documents and questions are embedded with, the service
// the turns read through, and the queue that embeds what has been added. They are
// apart from assemble.go because a process that has no knowledge base — a test
// that only wants the runtime, say — has no use for any of them.

import (
	"context"
	"fmt"
	"log/slog"

	"backend/internal/config"
	"backend/internal/embed"
	"backend/internal/kb"
	"backend/internal/outbound"
)

// newEmbedder builds the provider this process embeds with.
//
// It is built once, for the two things that embed: a document as it is indexed,
// and a question as it is searched for. Both have to be the same model — the
// base they meet in is the one that recorded which model its vectors came from.
func newEmbedder(cfg config.Config) (embed.Embedder, error) {
	// The provider is a host on the internet — unlike the document engine, which
	// is a service beside this deployment — so its policy is the external one
	// (§2.6). The policy is chosen here because this is where the address comes
	// from; internal/outbound is told which one applies and never reads config.
	dial, err := outbound.DialContext(outbound.ExternalMediaPolicy{})
	if err != nil {
		return nil, fmt.Errorf("initialize the embedding client's boundary: %w", err)
	}
	embedder, err := embed.NewClient(embed.Options{
		BaseURL:   cfg.Embedding.BaseURL,
		APIKey:    cfg.Embedding.APIKey,
		Model:     cfg.Embedding.Model,
		Dimension: cfg.Embedding.Dimension,
		Timeout:   cfg.Embedding.Timeout,
		BatchSize: cfg.Embedding.BatchSize,
		Dial:      dial,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the embedding provider: %w", err)
	}
	return embedder, nil
}

// indexWorker builds the embedding queue's worker and starts it, returning the
// channel that closes when it has stopped.
//
// The channel rather than the worker itself, because nothing outside this file
// has anything to say to a worker: it has no state to read and no method to
// call, only a job to do and a way to ask it to stop.
func indexWorker(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	documents kb.Repository,
	embedder embed.Embedder,
) (<-chan struct{}, error) {
	worker, err := kb.NewWorker(kb.WorkerOptions{
		Lease:          cfg.KB.VectorLease,
		Workers:        cfg.KB.IndexWorkers,
		EmbedBatchSize: cfg.Embedding.BatchSize,
		Logger:         logger,
	}, documents, embedder)
	if err != nil {
		return nil, fmt.Errorf("initialize the index worker: %w", err)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		worker.Run(ctx)
	}()
	return stopped, nil
}

// kbSettings is the knowledge base domain's tuning, resolved from configuration.
func kbSettings(cfg config.Config, logger *slog.Logger) kb.Options {
	return kb.Options{
		Chunking:            kb.Chunking{Size: cfg.KB.ChunkSize, Overlap: cfg.KB.ChunkOverlap},
		RetrievalTopK:       cfg.KB.RetrievalTopK,
		Candidates:          cfg.KB.RetrievalCandidates,
		RRFK:                cfg.KB.RRFK,
		MaxCharsPerDocument: cfg.KB.MaxCharsPerDocument,
		EmbeddingModel:      cfg.Embedding.Model,
		Logger:              logger,
	}
}
