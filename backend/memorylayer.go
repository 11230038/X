package main

import (
	"fmt"

	"backend/internal/config"
	"backend/internal/memory"
	"gorm.io/gorm"
)

// newMemory builds the memory layer over the database the rest of the domains use
// and confirms the deployment's settings before the process serves anything.
//
// There is no join to write here, and that is worth saying rather than leaving
// out, because every other domain in this process has one. Memory has no
// capability, no tool and no route that hands a surface's observation in: the
// layers that write L1 are the surfaces themselves — mastery, reading, practice,
// and the user's own statement — and the routes this version adds are the ones
// that read what has been admitted and change it afterwards (§2.14). So this file
// builds one service, reached one way.
//
// Nothing about a turn's memory reaches here yet, which is the honest state of
// this slice: a turn observes nothing and remembers nothing of its own.
func newMemory(cfg config.Config, db *gorm.DB) (*memory.Service, error) {
	service, err := memory.NewService(memory.NewGORMRepository(db), memorySettings(cfg))
	if err != nil {
		return nil, fmt.Errorf("initialize the memory layer: %w", err)
	}
	return service, nil
}

// memorySettings is §5's six keys as the domain's options.
//
// The six are product questions rather than technical ones — how sure a model has
// to claim to be, how many independent observations count as corroboration and
// from how many surfaces, which kinds are never admitted without the user saying
// so, how far back "recent" reaches, and how much one surface may contribute —
// and they are refused here rather than at the first request: a threshold that is
// not a percentage, a gate with no corroboration, a window of no days and a bound
// of no facts are all checks Options.Validate makes, and the cost of making them
// at boot is a process that does not start instead of a person who cannot see
// their own memory.
func memorySettings(cfg config.Config) memory.Options {
	return memory.Options{
		ConfidenceThreshold: cfg.Memory.ConfidenceThreshold,
		MinCorroboration:    cfg.Memory.MinCorroboration,
		MinSurfaces:         cfg.Memory.MinSurfaces,
		SensitiveKinds:      cfg.Memory.SensitiveKinds,
		SynthesisWindowDays: cfg.Memory.SynthesisWindowDays,
		MaxFactsPerSurface:  cfg.Memory.MaxFactsPerSurface,
	}
}
