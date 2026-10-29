package config

import (
	"testing"
	"time"
)

func TestSemanticSearchConfiguration(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("MODEL_PATH", "model/asr-model")
	t.Setenv("REDIS_URL", "redis://localhost:6380/0")
	cfg, err := Load()
	if err != nil || cfg.SemanticSearch != DefaultSemanticSearch() {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("SEMANTIC_SEARCH_MODE", "hnsw")
	t.Setenv("SEMANTIC_SEARCH_CANDIDATES", "150")
	t.Setenv("SEMANTIC_SEARCH_EF_SEARCH", "200")
	t.Setenv("SEMANTIC_SEARCH_ANN_TIMEOUT", "750ms")
	cfg, err = Load()
	if err != nil || cfg.SemanticSearch.Mode != "hnsw" || cfg.SemanticSearch.Candidates != 150 || cfg.SemanticSearch.ANNTimeout != 750*time.Millisecond {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
	t.Setenv("SEMANTIC_SEARCH_MODE", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestSemanticSearchRejectsUnboundedSettings(t *testing.T) {
	for _, mutate := range []func(*SemanticSearch){
		func(c *SemanticSearch) { c.Candidates = 19 },
		func(c *SemanticSearch) { c.EFSearch = 99 },
		func(c *SemanticSearch) { c.MaxScanTuples = 0 },
		func(c *SemanticSearch) { c.ExactThreshold = -1 },
		func(c *SemanticSearch) { c.ANNTimeout = 0 },
	} {
		cfg := DefaultSemanticSearch()
		mutate(&cfg)
		if cfg.Validate() == nil {
			t.Fatalf("accepted invalid settings: %+v", cfg)
		}
	}
}
