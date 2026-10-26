package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// embeddingPurposeDocument и embeddingPurposeQuery — роли /api/embed.
const (
	embeddingPurposeDocument = "document"
	embeddingPurposeQuery    = "query"
)

// AIEmbedder вычисляет векторы через эндпоинт /api/embed AI-сервиса.
// Векторы сравнимы, только пока не меняется версия модели.
type AIEmbedder struct {
	aiURL string
}

// NewAIEmbedder создаёт эмбеддер поверх AI-сервиса.
func NewAIEmbedder(aiURL string) *AIEmbedder {
	return &AIEmbedder{aiURL: aiURL}
}

// Embed превращает текст в L2-нормализованный вектор с идентичностью модели.
func (e *AIEmbedder) Embed(ctx context.Context, purpose, text string) (model.EmbeddingResult, error) {
	if purpose != embeddingPurposeDocument && purpose != embeddingPurposeQuery {
		return model.EmbeddingResult{}, fmt.Errorf("unknown embedding purpose %q", purpose)
	}

	client, err := aiClient(e.aiURL, 20*time.Second)
	if err != nil {
		return model.EmbeddingResult{}, err
	}

	resp, err := client.EmbedText(ctx, ai.EmbeddingRequest{
		Purpose: ai.EmbeddingRequestPurpose(purpose),
		Text:    text,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return model.EmbeddingResult{}, fmt.Errorf("%w: embed request: %w", model.ErrGatewayTimeout, err)
		}
		return model.EmbeddingResult{}, fmt.Errorf("%w: embed request: %w", model.ErrBadGateway, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return model.EmbeddingResult{}, mapAIStatus(resp.StatusCode, resp.Body)
	}

	var embed ai.EmbeddingResponse
	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&embed); err != nil {
		return model.EmbeddingResult{}, fmt.Errorf("%w: decode embed response: %w", model.ErrBadGateway, err)
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return model.EmbeddingResult{}, fmt.Errorf("%w: embed response contains trailing data", model.ErrBadGateway)
	}

	return validateEmbedding(embed)
}

// validateEmbedding проверяет контракт: 384 конечных ненулевых значения
// и непустые идентификация модели и версия.
func validateEmbedding(embed ai.EmbeddingResponse) (model.EmbeddingResult, error) {
	if len(embed.Embedding) != model.EmbeddingDimension {
		return model.EmbeddingResult{}, fmt.Errorf("%w: embedding has %d values, want %d", model.ErrBadGateway, len(embed.Embedding), model.EmbeddingDimension)
	}
	sum := float64(0)
	for _, value := range embed.Embedding {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return model.EmbeddingResult{}, fmt.Errorf("%w: invalid embedding values", model.ErrBadGateway)
		}
		sum += float64(value) * float64(value)
	}
	if sum == 0 || math.IsInf(sum, 0) {
		return model.EmbeddingResult{}, fmt.Errorf("%w: invalid embedding values", model.ErrBadGateway)
	}
	if strings.TrimSpace(embed.Model) == "" || strings.TrimSpace(embed.ModelVersion) == "" {
		return model.EmbeddingResult{}, fmt.Errorf("%w: embed response is missing model identity", model.ErrBadGateway)
	}
	return model.EmbeddingResult{
		Vector:  embed.Embedding,
		Model:   embed.Model,
		Version: embed.ModelVersion,
	}, nil
}
