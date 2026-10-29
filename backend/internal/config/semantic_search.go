package config

import (
	"fmt"
	"time"
)

// SemanticSearch configures retrieval, not the embedding model or public API.
type SemanticSearch struct {
	Mode           string        `yaml:"mode" env:"SEMANTIC_SEARCH_MODE" env-default:"exact"`
	EFSearch       int           `yaml:"ef_search" env:"SEMANTIC_SEARCH_EF_SEARCH" env-default:"100"`
	MaxScanTuples  int           `yaml:"max_scan_tuples" env:"SEMANTIC_SEARCH_MAX_SCAN_TUPLES" env-default:"20000"`
	Candidates     int           `yaml:"candidates" env:"SEMANTIC_SEARCH_CANDIDATES" env-default:"100"`
	ExactThreshold int           `yaml:"exact_threshold" env:"SEMANTIC_SEARCH_EXACT_THRESHOLD" env-default:"1500"`
	ANNTimeout     time.Duration `yaml:"ann_timeout" env:"SEMANTIC_SEARCH_ANN_TIMEOUT" env-default:"500ms"`
}

// DefaultSemanticSearch keeps exact retrieval until ANN is validated on the workload.
func DefaultSemanticSearch() SemanticSearch {
	return SemanticSearch{
		Mode: "exact", EFSearch: 100, MaxScanTuples: 20000,
		Candidates: 100, ExactThreshold: 1500, ANNTimeout: 500 * time.Millisecond,
	}
}

// Validate rejects unsupported modes and unbounded resource settings.
func (s SemanticSearch) Validate() error {
	if s.Mode != "exact" && s.Mode != "hnsw" {
		return fmt.Errorf("mode must be exact or hnsw")
	}
	if s.Candidates < 20 || s.Candidates > 1000 {
		return fmt.Errorf("candidates must be in 20..1000")
	}
	if s.EFSearch < s.Candidates || s.EFSearch > 1000 {
		return fmt.Errorf("ef_search must be >= candidates and <= 1000")
	}
	if s.MaxScanTuples < s.Candidates || s.MaxScanTuples > 1000000 {
		return fmt.Errorf("max_scan_tuples must be >= candidates and <= 1000000")
	}
	if s.ExactThreshold < 0 || s.ExactThreshold > 100000 {
		return fmt.Errorf("exact_threshold must be in 0..100000")
	}
	if s.ANNTimeout <= 0 || s.ANNTimeout > 10*time.Second {
		return fmt.Errorf("ann_timeout must be in (0,10s]")
	}
	return nil
}
