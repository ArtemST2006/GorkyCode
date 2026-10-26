package service

import (
	"errors"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"math"
	"testing"
)

func TestValidateEmbeddingRejectsNonfiniteAndEmptyVectors(t *testing.T) {
	valid := make([]float32, model.EmbeddingDimension)
	valid[0] = 1
	for _, tc := range []struct {
		name   string
		vector []float32
		bad    bool
	}{
		{name: "valid", vector: valid},
		{name: "empty", vector: make([]float32, model.EmbeddingDimension), bad: true},
		{name: "short", vector: []float32{1}, bad: true},
		{name: "nan", vector: func() []float32 {
			v := append([]float32(nil), valid...)
			v[1] = float32(math.NaN())
			return v
		}(), bad: true},
		{name: "infinity", vector: func() []float32 {
			v := append([]float32(nil), valid...)
			v[1] = float32(math.Inf(1))
			return v
		}(), bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateEmbedding(ai.EmbeddingResponse{
				Embedding: tc.vector, Model: "e5", ModelVersion: "v1",
			})
			if tc.bad && !errors.Is(err, model.ErrBadGateway) {
				t.Fatalf("error = %v, want bad gateway", err)
			}
			if !tc.bad && err != nil {
				t.Fatal(err)
			}
		})
	}
}
